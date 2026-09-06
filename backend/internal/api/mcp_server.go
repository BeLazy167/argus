// Package api — mcp_server.go: the Model Context Protocol surface.
//
// Mounted at /mcp as a sibling of the webhook routes, not under /api/v1 —
// that group carries a 60s timeout and its request logger would tee every
// response body (memory content, review findings) into the payload log.
//
// Tenant scope is captured BY VALUE in the getServer closure. Tool handlers
// receive *mcp.CallToolRequest, which has no HTTP request, and they never read
// scope back out of ctx. In stateful mode the SDK binds a session to the
// initialize request's context, so tool handlers would inherit the first
// caller's tenant; Stateless plus closure capture makes that impossible.
package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/BeLazy167/argus/backend/internal/memory"
)

// errNotAccessible is the one string every tool uses for an id that is missing
// OR belongs to another tenant. handleDBError already conflates the two as 404
// for the REST API; the tool surface must not let a caller tell them apart.
var errNotAccessible = errors.New("not found or not accessible")

// errMemoryUnavailable means the Postgres memory backend was never wired.
// Registry.GetIndexer returns a literal nil in that state, never an error; a
// tool must say so rather than return an empty result set.
var errMemoryUnavailable = errors.New("memory backend unavailable")

// errInsufficientScope is the fixed scope-denial error. Confirmation flags
// never substitute for a granted scope.
func errInsufficientScope(scope string) error {
	return fmt.Errorf("insufficient scope: %s required", scope)
}

// tenantScope is what a verified request is allowed to touch. It is built once
// in mcpGetServer and closed over by every tool handler for that request.
type tenantScope struct {
	userID          string
	orgID           string
	installationIDs []int64
	grantedScopes   []string
}

// indexerSource is the narrow seam through which tools reach memory. The
// production value is *memory.Registry; tests substitute a stub.
type indexerSource interface {
	GetIndexer(context.Context, int64) memory.Indexer
	EmbedderAvailable(context.Context, int64) bool
}

// mcpTools carries one request's scope. Tool handlers are its methods so each
// one sees exactly the scope its request was verified for.
type mcpTools struct {
	srv   *Server
	scope tenantScope
}

// requireScope is the first check in every tool handler.
func (t *mcpTools) requireScope(scope string) error {
	if !slices.Contains(t.scope.grantedScopes, scope) {
		return errInsufficientScope(scope)
	}
	return nil
}

// internalErr logs the real error and returns a fixed string. 403/500 bodies
// on the REST path echo wrapped DB errors to the client; the tool surface must
// not, because tool errors land verbatim in an agent's transcript.
func (t *mcpTools) internalErr(ctx context.Context, tool string, err error) error {
	t.srv.logger.ErrorContext(ctx, "mcp tool failed", "tool", tool, "user", t.scope.userID, "org", t.scope.orgID, "error", err)
	return fmt.Errorf("%s failed", tool)
}

// resolveIndexer is the per-installation memory handle. Call it INSIDE a tool
// handler after the scope and tenant checks, never in mcpGetServer: each call
// costs an uncached installations read plus a possible embedder resolve, and
// most requests do not touch memory at all.
func (s *Server) resolveIndexer(ctx context.Context, installationID int64) memory.Indexer {
	if s.indexers == nil {
		return nil
	}
	return s.indexers.GetIndexer(ctx, installationID)
}

// schemaCache is the process-wide resolved-schema cache for the MCP surface.
// Stateless mode builds a fresh *mcp.Server per JSON-RPC request and re-runs
// mcp.AddTool for all nine tools, and each AddTool reflects and resolves an
// input schema — the exact topology the SDK documents the cache for. NewServer
// creates it once; a bare &Server{} (test literals) gets a per-call cache,
// which is still correct, just not shared. Never mutates s, so concurrent
// requests cannot race on the field.
func (s *Server) schemaCache() *mcp.SchemaCache {
	if s.mcpSchemaCache != nil {
		return s.mcpSchemaCache
	}
	return mcp.NewSchemaCache()
}

// newMCPServer builds a per-request server whose tools are closed over scope.
// It performs no I/O.
func (s *Server) newMCPServer(scope tenantScope) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "argus", Title: "Argus", Version: "1"}, &mcp.ServerOptions{
		Instructions: "Argus code-review memory and review results for the organization you authorized. Call list_repos first to resolve repo_id and installation_id; every other tool takes those local ids, never GitHub ids. Read tools need the argus:read scope; create_memory, delete_memory and retire_memory need argus:memory:write.",
		Logger:       s.logger,
		SchemaCache:  s.schemaCache(),
	})
	registerMCPTools(server, &mcpTools{srv: s, scope: scope})
	return server
}

// registerMCPTools attaches every tool. It is also the boot-time schema smoke
// test: mcp.AddTool panics on an invalid schema, and registerMCPRoutes runs
// this once before any request can reach it.
func registerMCPTools(server *mcp.Server, t *mcpTools) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_repos",
		Description: "List the repositories and installations the caller can access in the authorized organization. Returns the local repo_id and installation_id every other tool requires. Call this first.",
	}, t.listRepos)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_memory",
		Description: "Semantic search over the team's institutional memory for one repo (or org-wide with scope=shared): learned patterns, conventions, dismissed false positives, scenarios, and past-review context. Each match carries custom_id (for retire_memory) and pattern_ids (for delete_memory; empty means retire-only). Check embeddings_available — false means similarity search is off for this installation.",
	}, t.searchMemory)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_memory_briefing",
		Description: "The assembled institutional-memory briefing Argus gives its reviewers for a repo, as markdown. Optionally focused on one file. Empty is a legitimate result.",
	}, t.getMemoryBriefing)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "create_memory",
		Description: "Record a convention, pattern or fact in the team's memory so Argus applies it in future reviews. Repo-scoped by default; shared=true with confirm_shared=true writes org-wide. Result status is created, existing (identical memory reused, nothing written) or confirmation_required (similar memories returned; retry with confirm_duplicate=true to write anyway). Requires argus:memory:write.",
	}, t.createMemory)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "delete_memory",
		Description: "Permanently delete ONE contributing pattern row. The memory itself is removed later by the mirror worker, and only if no other pattern still owns it — check siblings_at_delete and retention_expected. To stop a memory being retrieved, use retire_memory instead. Refuses pipeline-learned patterns unless confirm_pipeline_learned=true. Requires argus:memory:write.",
	}, t.deleteMemory)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "retire_memory",
		Description: "Stop a memory being retrieved (invalidate), or mark it superseded by another live memory when replaced_by_custom_id is given. This is the operation to use when knowledge should no longer influence reviews. Only memories that carry recorded provenance — patterns and feedback, whose search_memory match reports a source — can be retired; review, rule and scenario memories record none and are refused. Irreversible; a reason is required and logged. Refuses pipeline-learned memories unless confirm_pipeline_learned=true. Requires argus:memory:write.",
	}, t.retireMemory)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_reviews",
		Description: "List Argus reviews the caller can access, newest first, optionally for one repo. Returns review_id for get_review_status and get_review.",
	}, t.listReviews)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_review_status",
		Description: "Cheap poll for one review: status, current pipeline stage while running, score, error, timings and the GitHub PR link. Use get_review for findings.",
	}, t.getReviewStatus)
	mcp.AddTool[getReviewInput, any](server, &mcp.Tool{
		Name:        "get_review",
		Description: "Everything the Argus review page shows for one review: the review record, findings (severity, file, line, body), minor notes, per-push history, auto-resolve events, what the review learned into memory, counts by severity and file, and which sections (if any) failed to load. Suppressed findings are returned only with include_suppressed=true.",
	}, t.getReview)
}

// mcpGetServer is the SDK's per-request hook — the only place with the
// *http.Request, so it is where scope is read from the context the auth chain
// populated and frozen into the closure.
func (s *Server) mcpGetServer(r *http.Request) *mcp.Server {
	return s.newMCPServer(tenantScope{
		userID:          getUserID(r.Context()),
		orgID:           getOrgID(r.Context()),
		installationIDs: getInstallationIDs(r.Context()),
		grantedScopes:   getMCPScopes(r.Context()),
	})
}

const (
	// mcpMaxBodyBytes caps a single /mcp request body. A JSON-RPC tool call is
	// a few KB; the largest legitimate one is a create_memory whose content is
	// capped at 4000 runes well inside this.
	mcpMaxBodyBytes = 1 << 20 // 1 MiB
	// mcpRequestTimeout bounds one /mcp request end to end, matching the
	// /api/v1 group. It is the deadline the tool handlers hand to the embedder
	// and the store.
	mcpRequestTimeout = 60 * time.Second
)

// mcpLimitRequestBody caps the request body at mcpMaxBodyBytes.
//
// Two layers, because they catch different things. A declared Content-Length
// over the cap is refused outright with 413, before the token verifier does
// any JWKS work for a request that cannot be served. That check alone is not
// enough — Content-Length is client-supplied and chunked requests carry none —
// so MaxBytesReader is the actual enforcement, capping whatever the SDK
// handler's io.ReadAll consumes no matter what the client declared.
func mcpLimitRequestBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > mcpMaxBodyBytes {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request body too large"})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, mcpMaxBodyBytes)
		next.ServeHTTP(w, r)
	})
}

// mcpRequestLimits is the size + duration pair the /mcp route applies before
// authentication. Returned as a slice rather than applied inline so a test can
// mount the same stack in front of a probe handler and assert what it does,
// instead of asserting against a copy that could drift from the route.
func mcpRequestLimits() []func(http.Handler) http.Handler {
	return []func(http.Handler) http.Handler{
		mcpLimitRequestBody,
		chimiddleware.Timeout(mcpRequestTimeout),
	}
}

// registerMCPRoutes mounts the MCP surface when MCP_ENABLED is true. When it
// is not, nothing is registered and every path 404s via chi's default
// handler, the same "don't advertise" shape as registerPprofRoutes.
func (s *Server) registerMCPRoutes(r chi.Router) {
	if s.cfg == nil || !s.cfg.MCPEnabled {
		return
	}
	s.newMCPServer(tenantScope{}) // boot-time schema smoke test

	// RFC 9728 §3: the well-known document at the origin, and the path-suffixed
	// form for the /mcp resource. Both serve the same document.
	r.Get("/.well-known/oauth-protected-resource", s.handleProtectedResourceMetadata)
	r.Get("/.well-known/oauth-protected-resource/mcp", s.handleProtectedResourceMetadata)

	handler := mcp.NewStreamableHTTPHandler(s.mcpGetServer, &mcp.StreamableHTTPOptions{
		// Two Fly machines, no affinity: a stateful session lives in one
		// process's map and 404s on the other. Stateless also removes the
		// initialize-context tenant leak described in the package comment.
		Stateless:    true,
		JSONResponse: true,
		Logger:       s.logger,
	})
	r.Route("/mcp", func(r chi.Router) {
		// Body cap and timeout come BEFORE auth, so an oversized or slow
		// request is bounded without the work of verifying it first.
		//
		// The SDK handler reads the whole body with io.ReadAll, so without a
		// cap an unauthenticated client could stream indefinitely into memory.
		// /mcp is a sibling of /api/v1, not a child, so it also inherits none
		// of that group's 60s timeout; the tool handlers pass the request
		// context straight to the embedder, which is where a request with no
		// deadline would sit. JSONResponse mode never streams a response, so
		// a write-deadline-style timeout is safe here in a way it would not be
		// on an SSE route.
		r.Use(mcpRequestLimits()...)
		// auth.RequireBearerToken is the spec's mcpAuthChallenge + mcpAuth:
		// it extracts the bearer token, runs our policy, and on failure writes
		// 401 with a resource_metadata challenge.
		r.Use(auth.RequireBearerToken(s.mcpTokenVerifier, &auth.RequireBearerTokenOptions{
			ResourceMetadataURL: s.mcpResourceMetadataURL(),
		}))
		r.Use(mcpClaimsToContext)
		r.Use(s.requireMCPInstallationScope)
		r.Handle("/", handler)
		r.Handle("/*", handler)
	})
}
