# Argus MCP Server Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Expose Argus's memory corpus (search, briefing, create, delete, retire) and review results (list, status, full detail) over MCP, scoped to the single Clerk organization a user selected during OAuth consent, mounted inside the existing Go backend at `/mcp`.

**Architecture:** New files in the existing `internal/api` package mount `mcp.NewStreamableHTTPHandler` at `/mcp` behind three middlewares: the SDK's `auth.RequireBearerToken` wrapping an MCP-specific token policy (issuer, resource audience, temporal claims, subject, org, token type, granted scopes) built on the existing JWKS verifier; an adapter that copies verified claims into the context keys the api package already reads; and `requireMCPInstallationScope`, which resolves only the selected org's installation and rejects conflicting hints. `getServer(*http.Request)` captures the caller's `tenantScope` (user, org, installation set, granted scopes) by value into a closure; every tool checks its required scope and validates its id arguments against that scope before touching the store or the per-installation memory indexer. Three store/memory operations are new: an atomic manual-pattern create-or-get under the existing per-identity advisory lock (N10), a transactional guarded delete that snapshots siblings (N2), and a transactional guarded retirement that reads provenance from the memory row (N8). Stateless transport is mandatory (two Fly machines, no affinity).

**Tech Stack:** Go 1.24.1, chi v5, `github.com/modelcontextprotocol/go-sdk` **v1.4.0** (v1.7.0 requires Go 1.25 — do not upgrade), pgx v5, sqlc v1.30.0, Clerk (RS256 JWT OAuth access tokens via JWKS).

**Spec:** `docs/superpowers/specs/2026-09-05-argus-mcp-server-design.md`

## Global Constraints

- Pin `github.com/modelcontextprotocol/go-sdk@v1.4.0`. Its `go.mod` declares `go 1.24.0`; v1.7.0 declares `go 1.25.0` and would fail the CI build job (`go 1.24` in `.github/workflows/ci.yml`, `golang:1.24-alpine` in the Dockerfile).
- All MCP HTTP/tool code lives in package `api` (`backend/internal/api/mcp_*.go`) so it can use the unexported `containsID`, `getUserID`, `getOrgID`, `getOrgRole`, `getInstallationIDs`, `installationIDsKey`, `resolveInstallationIDs`, `writeJSON`, `beginOperation`.
- `StreamableHTTPOptions{Stateless: true, JSONResponse: true}` — never stateful.
- Tenant scope is captured **by value** in the `getServer` closure. Tool handlers never read it from `ctx`.
- **Selected org only.** The installation set comes from the token's `org_id` claim via the existing org branch of `resolveInstallationIDs` (which auto-links the user). The user-wide fallback (`GetUserInstallationIDs`) is never taken. A missing org claim is a 401; a malformed or non-member `X-Installation-ID` is a 403 (the REST middleware silently ignores it — the MCP wrapper must not).
- **Scopes.** Read tools require `argus:read`; `create_memory`, `delete_memory`, `retire_memory` require `argus:memory:write`. Granted scopes are copied into `tenantScope.grantedScopes` and checked first in every handler with the fixed error `insufficient scope: <scope> required`. Confirmation flags are acknowledgments; they never substitute for a scope or org membership.
- **Token policy** (`mcpTokenVerifier`): RS256 signature via the existing JWKS cache; `iss == CLERK_ISSUER_URL`; `aud` contains `MCP_RESOURCE_URL` (required — never disable); `exp` not passed; `nbf` (if present) not in the future beyond 5s skew; `sub` non-empty; `org_id` non-empty; no `sid` claim (a `sid` marks a Clerk session token, which is not an MCP credential); at least one granted scope. Every rejection wraps `auth.ErrInvalidToken` so the SDK emits a 401 challenge. Issuer and audience come from config only, never from request headers.
- Clerk does not document the scope claim's name for JWT access tokens. `grantedScopes(decodedClaims)` accepts `scope` (space-delimited string, RFC 8693/9068) and `scp`/`scopes` (string array). The OAuth end-to-end acceptance run (Task 13, manual) confirms the real shape; if Clerk uses another name, that one function changes.
- `getServer` performs no I/O. `resolveIndexer` is called inside tool handlers, after the scope and tenant checks.
- Every id argument is validated with `containsID(scope.installationIDs, …)` or `store.GetRepoScoped(id, scope.installationIDs)`. No tool infers a tenant from `installationIDs[0]`.
- Tool errors are fixed strings. Shared sentinels: `errNotAccessible` ("not found or not accessible"), `errMemoryUnavailable` ("memory backend unavailable"). Internal failures are logged with the real error and returned as `"<tool> failed"`.
- Tool **outputs** are Go structs (the SDK requires `StructuredContent` to marshal to a JSON object). Outputs that embed `store.*` types (which contain `uuid.UUID` / `json.RawMessage`) use `Out = any` to skip output-schema inference; purpose-built output structs use a typed `Out`.
- `source` for MCP-created patterns is `"manual"`, never `"mcp"` — `source` is hashed into `custom_id`, so a new value forks identity. Authorship goes in `mirrorExtra["created_by"]`.
- Human-authored sources are exactly `manual` and `remember_command` (`store.IsHumanAuthoredSource`). Delete and retire refuse any other source unless `confirm_pipeline_learned=true`, and the check runs **inside the mutation transaction** against the stored row, never only against an earlier handler read. Retirement reads provenance from `memories.metadata->>'source'`; unknown or missing provenance is an error even with the flag.
- Content is trimmed and stripped of `\x00` **before** length validation, identity derivation, duplicate checks and insertion (jsonb rejects NUL: SQLSTATE 22P05).
- Memory identity for a `patterns` row is `COALESCE(NULLIF(memory_custom_id,''), memory_doc_id)` — the same precedence `firstNonEmpty(row.MemoryCustomID, row.MemoryDocID)` uses in `DeletePattern`. Every sibling query in this plan uses that expression.
- No tool result uses the word "forgotten". Write results never assert searchability; they report `mirror_state:"pending"`.
- Commit messages do **not** carry a "Generated with Claude Code" footer or `Co-Authored-By` trailer (user's standing instruction).
- CI runs `go run ./cmd/migrate && go vet ./... && go test -race -count=1 ./...` from `backend/` with `TEST_DATABASE_URL` set. `_pg_test.go` tests `t.Skip` without it. Run `make sqlc-check` and `make tygo-check` locally before pushing.
- This plan adds **no** migrations. If one becomes necessary, take the next free `NNN_` at push time (087 is taken on the private tree).

---

## File Structure

**New files (package `api`, `backend/internal/api/`):**

| File | Responsibility |
|---|---|
| `mcp_server.go` | `tenantScope`, `indexerSource`, `mcpTools`, sentinels, `errInsufficientScope`, `requireScope`, `internalErr`, `newMCPServer`, `registerMCPTools`, `mcpGetServer`, `registerMCPRoutes`, `resolveIndexer` |
| `mcp_auth.go` | scope constants, `grantedScopes`, `mcpTokenVerifier`, `mcpClaimsToContext`, `getMCPScopes`, `requireMCPInstallationScope`, `handleProtectedResourceMetadata`, `mcpResourceMetadataURL` |
| `mcp_tools_discovery.go` | `list_repos` |
| `mcp_tools_memory_read.go` | `scopedRepo`, `search_memory`, `get_memory_briefing` |
| `mcp_tools_memory_write.go` | `sanitizeMemoryContent`, `create_memory`, `delete_memory`, `retire_memory` |
| `mcp_tools_review.go` | `scopedReview`, `reviewToolErr`, `githubPRURL`, `list_reviews`, `get_review_status`, `get_review` |
| `mcp_*_test.go` | one test file per source file above, plus `mcp_e2e_pg_test.go` |

**Modified files:**

| File | Change |
|---|---|
| `backend/go.mod`, `go.sum` | add go-sdk v1.4.0 |
| `backend/internal/config/config.go` | `ClerkIssuerURL`, `MCPEnabled`, `MCPResourceURL`; `ValidateMCP` |
| `backend/internal/api/middleware.go` | extract `verifyJWT` returning `decodedClaims` (iss/aud/nbf/sid/scope added); `validateToken` becomes a wrapper |
| `backend/internal/api/middleware_logging.go` | `skipBodyLogging(path)`; exclude `/mcp` request+response bodies |
| `backend/internal/api/server.go` | `indexers indexerSource` field; `registerMCPRoutes(r)` call |
| `backend/internal/api/handlers_patterns.go:131` | dashboard manual create uses `CreateOrGetPattern` (N10) |
| `backend/internal/store/sqlc/query/pipeline.sql`, `db/pipeline.sql.go` | `GetLatestRunStateForReview` (N1) |
| `backend/internal/store/sqlc/query/patterns.sql`, `db/patterns.sql.go` | `GetPattern` selects `memory_custom_id` |
| `backend/internal/store/queries.go` | `GetLatestRunStateForReview` wrapper |
| `backend/internal/store/patterns.go` | `Pattern.MemoryCustomID`; `IsHumanAuthoredSource`; `createPatternTx`/`deletePatternTx` extraction; identity lock in `CreatePattern`; `CreateOrGetPattern` (N10); `FindPatternIDByIdentity`; `ListPatternIDsByIdentity` (N9); `DeletePatternGuarded` + `PatternDeletion` + sentinels (N2) |
| `backend/internal/memory/indexer.go` | `RetireDocument` on `Indexer`; `RetireRequest`/`RetireResult`; sentinels; `knownMemorySources` (N8) |
| `backend/internal/memory/pgindexer.go` | SQL constants shared by Invalidate/Supersede/Retire; `RetireDocument`; `%w ErrDocumentNotFound` |
| `backend/internal/memory/memorytest/fake.go` | `RetireDocument` recording + `RetireFn` stub |
| `backend/internal/memory/registry.go` | `EmbedderAvailable`, `WarmVectorProbe` (N3/N4) |
| `backend/internal/app/app.go` | call `WarmVectorProbe` after `WithPostgresBackend` |
| `README.md` (repo root) | MCP client + Clerk configuration section |

---

### Task 1: Dependency and configuration

**Files:**
- Modify: `backend/go.mod`, `backend/go.sum`
- Modify: `backend/internal/config/config.go` (struct lines 18-55; `Load` literal lines ~107-138)
- Test: `backend/internal/config/config_mcp_test.go`

**Interfaces:**
- Produces: `config.Config.ClerkIssuerURL string`, `config.Config.MCPEnabled bool`, `config.Config.MCPResourceURL string`, `func (c *Config) ValidateMCP() error`.

- [ ] **Step 1: Add the SDK dependency at the pinned version**

```bash
cd /Users/belazy/personal/argus/backend
go get github.com/modelcontextprotocol/go-sdk@v1.4.0
go mod tidy
grep -n "modelcontextprotocol/go-sdk" go.mod && grep -n "^go " go.mod
```

Expected: `github.com/modelcontextprotocol/go-sdk v1.4.0` in `go.mod`, and the `go` directive still `1.24.1`. If tidy bumps it, stop — a transitive dependency needs a newer Go and the version must be re-examined.

- [ ] **Step 2: Write the failing config test**

Create `backend/internal/config/config_mcp_test.go`:

```go
package config

import "testing"

func TestValidateMCP(t *testing.T) {
	t.Parallel()
	const jwks = "https://x.clerk.accounts.dev/.well-known/jwks.json"
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{"disabled needs nothing", Config{MCPEnabled: false}, false},
		{"enabled needs jwks", Config{MCPEnabled: true, ClerkIssuerURL: "https://x.clerk.accounts.dev", MCPResourceURL: "https://api.example/mcp"}, true},
		{"enabled needs issuer", Config{MCPEnabled: true, ClerkJWKSURL: jwks, MCPResourceURL: "https://api.example/mcp"}, true},
		{"issuer must be absolute http(s)", Config{MCPEnabled: true, ClerkJWKSURL: jwks, ClerkIssuerURL: "clerk.accounts.dev", MCPResourceURL: "https://api.example/mcp"}, true},
		{"resource url required", Config{MCPEnabled: true, ClerkJWKSURL: jwks, ClerkIssuerURL: "https://x.clerk.accounts.dev"}, true},
		{"resource url must be https", Config{MCPEnabled: true, ClerkJWKSURL: jwks, ClerkIssuerURL: "https://x.clerk.accounts.dev", MCPResourceURL: "http://api.example/mcp"}, true},
		{"resource url must end with /mcp", Config{MCPEnabled: true, ClerkJWKSURL: jwks, ClerkIssuerURL: "https://x.clerk.accounts.dev", MCPResourceURL: "https://api.example/"}, true},
		{"enabled and complete", Config{MCPEnabled: true, ClerkJWKSURL: jwks, ClerkIssuerURL: "https://x.clerk.accounts.dev", MCPResourceURL: "https://api.example/mcp"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.cfg.ValidateMCP()
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateMCP() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
```

- [ ] **Step 3: Run it to verify it fails**

```bash
cd /Users/belazy/personal/argus/backend && go test ./internal/config/ -run TestValidateMCP 2>&1 | tail -5
```

Expected: build failure — `cfg.ValidateMCP undefined`, `MCPEnabled`/`ClerkIssuerURL`/`MCPResourceURL` undefined.

- [ ] **Step 4: Add the fields, loading, and validation**

In `backend/internal/config/config.go`, extend the Clerk block of the struct (currently `ClerkJWKSURL` / `CORSAllowOrigin`):

```go
	// Clerk (auth)
	ClerkJWKSURL    string
	CORSAllowOrigin string

	// MCP (team memory + review access over Model Context Protocol). Clerk is
	// the OAuth authorization server; this backend is a resource server. The
	// issuer is pinned as `iss` and advertised in RFC 9728 metadata. The
	// resource URL is the canonical public HTTPS URL of the endpoint, ending in
	// /mcp; tokens must carry it as `aud`.
	ClerkIssuerURL string
	MCPEnabled     bool
	MCPResourceURL string
```

In the `cfg := &Config{...}` literal, after `CORSAllowOrigin:`:

```go
		ClerkIssuerURL: os.Getenv("CLERK_ISSUER_URL"),
		MCPEnabled:     getEnv("MCP_ENABLED", "false") == "true",
		MCPResourceURL: os.Getenv("MCP_RESOURCE_URL"),
```

Replace the trailing `return cfg, nil` of `Load` with:

```go
	if err := cfg.ValidateMCP(); err != nil {
		return nil, err
	}
	return cfg, nil
```

Add the method after the struct:

```go
// ValidateMCP checks the configuration the MCP route depends on. It runs only
// when the route is enabled, so a deployment without MCP needs none of it.
//
// The issuer is what protected-resource metadata advertises and what the token
// verifier pins `iss` to; the JWKS URL is what signatures are checked against;
// the resource URL is the required `aud` and the identifier clients see. A gap
// in any of them produces an /mcp that 401s every request with a challenge
// pointing nowhere, so fail at boot instead.
func (c *Config) ValidateMCP() error {
	if !c.MCPEnabled {
		return nil
	}
	if c.ClerkJWKSURL == "" {
		return fmt.Errorf("MCP_ENABLED requires CLERK_JWKS_URL")
	}
	issuer, err := url.Parse(c.ClerkIssuerURL)
	if c.ClerkIssuerURL == "" || err != nil || issuer.Host == "" || (issuer.Scheme != "http" && issuer.Scheme != "https") {
		return fmt.Errorf("MCP_ENABLED requires CLERK_ISSUER_URL to be an absolute http(s) URL")
	}
	resource, err := url.Parse(c.MCPResourceURL)
	if c.MCPResourceURL == "" || err != nil || resource.Host == "" || resource.Scheme != "https" || !strings.HasSuffix(resource.Path, "/mcp") {
		return fmt.Errorf("MCP_ENABLED requires MCP_RESOURCE_URL to be an absolute https URL ending in /mcp")
	}
	return nil
}
```

`fmt` and `net/url` are already imported (used by the `MERMAID_VALIDATOR` validation). Add `"strings"` to the import block if it is not there.

- [ ] **Step 5: Run the test and the whole config package**

```bash
cd /Users/belazy/personal/argus/backend && go test ./internal/config/ -count=1 2>&1 | tail -5 && go build ./... && go vet ./internal/config/
```

Expected: `ok`; build and vet clean.

- [ ] **Step 6: Commit**

```bash
cd /Users/belazy/personal/argus
git add backend/go.mod backend/go.sum backend/internal/config/config.go backend/internal/config/config_mcp_test.go
git commit -m "feat(mcp): pin go-sdk v1.4.0 and add MCP config

v1.7.0 declares go 1.25.0; the module, CI and Dockerfile are on 1.24, so
v1.4.0 (go 1.24.0) is the newest usable release. It carries everything
the design needs: Stateless/JSONResponse transport options and the auth
package's RequireBearerToken.

MCP_ENABLED gates the route. When on, CLERK_ISSUER_URL (advertised in
RFC 9728 metadata and pinned as iss), MCP_RESOURCE_URL (the canonical
https .../mcp URL tokens must carry as aud) and CLERK_JWKS_URL are
required at boot rather than discovered as a 401 loop in production."
```

---

### Task 2: `GetLatestRunStateForReview` (N1)

**Files:**
- Modify: `backend/internal/store/sqlc/query/pipeline.sql` (after `GetLatestRunForReview`, line 17)
- Generate: `backend/internal/store/db/pipeline.sql.go` (via `make sqlc`)
- Modify: `backend/internal/store/queries.go` (after `GetLatestRunForReview`, ends line 3808)
- Test: `backend/internal/store/pipeline_state_pg_test.go`

**Interfaces:**
- Produces: `func (s *Store) GetLatestRunStateForReview(ctx context.Context, reviewID uuid.UUID) (string, error)` — `pgx.ErrNoRows` when the review has no run.

- [ ] **Step 1: Write the failing pg test**

Create `backend/internal/store/pipeline_state_pg_test.go`:

```go
package store

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func seedPipelineRun(t *testing.T, ctx context.Context, pool *pgxpool.Pool, reviewID uuid.UUID, state string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `INSERT INTO pipeline_states (review_id, state) VALUES ($1, $2)`, reviewID, state); err != nil {
		t.Fatalf("seed pipeline run: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM pipeline_states WHERE review_id = $1`, reviewID)
	})
}

func TestGetLatestRunStateForReview(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	_, withRun, withoutRun := seedLearnTenant(t, ctx, pool, "mcp-run-state")
	st := NewWithDB(pool)

	// Two runs; the later-updated one wins. Bump updated_at explicitly so the
	// ORDER BY is exercised rather than relying on insert order.
	seedPipelineRun(t, ctx, pool, withRun, "reviewing")
	seedPipelineRun(t, ctx, pool, withRun, "synthesizing")
	if _, err := pool.Exec(ctx, `UPDATE pipeline_states SET updated_at = NOW() + interval '1 minute' WHERE review_id = $1 AND state = 'synthesizing'`, withRun); err != nil {
		t.Fatal(err)
	}

	state, err := st.GetLatestRunStateForReview(ctx, withRun)
	if err != nil {
		t.Fatalf("GetLatestRunStateForReview: %v", err)
	}
	if state != "synthesizing" {
		t.Fatalf("state = %q, want synthesizing (the most recently updated run)", state)
	}

	if _, err := st.GetLatestRunStateForReview(ctx, withoutRun); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("review with no run: err = %v, want pgx.ErrNoRows", err)
	}
}
```

- [ ] **Step 2: Run to verify failure**

```bash
cd /Users/belazy/personal/argus/backend && go test ./internal/store/ -run TestGetLatestRunStateForReview 2>&1 | tail -5
```

Expected: build failure `st.GetLatestRunStateForReview undefined`.

- [ ] **Step 3: Add the sqlc query and regenerate**

Append to `backend/internal/store/sqlc/query/pipeline.sql` after the `GetLatestRunForReview` query:

```sql

-- name: GetLatestRunStateForReview :one
-- Stage of the most recently touched run for a review. "Latest" means
-- updated_at, matching GetLatestRunForReview: a recovered run that resumed is
-- the current one even if an older row was created later.
SELECT state FROM pipeline_states WHERE review_id = $1 ORDER BY updated_at DESC LIMIT 1;
```

```bash
cd /Users/belazy/personal/argus/backend && make sqlc && git diff --stat internal/store/db/
```

Expected: `internal/store/db/pipeline.sql.go` gains `func (q *Queries) GetLatestRunStateForReview(ctx context.Context, reviewID uuid.UUID) (string, error)`.

- [ ] **Step 4: Add the Store wrapper**

In `backend/internal/store/queries.go`, after `GetLatestRunForReview`:

```go
// GetLatestRunStateForReview returns pipeline_states.state for the review's
// most recently updated run. pgx.ErrNoRows means no run exists yet — a review
// can sit at status pending before its first persist.
func (s *Store) GetLatestRunStateForReview(ctx context.Context, reviewID uuid.UUID) (storeResult0 string, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "GetLatestRunStateForReview",

			"review_id",

			storeLogValue(reviewID))
	defer func() {
		if recovered := recover(); recovered !=
			nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr,
			storeResult0)
	}()

	return s.q.GetLatestRunStateForReview(ctx, reviewID)
}
```

- [ ] **Step 5: Run the test**

```bash
cd /Users/belazy/personal/argus/backend && go test ./internal/store/ -run TestGetLatestRunStateForReview -count=1 -v 2>&1 | tail -6 && make sqlc-check
```

Expected: PASS with `TEST_DATABASE_URL` (SKIP without); `sqlc-check` exits 0.

- [ ] **Step 6: Commit**

```bash
cd /Users/belazy/personal/argus
git add backend/internal/store/sqlc/query/pipeline.sql backend/internal/store/db/pipeline.sql.go backend/internal/store/queries.go backend/internal/store/pipeline_state_pg_test.go
git commit -m "feat(store): expose the latest pipeline stage for a review

get_review_status needs the in-flight stage, which today is only visible
over the WebSocket stream. GetLatestRunForReview returns just the run id;
this returns pipeline_states.state for the most recently updated run and
pgx.ErrNoRows when the review has none yet. Ordered by updated_at to match
its sibling: a recovered run that resumed is the current one."
```

---

### Task 3: `Registry.EmbedderAvailable` and `WarmVectorProbe` (N3, N4)

**Files:**
- Modify: `backend/internal/memory/registry.go` (after `GetIndexer`, ends line 125)
- Modify: `backend/internal/app/app.go` (after the `WithPostgresBackend` chain, ~line 176)
- Test: `backend/internal/memory/registry_mcp_test.go`

**Interfaces:**
- Produces: `func (r *Registry) EmbedderAvailable(ctx context.Context, installationID int64) bool`, `func (r *Registry) WarmVectorProbe(ctx context.Context)`.
- Consumes (all exist): `pgTestPool(t) (*pgxpool.Pool, int64)` and `discardLogger()` in package `memory` tests; `NewEmbedderRegistry(resolver EmbedKeyResolver, platform PlatformEmbeddings, logger *slog.Logger)`; `PlatformEmbeddings{APIKey, BaseURL, Model string; Dimensions int}`; `StorageDimensions = 1024`; `EmbedKeyResolver.ResolveEmbeddingsKey(ctx, installationID) (apiKey, baseURL, model string, found bool, err error)`.

- [ ] **Step 1: Write the failing tests**

Create `backend/internal/memory/registry_mcp_test.go`:

```go
package memory

import (
	"context"
	"testing"
)

// An unwired registry must answer false, not panic, because MCP tool handlers
// call this before deciding how to describe an empty result.
func TestEmbedderAvailableUnwiredRegistry(t *testing.T) {
	t.Parallel()
	r := NewRegistry(discardLogger())
	if r.EmbedderAvailable(context.Background(), 1) {
		t.Fatal("unwired registry reported an available embedder")
	}
}

// WarmVectorProbe on an unwired registry is a no-op; it must never touch a
// nil pool.
func TestWarmVectorProbeUnwiredRegistryIsNoop(t *testing.T) {
	t.Parallel()
	NewRegistry(discardLogger()).WarmVectorProbe(context.Background())
}

func TestEmbedderAvailableFollowsEmbedderRegistry(t *testing.T) {
	pool, install := pgTestPool(t)
	ctx := context.Background()

	// No platform key and no BYOK row: embeddings are off for this tenant.
	off := NewRegistry(discardLogger()).WithPostgresBackend(pool,
		NewEmbedderRegistry(noKeysResolver{}, PlatformEmbeddings{Dimensions: StorageDimensions}, discardLogger()))
	if off.EmbedderAvailable(ctx, install) {
		t.Fatal("registry with no keys reported embeddings available")
	}

	// A platform key makes an embedder resolvable for every installation.
	on := NewRegistry(discardLogger()).WithPostgresBackend(pool,
		NewEmbedderRegistry(noKeysResolver{}, PlatformEmbeddings{APIKey: "k", BaseURL: "https://example.invalid", Model: "m", Dimensions: StorageDimensions}, discardLogger()))
	if !on.EmbedderAvailable(ctx, install) {
		t.Fatal("registry with a platform key reported embeddings unavailable")
	}
}

type noKeysResolver struct{}

func (noKeysResolver) ResolveEmbeddingsKey(context.Context, int64) (string, string, string, bool, error) {
	return "", "", "", false, nil
}
```

- [ ] **Step 2: Run to verify failure**

```bash
cd /Users/belazy/personal/argus/backend && go test ./internal/memory/ -run 'TestEmbedderAvailable|TestWarmVectorProbe' 2>&1 | tail -5
```

Expected: build failure `r.EmbedderAvailable undefined`.

- [ ] **Step 3: Implement both methods**

In `backend/internal/memory/registry.go`, after `GetIndexer`:

```go
// EmbedderAvailable reports whether searches for this installation will use
// vectors. With the embedder off, every positive-threshold Search returns
// (nil, nil) — indistinguishable from "nothing matched" — so callers that show
// results to a person need this to say "embeddings are not configured" instead
// of "no results". Resolution is cached by the EmbedderRegistry, so this is
// cheap to call per request.
func (r *Registry) EmbedderAvailable(ctx context.Context, installationID int64) bool {
	if r.pool == nil || r.embedders == nil {
		return false
	}
	embedder, _ := r.embedders.GetEmbedder(ctx, installationID)
	return embedder != nil
}

// WarmVectorProbe latches usesPGContextVector from a long-lived context.
//
// The probe is a process-wide sync.Once that runs on the FIRST search using
// THAT caller's context. If the first search in a process arrives with a short
// or already-cancelled deadline, the probe errors, latches "pgvector", and on a
// pgcontext column every later vector operation in the process fails until
// restart. Boot warms it so no request can be the first.
func (r *Registry) WarmVectorProbe(ctx context.Context) {
	if r.pool == nil {
		return
	}
	usesPGContextVector(ctx, r.pool, r.log())
}
```

- [ ] **Step 4: Call the warm at boot**

In `backend/internal/app/app.go`, immediately after the `WithPostgresBackend` chain completes and before the "memory registry initialization completed" log:

```go
	// Latch the vector-type probe from a boot context. Left to the first search,
	// a short-deadline request could latch it wrong for the whole process.
	memRegistry.WarmVectorProbe(ctx)
```

`ctx` is the cancel-only background context declared near line 113, not a request context.

- [ ] **Step 5: Run the tests**

```bash
cd /Users/belazy/personal/argus/backend && go test ./internal/memory/ -run 'TestEmbedderAvailable|TestWarmVectorProbe' -count=1 -v 2>&1 | tail -10 && go build ./... && go vet ./internal/memory/ ./internal/app/
```

Expected: unit tests PASS; the pg test PASSes with `TEST_DATABASE_URL`; build and vet clean.

- [ ] **Step 6: Commit**

```bash
cd /Users/belazy/personal/argus
git add backend/internal/memory/registry.go backend/internal/memory/registry_mcp_test.go backend/internal/app/app.go
git commit -m "feat(memory): report embedder availability and warm the vector probe at boot

EmbedderAvailable lets a caller distinguish 'embeddings are off' from 'no
results': with a nil embedder every positive-threshold Search returns
(nil, nil), and nothing exported said why.

WarmVectorProbe closes a latent footgun. usesPGContextVector is a
process-wide sync.Once that runs on the first search with that caller's
context. A short-deadline first search errors the probe, latches
pgvector, and on a pgcontext column breaks every later vector operation
until restart. Boot now latches it from a long-lived context."
```

---

### Task 4: Exclude `/mcp` from body logging (N6)

**Files:**
- Modify: `backend/internal/api/middleware_logging.go:44-109`
- Test: `backend/internal/api/middleware_logging_mcp_test.go`

**Interfaces:**
- Produces: `func skipBodyLogging(path string) bool`.

- [ ] **Step 1: Write the failing test**

Create `backend/internal/api/middleware_logging_mcp_test.go`:

```go
package api

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestSkipBodyLogging(t *testing.T) {
	t.Parallel()
	tests := []struct {
		path string
		skip bool
	}{
		{"/webhooks/github", true},
		{"/mcp", true},
		{"/mcp/", true},
		{"/mcp/anything", true},
		{"/mcpx", false},
		{"/api/v1/reviews", false},
		{"/", false},
	}
	for _, tt := range tests {
		if got := skipBodyLogging(tt.path); got != tt.skip {
			t.Errorf("skipBodyLogging(%q) = %v, want %v", tt.path, got, tt.skip)
		}
	}
}

// Neither marker may appear in any log record — MCP requests and responses
// carry memory content and review findings derived from private source code —
// and that must hold for tool errors and auth failures too, not just 200s.
func TestRequestLoggingOmitsMCPBodies(t *testing.T) {
	t.Parallel()
	const reqMarker = "REQ-MARKER-8f3a-MUST-NOT-LOG"
	const respMarker = "RESP-MARKER-2c1d-MUST-NOT-LOG"
	var logs bytes.Buffer
	s := &Server{logger: slog.New(slog.NewJSONHandler(&logs, nil))}

	for _, tc := range []struct {
		name   string
		path   string
		status int
	}{
		{"ok", "/mcp", http.StatusOK},
		{"trailing slash", "/mcp/", http.StatusOK},
		{"auth failure", "/mcp", http.StatusUnauthorized},
		{"tool error", "/mcp", http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs.Reset()
			inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if !strings.Contains(string(body), reqMarker) {
					t.Fatal("handler did not receive the request body — exclusion must not consume it")
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"body":"` + respMarker + `"}`))
			})
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(`{"q":"`+reqMarker+`"}`))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			s.requestLogging(inner).ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d", rec.Code, tc.status)
			}
			out := logs.String()
			if strings.Contains(out, reqMarker) || strings.Contains(out, respMarker) {
				t.Fatalf("a body marker reached the log:\n%s", out)
			}
			if !strings.Contains(out, "inbound HTTP request completed") || !strings.Contains(out, `"status":`+strconv.Itoa(tc.status)) {
				t.Fatalf("the completion record with status must still be written:\n%s", out)
			}
		})
	}

	// Control: a non-excluded path still logs its body, proving the assertions
	// above are exercising body logging at all.
	logs.Reset()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/anything", strings.NewReader(reqMarker))
	req.Header.Set("Content-Type", "text/plain")
	s.requestLogging(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })).ServeHTTP(httptest.NewRecorder(), req)
	if !strings.Contains(logs.String(), reqMarker) {
		t.Fatal("control path did not log its body; the exclusion assertions are vacuous")
	}
}
```

If the completion record's status attribute is named differently in `requestLogging` (check the `s.logger.InfoContext(... "inbound HTTP request completed" ...)` call for its key), adjust the `"status":` substring to that key.

- [ ] **Step 2: Run to verify failure**

```bash
cd /Users/belazy/personal/argus/backend && go test ./internal/api/ -run 'TestSkipBodyLogging|TestRequestLoggingOmitsMCPBodies' 2>&1 | tail -6
```

Expected: build failure `undefined: skipBodyLogging`.

- [ ] **Step 3: Implement the exclusion**

In `backend/internal/api/middleware_logging.go`, add above `requestLogging`:

```go
// skipBodyLogging names the paths whose request AND response bodies must not
// reach the payload log. /webhooks/github was excluded on the request side
// already. /mcp carries memory content and review findings derived from
// private source code in both directions — including 401 challenges and tool
// error bodies — and the previous unconditional response tee would have
// written customer code to Fly logs on the first call.
func skipBodyLogging(path string) bool {
	return path == "/webhooks/github" || path == "/mcp" || strings.HasPrefix(path, "/mcp/")
}
```

In `requestLogging`, request side (line ~62) — replace `&& r.URL.Path != "/webhooks/github"` with `&& !skipBodyLogging(r.URL.Path)`:

```go
		if r.Body != nil && r.Body != http.NoBody && r.ContentLength != 0 && !skipBodyLogging(r.URL.Path) {
```

Response side (lines ~76-84) — make the tee conditional:

```go
		var responseBody *obs.PayloadStream
		ww := chimiddleware.NewWrapResponseWriter(w, r.ProtoMajor)
		if !skipBodyLogging(r.URL.Path) {
			responseBody = obs.NewPayloadStream(r.Context(), s.logger, "inbound HTTP response body", operationID, "response", "")
			ww.Tee(responseBody)
		}

		next.ServeHTTP(ww, r)

		requestBytes, requestChunks := int64(0), 0
		if requestBody != nil {
			requestBytes, requestChunks = requestBody.Finish()
		}
		responseBytes, responseChunks := int64(0), 0
		if responseBody != nil {
			responseBytes, responseChunks = responseBody.Finish()
		}
```

Everything after (status, route, the completion log line) is unchanged.

- [ ] **Step 4: Run the tests**

```bash
cd /Users/belazy/personal/argus/backend && go test ./internal/api/ -run 'TestSkipBodyLogging|TestRequestLoggingOmitsMCPBodies' -count=1 -v 2>&1 | tail -12 && go test ./internal/api/ -count=1 2>&1 | tail -3
```

Expected: PASS; the whole `api` package still passes.

- [ ] **Step 5: Commit**

```bash
cd /Users/belazy/personal/argus
git add backend/internal/api/middleware_logging.go backend/internal/api/middleware_logging_mcp_test.go
git commit -m "fix(api): keep /mcp bodies out of the payload log

requestLogging excluded only the webhook request body; every response body
was teed into the payload log unconditionally. MCP responses carry memory
content and review findings derived from private source code, so the first
tool call would have written customer code to Fly logs.

skipBodyLogging now names both excluded paths and applies to request and
response alike, for every status including auth failures and tool errors.
The completion record still fires with status, route and duration."
```

---

### Task 5: Auth — `verifyJWT`, MCP token policy, claims adapter, protected-resource metadata (N5a, N5b, N5c)

**Files:**
- Modify: `backend/internal/api/middleware.go:130-188` (extract `verifyJWT`; keep `validateToken` as wrapper)
- Create: `backend/internal/api/mcp_auth.go`
- Test: `backend/internal/api/mcp_auth_test.go`

**Interfaces:**
- Produces:
  ```go
  type decodedClaims struct{ Sub, Iss, OrgID, OrgRole, Sid, Scope string; Aud, Scp []string; Exp, Nbf float64 }
  func verifyJWT(raw string) (decodedClaims, error)
  const scopeRead = "argus:read"; const scopeMemoryWrite = "argus:memory:write"; const scopeOrgRead = "user:org:read"
  func grantedScopes(c decodedClaims) []string
  func (s *Server) mcpTokenVerifier(ctx context.Context, token string, req *http.Request) (*auth.TokenInfo, error)
  func mcpClaimsToContext(next http.Handler) http.Handler
  func getMCPScopes(ctx context.Context) []string
  func (s *Server) mcpResourceMetadataURL() string
  func (s *Server) handleProtectedResourceMetadata(w http.ResponseWriter, r *http.Request)
  ```
- Consumes: `config.Config.ClerkIssuerURL`, `MCPResourceURL` (Task 1).

- [ ] **Step 1: Write the failing tests**

Create `backend/internal/api/mcp_auth_test.go`:

```go
package api

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/BeLazy167/argus/backend/internal/config"
)

// testJWKS serves one RSA key and points the package-level JWKS cache at it.
// The cache is package state, so tests using this must not run in parallel
// with other JWKS-dependent tests.
func testJWKS(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	const kid = "test-kid"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": kid, "alg": "RS256", "use": "sig",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	}))
	t.Cleanup(srv.Close)
	InitJWKS(srv.URL, slog.New(slog.DiscardHandler))
	t.Cleanup(func() { cache = nil })
	return key, kid
}

func signTestJWT(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	enc := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(b)
	}
	signing := enc(map[string]string{"alg": "RS256", "kid": kid}) + "." + enc(claims)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

const (
	testIssuer   = "https://clerk.example"
	testResource = "https://api.example/mcp"
)

// accessTokenClaims is a valid MCP access token; tests mutate one field.
func accessTokenClaims() map[string]any {
	now := time.Now()
	return map[string]any{
		"sub": "user_1", "iss": testIssuer, "aud": testResource,
		"exp": float64(now.Add(time.Hour).Unix()), "nbf": float64(now.Add(-time.Minute).Unix()),
		"org_id": "org_1", "org_role": "org:member",
		"scope": "argus:read argus:memory:write user:org:read",
	}
}

func mcpTestServer() *Server {
	return &Server{logger: slog.New(slog.DiscardHandler), cfg: &config.Config{
		MCPEnabled: true, ClerkIssuerURL: testIssuer, MCPResourceURL: testResource,
	}}
}

func TestVerifyJWTDecodesMCPClaims(t *testing.T) {
	key, kid := testJWKS(t)
	c, err := verifyJWT(signTestJWT(t, key, kid, accessTokenClaims()))
	if err != nil {
		t.Fatal(err)
	}
	if c.Sub != "user_1" || c.Iss != testIssuer || len(c.Aud) != 1 || c.Aud[0] != testResource || c.OrgID != "org_1" || c.Nbf == 0 || c.Scope == "" {
		t.Fatalf("claims = %+v", c)
	}
	arr := accessTokenClaims()
	arr["aud"] = []string{"a", testResource}
	c, err = verifyJWT(signTestJWT(t, key, kid, arr))
	if err != nil || len(c.Aud) != 2 {
		t.Fatalf("array aud: %+v %v", c, err)
	}
	// The dashboard path is unchanged in behavior.
	sess := map[string]any{"sub": "user_1", "org_id": "org_9", "exp": float64(time.Now().Add(time.Hour).Unix())}
	jc, err := validateToken(signTestJWT(t, key, kid, sess))
	if err != nil || jc.Sub != "user_1" || jc.OrgID != "org_9" {
		t.Fatalf("validateToken = %+v, %v", jc, err)
	}
}

func TestGrantedScopes(t *testing.T) {
	t.Parallel()
	if got := grantedScopes(decodedClaims{Scope: "a  b c"}); len(got) != 3 || got[2] != "c" {
		t.Fatalf("scope string: %v", got)
	}
	if got := grantedScopes(decodedClaims{Scp: []string{"x", "y"}}); len(got) != 2 {
		t.Fatalf("scp array: %v", got)
	}
	if got := grantedScopes(decodedClaims{}); len(got) != 0 {
		t.Fatalf("none: %v", got)
	}
}

func TestMCPTokenVerifierPolicy(t *testing.T) {
	key, kid := testJWKS(t)
	s := mcpTestServer()
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"wrong issuer", func(c map[string]any) { c["iss"] = "https://evil.example" }},
		{"wrong audience", func(c map[string]any) { c["aud"] = "https://other/mcp" }},
		{"missing audience", func(c map[string]any) { delete(c, "aud") }},
		{"expired", func(c map[string]any) { c["exp"] = float64(time.Now().Add(-time.Minute).Unix()) }},
		{"not yet valid", func(c map[string]any) { c["nbf"] = float64(time.Now().Add(time.Hour).Unix()) }},
		{"missing subject", func(c map[string]any) { delete(c, "sub") }},
		{"missing org", func(c map[string]any) { delete(c, "org_id") }},
		{"session token (sid)", func(c map[string]any) { c["sid"] = "sess_123" }},
		{"no scopes", func(c map[string]any) { delete(c, "scope") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims := accessTokenClaims()
			tt.mutate(claims)
			_, err := s.mcpTokenVerifier(context.Background(), signTestJWT(t, key, kid, claims), nil)
			if err == nil || !errors.Is(err, auth.ErrInvalidToken) {
				t.Fatalf("err = %v, want ErrInvalidToken", err)
			}
		})
	}

	info, err := s.mcpTokenVerifier(context.Background(), signTestJWT(t, key, kid, accessTokenClaims()), nil)
	if err != nil {
		t.Fatal(err)
	}
	if info.UserID != "user_1" || info.Extra["org_id"] != "org_1" || info.Extra["org_role"] != "org:member" || len(info.Scopes) != 3 {
		t.Fatalf("TokenInfo = %+v", info)
	}
	// An opaque token is not a JWT and must be rejected the same way.
	if _, err := s.mcpTokenVerifier(context.Background(), "oat_opaque_token_value", nil); !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatalf("opaque token: err = %v", err)
	}
}

func TestMCPClaimsToContext(t *testing.T) {
	t.Parallel()
	var got struct {
		user, org, role string
		scopes          []string
	}
	inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got.user, got.org, got.role = getUserID(r.Context()), getOrgID(r.Context()), getOrgRole(r.Context())
		got.scopes = getMCPScopes(r.Context())
	})
	stub := func(context.Context, string, *http.Request) (*auth.TokenInfo, error) {
		return &auth.TokenInfo{UserID: "user_7", Scopes: []string{scopeRead}, Extra: map[string]any{"org_id": "org_3", "org_role": "org:member"}}, nil
	}
	h := auth.RequireBearerToken(stub, nil)(mcpClaimsToContext(inner))
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer any")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if got.user != "user_7" || got.org != "org_3" || got.role != "org:member" || len(got.scopes) != 1 || got.scopes[0] != scopeRead {
		t.Fatalf("context = %+v", got)
	}
}

func TestProtectedResourceMetadata(t *testing.T) {
	t.Parallel()
	s := mcpTestServer()
	rec := httptest.NewRecorder()
	s.handleProtectedResourceMetadata(rec, httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body struct {
		Resource               string   `json:"resource"`
		AuthorizationServers   []string `json:"authorization_servers"`
		ScopesSupported        []string `json:"scopes_supported"`
		BearerMethodsSupported []string `json:"bearer_methods_supported"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Resource != testResource || len(body.AuthorizationServers) != 1 || body.AuthorizationServers[0] != testIssuer {
		t.Fatalf("body = %+v", body)
	}
	if strings.Join(body.ScopesSupported, " ") != "argus:read argus:memory:write user:org:read" || len(body.BearerMethodsSupported) != 1 || body.BearerMethodsSupported[0] != "header" {
		t.Fatalf("body = %+v", body)
	}
	if got := s.mcpResourceMetadataURL(); got != "https://api.example/.well-known/oauth-protected-resource" {
		t.Fatalf("metadata url = %q", got)
	}
}

// End-to-end through the SDK middleware: no token yields a 401 whose
// WWW-Authenticate points at our metadata document; a valid token reaches the
// handler with the user id in the context the rest of the api package reads.
func TestMCPAuthChainChallengeAndPassThrough(t *testing.T) {
	key, kid := testJWKS(t)
	s := mcpTestServer()
	var seenUser string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenUser = getUserID(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})
	h := auth.RequireBearerToken(s.mcpTokenVerifier, &auth.RequireBearerTokenOptions{
		ResourceMetadataURL: s.mcpResourceMetadataURL(),
	})(mcpClaimsToContext(inner))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: status = %d, want 401", rec.Code)
	}
	if www := rec.Header().Get("WWW-Authenticate"); !strings.Contains(www, "resource_metadata=") || !strings.Contains(www, "/.well-known/oauth-protected-resource") {
		t.Fatalf("WWW-Authenticate = %q, want a resource_metadata challenge", www)
	}

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+signTestJWT(t, key, kid, accessTokenClaims()))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("valid token: status = %d body=%s", rec.Code, rec.Body.String())
	}
	if seenUser != "user_1" {
		t.Fatalf("handler saw user %q, want user_1", seenUser)
	}
}
```

- [ ] **Step 2: Run to verify failure**

```bash
cd /Users/belazy/personal/argus/backend && go test ./internal/api/ -run 'TestVerifyJWT|TestGrantedScopes|TestMCPToken|TestMCPClaims|TestProtectedResource|TestMCPAuthChain' 2>&1 | tail -6
```

Expected: build failure — `verifyJWT`, `decodedClaims`, `grantedScopes`, `mcpTokenVerifier`, `mcpClaimsToContext`, `getMCPScopes`, `scopeRead`, `handleProtectedResourceMetadata`, `mcpResourceMetadataURL` undefined.

- [ ] **Step 3: Extract `verifyJWT` in middleware.go**

Replace lines 130-188 of `backend/internal/api/middleware.go` (the `jwtClaims` type and `validateToken`) with:

```go
type jwtClaims struct {
	Sub     string
	OrgID   string
	OrgRole string
}

// decodedClaims is everything verifyJWT reads from a verified payload. The
// dashboard path (validateToken) uses sub/org_id/org_role; the MCP path
// additionally pins iss/aud, checks nbf, and reads sid and the scope claims.
// Decoding is shared; policy is not — the dashboard's acceptance rules are
// deliberately unchanged.
type decodedClaims struct {
	Sub     string
	Iss     string
	Aud     []string
	OrgID   string
	OrgRole string
	Sid     string
	Scope   string
	Scp     []string
	Exp     float64
	Nbf     float64
}

// stringOrArray accepts a JSON string or array of strings (RFC 7519 `aud`;
// also the `scp`/`scopes` claim shapes).
type stringOrArray []string

func (a *stringOrArray) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*a = stringOrArray{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	*a = stringOrArray(many)
	return nil
}

// verifyJWT checks the token's structure, RS256 signature against the JWKS
// cache, and expiry, then returns the decoded claims. Every error string is a
// fixed literal — jwtAuth echoes them to the client.
func verifyJWT(raw string) (decodedClaims, error) {
	if cache == nil || cache.url == "" {
		return decodedClaims{}, fmt.Errorf("JWKS not configured")
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return decodedClaims{}, fmt.Errorf("invalid token format")
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return decodedClaims{}, fmt.Errorf("invalid token header")
	}
	var header struct {
		Kid string `json:"kid"`
		Alg string `json:"alg"`
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return decodedClaims{}, fmt.Errorf("invalid token header")
	}
	if header.Alg != "RS256" {
		return decodedClaims{}, fmt.Errorf("unsupported signing algorithm")
	}
	pubKey, err := cache.getKey(header.Kid)
	if err != nil {
		return decodedClaims{}, fmt.Errorf("unknown signing key")
	}
	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return decodedClaims{}, fmt.Errorf("invalid signature")
	}
	hash := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(pubKey, crypto.SHA256, hash[:], sigBytes); err != nil {
		return decodedClaims{}, fmt.Errorf("invalid signature")
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return decodedClaims{}, fmt.Errorf("invalid token payload")
	}
	var claims struct {
		Sub     string        `json:"sub"`
		Iss     string        `json:"iss"`
		Aud     stringOrArray `json:"aud"`
		Exp     float64       `json:"exp"`
		Nbf     float64       `json:"nbf"`
		OrgID   string        `json:"org_id"`
		OrgRole string        `json:"org_role"`
		Sid     string        `json:"sid"`
		Scope   string        `json:"scope"`
		Scp     stringOrArray `json:"scp"`
		Scopes  stringOrArray `json:"scopes"`
	}
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return decodedClaims{}, fmt.Errorf("invalid claims")
	}
	if time.Now().Unix() > int64(claims.Exp) {
		return decodedClaims{}, fmt.Errorf("token expired")
	}
	scp := []string(claims.Scp)
	if len(scp) == 0 {
		scp = []string(claims.Scopes)
	}
	return decodedClaims{
		Sub: claims.Sub, Iss: claims.Iss, Aud: []string(claims.Aud), OrgID: claims.OrgID, OrgRole: claims.OrgRole,
		Sid: claims.Sid, Scope: claims.Scope, Scp: scp, Exp: claims.Exp, Nbf: claims.Nbf,
	}, nil
}

// validateToken parses and verifies a JWT, returning the claims the dashboard
// API uses. It is verifyJWT minus the MCP-only fields; its checks and error
// strings are unchanged.
func validateToken(raw string) (jwtClaims, error) {
	c, err := verifyJWT(raw)
	if err != nil {
		return jwtClaims{}, err
	}
	return jwtClaims{Sub: c.Sub, OrgID: c.OrgID, OrgRole: c.OrgRole}, nil
}
```

- [ ] **Step 4: Create `mcp_auth.go`**

Create `backend/internal/api/mcp_auth.go`:

```go
package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
)

// OAuth scopes the MCP surface enforces. Defined as custom scopes in Clerk and
// advertised in protected-resource metadata; read tools need scopeRead, the
// three memory mutations need scopeMemoryWrite. user:org:read is Clerk's own
// scope that makes the consent screen ask which org the token is for.
const (
	scopeRead        = "argus:read"
	scopeMemoryWrite = "argus:memory:write"
	scopeOrgRead     = "user:org:read"
)

// nbfSkew is the clock tolerance applied to `nbf`, matching Clerk's SDKs.
const nbfSkew = 5 * time.Second

type mcpContextKey string

const mcpScopesKey mcpContextKey = "mcp_scopes"

// grantedScopes reads the token's granted scopes. Clerk does not document the
// claim name for JWT access tokens, so both standard encodings are accepted:
// RFC 8693/9068 `scope` (space-delimited) and an `scp`/`scopes` array. The
// OAuth acceptance run confirms the real shape; this is the one place to
// adjust if it differs.
func grantedScopes(c decodedClaims) []string {
	if c.Scope != "" {
		return strings.Fields(c.Scope)
	}
	return c.Scp
}

// mcpResourceMetadataURL is where RequireBearerToken points a 401 challenge:
// the RFC 9728 well-known document at the resource's origin.
func (s *Server) mcpResourceMetadataURL() string {
	u, err := url.Parse(s.cfg.MCPResourceURL)
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host + "/.well-known/oauth-protected-resource"
}

// mcpTokenVerifier is the ONLY token verifier the MCP path uses, adapting the
// existing JWKS verifier to the SDK's auth.TokenVerifier and adding the
// MCP-specific policy the dashboard path does not need:
//
//   - iss pinned to the configured Clerk issuer;
//   - aud must contain the configured resource URL (confused-deputy
//     protection — a token minted for another API must not work here);
//   - nbf honored; sub and org_id required (org selection is what scopes the
//     connection to one tenant);
//   - a `sid` claim marks a Clerk session token, which is not an MCP
//     credential; opaque tokens fail structural parsing;
//   - at least one granted scope, copied into TokenInfo.Scopes.
//
// Every rejection wraps auth.ErrInvalidToken so the SDK emits a 401 with the
// resource_metadata challenge, which is what tells a client to (re)authorize.
func (s *Server) mcpTokenVerifier(_ context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
	c, err := verifyJWT(token)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", auth.ErrInvalidToken, err.Error())
	}
	if c.Sid != "" {
		return nil, fmt.Errorf("%w: session tokens are not accepted", auth.ErrInvalidToken)
	}
	if c.Sub == "" {
		return nil, fmt.Errorf("%w: missing subject", auth.ErrInvalidToken)
	}
	if c.Iss != s.cfg.ClerkIssuerURL {
		return nil, fmt.Errorf("%w: issuer mismatch", auth.ErrInvalidToken)
	}
	if !slices.Contains(c.Aud, s.cfg.MCPResourceURL) {
		return nil, fmt.Errorf("%w: audience mismatch", auth.ErrInvalidToken)
	}
	if c.Nbf > 0 && time.Now().Add(nbfSkew).Unix() < int64(c.Nbf) {
		return nil, fmt.Errorf("%w: token not yet valid", auth.ErrInvalidToken)
	}
	if c.OrgID == "" {
		return nil, fmt.Errorf("%w: organization selection required (authorize with scope %s)", auth.ErrInvalidToken, scopeOrgRead)
	}
	scopes := grantedScopes(c)
	if len(scopes) == 0 {
		return nil, fmt.Errorf("%w: token carries no scopes", auth.ErrInvalidToken)
	}
	return &auth.TokenInfo{
		UserID:     c.Sub,
		Scopes:     scopes,
		Expiration: time.Unix(int64(c.Exp), 0),
		Extra:      map[string]any{"org_id": c.OrgID, "org_role": c.OrgRole},
	}, nil
}

// mcpClaimsToContext copies the verified TokenInfo into the context keys the
// rest of the api package reads (getUserID/getOrgID/getOrgRole) plus the
// granted scopes, so requireMCPInstallationScope and mcpGetServer need no
// SDK types.
func mcpClaimsToContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info := auth.TokenInfoFromContext(r.Context())
		if info == nil || info.UserID == "" {
			// RequireBearerToken already rejected; defensive against a
			// misordered chain.
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		ctx := context.WithValue(r.Context(), userIDKey, info.UserID)
		ctx = context.WithValue(ctx, mcpScopesKey, info.Scopes)
		if org, _ := info.Extra["org_id"].(string); org != "" {
			ctx = context.WithValue(ctx, orgIDKey, org)
		}
		if role, _ := info.Extra["org_role"].(string); role != "" {
			ctx = context.WithValue(ctx, orgRoleKey, role)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func getMCPScopes(ctx context.Context) []string {
	scopes, _ := ctx.Value(mcpScopesKey).([]string)
	return scopes
}

// protectedResourceMetadata is the RFC 9728 document MCP clients fetch to
// discover which authorization server to obtain a token from and which
// scopes to ask for.
type protectedResourceMetadata struct {
	Resource               string   `json:"resource"`
	AuthorizationServers   []string `json:"authorization_servers"`
	ScopesSupported        []string `json:"scopes_supported"`
	BearerMethodsSupported []string `json:"bearer_methods_supported"`
}

func (s *Server) handleProtectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	op := s.beginOperation(r.Context(), "api.mcp.protected_resource_metadata")
	defer op.Finish(w)
	writeJSON(w, http.StatusOK, protectedResourceMetadata{
		Resource:               s.cfg.MCPResourceURL,
		AuthorizationServers:   []string{s.cfg.ClerkIssuerURL},
		ScopesSupported:        []string{scopeRead, scopeMemoryWrite, scopeOrgRead},
		BearerMethodsSupported: []string{"header"},
	})
}
```

- [ ] **Step 5: Run the tests**

```bash
cd /Users/belazy/personal/argus/backend && go test ./internal/api/ -run 'TestVerifyJWT|TestGrantedScopes|TestMCPToken|TestMCPClaims|TestProtectedResource|TestMCPAuthChain' -count=1 -v 2>&1 | tail -30 && go test ./internal/api/ -count=1 2>&1 | tail -3 && go vet ./internal/api/
```

Expected: all PASS, including every existing middleware test (validateToken's behavior is unchanged); vet clean.

- [ ] **Step 6: Commit**

```bash
cd /Users/belazy/personal/argus
git add backend/internal/api/middleware.go backend/internal/api/mcp_auth.go backend/internal/api/mcp_auth_test.go
git commit -m "feat(api): MCP token policy, claims adapter, RFC 9728 metadata

The MCP spec makes the server an OAuth 2.1 resource server: it validates
tokens Clerk issued, advertises Clerk in protected-resource metadata, and
answers unauthenticated calls with 401 + WWW-Authenticate pointing at it.

validateToken is split: verifyJWT does the structural, signature and
expiry checks and returns every claim (now including iss, aud, nbf, sid
and the scope claims); validateToken is a thin wrapper with identical
behavior. mcpTokenVerifier adds the MCP-only policy on top — issuer and
resource-audience pinning, nbf, required sub and org_id, rejection of
session (sid) and opaque tokens, at least one granted scope — without
broadening what the dashboard accepts. mcpClaimsToContext copies the
verified claims and scopes into the context keys the api package reads."
```

---

### Task 6: `requireMCPInstallationScope` (N5d), server skeleton, route, `list_repos`

**Files:**
- Modify: `backend/internal/api/mcp_auth.go` (append `requireMCPInstallationScope`)
- Create: `backend/internal/api/mcp_server.go`
- Create: `backend/internal/api/mcp_tools_discovery.go`
- Modify: `backend/internal/api/server.go` (`indexers` field after `memRegistry` line 71; set inside `if memRegistry != nil` block line ~112; call `registerMCPRoutes(r)` after line 145)
- Test: `backend/internal/api/mcp_server_test.go`, `backend/internal/api/mcp_tools_discovery_test.go`

**Interfaces:**
- Produces:
  ```go
  func (s *Server) requireMCPInstallationScope(next http.Handler) http.Handler
  var errNotAccessible = errors.New("not found or not accessible")
  var errMemoryUnavailable = errors.New("memory backend unavailable")
  func errInsufficientScope(scope string) error   // "insufficient scope: <scope> required"
  type tenantScope struct { userID, orgID string; installationIDs []int64; grantedScopes []string }
  type indexerSource interface { GetIndexer(context.Context, int64) memory.Indexer; EmbedderAvailable(context.Context, int64) bool }
  type mcpTools struct { srv *Server; scope tenantScope }
  func (t *mcpTools) requireScope(scope string) error
  func (t *mcpTools) internalErr(ctx context.Context, tool string, err error) error
  func (s *Server) resolveIndexer(ctx context.Context, installationID int64) memory.Indexer
  func (s *Server) newMCPServer(scope tenantScope) *mcp.Server
  func registerMCPTools(server *mcp.Server, t *mcpTools)
  func (s *Server) mcpGetServer(r *http.Request) *mcp.Server
  func (s *Server) registerMCPRoutes(r chi.Router)
  ```
  and the `Server.indexers indexerSource` field. Later tasks add `AddTool` calls inside `registerMCPTools`.
- Consumes: Task 5's symbols; `resolveInstallationIDs` (middleware.go:193); `store.SetInstallationClerkOrgID(ctx, installationID, clerkOrgID)`, `store.LinkUserInstallation(ctx, clerkUserID, installationID, role)` (tests).

- [ ] **Step 1: Write the failing tests**

Create `backend/internal/api/mcp_server_test.go`:

```go
package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BeLazy167/argus/backend/internal/config"
	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/store"
)

// stubIndexers is the test double for the indexerSource seam.
type stubIndexers struct {
	indexer   memory.Indexer
	available bool
}

func (s *stubIndexers) GetIndexer(context.Context, int64) memory.Indexer { return s.indexer }
func (s *stubIndexers) EmbedderAvailable(context.Context, int64) bool   { return s.available }

func TestRegisterMCPRoutesDisabledIs404(t *testing.T) {
	t.Parallel()
	s := &Server{logger: slog.New(slog.DiscardHandler), cfg: &config.Config{MCPEnabled: false}}
	r := chi.NewRouter()
	s.registerMCPRoutes(r)
	for _, path := range []string{"/mcp", "/.well-known/oauth-protected-resource"} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s with MCP disabled: status = %d, want 404", path, rec.Code)
		}
	}
}

func TestRegisterMCPRoutesEnabledServesMetadataAndChallenges(t *testing.T) {
	t.Parallel()
	s := mcpTestServer()
	r := chi.NewRouter()
	s.registerMCPRoutes(r)
	for _, path := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d body=%s", path, rec.Code, rec.Body.String())
		}
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	if rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("unauthenticated /mcp: status = %d, WWW-Authenticate = %q", rec.Code, rec.Header().Get("WWW-Authenticate"))
	}
}

// Registered once at boot as a schema smoke test: an invalid input/output
// schema panics here, at startup, not on the first production request.
func TestNewMCPServerRegistersAllToolsWithoutPanic(t *testing.T) {
	t.Parallel()
	if mcpTestServer().newMCPServer(tenantScope{}) == nil {
		t.Fatal("newMCPServer returned nil")
	}
}

func TestResolveIndexerNilWhenUnwired(t *testing.T) {
	t.Parallel()
	s := &Server{logger: slog.New(slog.DiscardHandler)}
	if idx := s.resolveIndexer(context.Background(), 1); idx != nil {
		t.Fatalf("unwired server returned an indexer: %v", idx)
	}
	s.indexers = &stubIndexers{}
	if idx := s.resolveIndexer(context.Background(), 1); idx != nil {
		t.Fatalf("stub returning nil indexer should propagate nil, got %v", idx)
	}
}

func TestRequireScope(t *testing.T) {
	t.Parallel()
	tools := &mcpTools{scope: tenantScope{grantedScopes: []string{scopeRead}}}
	if err := tools.requireScope(scopeRead); err != nil {
		t.Fatal(err)
	}
	err := tools.requireScope(scopeMemoryWrite)
	if err == nil || err.Error() != "insufficient scope: argus:memory:write required" {
		t.Fatalf("err = %v", err)
	}
}

// requireMCPInstallationScope: only the org selected during consent, never
// the user's whole installation set, and a hint can only narrow within it.
func TestRequireMCPInstallationScope(t *testing.T) {
	pool, ctx := architectureTestPool(t)
	installA, _ := seedArchitectureRepo(t, ctx, pool)
	installB, _ := seedArchitectureRepo(t, ctx, pool)
	st := store.NewWithDB(pool)
	orgA := "org_mcp_scope_" + strconv.FormatInt(installA, 10)
	if err := st.SetInstallationClerkOrgID(ctx, installA, orgA); err != nil {
		t.Fatal(err)
	}
	const user = "user_mcp_scope"
	// The user belongs to BOTH installations via user_installations; the org
	// claim must still confine the request to A.
	for _, id := range []int64{installA, installB} {
		if _, err := st.LinkUserInstallation(ctx, user, id, "owner"); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM user_installations WHERE clerk_user_id = $1`, user) })
	s := &Server{store: st, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	var seen []int64
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = getInstallationIDs(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})
	call := func(org, hint string) int {
		seen = nil
		req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		ctx := context.WithValue(req.Context(), userIDKey, user)
		if org != "" {
			ctx = context.WithValue(ctx, orgIDKey, org)
		}
		if hint != "" {
			req.Header.Set("X-Installation-ID", hint)
		}
		rec := httptest.NewRecorder()
		s.requireMCPInstallationScope(inner).ServeHTTP(rec, req.WithContext(ctx))
		return rec.Code
	}

	if code := call(orgA, ""); code != http.StatusNoContent || len(seen) != 1 || seen[0] != installA {
		t.Fatalf("selected org: code=%d ids=%v, want [%d]", code, seen, installA)
	}
	if code := call("", ""); code != http.StatusUnauthorized {
		t.Fatalf("missing org claim: code=%d, want 401 (must never fall back to the user's installations)", code)
	}
	if code := call("org_unlinked_"+strconv.FormatInt(installA, 10), ""); code != http.StatusForbidden {
		t.Fatalf("org with no installation: code=%d, want 403", code)
	}
	if code := call(orgA, strconv.FormatInt(installB, 10)); code != http.StatusForbidden {
		t.Fatalf("hint for another installation the user belongs to: code=%d, want 403 (REST silently ignores this; MCP must not)", code)
	}
	if code := call(orgA, "not-a-number"); code != http.StatusForbidden {
		t.Fatalf("malformed hint: code=%d, want 403", code)
	}
	if code := call(orgA, strconv.FormatInt(installA, 10)); code != http.StatusNoContent || len(seen) != 1 || seen[0] != installA {
		t.Fatalf("matching hint: code=%d ids=%v", code, seen)
	}
}
```

Create `backend/internal/api/mcp_tools_discovery_test.go`:

```go
package api

import (
	"io"
	"log/slog"
	"testing"

	"github.com/BeLazy167/argus/backend/internal/store"
)

func TestListReposIsScopedToCaller(t *testing.T) {
	pool, ctx := architectureTestPool(t)
	installA, repoA := seedArchitectureRepo(t, ctx, pool)
	installB, repoB := seedArchitectureRepo(t, ctx, pool)
	s := &Server{store: store.NewWithDB(pool), logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	tools := &mcpTools{srv: s, scope: tenantScope{userID: "user_a", installationIDs: []int64{installA}, grantedScopes: []string{scopeRead}}}
	_, out, err := tools.listRepos(ctx, nil, listReposInput{})
	if err != nil {
		t.Fatalf("listRepos: %v", err)
	}
	var sawA, sawB bool
	for _, r := range out.Repos {
		if r.RepoID == repoA {
			sawA = true
			if r.InstallationID != installA {
				t.Fatalf("repo %d reported installation %d, want %d", r.RepoID, r.InstallationID, installA)
			}
		}
		sawB = sawB || r.RepoID == repoB
	}
	if !sawA {
		t.Fatal("caller's own repo missing from list_repos")
	}
	if sawB {
		t.Fatalf("list_repos leaked repo %d from installation %d into installation %d's scope", repoB, installB, installA)
	}

	// A token without argus:read cannot even list.
	noRead := &mcpTools{srv: s, scope: tenantScope{installationIDs: []int64{installA}, grantedScopes: []string{scopeMemoryWrite}}}
	if _, _, err := noRead.listRepos(ctx, nil, listReposInput{}); err == nil || err.Error() != errInsufficientScope(scopeRead).Error() {
		t.Fatalf("missing read scope: err = %v", err)
	}
}
```

- [ ] **Step 2: Run to verify failure**

```bash
cd /Users/belazy/personal/argus/backend && go test ./internal/api/ -run 'TestRegisterMCPRoutes|TestNewMCPServer|TestResolveIndexer|TestRequireScope|TestRequireMCPInstallationScope|TestListRepos' 2>&1 | tail -6
```

Expected: build failure — the Task 6 symbols are undefined.

- [ ] **Step 3: Append `requireMCPInstallationScope` to `mcp_auth.go`**

```go
// requireMCPInstallationScope confines the request to the org the user
// selected during OAuth consent.
//
// It reuses resolveInstallationIDs' org branch — which maps org_id to the
// linked installation and auto-links the user — but never its user-wide
// fallback: a missing org claim is a 401 here, not a lookup of everything the
// user can see. The REST middleware silently ignores a malformed or
// non-member X-Installation-ID and returns the full set (middleware.go:229);
// this wrapper rejects both, so a hint can only ever narrow within the org.
func (s *Server) requireMCPInstallationScope(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		op := s.beginOperation(r.Context(), "middleware.mcp_installation_scope")
		defer op.Finish(w)
		userID, orgID := getUserID(r.Context()), getOrgID(r.Context())
		if userID == "" || orgID == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "organization selection required"})
			return
		}
		claims := jwtClaims{Sub: userID, OrgID: orgID, OrgRole: getOrgRole(r.Context())}
		ids, err := s.resolveInstallationIDs(r.Context(), claims, "")
		if err != nil {
			s.logger.WarnContext(r.Context(), "mcp installation scope", "user", userID, "org", orgID, "error", err)
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "no installation linked to the selected organization"})
			return
		}
		if hint := r.Header.Get("X-Installation-ID"); hint != "" {
			id, perr := strconv.ParseInt(hint, 10, 64)
			if perr != nil || !containsID(ids, id) {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "installation hint conflicts with the selected organization"})
				return
			}
			ids = []int64{id}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), installationIDsKey, ids)))
	})
}
```

Add `"strconv"` to `mcp_auth.go`'s imports.

- [ ] **Step 4: Create `mcp_server.go`**

```go
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

	"github.com/go-chi/chi/v5"
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

// newMCPServer builds a per-request server whose tools are closed over scope.
// It performs no I/O.
func (s *Server) newMCPServer(scope tenantScope) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "argus", Title: "Argus", Version: "1"}, &mcp.ServerOptions{
		Instructions: "Argus code-review memory and review results for the organization you authorized. Call list_repos first to resolve repo_id and installation_id; every other tool takes those local ids, never GitHub ids. Read tools need the argus:read scope; create_memory, delete_memory and retire_memory need argus:memory:write.",
		Logger:       s.logger,
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
```

- [ ] **Step 5: Create `mcp_tools_discovery.go`**

```go
package api

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type listReposInput struct{}

type repoSummary struct {
	RepoID         int64  `json:"repo_id" jsonschema:"local repos.id — pass as repo_id to other tools"`
	FullName       string `json:"full_name" jsonschema:"owner/name as on GitHub"`
	InstallationID int64  `json:"installation_id" jsonschema:"local installations.id that owns this repo — pass as installation_id to other tools"`
	Enabled        bool   `json:"enabled" jsonschema:"whether Argus reviews are enabled for this repo"`
	DefaultBranch  string `json:"default_branch"`
}

type listReposOutput struct {
	Repos []repoSummary `json:"repos"`
}

// listRepos exists because three surfaces disagree about identifiers:
// MemoryQuery.Repo takes the short name, memory tenancy takes the local
// installation id, and review detail carries no full_name at all. One call
// resolves all three. ListReposScoped filters by installation in SQL.
func (t *mcpTools) listRepos(ctx context.Context, _ *mcp.CallToolRequest, _ listReposInput) (*mcp.CallToolResult, listReposOutput, error) {
	if err := t.requireScope(scopeRead); err != nil {
		return nil, listReposOutput{}, err
	}
	repos, err := t.srv.store.ListReposScoped(ctx, t.scope.installationIDs)
	if err != nil {
		return nil, listReposOutput{}, t.internalErr(ctx, "list_repos", err)
	}
	out := listReposOutput{Repos: make([]repoSummary, 0, len(repos))}
	for _, r := range repos {
		out.Repos = append(out.Repos, repoSummary{RepoID: r.ID, FullName: r.FullName, InstallationID: r.InstallationID, Enabled: r.Enabled, DefaultBranch: r.DefaultBranch})
	}
	return nil, out, nil
}
```

- [ ] **Step 6: Wire the `Server` field and the route**

In `backend/internal/api/server.go`, after `memRegistry *memory.Registry` in the struct:

```go
	// indexers is the seam MCP tools use to reach memory; nil means memory is
	// off. Production sets it to memRegistry; tests set a stub.
	indexers indexerSource
```

Inside the existing `if memRegistry != nil {` block in `NewServer`:

```go
	if memRegistry != nil {
		s.indexers = memRegistry
		s.reembedMemories = func(ctx context.Context, installationID int64) (int, error) {
			return memRegistry.ReembedCurrentSpace(ctx, installationID, 100)
		}
	}
```

After the public export route (`r.Get("/api/v1/reviews/{reviewID}/export", s.exportReviewPublic)`) and before the `// API v1` comment:

```go
	// MCP — team memory + review access. Own auth chain (Clerk OAuth resource
	// server); deliberately outside /api/v1's timeout and body logging.
	s.registerMCPRoutes(r)
```

- [ ] **Step 7: Run the tests**

```bash
cd /Users/belazy/personal/argus/backend && go build ./... && go vet ./internal/api/ && go test ./internal/api/ -run 'TestRegisterMCPRoutes|TestNewMCPServer|TestResolveIndexer|TestRequireScope|TestRequireMCPInstallationScope|TestListRepos' -count=1 -v 2>&1 | tail -20
```

Expected: build clean; unit tests PASS; the two pg tests PASS with `TEST_DATABASE_URL`. If the schema smoke test panics, the message names the offending field — fix the struct tag, never catch the panic.

- [ ] **Step 8: Commit**

```bash
cd /Users/belazy/personal/argus
git add backend/internal/api/mcp_auth.go backend/internal/api/mcp_server.go backend/internal/api/mcp_tools_discovery.go backend/internal/api/server.go backend/internal/api/mcp_server_test.go backend/internal/api/mcp_tools_discovery_test.go
git commit -m "feat(api): mount the MCP server at /mcp, scoped to the selected org

Streamable HTTP, Stateless — two Fly machines share no session map, and
stateful mode binds tool handlers to the initialize request's tenant.
Scope (user, org, installation set, granted scopes) is read once in
getServer and captured by value in the closure every handler runs in.

requireMCPInstallationScope confines a request to the org the user chose
at consent: it reuses resolveInstallationIDs' org branch and never its
user-wide fallback, and it rejects malformed or non-member
X-Installation-ID hints that the REST middleware silently ignores.

The route is gated on MCP_ENABLED and 404s when off, like pprof. Tools
are registered once at boot so an invalid schema panics at startup.
list_repos, behind argus:read, is the id-resolution entry point."
```

---

### Task 7: `ListPatternIDsByIdentity` (N9), `search_memory`, `get_memory_briefing`

**Files:**
- Modify: `backend/internal/store/patterns.go` (append `patternIdentityExpr`, `ListPatternIDsByIdentity`)
- Create: `backend/internal/api/mcp_tools_memory_read.go`
- Modify: `backend/internal/api/mcp_server.go` (`registerMCPTools`: two `AddTool` calls)
- Test: `backend/internal/store/pattern_identity_pg_test.go`, `backend/internal/api/mcp_tools_memory_read_test.go`

**Interfaces:**
- Produces: `const patternIdentityExpr = "COALESCE(NULLIF(memory_custom_id, ''), memory_doc_id)"` (store, unexported); `func (s *Store) ListPatternIDsByIdentity(ctx context.Context, installationID int64, identities []string) (map[string][]int64, error)`; `func (t *mcpTools) scopedRepo(ctx, repoID int64) (*store.Repo, string, error)`; handlers `searchMemory`, `getMemoryBriefing`; test helpers `recordingIndexer`, `readScope(...)`, `writeScope(...)`.
- Consumes: `memory.MemoryQuery{Query, Repo, Scope, Type, Limit, Threshold}`, `memory.PatternMatch{ID, Content, Score, Metadata}`, `memory.ScopeRepo/ScopeShared/ScopeBoth`, `memory.RepoTagNew(repo)`, `memory.SharedTag`, `memory.NewThresholds().FindingEnrich`, `memory.BriefingQuery`, `memory.BriefingOptions{Profile, Thresholds, CharCap}`, `memory.ProfileReview/ProfileSpecialist`. The `RetireRequest`/`RetireResult` types referenced by `recordingIndexer` arrive in Task 10 — see the note in Step 4.

- [ ] **Step 1: Write the failing store test**

Create `backend/internal/store/pattern_identity_pg_test.go`:

```go
package store

import (
	"context"
	"testing"
)

func TestListPatternIDsByIdentity(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	installA, _, _ := seedLearnTenant(t, ctx, pool, "mcp-identity-a")
	installB, _, _ := seedLearnTenant(t, ctx, pool, "mcp-identity-b")
	st := NewWithDB(pool)
	src := "manual"
	t.Cleanup(func() {
		bg := context.Background()
		for _, id := range []int64{installA, installB} {
			_, _ = pool.Exec(bg, `DELETE FROM memory_mirror_outbox WHERE installation_id = $1`, id)
			_, _ = pool.Exec(bg, `DELETE FROM patterns WHERE installation_id = $1`, id)
		}
	})

	// custom-id row, two legacy rows sharing a doc id, and the same custom id
	// in another installation (must never contribute).
	cid := "sm_custom_identity"
	doc := "sm_legacy_doc"
	one, err := st.CreatePattern(ctx, installA, nil, "one", nil, nil, &src, nil, nil, &cid, nil)
	if err != nil {
		t.Fatal(err)
	}
	legacy1, err := st.CreatePattern(ctx, installA, nil, "legacy", &doc, nil, &src, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	legacy2, err := st.CreatePattern(ctx, installA, nil, "legacy", &doc, nil, &src, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreatePattern(ctx, installB, nil, "one", nil, nil, &src, nil, nil, &cid, nil); err != nil {
		t.Fatal(err)
	}

	got, err := st.ListPatternIDsByIdentity(ctx, installA, []string{cid, doc, "absent"})
	if err != nil {
		t.Fatal(err)
	}
	if ids := got[cid]; len(ids) != 1 || ids[0] != one.ID {
		t.Fatalf("custom id → %v, want [%d]", ids, one.ID)
	}
	if ids := got[doc]; len(ids) != 2 || ids[0] != legacy1.ID || ids[1] != legacy2.ID {
		t.Fatalf("legacy doc id → %v, want [%d %d] ordered by id", ids, legacy1.ID, legacy2.ID)
	}
	if _, ok := got["absent"]; ok {
		t.Fatal("absent identity must not appear in the map")
	}
	if empty, err := st.ListPatternIDsByIdentity(ctx, installA, nil); err != nil || len(empty) != 0 {
		t.Fatalf("empty input: %v %v", empty, err)
	}
}
```

- [ ] **Step 2: Run to verify failure**

```bash
cd /Users/belazy/personal/argus/backend && go test ./internal/store/ -run TestListPatternIDsByIdentity 2>&1 | tail -4
```

Expected: build failure `st.ListPatternIDsByIdentity undefined`.

- [ ] **Step 3: Implement the store lookup**

Append to `backend/internal/store/patterns.go`:

```go
// patternIdentityExpr is the effective memory identity of a patterns row:
// the deterministic custom id for every row written since the custom-id
// migration, the legacy memory doc id otherwise. It matches the precedence
// firstNonEmpty(row.MemoryCustomID, row.MemoryDocID) uses in DeletePattern's
// tombstone, so every sibling query in the store agrees on identity.
const patternIdentityExpr = "COALESCE(NULLIF(memory_custom_id, ''), memory_doc_id)"

// ListPatternIDsByIdentity maps each memory identity to EVERY patterns row in
// the installation that carries it, ordered by id. Search returns identities
// (custom ids); delete_memory takes pattern ids; this is the bridge. Multiple
// siblings can share one identity, so GetPatternIDByCustomID's LIMIT 1 is the
// wrong tool here. Identities with no row are absent from the map.
func (s *Store) ListPatternIDsByIdentity(ctx context.Context, installationID int64, identities []string) (storeResult0 map[string][]int64, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "ListPatternIDsByIdentity",

			"installation_id",

			storeLogValue(installationID), "identity_count", len(identities))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr,
			storeResult0)
	}()

	out := map[string][]int64{}
	if len(identities) == 0 {
		return out, nil
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT `+patternIdentityExpr+` AS identity, id
		FROM patterns
		WHERE installation_id = $1 AND `+patternIdentityExpr+` = ANY($2::text[])
		ORDER BY id`, installationID, identities)
	if err != nil {
		return nil, fmt.Errorf("listing pattern ids by identity: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var identity string
		var id int64
		if err := rows.Scan(&identity, &id); err != nil {
			return nil, fmt.Errorf("scanning pattern identity: %w", err)
		}
		out[identity] = append(out[identity], id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing pattern ids by identity: %w", err)
	}
	return out, nil
}
```

```bash
cd /Users/belazy/personal/argus/backend && go test ./internal/store/ -run TestListPatternIDsByIdentity -count=1 -v 2>&1 | tail -4
```

Expected: PASS with `TEST_DATABASE_URL`.

- [ ] **Step 4: Write the failing tool tests**

Create `backend/internal/api/mcp_tools_memory_read_test.go`. **Note:** `recordingIndexer.RetireDocument` and its `retireFn` field reference `memory.RetireRequest`/`RetireResult`, which Task 10 defines. Write the file as shown but with those two members commented out; Task 10 uncomments them.

```go
package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/store"
)

// recordingIndexer is a partial fake: only the methods these tools call are
// implemented; anything else nil-panics, the established idiom for
// memory.Indexer doubles outside memorytest.
type recordingIndexer struct {
	memory.Indexer
	lastQuery    memory.MemoryQuery
	lastBriefing memory.BriefingQuery
	matches      []memory.PatternMatch
	briefing     string
	err          error
	retireFn     func(memory.RetireRequest) (memory.RetireResult, error)
}

func (r *recordingIndexer) Search(_ context.Context, q memory.MemoryQuery) ([]memory.PatternMatch, error) {
	r.lastQuery = q
	return r.matches, r.err
}
func (r *recordingIndexer) Briefing(_ context.Context, q memory.BriefingQuery) (string, error) {
	r.lastBriefing = q
	return r.briefing, r.err
}
func (r *recordingIndexer) RetireDocument(_ context.Context, req memory.RetireRequest) (memory.RetireResult, error) {
	if r.retireFn != nil {
		return r.retireFn(req)
	}
	return memory.RetireResult{}, errors.New("retireFn not stubbed")
}

func readScope(installationIDs ...int64) tenantScope {
	return tenantScope{userID: "user_r", orgID: "org_r", installationIDs: installationIDs, grantedScopes: []string{scopeRead}}
}

func writeScope(installationIDs ...int64) tenantScope {
	return tenantScope{userID: "user_w", orgID: "org_w", installationIDs: installationIDs, grantedScopes: []string{scopeRead, scopeMemoryWrite}}
}

func newMemoryReadFixture(t *testing.T) (context.Context, *Server, int64, int64, *recordingIndexer, *stubIndexers) {
	t.Helper()
	pool, ctx := architectureTestPool(t)
	installID, repoID := seedArchitectureRepo(t, ctx, pool)
	idx := &recordingIndexer{}
	src := &stubIndexers{indexer: idx, available: true}
	s := &Server{store: store.NewWithDB(pool), logger: slog.New(slog.NewTextHandler(io.Discard, nil)), indexers: src}
	return ctx, s, installID, repoID, idx, src
}

func TestSearchMemoryBuildsScopedQueryAndMapsPatternIDs(t *testing.T) {
	ctx, s, installID, repoID, idx, _ := newMemoryReadFixture(t)
	// Two contributing pattern rows for m1 and none for m2.
	src := "manual"
	cid := "m1"
	a, err := s.store.CreatePattern(ctx, installID, &repoID, "guard writes", nil, nil, &src, nil, nil, &cid, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.store.CreatePattern(ctx, installID, &repoID, "guard writes legacy", &cid, nil, &src, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = s.store.Pool.Exec(bg, `DELETE FROM memory_mirror_outbox WHERE installation_id = $1`, installID)
		_, _ = s.store.Pool.Exec(bg, `DELETE FROM patterns WHERE installation_id = $1`, installID)
	})
	idx.matches = []memory.PatternMatch{
		{ID: "m1", Content: "guard writes", Score: 0.91, Metadata: map[string]string{"type": "pattern", "source": "manual", "created_at": "2026-09-01T00:00:00Z"}},
		{ID: "m2", Content: "pipeline learned", Score: 0.80, Metadata: map[string]string{"type": "pattern", "source": "auto_learn"}},
	}
	tools := &mcpTools{srv: s, scope: readScope(installID)}

	_, out, err := tools.searchMemory(ctx, nil, searchMemoryInput{RepoID: repoID, Query: "guard"})
	if err != nil {
		t.Fatal(err)
	}
	if idx.lastQuery.Scope != memory.ScopeRepo || idx.lastQuery.Repo == "" || idx.lastQuery.Limit != 10 || idx.lastQuery.Threshold != memory.NewThresholds().FindingEnrich || idx.lastQuery.PointLookup || len(idx.lastQuery.Filters) != 0 {
		t.Fatalf("query = %+v", idx.lastQuery)
	}
	if out.InstallationID != installID || len(out.Matches) != 2 || !out.EmbeddingsAvailable {
		t.Fatalf("out = %+v", out)
	}
	m1, m2 := out.Matches[0], out.Matches[1]
	if m1.CustomID != "m1" || len(m1.PatternIDs) != 2 || m1.PatternIDs[0] != a.ID || m1.PatternIDs[1] != b.ID || m1.Type != "pattern" || m1.Source != "manual" || m1.WrittenAt != "2026-09-01T00:00:00Z" || m1.ContainerTag != memory.RepoTagNew(idx.lastQuery.Repo) {
		t.Fatalf("m1 = %+v", m1)
	}
	if m2.CustomID != "m2" || m2.PatternIDs == nil || len(m2.PatternIDs) != 0 {
		t.Fatalf("m2 must carry an EMPTY (not null) pattern_ids: %+v", m2)
	}
}

func TestSearchMemoryScopeAndTenantGuards(t *testing.T) {
	ctx, s, installID, repoID, idx, src := newMemoryReadFixture(t)
	pool, _ := architectureTestPool(t)
	foreignInstall, foreignRepo := seedArchitectureRepo(t, ctx, pool)
	tools := &mcpTools{srv: s, scope: readScope(installID)}

	if _, _, err := tools.searchMemory(ctx, nil, searchMemoryInput{RepoID: foreignRepo, Query: "x"}); !errors.Is(err, errNotAccessible) {
		t.Fatalf("foreign repo: %v", err)
	}
	if _, _, err := tools.searchMemory(ctx, nil, searchMemoryInput{InstallationID: foreignInstall, Scope: "shared", Query: "x"}); !errors.Is(err, errNotAccessible) {
		t.Fatalf("foreign installation on shared scope: %v", err)
	}
	if _, _, err := tools.searchMemory(ctx, nil, searchMemoryInput{InstallationID: installID, Scope: "shared", Query: "x"}); err != nil || idx.lastQuery.Scope != memory.ScopeShared {
		t.Fatalf("shared scope: err=%v query=%+v", err, idx.lastQuery)
	}
	noRead := &mcpTools{srv: s, scope: tenantScope{installationIDs: []int64{installID}, grantedScopes: []string{scopeMemoryWrite}}}
	if _, _, err := noRead.searchMemory(ctx, nil, searchMemoryInput{RepoID: repoID, Query: "x"}); err == nil || err.Error() != errInsufficientScope(scopeRead).Error() {
		t.Fatalf("missing read scope: %v", err)
	}
	if _, _, err := tools.searchMemory(ctx, nil, searchMemoryInput{RepoID: repoID, Query: "x", Limit: 500}); err != nil || idx.lastQuery.Limit != 25 {
		t.Fatalf("limit clamp: err=%v limit=%d", err, idx.lastQuery.Limit)
	}

	src.available = false
	idx.matches = nil
	_, out, err := tools.searchMemory(ctx, nil, searchMemoryInput{RepoID: repoID, Query: "x"})
	if err != nil || out.EmbeddingsAvailable {
		t.Fatalf("embeddings off must be reported, not hidden as 'no matches': out=%+v err=%v", out, err)
	}
	s.indexers = nil
	if _, _, err := tools.searchMemory(ctx, nil, searchMemoryInput{RepoID: repoID, Query: "x"}); !errors.Is(err, errMemoryUnavailable) {
		t.Fatalf("unwired backend must be an explicit error, never an empty result: %v", err)
	}
}

func TestGetMemoryBriefing(t *testing.T) {
	ctx, s, installID, repoID, idx, _ := newMemoryReadFixture(t)
	idx.briefing = "## Institutional memory\n- guard writes"
	tools := &mcpTools{srv: s, scope: readScope(installID)}

	_, out, err := tools.getMemoryBriefing(ctx, nil, getMemoryBriefingInput{RepoID: repoID, Query: "writes", Profile: "specialist"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Empty || out.Markdown != idx.briefing || idx.lastBriefing.Options.Profile != memory.ProfileSpecialist || idx.lastBriefing.Options.CharCap != 2400 || idx.lastBriefing.Repo == "" || idx.lastBriefing.Owner == "" {
		t.Fatalf("out=%+v query=%+v", out, idx.lastBriefing)
	}
	idx.briefing = ""
	_, out, err = tools.getMemoryBriefing(ctx, nil, getMemoryBriefingInput{RepoID: repoID, Query: "writes"})
	if err != nil || !out.Empty || idx.lastBriefing.Options.Profile != memory.ProfileReview || idx.lastBriefing.Options.CharCap != 3200 {
		t.Fatalf("default profile: out=%+v query=%+v err=%v", out, idx.lastBriefing, err)
	}
}
```

- [ ] **Step 5: Run to verify failure**

```bash
cd /Users/belazy/personal/argus/backend && go test ./internal/api/ -run 'TestSearchMemory|TestGetMemoryBriefing' 2>&1 | tail -5
```

Expected: build failure — `searchMemory`, `searchMemoryInput`, `getMemoryBriefing`, `getMemoryBriefingInput` undefined.

- [ ] **Step 6: Implement the two tools**

Create `backend/internal/api/mcp_tools_memory_read.go`:

```go
package api

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/store"
)

const (
	searchMemoryDefaultLimit  = 10
	searchMemoryMaxLimit      = 25
	briefingCharCapReview     = 3200
	briefingCharCapSpecialist = 2400
)

// scopedRepo is the authorization step every repo-taking tool runs first:
// GetRepoScoped fails unless one of the caller's installations owns the repo.
// The returned repo is ALSO the tenant for any memory call that follows — read
// installation and short name off it, never off the request.
func (t *mcpTools) scopedRepo(ctx context.Context, repoID int64) (*store.Repo, string, error) {
	repo, err := t.srv.store.GetRepoScoped(ctx, repoID, t.scope.installationIDs)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, "", errNotAccessible
		}
		return nil, "", err
	}
	_, short, ok := strings.Cut(repo.FullName, "/")
	if !ok || short == "" {
		return nil, "", errNotAccessible
	}
	return repo, short, nil
}

type searchMemoryInput struct {
	RepoID         int64   `json:"repo_id,omitempty" jsonschema:"local repo id from list_repos; required unless scope is shared"`
	InstallationID int64   `json:"installation_id,omitempty" jsonschema:"local installation id from list_repos; required only when scope is shared"`
	Query          string  `json:"query" jsonschema:"natural-language search text"`
	Scope          string  `json:"scope,omitempty" jsonschema:"repo (default), shared (org-wide memory), or both"`
	Type           string  `json:"type,omitempty" jsonschema:"restrict to one memory type: pattern, scenario, trace, feedback, synthesis, pr_summary, review, topology, rule"`
	Limit          int     `json:"limit,omitempty" jsonschema:"max results, default 10, max 25"`
	Threshold      float64 `json:"threshold,omitempty" jsonschema:"minimum similarity 0..1; default is the pipeline's enrichment floor"`
}

type memoryMatch struct {
	CustomID     string            `json:"custom_id" jsonschema:"memory identity — pass to retire_memory"`
	PatternIDs   []int64           `json:"pattern_ids" jsonschema:"every contributing pattern row in this installation, ordered by id; empty means the memory has no pattern row and can only be retired — pass one to delete_memory"`
	Content      string            `json:"content"`
	Score        float64           `json:"score"`
	Type         string            `json:"type,omitempty"`
	Source       string            `json:"source,omitempty"`
	ContainerTag string            `json:"container_tag,omitempty"`
	WrittenAt    string            `json:"written_at,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

type searchMemoryOutput struct {
	InstallationID      int64         `json:"installation_id" jsonschema:"the installation these results belong to — pass to retire_memory"`
	Matches             []memoryMatch `json:"matches"`
	EmbeddingsAvailable bool          `json:"embeddings_available" jsonschema:"false means no embedder is configured for this installation and similarity search returned nothing for that reason, not because nothing matched"`
	Truncated           bool          `json:"truncated" jsonschema:"true when the result hit the limit"`
}

func (t *mcpTools) searchMemory(ctx context.Context, _ *mcp.CallToolRequest, in searchMemoryInput) (*mcp.CallToolResult, searchMemoryOutput, error) {
	var zero searchMemoryOutput
	if err := t.requireScope(scopeRead); err != nil {
		return nil, zero, err
	}
	if strings.TrimSpace(in.Query) == "" {
		return nil, zero, errors.New("query is required")
	}
	scope := memory.ContainerScope(in.Scope)
	if in.Scope == "" {
		scope = memory.ScopeRepo
	}

	var installationID int64
	var repoShort, containerTag string
	switch scope {
	case memory.ScopeShared:
		if in.InstallationID == 0 || !containsID(t.scope.installationIDs, in.InstallationID) {
			return nil, zero, errNotAccessible
		}
		installationID, containerTag = in.InstallationID, memory.SharedTag
	case memory.ScopeRepo, memory.ScopeBoth:
		if in.RepoID == 0 {
			return nil, zero, errors.New("repo_id is required for scope repo or both")
		}
		repo, short, err := t.scopedRepo(ctx, in.RepoID)
		if err != nil {
			if errors.Is(err, errNotAccessible) {
				return nil, zero, err
			}
			return nil, zero, t.internalErr(ctx, "search_memory", err)
		}
		installationID, repoShort = repo.InstallationID, short
		if scope == memory.ScopeRepo {
			containerTag = memory.RepoTagNew(short)
		}
	default:
		return nil, zero, errors.New("scope must be repo, shared, or both")
	}

	idx := t.srv.resolveIndexer(ctx, installationID)
	if idx == nil {
		return nil, zero, errMemoryUnavailable
	}
	limit := in.Limit
	if limit <= 0 {
		limit = searchMemoryDefaultLimit
	}
	if limit > searchMemoryMaxLimit {
		limit = searchMemoryMaxLimit
	}
	threshold := in.Threshold
	if threshold <= 0 {
		threshold = memory.NewThresholds().FindingEnrich
	}

	// Filters and PointLookup are deliberately not exposed: PointLookup
	// licenses a predicate scan with no score gate, and malformed filters
	// compile to false and silently return nothing.
	matches, err := idx.Search(ctx, memory.MemoryQuery{
		Query: in.Query, Repo: repoShort, Scope: scope, Type: memory.MemoryType(in.Type), Limit: limit, Threshold: threshold,
	})
	if err != nil {
		return nil, zero, t.internalErr(ctx, "search_memory", err)
	}

	// N9: every identity → all contributing pattern rows, one scoped batch.
	// A lookup failure is a tool error; an empty array is a real answer.
	identities := make([]string, 0, len(matches))
	for _, m := range matches {
		identities = append(identities, m.ID)
	}
	patternIDs, err := t.srv.store.ListPatternIDsByIdentity(ctx, installationID, identities)
	if err != nil {
		return nil, zero, t.internalErr(ctx, "search_memory", err)
	}

	out := searchMemoryOutput{
		InstallationID:      installationID,
		Matches:             make([]memoryMatch, 0, len(matches)),
		EmbeddingsAvailable: t.srv.indexers.EmbedderAvailable(ctx, installationID),
		Truncated:           len(matches) >= limit,
	}
	for _, m := range matches {
		ids := patternIDs[m.ID]
		if ids == nil {
			ids = []int64{}
		}
		tag := containerTag
		if mt := m.Metadata["container_tag"]; mt != "" {
			tag = mt
		}
		out.Matches = append(out.Matches, memoryMatch{
			CustomID: m.ID, PatternIDs: ids, Content: m.Content, Score: m.Score,
			Type: m.Metadata["type"], Source: m.Metadata["source"], ContainerTag: tag, WrittenAt: m.Metadata["created_at"],
			Metadata: m.Metadata,
		})
	}
	return nil, out, nil
}

type getMemoryBriefingInput struct {
	RepoID   int64  `json:"repo_id" jsonschema:"local repo id from list_repos"`
	Query    string `json:"query" jsonschema:"what the briefing should be about, e.g. a file path or a change description"`
	FilePath string `json:"file_path,omitempty" jsonschema:"optional file to focus the briefing on"`
	Profile  string `json:"profile,omitempty" jsonschema:"review (default, broader: includes org rules and past-review context) or specialist (deep-review block)"`
	CharCap  int    `json:"char_cap,omitempty" jsonschema:"max characters; default 3200 for review, 2400 for specialist"`
}

type getMemoryBriefingOutput struct {
	Markdown string `json:"markdown"`
	Empty    bool   `json:"empty" jsonschema:"true when memory had nothing relevant; an empty briefing is a legitimate result"`
}

func (t *mcpTools) getMemoryBriefing(ctx context.Context, _ *mcp.CallToolRequest, in getMemoryBriefingInput) (*mcp.CallToolResult, getMemoryBriefingOutput, error) {
	var zero getMemoryBriefingOutput
	if err := t.requireScope(scopeRead); err != nil {
		return nil, zero, err
	}
	if in.RepoID == 0 {
		return nil, zero, errors.New("repo_id is required")
	}
	repo, short, err := t.scopedRepo(ctx, in.RepoID)
	if err != nil {
		if errors.Is(err, errNotAccessible) {
			return nil, zero, err
		}
		return nil, zero, t.internalErr(ctx, "get_memory_briefing", err)
	}
	idx := t.srv.resolveIndexer(ctx, repo.InstallationID)
	if idx == nil {
		return nil, zero, errMemoryUnavailable
	}
	profile, charCap := memory.ProfileReview, briefingCharCapReview
	switch in.Profile {
	case "", "review":
	case "specialist":
		profile, charCap = memory.ProfileSpecialist, briefingCharCapSpecialist
	default:
		return nil, zero, errors.New("profile must be review or specialist")
	}
	if in.CharCap > 0 {
		charCap = in.CharCap
	}
	owner, _, _ := strings.Cut(repo.FullName, "/")
	// Briefing runs OpenConventionConflicts first and hard-fails if that
	// query errors; that surfaces here as a tool error, which is correct.
	md, err := idx.Briefing(ctx, memory.BriefingQuery{
		Owner: owner, Repo: short, FilePath: in.FilePath, Query: in.Query,
		Options: memory.BriefingOptions{Profile: profile, Thresholds: memory.NewThresholds(), CharCap: charCap},
	})
	if err != nil {
		return nil, zero, t.internalErr(ctx, "get_memory_briefing", err)
	}
	return nil, getMemoryBriefingOutput{Markdown: md, Empty: strings.TrimSpace(md) == ""}, nil
}
```

Register both in `registerMCPTools` (`mcp_server.go`) after `list_repos`:

```go
	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_memory",
		Description: "Semantic search over the team's institutional memory for one repo (or org-wide with scope=shared): learned patterns, conventions, dismissed false positives, scenarios, and past-review context. Each match carries custom_id (for retire_memory) and pattern_ids (for delete_memory; empty means retire-only). Check embeddings_available — false means similarity search is off for this installation.",
	}, t.searchMemory)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_memory_briefing",
		Description: "The assembled institutional-memory briefing Argus gives its reviewers for a repo, as markdown. Optionally focused on one file. Empty is a legitimate result.",
	}, t.getMemoryBriefing)
```

- [ ] **Step 7: Run the tests**

```bash
cd /Users/belazy/personal/argus/backend && go build ./... && go vet ./internal/api/ ./internal/store/ && go test ./internal/api/ -run 'TestSearchMemory|TestGetMemoryBriefing|TestNewMCPServer' -count=1 -v 2>&1 | tail -16
```

Expected: PASS with `TEST_DATABASE_URL`; the schema smoke test still passes.

- [ ] **Step 8: Commit**

```bash
cd /Users/belazy/personal/argus
git add backend/internal/store/patterns.go backend/internal/store/pattern_identity_pg_test.go backend/internal/api/mcp_tools_memory_read.go backend/internal/api/mcp_tools_memory_read_test.go backend/internal/api/mcp_server.go
git commit -m "feat(api): search_memory and get_memory_briefing MCP tools

Both authorize through GetRepoScoped and take the tenant off the returned
repo, never off the request; the indexer is resolved inside the handler,
after the scope and tenant checks.

search_memory exposes identities a mutation tool can act on: custom_id
for retire_memory and, via the new installation-scoped batch lookup
ListPatternIDsByIdentity, every contributing pattern id for delete_memory.
Siblings can share one identity, so GetPatternIDByCustomID's LIMIT 1 was
the wrong bridge; a lookup failure is a tool error, never an empty array.
It also reports embeddings_available — with the embedder off every
positive-threshold search returns (nil, nil), which an agent would read
as 'nothing matched'. Filters and PointLookup are not exposed."
```

---

### Task 8: Atomic create-or-get (N10) and `create_memory`

**Files:**
- Modify: `backend/internal/store/sqlc/query/patterns.sql:27-29` (`GetPattern` selects `memory_custom_id`) + `make sqlc`
- Modify: `backend/internal/store/patterns.go` (`Pattern.MemoryCustomID`; extract `createPatternTx`; identity lock in `CreatePattern`; add `findPatternIDByIdentity`, `FindPatternIDByIdentity`, `CreateOrGetPattern`)
- Modify: `backend/internal/api/handlers_patterns.go:131` (dashboard manual create → `CreateOrGetPattern`)
- Create: `backend/internal/api/mcp_tools_memory_write.go`
- Modify: `backend/internal/api/mcp_server.go` (`registerMCPTools`: one `AddTool`)
- Test: `backend/internal/store/pattern_create_or_get_pg_test.go`, `backend/internal/api/mcp_tools_memory_write_test.go`

**Interfaces:**
- Produces:
  ```go
  // store
  Pattern.MemoryCustomID *string   // populated by GetPattern
  func (s *Store) FindPatternIDByIdentity(ctx, installationID int64, identity string) (int64, error)  // min(id); pgx.ErrNoRows when none
  func (s *Store) CreateOrGetPattern(ctx, installationID int64, repoID *int64, content string, createdBy, source, category *string, memoryCustomID string, mirrorExtra map[string]string) (pattern *Pattern, created bool, err error)
  // api
  func sanitizeMemoryContent(s string) string
  ```
  handler `createMemory` and types `createMemoryInput`, `createMemoryOutput`, `similarMemory`; test helpers `newMemoryWriteFixture`, `repoShortName`.
- Consumes: `lockMemoryMirrorCustomID(ctx, tx, installationID, customID)` (memory_mirror_outbox.go:270), `enqueueMemoryMirrorEvent(ctx, tx, event)` (:120), `newPatternOutboxPayload`, `patternFromSQLC`, `db.CreatePatternParams`; `memory.PatternCustomID("", repoShort, "manual", content)`, `memory.SharedPatternCustomID("manual", content)`.

- [ ] **Step 1: Write the failing store tests**

Create `backend/internal/store/pattern_create_or_get_pg_test.go`:

```go
package store

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
)

func countPatternsAndEvents(t *testing.T, ctx context.Context, st *Store, installationID int64, identity string) (int, int) {
	t.Helper()
	var patterns, events int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM patterns WHERE installation_id=$1 AND `+patternIdentityExpr+` = $2`, installationID, identity).Scan(&patterns); err != nil {
		t.Fatal(err)
	}
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM memory_mirror_outbox WHERE installation_id=$1 AND aggregate_type='pattern'`, installationID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	return patterns, events
}

func TestCreateOrGetPattern(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	install, _, _ := seedLearnTenant(t, ctx, pool, "mcp-create-or-get")
	st := NewWithDB(pool)
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM memory_mirror_outbox WHERE installation_id = $1`, install)
		_, _ = pool.Exec(bg, `DELETE FROM patterns WHERE installation_id = $1`, install)
	})
	src, author := "manual", "user_a"
	cid := "sm_create_or_get"

	first, created, err := st.CreateOrGetPattern(ctx, install, nil, "content", &author, &src, nil, cid, map[string]string{"created_by": author, "origin": "mcp"})
	if err != nil || !created {
		t.Fatalf("first: created=%v err=%v", created, err)
	}
	if first.MemoryCustomID == nil || *first.MemoryCustomID != cid {
		t.Fatalf("GetPattern must expose memory_custom_id: %+v", first)
	}
	if p, e := countPatternsAndEvents(t, ctx, st, install, cid); p != 1 || e != 1 {
		t.Fatalf("after first: patterns=%d events=%d, want 1/1", p, e)
	}

	// Exact retry by a different author: same row, author untouched, no
	// second row and — the point — no second outbox event.
	other := "user_b"
	again, created, err := st.CreateOrGetPattern(ctx, install, nil, "content", &other, &src, nil, cid, nil)
	if err != nil || created || again.ID != first.ID {
		t.Fatalf("retry: created=%v id=%d want %d err=%v", created, again.ID, first.ID, err)
	}
	if again.CreatedBy == nil || *again.CreatedBy != author {
		t.Fatalf("retry changed created_by to %v", again.CreatedBy)
	}
	if p, e := countPatternsAndEvents(t, ctx, st, install, cid); p != 1 || e != 1 {
		t.Fatalf("after retry: patterns=%d events=%d, want unchanged 1/1", p, e)
	}

	// Legacy siblings: lowest id wins deterministically.
	doc := "sm_legacy_pair"
	low, err := st.CreatePattern(ctx, install, nil, "legacy", &doc, nil, &src, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreatePattern(ctx, install, nil, "legacy", &doc, nil, &src, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	got, created, err := st.CreateOrGetPattern(ctx, install, nil, "legacy", &author, &src, nil, doc, nil)
	if err != nil || created || got.ID != low.ID {
		t.Fatalf("legacy siblings: got=%d created=%v want lowest %d err=%v", got.ID, created, low.ID, err)
	}

	if _, err := st.FindPatternIDByIdentity(ctx, install, "absent"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("absent identity: %v", err)
	}
}

// Two concurrent callers racing on an absent identity produce one row and one
// outbox event. This is the property a handler-level check-then-insert lacks.
func TestCreateOrGetPatternConcurrent(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	install, _, _ := seedLearnTenant(t, ctx, pool, "mcp-create-race")
	st := NewWithDB(pool)
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM memory_mirror_outbox WHERE installation_id = $1`, install)
		_, _ = pool.Exec(bg, `DELETE FROM patterns WHERE installation_id = $1`, install)
	})
	src := "manual"
	const cid = "sm_race"
	const callers = 8
	var wg sync.WaitGroup
	createdCount := make(chan bool, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, created, err := st.CreateOrGetPattern(ctx, install, nil, "raced", nil, &src, nil, cid, nil)
			if err != nil {
				t.Errorf("caller: %v", err)
				return
			}
			createdCount <- created
		}()
	}
	wg.Wait()
	close(createdCount)
	n := 0
	for c := range createdCount {
		if c {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d callers reported created=true, want exactly 1", n)
	}
	if p, e := countPatternsAndEvents(t, ctx, st, install, cid); p != 1 || e != 1 {
		t.Fatalf("patterns=%d events=%d, want 1/1", p, e)
	}
}
```

- [ ] **Step 2: Run to verify failure**

```bash
cd /Users/belazy/personal/argus/backend && go test ./internal/store/ -run 'TestCreateOrGetPattern' 2>&1 | tail -4
```

Expected: build failure `st.CreateOrGetPattern undefined`.

- [ ] **Step 3: Expose `memory_custom_id`, refactor `CreatePattern`, add the new store operations**

In `backend/internal/store/sqlc/query/patterns.sql`, change the `GetPattern` query to:

```sql
-- name: GetPattern :one
SELECT id, installation_id, repo_id, content, memory_doc_id, memory_custom_id, created_by, COALESCE(source, 'manual') as source, category, pr_number, created_at, updated_at
FROM patterns WHERE id = sqlc.arg(id)::bigint;
```

Run `cd backend && make sqlc` — `GetPatternRow` gains `MemoryCustomID *string`.

In `backend/internal/store/patterns.go`:

Add to the `Pattern` struct after `MemoryDocID`:

```go
	MemoryDocID    *string   `json:"memory_doc_id,omitempty"`
	// MemoryCustomID is the deterministic memory identity for rows written with
	// one (every path since the custom-id migration). Legacy rows carry only
	// MemoryDocID. Populated by GetPattern.
	MemoryCustomID *string   `json:"memory_custom_id,omitempty"`
```

In the `GetPattern` wrapper, after `patternFromSQLC(...)` succeeds:

```go
	pattern.MemoryCustomID = row.MemoryCustomID
	return &pattern, nil
```

Replace the body of `CreatePattern`'s `WithMemoryMirrorTx` closure with the lock plus a call to the extracted helper, and add the helper and the new methods:

```go
	var pattern Pattern
	err := s.WithMemoryMirrorTx(ctx, func(tx pgx.Tx) (MemoryMirrorEvent, error) {
		// Every creation path takes the per-identity lock so CreateOrGetPattern's
		// recheck is meaningful against concurrent pipeline/dashboard writers.
		// Same key the mirror worker uses; cheap; released at commit.
		if err := lockMemoryMirrorCustomID(ctx, tx, installationID, customID); err != nil {
			return MemoryMirrorEvent{}, err
		}
		created, event, err := createPatternTx(ctx, tx, installationID, repoID, content, memoryDocID, createdBy, source, category, prNumber, memoryCustomID, mirrorExtra)
		if err != nil {
			return MemoryMirrorEvent{}, err
		}
		pattern = created
		return event, nil
	})
	if err != nil {
		return nil, err
	}
	return &pattern, nil
}

// createPatternTx is CreatePattern's insert + outbox payload inside a caller's
// transaction. It keeps the existing ON CONFLICT upsert semantics — pipeline
// writers rely on them — and is shared with CreateOrGetPattern, which decides
// BEFORE calling it whether an insert should happen at all.
func createPatternTx(ctx context.Context, tx pgx.Tx, installationID int64, repoID *int64, content string, memoryDocID *string, createdBy *string, source *string, category *string, prNumber *int, memoryCustomID *string, mirrorExtra map[string]string) (Pattern, MemoryMirrorEvent, error) {
	customID := firstNonEmpty(memoryCustomID, memoryDocID)
	q := db.New(tx)
	repo := ""
	if repoID != nil {
		repoRow, err := q.GetRepoScoped(ctx, db.GetRepoScopedParams{ID: *repoID, Column2: []int64{installationID}})
		if err != nil {
			return Pattern{}, MemoryMirrorEvent{}, fmt.Errorf("resolve pattern repo: %w", err)
		}
		_, repo, _ = strings.Cut(repoRow.FullName, "/")
		if repo == "" {
			return Pattern{}, MemoryMirrorEvent{}, fmt.Errorf("repo %d has invalid full name", *repoID)
		}
	}
	row, err := q.CreatePattern(ctx, db.CreatePatternParams{
		InstallationID: installationID, RepoID: repoID, Content: content,
		MemoryDocID: memoryDocID, CreatedBy: createdBy, Source: source,
		Category: category, PRNumber: prNumber, MemoryCustomID: memoryCustomID,
	})
	if err != nil {
		return Pattern{}, MemoryMirrorEvent{}, err
	}
	pattern, err := patternFromSQLC(row.ID, row.InstallationID, row.RepoID, row.Content, row.MemoryDocID, row.CreatedBy, row.Source, row.Category, row.PRNumber, row.CreatedAt, row.UpdatedAt)
	if err != nil {
		return Pattern{}, MemoryMirrorEvent{}, err
	}
	pattern.MemoryCustomID = memoryCustomID
	payload, err := newPatternOutboxPayload(pattern, customID, repo, mirrorExtra)
	if err != nil {
		return Pattern{}, MemoryMirrorEvent{}, err
	}
	return pattern, MemoryMirrorEvent{
		InstallationID: installationID, AggregateType: MemoryMirrorPattern, AggregateID: pattern.ID,
		Operation: MemoryMirrorUpsert, Payload: payload,
	}, nil
}

// identityQuerier is satisfied by *pgxpool.Pool and pgx.Tx.
type identityQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// findPatternIDByIdentity returns the LOWEST pattern id carrying the identity —
// deterministic when legacy siblings exist — or pgx.ErrNoRows.
func findPatternIDByIdentity(ctx context.Context, q identityQuerier, installationID int64, identity string) (int64, error) {
	var id *int64
	if err := q.QueryRow(ctx, `SELECT min(id) FROM patterns WHERE installation_id = $1 AND `+patternIdentityExpr+` = $2`, installationID, identity).Scan(&id); err != nil {
		return 0, err
	}
	if id == nil {
		return 0, pgx.ErrNoRows
	}
	return *id, nil
}

// FindPatternIDByIdentity is the read-only exact-duplicate check callers run
// before any similarity work. CreateOrGetPattern repeats it under the lock.
func (s *Store) FindPatternIDByIdentity(ctx context.Context, installationID int64, identity string) (storeResult0 int64, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "FindPatternIDByIdentity",

			"installation_id",

			storeLogValue(installationID), "identity", storeLogValue(identity))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr,
			storeResult0)
	}()

	return findPatternIDByIdentity(ctx, s.Pool, installationID, identity)
}

// CreateOrGetPattern is the manual-creation path (MCP and dashboard). Under
// the per-identity advisory lock it rechecks for an existing row and returns
// it untouched — no upsert, no second outbox event, no author or content
// rewrite, nothing that could revive a retired memory — or inserts a new row
// and its mirror event. Two callers racing on an absent identity produce one
// row and one event. No network work happens while the lock is held.
func (s *Store) CreateOrGetPattern(ctx context.Context, installationID int64, repoID *int64, content string, createdBy *string, source *string, category *string, memoryCustomID string, mirrorExtra map[string]string) (storeResult0 *Pattern, storeResult1 bool, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "CreateOrGetPattern",

			"installation_id",

			storeLogValue(installationID), "repo_id", storeLogValue(repoID), "source", storeLogValue(source),
			"memory_custom_id", storeLogValue(memoryCustomID))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered, storeResult0, storeResult1)
			panic(recovered)
		}
		storeFinish(storeErr,
			storeResult0, storeResult1)
	}()

	if memoryCustomID == "" {
		return nil, false, fmt.Errorf("creating pattern requires a deterministic memory identity")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("begin create-or-get transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockMemoryMirrorCustomID(ctx, tx, installationID, memoryCustomID); err != nil {
		return nil, false, err
	}
	existingID, err := findPatternIDByIdentity(ctx, tx, installationID, memoryCustomID)
	if err == nil {
		if err := tx.Commit(ctx); err != nil { // nothing written; releases the lock
			return nil, false, fmt.Errorf("commit create-or-get transaction: %w", err)
		}
		existing, err := s.GetPattern(ctx, existingID)
		if err != nil {
			return nil, false, err
		}
		return existing, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, fmt.Errorf("checking pattern identity: %w", err)
	}

	pattern, event, err := createPatternTx(ctx, tx, installationID, repoID, content, nil, createdBy, source, category, nil, &memoryCustomID, mirrorExtra)
	if err != nil {
		return nil, false, err
	}
	if err := enqueueMemoryMirrorEvent(ctx, tx, event); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("commit create-or-get transaction: %w", err)
	}
	return &pattern, true, nil
}
```

Then in `backend/internal/api/handlers_patterns.go:131`, replace the dashboard's `CreatePattern` call:

```go
	pattern, _, err := s.store.CreateOrGetPattern(r.Context(), body.InstallationID, body.RepoID, body.Content, &createdBy, &source, nil, customID, nil)
```

(`customID` there is already a `string` derived a few lines above; pass it by value.)

- [ ] **Step 4: Run the store tests**

```bash
cd /Users/belazy/personal/argus/backend && go build ./... && go test ./internal/store/ -run 'TestCreateOrGetPattern|TestListPatternIDsByIdentity' -race -count=1 -v 2>&1 | tail -10 && go test ./internal/api/ -run 'Pattern' -count=1 2>&1 | tail -3 && make sqlc-check
```

Expected: all PASS with `TEST_DATABASE_URL` (the concurrent test under `-race`); existing dashboard pattern tests still pass; `sqlc-check` clean.

- [ ] **Step 5: Write the failing tool tests**

Create `backend/internal/api/mcp_tools_memory_write_test.go`:

```go
package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/store"
)

func newMemoryWriteFixture(t *testing.T) (context.Context, *Server, int64, int64, *recordingIndexer, *stubIndexers) {
	t.Helper()
	pool, ctx := architectureTestPool(t)
	installID, repoID := seedArchitectureRepo(t, ctx, pool)
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM memory_mirror_outbox WHERE installation_id = $1`, installID)
		_, _ = pool.Exec(bg, `DELETE FROM patterns WHERE installation_id = $1`, installID)
	})
	idx := &recordingIndexer{}
	src := &stubIndexers{indexer: idx, available: true}
	s := &Server{store: store.NewWithDB(pool), logger: slog.New(slog.NewTextHandler(io.Discard, nil)), indexers: src}
	return ctx, s, installID, repoID, idx, src
}

func repoShortName(t *testing.T, ctx context.Context, s *Server, installID, repoID int64) string {
	t.Helper()
	repo, err := s.store.GetRepoScoped(ctx, repoID, []int64{installID})
	if err != nil {
		t.Fatal(err)
	}
	_, short, _ := strings.Cut(repo.FullName, "/")
	return short
}

func TestCreateMemoryCreatedThenExisting(t *testing.T) {
	ctx, s, installID, repoID, _, _ := newMemoryWriteFixture(t)
	tools := &mcpTools{srv: s, scope: writeScope(installID)}
	in := createMemoryInput{InstallationID: installID, RepoID: &repoID, Content: "Always guard shared writes with a lock"}

	_, out, err := tools.createMemory(ctx, nil, in)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "created" || out.PatternID == 0 || out.Scope != "repo" || out.MirrorState != "pending" {
		t.Fatalf("out = %+v", out)
	}
	if want := memory.PatternCustomID("", repoShortName(t, ctx, s, installID, repoID), "manual", in.Content); out.CustomID != want {
		t.Fatalf("custom_id = %q, want the dashboard-compatible %q", out.CustomID, want)
	}
	p, err := s.store.GetPattern(ctx, out.PatternID)
	if err != nil || p.Source != "manual" || p.CreatedBy == nil || *p.CreatedBy != "user_w" || p.RepoID == nil || *p.RepoID != repoID {
		t.Fatalf("row = %+v err=%v", p, err)
	}

	// Exact retry, even with confirm_duplicate, is status existing: same id,
	// nothing written.
	in.ConfirmDuplicate = true
	_, again, err := tools.createMemory(ctx, nil, in)
	if err != nil || again.Status != "existing" || again.PatternID != out.PatternID || again.MirrorState != "" {
		t.Fatalf("retry = %+v err=%v", again, err)
	}
	var events int
	if err := s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM memory_mirror_outbox WHERE installation_id=$1 AND aggregate_type='pattern'`, installID).Scan(&events); err != nil || events != 1 {
		t.Fatalf("outbox events = %d err=%v, want exactly 1", events, err)
	}
}

func TestCreateMemoryNearDuplicateRequiresAcknowledgment(t *testing.T) {
	ctx, s, installID, repoID, idx, _ := newMemoryWriteFixture(t)
	idx.matches = []memory.PatternMatch{{ID: "existing", Content: "Guard shared writes with a lock", Score: 0.95}}
	tools := &mcpTools{srv: s, scope: writeScope(installID)}
	in := createMemoryInput{InstallationID: installID, RepoID: &repoID, Content: "Always guard shared writes with a lock"}

	_, out, err := tools.createMemory(ctx, nil, in)
	if err != nil || out.Status != "confirmation_required" || len(out.Similar) != 1 || out.Similar[0].CustomID != "existing" || out.PatternID != 0 {
		t.Fatalf("near-duplicate: out=%+v err=%v", out, err)
	}
	if idx.lastQuery.Type != memory.TypePattern || idx.lastQuery.Limit != 5 || idx.lastQuery.Threshold != memory.NewThresholds().FindingEnrich {
		t.Fatalf("similarity query = %+v", idx.lastQuery)
	}
	var n int
	if err := s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM patterns WHERE installation_id=$1`, installID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("confirmation_required must write nothing; patterns=%d", n)
	}
	in.ConfirmDuplicate = true
	_, out, err = tools.createMemory(ctx, nil, in)
	if err != nil || out.Status != "created" {
		t.Fatalf("acknowledged retry: out=%+v err=%v", out, err)
	}
}

func TestCreateMemorySimilarityUnavailableNeedsAcknowledgment(t *testing.T) {
	ctx, s, installID, repoID, idx, src := newMemoryWriteFixture(t)
	tools := &mcpTools{srv: s, scope: writeScope(installID)}
	in := createMemoryInput{InstallationID: installID, RepoID: &repoID, Content: "no embedder here"}

	src.available = false
	if _, _, err := tools.createMemory(ctx, nil, in); err == nil || !strings.Contains(err.Error(), "confirm_duplicate") {
		t.Fatalf("embedder off without acknowledgment: %v", err)
	}
	src.available = true
	idx.err = errors.New("search exploded")
	if _, _, err := tools.createMemory(ctx, nil, in); err == nil || !strings.Contains(err.Error(), "confirm_duplicate") {
		t.Fatalf("failed search without acknowledgment: %v", err)
	}
	in.ConfirmDuplicate = true
	if _, out, err := tools.createMemory(ctx, nil, in); err != nil || out.Status != "created" {
		t.Fatalf("acknowledged bypass: out=%+v err=%v", out, err)
	}
}

func TestCreateMemoryScopeMatrix(t *testing.T) {
	ctx, s, installID, repoID, _, _ := newMemoryWriteFixture(t)
	pool, _ := architectureTestPool(t)
	_, foreignRepo := seedArchitectureRepo(t, ctx, pool)
	tools := &mcpTools{srv: s, scope: writeScope(installID)}
	reject := []createMemoryInput{
		{InstallationID: installID, Content: "no repo, not shared"},
		{InstallationID: installID, Content: "no repo, not shared, flag set", ConfirmShared: true},
		{InstallationID: installID, RepoID: &repoID, Shared: true, ConfirmShared: true, Content: "shared with repo"},
		{InstallationID: installID, Shared: true, Content: "shared without confirm"},
		{InstallationID: installID, RepoID: &foreignRepo, Content: "repo from another installation"},
		{InstallationID: installID + 99999, RepoID: &repoID, Content: "foreign installation"},
		{InstallationID: installID, RepoID: &repoID, Content: "   \x00 "},
		{InstallationID: installID, RepoID: &repoID, Content: strings.Repeat("x", 4001)},
	}
	for i, in := range reject {
		if _, out, err := tools.createMemory(ctx, nil, in); err == nil {
			t.Fatalf("case %d accepted: %+v", i, out)
		}
	}
	var n int
	if err := s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM patterns WHERE installation_id=$1`, installID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rejected inputs must reach neither patterns nor outbox; patterns=%d", n)
	}
	_, out, err := tools.createMemory(ctx, nil, createMemoryInput{InstallationID: installID, Shared: true, ConfirmShared: true, Content: "org rule"})
	if err != nil || out.Status != "created" || out.Scope != "shared" || out.CustomID != memory.SharedPatternCustomID("manual", "org rule") {
		t.Fatalf("shared write: out=%+v err=%v", out, err)
	}
	noWrite := &mcpTools{srv: s, scope: readScope(installID)}
	if _, _, err := noWrite.createMemory(ctx, nil, createMemoryInput{InstallationID: installID, RepoID: &repoID, Content: "x", ConfirmShared: true, ConfirmDuplicate: true}); err == nil || err.Error() != errInsufficientScope(scopeMemoryWrite).Error() {
		t.Fatalf("read-only token with every flag set must be denied by scope: %v", err)
	}
}

func TestCreateMemorySanitizesBeforeIdentity(t *testing.T) {
	ctx, s, installID, repoID, _, _ := newMemoryWriteFixture(t)
	tools := &mcpTools{srv: s, scope: writeScope(installID)}
	_, a, err := tools.createMemory(ctx, nil, createMemoryInput{InstallationID: installID, RepoID: &repoID, Content: "  nul\x00inside  "})
	if err != nil {
		t.Fatal(err)
	}
	_, b, err := tools.createMemory(ctx, nil, createMemoryInput{InstallationID: installID, RepoID: &repoID, Content: "nulinside"})
	if err != nil || b.Status != "existing" || b.PatternID != a.PatternID {
		t.Fatalf("sanitized content must dedup against its clean form: a=%+v b=%+v err=%v", a, b, err)
	}
	p, _ := s.store.GetPattern(ctx, a.PatternID)
	if strings.ContainsRune(p.Content, 0) || p.Content != "nulinside" {
		t.Fatalf("stored content = %q", p.Content)
	}
}
```

- [ ] **Step 6: Run to verify failure**

```bash
cd /Users/belazy/personal/argus/backend && go test ./internal/api/ -run 'TestCreateMemory' 2>&1 | tail -4
```

Expected: build failure — `createMemory`, `createMemoryInput` undefined.

- [ ] **Step 7: Implement `create_memory`**

Create `backend/internal/api/mcp_tools_memory_write.go`:

```go
package api

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/BeLazy167/argus/backend/internal/memory"
)

const (
	// mcpPatternSource is the dashboard's manual source, NOT a new "mcp" value:
	// source is hashed into custom_id, so a distinct string would make the same
	// sentence written from the dashboard and from MCP two live copies that
	// never dedup. Authorship travels in mirrorExtra instead.
	mcpPatternSource       = "manual"
	createMemoryMaxContent = 4000
	nearDuplicateLimit     = 5
)

// sanitizeMemoryContent trims and strips NUL. It runs BEFORE validation,
// identity derivation, duplicate checks and insertion: jsonb rejects \x00
// (SQLSTATE 22P05), and a NUL that survived to hashing would fork identity.
func sanitizeMemoryContent(s string) string {
	return strings.TrimSpace(strings.ReplaceAll(s, "\x00", ""))
}

type createMemoryInput struct {
	InstallationID   int64  `json:"installation_id" jsonschema:"local installation id from list_repos"`
	RepoID           *int64 `json:"repo_id,omitempty" jsonschema:"local repo id; required unless shared=true"`
	Content          string `json:"content" jsonschema:"the convention, pattern or fact to remember; max 4000 chars"`
	Category         string `json:"category,omitempty" jsonschema:"optional category such as testing, security, performance"`
	Shared           bool   `json:"shared,omitempty" jsonschema:"write org-wide instead of to one repo; requires confirm_shared=true and no repo_id"`
	ConfirmShared    bool   `json:"confirm_shared,omitempty" jsonschema:"acknowledge that an org-wide memory influences every review in the installation"`
	ConfirmDuplicate bool   `json:"confirm_duplicate,omitempty" jsonschema:"acknowledge the similar memories returned by a confirmation_required result (or that the similarity check is unavailable) and write anyway; never bypasses exact-duplicate reuse"`
}

type similarMemory struct {
	CustomID string  `json:"custom_id"`
	Content  string  `json:"content"`
	Score    float64 `json:"score"`
}

type createMemoryOutput struct {
	Status      string          `json:"status" jsonschema:"created: new pattern and mirror event committed; existing: identical memory already existed, nothing changed; confirmation_required: similar memories exist, nothing written — review similar and retry with confirm_duplicate=true"`
	PatternID   int64           `json:"pattern_id,omitempty"`
	CustomID    string          `json:"custom_id,omitempty" jsonschema:"deterministic memory identity; pass to retire_memory"`
	Scope       string          `json:"scope,omitempty" jsonschema:"repo or shared"`
	MirrorState string          `json:"mirror_state,omitempty" jsonschema:"pending: written to patterns; becomes searchable when the mirror drains. Never asserts searchability."`
	Similar     []similarMemory `json:"similar,omitempty"`
}

func (t *mcpTools) createMemory(ctx context.Context, _ *mcp.CallToolRequest, in createMemoryInput) (*mcp.CallToolResult, createMemoryOutput, error) {
	var zero createMemoryOutput
	if err := t.requireScope(scopeMemoryWrite); err != nil {
		return nil, zero, err
	}
	if in.InstallationID == 0 || !containsID(t.scope.installationIDs, in.InstallationID) {
		return nil, zero, errNotAccessible
	}
	content := sanitizeMemoryContent(in.Content)
	if content == "" {
		return nil, zero, errors.New("content is required")
	}
	if len(content) > createMemoryMaxContent {
		return nil, zero, fmt.Errorf("content exceeds %d characters", createMemoryMaxContent)
	}

	// Scope matrix — validated before identity derivation or any store call.
	// The store derives shared scope from RepoID == nil, so a missing repo_id
	// must never reach it through the repo-write path.
	var (
		repoIDPtr *int64
		customID  string
		scopeName string
		query     memory.MemoryQuery
	)
	switch {
	case in.Shared && in.RepoID != nil:
		return nil, zero, errors.New("shared=true cannot be combined with repo_id")
	case in.Shared && !in.ConfirmShared:
		return nil, zero, errors.New("org-wide memories reach every review in the installation with maximum confidence; pass confirm_shared=true to acknowledge")
	case in.Shared:
		customID = memory.SharedPatternCustomID(mcpPatternSource, content)
		scopeName = "shared"
		query = memory.MemoryQuery{Query: content, Scope: memory.ScopeShared, Type: memory.TypePattern, Limit: nearDuplicateLimit, Threshold: memory.NewThresholds().FindingEnrich}
	case in.RepoID == nil || *in.RepoID == 0:
		return nil, zero, errors.New("repo_id is required (or set shared=true with confirm_shared=true for an org-wide memory)")
	default:
		repo, short, err := t.scopedRepo(ctx, *in.RepoID)
		if err != nil {
			if errors.Is(err, errNotAccessible) {
				return nil, zero, err
			}
			return nil, zero, t.internalErr(ctx, "create_memory", err)
		}
		if repo.InstallationID != in.InstallationID {
			return nil, zero, errNotAccessible
		}
		repoIDPtr = in.RepoID
		customID = memory.PatternCustomID("", short, mcpPatternSource, content)
		scopeName = "repo"
		query = memory.MemoryQuery{Query: content, Repo: short, Scope: memory.ScopeRepo, Type: memory.TypePattern, Limit: nearDuplicateLimit, Threshold: memory.NewThresholds().FindingEnrich}
	}

	// Exact identity first: same installation + same deterministic custom_id
	// is the same memory. Return it; confirm_duplicate never overrides this.
	if existingID, err := t.srv.store.FindPatternIDByIdentity(ctx, in.InstallationID, customID); err == nil {
		return nil, createMemoryOutput{Status: "existing", PatternID: existingID, CustomID: customID, Scope: scopeName}, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, zero, t.internalErr(ctx, "create_memory", err)
	}

	// Advisory near-duplicate check, before any lock. It sees only mirrored
	// memories, so it cannot promise the absence of concurrent near-duplicates.
	// If it cannot run, the caller must acknowledge writing blind.
	idx := t.srv.resolveIndexer(ctx, in.InstallationID)
	var similar []similarMemory
	switch {
	case idx == nil || !t.srv.indexers.EmbedderAvailable(ctx, in.InstallationID):
		if !in.ConfirmDuplicate {
			return nil, zero, errors.New("similarity check unavailable for this installation; pass confirm_duplicate=true to write without it")
		}
	default:
		matches, err := idx.Search(ctx, query)
		if err != nil {
			t.srv.logger.WarnContext(ctx, "create_memory similarity search failed", "error", err)
			if !in.ConfirmDuplicate {
				return nil, zero, errors.New("similarity check failed; pass confirm_duplicate=true to write without it")
			}
		}
		for _, m := range matches {
			if m.ID == customID {
				continue
			}
			similar = append(similar, similarMemory{CustomID: m.ID, Content: m.Content, Score: m.Score})
		}
		if len(similar) > 0 && !in.ConfirmDuplicate {
			return nil, createMemoryOutput{Status: "confirmation_required", Similar: similar}, nil
		}
	}

	createdBy, source := t.scope.userID, mcpPatternSource
	var category *string
	if c := strings.TrimSpace(in.Category); c != "" {
		category = &c
	}
	// mirrorExtra is the ONLY channel that carries authorship into the memory
	// row; both existing human write paths pass nil and lose it.
	mirrorExtra := map[string]string{"created_by": createdBy, "origin": "mcp"}

	// N10: exact recheck + insert serialized under the per-identity lock.
	pattern, created, err := t.srv.store.CreateOrGetPattern(ctx, in.InstallationID, repoIDPtr, content, &createdBy, &source, category, customID, mirrorExtra)
	if err != nil {
		return nil, zero, t.internalErr(ctx, "create_memory", err)
	}
	out := createMemoryOutput{Status: "existing", PatternID: pattern.ID, CustomID: customID, Scope: scopeName, Similar: similar}
	if created {
		out.Status, out.MirrorState = "created", "pending"
		t.srv.logger.InfoContext(ctx, "mcp memory created", "user", createdBy, "org", t.scope.orgID, "installation_id", in.InstallationID, "pattern_id", pattern.ID, "scope", scopeName)
	}
	return nil, out, nil
}
```

Register in `registerMCPTools`:

```go
	mcp.AddTool(server, &mcp.Tool{
		Name:        "create_memory",
		Description: "Record a convention, pattern or fact in the team's memory so Argus applies it in future reviews. Repo-scoped by default; shared=true with confirm_shared=true writes org-wide. Result status is created, existing (identical memory reused, nothing written) or confirmation_required (similar memories returned; retry with confirm_duplicate=true to write anyway). Requires argus:memory:write.",
	}, t.createMemory)
```

- [ ] **Step 8: Run the tests**

```bash
cd /Users/belazy/personal/argus/backend && go build ./... && go vet ./internal/api/ ./internal/store/ && go test ./internal/api/ -run 'TestCreateMemory|TestNewMCPServer' -count=1 -v 2>&1 | tail -20
```

Expected: all PASS with `TEST_DATABASE_URL`.

- [ ] **Step 9: Commit**

```bash
cd /Users/belazy/personal/argus
git add backend/internal/store/patterns.go backend/internal/store/sqlc/query/patterns.sql backend/internal/store/db/patterns.sql.go backend/internal/store/pattern_create_or_get_pg_test.go backend/internal/api/handlers_patterns.go backend/internal/api/mcp_tools_memory_write.go backend/internal/api/mcp_tools_memory_write_test.go backend/internal/api/mcp_server.go
git commit -m "feat: atomic manual-pattern create-or-get and the create_memory MCP tool

CreatePattern upserts: an exact retry rewrote source, repo and content,
touched created_by and queued another mirror upsert — which can revive a
retired memory. A handler-level check-then-insert would not close the
race either: two callers can both observe no row.

CreateOrGetPattern takes the per-identity advisory lock the mirror
worker already uses, rechecks for an existing row (lowest id when legacy
siblings share an identity) and returns it untouched, or inserts and
enqueues exactly one event. CreatePattern itself now takes the same lock
so pipeline writers participate in the protocol while keeping their
upsert semantics. The dashboard's manual create switches to it.

create_memory validates the scope matrix before deriving identity,
sanitizes NUL before hashing, returns a discriminated status
(created / existing / confirmation_required), runs the advisory
similarity check at the enrichment threshold before any lock, and
requires acknowledgment to write when that check cannot run. Exact
duplicates are reused regardless of confirm_duplicate."
```

---

### Task 9: Guarded transactional delete (N2) and `delete_memory`

**Files:**
- Modify: `backend/internal/store/patterns.go` (extract `deletePatternTx`; add `IsHumanAuthoredSource`, `PatternDeletion`, `ErrPatternNotFound`, `ErrPipelineLearnedPattern`, `DeletePatternGuarded`)
- Modify: `backend/internal/api/mcp_tools_memory_write.go` (append `delete_memory`)
- Modify: `backend/internal/api/mcp_server.go` (`registerMCPTools`: one `AddTool`)
- Test: `backend/internal/store/pattern_delete_guarded_pg_test.go`, `backend/internal/api/mcp_tools_memory_write_test.go` (append)

**Interfaces:**
- Produces:
  ```go
  func IsHumanAuthoredSource(source string) bool            // store; manual | remember_command
  var ErrPatternNotFound = errors.New("pattern not found")
  var ErrPipelineLearnedPattern = errors.New("pattern was learned by the review pipeline")
  type PatternDeletion struct { CustomID, Source string; SiblingsAtDelete int }
  func (s *Store) DeletePatternGuarded(ctx, id int64, installationIDs []int64, allowPipelineLearned bool) (PatternDeletion, error)
  ```
  handler `deleteMemory`, types `deleteMemoryInput`, `deleteMemoryOutput`; test helper `seedPatternWithSource`.

- [ ] **Step 1: Write the failing store test**

Create `backend/internal/store/pattern_delete_guarded_pg_test.go`:

```go
package store

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestDeletePatternGuarded(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	installA, _, _ := seedLearnTenant(t, ctx, pool, "mcp-delete-a")
	installB, _, _ := seedLearnTenant(t, ctx, pool, "mcp-delete-b")
	st := NewWithDB(pool)
	t.Cleanup(func() {
		bg := context.Background()
		for _, id := range []int64{installA, installB} {
			_, _ = pool.Exec(bg, `DELETE FROM memory_mirror_outbox WHERE installation_id = $1`, id)
			_, _ = pool.Exec(bg, `DELETE FROM patterns WHERE installation_id = $1`, id)
		}
	})
	manual, learned := "manual", "auto_learn"

	// Human-authored, sole owner.
	cid := "sm_del_manual"
	p, err := st.CreatePattern(ctx, installA, nil, "delete me", nil, nil, &manual, nil, nil, &cid, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := st.DeletePatternGuarded(ctx, p.ID, []int64{installA}, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.CustomID != cid || res.Source != "manual" || res.SiblingsAtDelete != 0 {
		t.Fatalf("res = %+v", res)
	}
	if _, err := st.GetPattern(ctx, p.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("row still present: %v", err)
	}
	var deleteEvents int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM memory_mirror_outbox WHERE installation_id=$1 AND aggregate_id=$2 AND operation='delete'`, installA, p.ID).Scan(&deleteEvents); err != nil || deleteEvents != 1 {
		t.Fatalf("delete outbox events = %d err=%v, want 1", deleteEvents, err)
	}

	// Pipeline-learned: refused without the flag, row untouched; allowed with it.
	lcid := "sm_del_learned"
	l, err := st.CreatePattern(ctx, installA, nil, "learned", nil, nil, &learned, nil, nil, &lcid, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DeletePatternGuarded(ctx, l.ID, []int64{installA}, false); !errors.Is(err, ErrPipelineLearnedPattern) {
		t.Fatalf("learned without flag: %v", err)
	}
	if _, err := st.GetPattern(ctx, l.ID); err != nil {
		t.Fatalf("refused delete must roll back: %v", err)
	}
	if res, err := st.DeletePatternGuarded(ctx, l.ID, []int64{installA}, true); err != nil || res.Source != "auto_learn" {
		t.Fatalf("learned with flag: res=%+v err=%v", res, err)
	}

	// Legacy siblings: deleting one reports the survivor.
	doc := "sm_del_pair"
	a, _ := st.CreatePattern(ctx, installA, nil, "pair", &doc, nil, &manual, nil, nil, nil, nil)
	if _, err := st.CreatePattern(ctx, installA, nil, "pair", &doc, nil, &manual, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if res, err := st.DeletePatternGuarded(ctx, a.ID, []int64{installA}, false); err != nil || res.SiblingsAtDelete != 1 || res.CustomID != doc {
		t.Fatalf("sibling delete: res=%+v err=%v", res, err)
	}

	// Foreign tenant and missing id are both ErrPatternNotFound.
	fcid := "sm_del_foreign"
	f, _ := st.CreatePattern(ctx, installB, nil, "theirs", nil, nil, &manual, nil, nil, &fcid, nil)
	if _, err := st.DeletePatternGuarded(ctx, f.ID, []int64{installA}, true); !errors.Is(err, ErrPatternNotFound) {
		t.Fatalf("foreign: %v", err)
	}
	if _, err := st.GetPattern(ctx, f.ID); err != nil {
		t.Fatalf("foreign row must be untouched: %v", err)
	}
	if _, err := st.DeletePatternGuarded(ctx, 1<<40, []int64{installA}, true); !errors.Is(err, ErrPatternNotFound) {
		t.Fatalf("missing: %v", err)
	}
}
```

- [ ] **Step 2: Run to verify failure**

```bash
cd /Users/belazy/personal/argus/backend && go test ./internal/store/ -run TestDeletePatternGuarded 2>&1 | tail -4
```

Expected: build failure `st.DeletePatternGuarded undefined`.

- [ ] **Step 3: Implement the guarded delete**

In `backend/internal/store/patterns.go`: replace `DeletePattern`'s closure body with a call to the extracted helper, and add the helper and the new operation:

```go
	return s.WithMemoryMirrorTx(ctx, func(tx pgx.Tx) (MemoryMirrorEvent, error) {
		_, event, err := deletePatternTx(ctx, tx, id, installationIDs)
		return event, err
	})
}

// IsHumanAuthoredSource reports whether a pattern source was written by a
// person (dashboard or /argus remember) rather than learned by the pipeline.
// Guarded delete and retire refuse other sources without acknowledgment.
func IsHumanAuthoredSource(source string) bool {
	return source == "manual" || source == "remember_command"
}

var (
	ErrPatternNotFound        = errors.New("pattern not found")
	ErrPipelineLearnedPattern = errors.New("pattern was learned by the review pipeline")
)

// PatternDeletion is what DeletePatternGuarded observed inside its
// transaction. SiblingsAtDelete counts the OTHER rows still carrying the same
// identity after the delete; it is a snapshot, not the mirror worker's later
// decision — a concurrent writer can change ownership before the worker runs.
type PatternDeletion struct {
	CustomID         string
	Source           string
	SiblingsAtDelete int
}

// deletePatternTx is DeletePattern's delete + tombstone event inside a
// caller's transaction; shared with DeletePatternGuarded.
func deletePatternTx(ctx context.Context, tx pgx.Tx, id int64, installationIDs []int64) (db.DeletePatternRow, MemoryMirrorEvent, error) {
	q := db.New(tx)
	row, err := q.DeletePattern(ctx, db.DeletePatternParams{ID: id, InstallationIds: installationIDs})
	if errors.Is(err, pgx.ErrNoRows) {
		return row, MemoryMirrorEvent{}, ErrPatternNotFound
	}
	if err != nil {
		return row, MemoryMirrorEvent{}, err
	}
	repo := ""
	if row.RepoID != nil {
		repoRow, err := q.GetRepoScoped(ctx, db.GetRepoScopedParams{ID: *row.RepoID, Column2: []int64{row.InstallationID}})
		if err != nil {
			return row, MemoryMirrorEvent{}, fmt.Errorf("resolve deleted pattern repo: %w", err)
		}
		_, repo, _ = strings.Cut(repoRow.FullName, "/")
		if repo == "" {
			return row, MemoryMirrorEvent{}, fmt.Errorf("repo %d has invalid full name", *row.RepoID)
		}
	}
	// Legacy rows can predate both identity columns. Keep the full pattern
	// projection in the tombstone so the memory worker can reconstruct the
	// deterministic ID after the relational row is gone.
	customID := firstNonEmpty(row.MemoryCustomID, row.MemoryDocID)
	pattern := Pattern{
		ID: id, InstallationID: row.InstallationID, RepoID: row.RepoID,
		Content: row.Content, Source: row.Source, Category: row.Category, PRNumber: row.PRNumber,
	}
	payload, err := newPatternOutboxPayload(pattern, customID, repo, nil)
	if err != nil {
		return row, MemoryMirrorEvent{}, err
	}
	return row, MemoryMirrorEvent{
		InstallationID: row.InstallationID, AggregateType: MemoryMirrorPattern, AggregateID: id,
		Operation: MemoryMirrorDelete, Payload: payload,
	}, nil
}

// DeletePatternGuarded deletes one pattern row after checking, inside the same
// transaction and against the locked row, that its source is human-authored or
// the caller acknowledged deleting pipeline-learned knowledge. A refused delete
// rolls back. The result carries the identity and the sibling snapshot so the
// caller can say whether the memory is expected to survive this delete.
func (s *Store) DeletePatternGuarded(ctx context.Context, id int64, installationIDs []int64, allowPipelineLearned bool) (storeResult0 PatternDeletion, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "DeletePatternGuarded",

			"id",
			storeLogValue(id), "installation_ids_count", len(installationIDs), "allow_pipeline_learned", allowPipelineLearned)
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	var result PatternDeletion
	err := s.WithMemoryMirrorTx(ctx, func(tx pgx.Tx) (MemoryMirrorEvent, error) {
		var source string
		err := tx.QueryRow(ctx, `SELECT COALESCE(source, 'manual') FROM patterns WHERE id = $1 AND installation_id = ANY($2::bigint[]) FOR UPDATE`, id, installationIDs).Scan(&source)
		if errors.Is(err, pgx.ErrNoRows) {
			return MemoryMirrorEvent{}, ErrPatternNotFound
		}
		if err != nil {
			return MemoryMirrorEvent{}, fmt.Errorf("locking pattern for delete: %w", err)
		}
		if !IsHumanAuthoredSource(source) && !allowPipelineLearned {
			return MemoryMirrorEvent{}, ErrPipelineLearnedPattern
		}
		row, event, err := deletePatternTx(ctx, tx, id, installationIDs)
		if err != nil {
			return MemoryMirrorEvent{}, err
		}
		identity := firstNonEmpty(row.MemoryCustomID, row.MemoryDocID)
		var siblings int
		if identity != "" {
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM patterns WHERE installation_id = $1 AND `+patternIdentityExpr+` = $2`, row.InstallationID, identity).Scan(&siblings); err != nil {
				return MemoryMirrorEvent{}, fmt.Errorf("counting sibling patterns: %w", err)
			}
		}
		result = PatternDeletion{CustomID: identity, Source: row.Source, SiblingsAtDelete: siblings}
		return event, nil
	})
	if err != nil {
		return PatternDeletion{}, err
	}
	return result, nil
}
```

```bash
cd /Users/belazy/personal/argus/backend && go build ./... && go test ./internal/store/ -run 'TestDeletePatternGuarded' -count=1 -v 2>&1 | tail -6 && go test ./internal/store/ -run 'DeletePattern|Mirror' -count=1 2>&1 | tail -3
```

Expected: PASS; the existing DeletePattern / mirror tests still pass.

- [ ] **Step 4: Write the failing tool tests**

Append to `backend/internal/api/mcp_tools_memory_write_test.go`:

```go
func seedPatternWithSource(t *testing.T, ctx context.Context, s *Server, installID int64, repoID *int64, content, source string) (int64, string) {
	t.Helper()
	customID := memory.SharedPatternCustomID(source, content)
	if repoID != nil {
		customID = memory.PatternCustomID("", repoShortName(t, ctx, s, installID, *repoID), source, content)
	}
	src := source
	p, err := s.store.CreatePattern(ctx, installID, repoID, content, nil, nil, &src, nil, nil, &customID, nil)
	if err != nil {
		t.Fatalf("seed pattern: %v", err)
	}
	return p.ID, customID
}

func TestDeleteMemory(t *testing.T) {
	ctx, s, installID, repoID, _, _ := newMemoryWriteFixture(t)
	tools := &mcpTools{srv: s, scope: writeScope(installID)}

	id, cid := seedPatternWithSource(t, ctx, s, installID, &repoID, "delete me", "manual")
	_, out, err := tools.deleteMemory(ctx, nil, deleteMemoryInput{PatternID: id})
	if err != nil {
		t.Fatal(err)
	}
	if !out.PatternDeleted || out.CustomID != cid || out.Source != "manual" || out.SiblingsAtDelete != 0 || out.RetentionExpected || out.MirrorState != "pending" {
		t.Fatalf("out = %+v", out)
	}

	learned, _ := seedPatternWithSource(t, ctx, s, installID, &repoID, "learned", "auto_learn")
	if _, _, err := tools.deleteMemory(ctx, nil, deleteMemoryInput{PatternID: learned}); err == nil || !strings.Contains(err.Error(), "confirm_pipeline_learned") {
		t.Fatalf("pipeline-learned without acknowledgment: %v", err)
	}
	if _, out, err := tools.deleteMemory(ctx, nil, deleteMemoryInput{PatternID: learned, ConfirmPipelineLearned: true}); err != nil || out.Source != "auto_learn" {
		t.Fatalf("acknowledged: out=%+v err=%v", out, err)
	}

	// Sibling survives → retention expected.
	doc := "sm_api_pair"
	src := "manual"
	a, _ := s.store.CreatePattern(ctx, installID, &repoID, "pair", &doc, nil, &src, nil, nil, nil, nil)
	if _, err := s.store.CreatePattern(ctx, installID, &repoID, "pair", &doc, nil, &src, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, out, err := tools.deleteMemory(ctx, nil, deleteMemoryInput{PatternID: a.ID}); err != nil || out.SiblingsAtDelete != 1 || !out.RetentionExpected {
		t.Fatalf("sibling: out=%+v err=%v", out, err)
	}

	pool, _ := architectureTestPool(t)
	foreignInstall, foreignRepo := seedArchitectureRepo(t, ctx, pool)
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM patterns WHERE installation_id = $1`, foreignInstall) })
	foreign, _ := seedPatternWithSource(t, ctx, s, foreignInstall, &foreignRepo, "not yours", "manual")
	if _, _, err := tools.deleteMemory(ctx, nil, deleteMemoryInput{PatternID: foreign, ConfirmPipelineLearned: true}); !errors.Is(err, errNotAccessible) {
		t.Fatalf("foreign: %v", err)
	}
	if _, _, err := tools.deleteMemory(ctx, nil, deleteMemoryInput{PatternID: 1 << 40}); !errors.Is(err, errNotAccessible) {
		t.Fatalf("missing must match foreign: %v", err)
	}
	noWrite := &mcpTools{srv: s, scope: readScope(installID)}
	if _, _, err := noWrite.deleteMemory(ctx, nil, deleteMemoryInput{PatternID: id, ConfirmPipelineLearned: true}); err == nil || err.Error() != errInsufficientScope(scopeMemoryWrite).Error() {
		t.Fatalf("read-only token: %v", err)
	}
}
```

- [ ] **Step 5: Run to verify failure**

```bash
cd /Users/belazy/personal/argus/backend && go test ./internal/api/ -run 'TestDeleteMemory' 2>&1 | tail -4
```

Expected: build failure — `deleteMemory`, `deleteMemoryInput` undefined.

- [ ] **Step 6: Implement `delete_memory`**

Append to `backend/internal/api/mcp_tools_memory_write.go`:

```go
type deleteMemoryInput struct {
	PatternID              int64 `json:"pattern_id" jsonschema:"a contributing pattern id from search_memory.pattern_ids or create_memory"`
	ConfirmPipelineLearned bool  `json:"confirm_pipeline_learned,omitempty" jsonschema:"acknowledge deleting a pattern the review pipeline learned (any source other than manual or remember_command); irreversible"`
}

type deleteMemoryOutput struct {
	PatternDeleted    bool   `json:"pattern_deleted"`
	CustomID          string `json:"custom_id" jsonschema:"the memory identity this pattern contributed to"`
	Source            string `json:"source" jsonschema:"the deleted pattern's source"`
	SiblingsAtDelete  int    `json:"siblings_at_delete" jsonschema:"other pattern rows still carrying the same identity at the moment of deletion"`
	RetentionExpected bool   `json:"retention_expected" jsonschema:"true when siblings remain, so the memory is expected to stay searchable"`
	MirrorState       string `json:"mirror_state" jsonschema:"pending: the memory row is removed later by the mirror worker, and only if no sibling still owns it. Use retire_memory to stop retrieval now."`
}

// deleteMemory removes ONE contributing pattern row. Tenant, source guard,
// delete and sibling snapshot all happen inside DeletePatternGuarded's
// transaction; a foreign or missing id gets the same fixed answer.
func (t *mcpTools) deleteMemory(ctx context.Context, _ *mcp.CallToolRequest, in deleteMemoryInput) (*mcp.CallToolResult, deleteMemoryOutput, error) {
	var zero deleteMemoryOutput
	if err := t.requireScope(scopeMemoryWrite); err != nil {
		return nil, zero, err
	}
	if in.PatternID == 0 {
		return nil, zero, errors.New("pattern_id is required")
	}
	res, err := t.srv.store.DeletePatternGuarded(ctx, in.PatternID, t.scope.installationIDs, in.ConfirmPipelineLearned)
	switch {
	case errors.Is(err, store.ErrPatternNotFound):
		return nil, zero, errNotAccessible
	case errors.Is(err, store.ErrPipelineLearnedPattern):
		return nil, zero, fmt.Errorf("pattern %d was learned by the review pipeline; pass confirm_pipeline_learned=true to acknowledge deleting it — this cannot be undone", in.PatternID)
	case err != nil:
		return nil, zero, t.internalErr(ctx, "delete_memory", err)
	}
	t.srv.logger.InfoContext(ctx, "mcp memory pattern deleted", "user", t.scope.userID, "org", t.scope.orgID, "pattern_id", in.PatternID, "custom_id", res.CustomID, "source", res.Source, "siblings_at_delete", res.SiblingsAtDelete)
	return nil, deleteMemoryOutput{
		PatternDeleted: true, CustomID: res.CustomID, Source: res.Source,
		SiblingsAtDelete: res.SiblingsAtDelete, RetentionExpected: res.SiblingsAtDelete > 0, MirrorState: "pending",
	}, nil
}
```

Add `"github.com/BeLazy167/argus/backend/internal/store"` to this file's imports. Register:

```go
	mcp.AddTool(server, &mcp.Tool{
		Name:        "delete_memory",
		Description: "Permanently delete ONE contributing pattern row. The memory itself is removed later by the mirror worker, and only if no other pattern still owns it — check siblings_at_delete and retention_expected. To stop a memory being retrieved, use retire_memory instead. Refuses pipeline-learned patterns unless confirm_pipeline_learned=true. Requires argus:memory:write.",
	}, t.deleteMemory)
```

- [ ] **Step 7: Run the tests**

```bash
cd /Users/belazy/personal/argus/backend && go build ./... && go vet ./internal/api/ && go test ./internal/api/ -run 'TestDeleteMemory|TestNewMCPServer' -count=1 -v 2>&1 | tail -10
```

Expected: PASS with `TEST_DATABASE_URL`.

- [ ] **Step 8: Commit**

```bash
cd /Users/belazy/personal/argus
git add backend/internal/store/patterns.go backend/internal/store/pattern_delete_guarded_pg_test.go backend/internal/api/mcp_tools_memory_write.go backend/internal/api/mcp_tools_memory_write_test.go backend/internal/api/mcp_server.go
git commit -m "feat: guarded transactional pattern delete and the delete_memory MCP tool

DeletePatternGuarded locks the row, enforces the human-authored source
guard against THAT row (not an earlier handler read), deletes, enqueues
the tombstone, and counts remaining siblings — all in one transaction, so
a refused delete rolls back and the snapshot is consistent.

delete_memory reports pattern_deleted, custom_id, source,
siblings_at_delete, retention_expected and mirror_state:pending. There is
deliberately no 'memory removed' boolean: the worker decides later, a
stalled mirror can leave even the last contribution searchable, and the
tool description points callers wanting retrieval stopped at retire_memory."
```

---

### Task 10: Guarded retirement on the indexer seam (N8) and `retire_memory`

**Files:**
- Modify: `backend/internal/memory/indexer.go` (interface method; `RetireRequest`, `RetireResult`, sentinels, mode consts, `knownMemorySources`)
- Modify: `backend/internal/memory/pgindexer.go:880-940` (SQL constants; `RetireDocument`; wrap not-found with `ErrDocumentNotFound`)
- Modify: `backend/internal/memory/memorytest/fake.go` (`RetireFn`, `Retired`, `RetireDocument`)
- Modify: `backend/internal/api/mcp_tools_memory_read_test.go` (uncomment `recordingIndexer.retireFn` + `RetireDocument` if they were commented out in Task 7)
- Modify: `backend/internal/api/mcp_tools_memory_write.go` (append `retire_memory`)
- Modify: `backend/internal/api/mcp_server.go` (`registerMCPTools`: one `AddTool`)
- Test: `backend/internal/memory/pgindexer_retire_pg_test.go`, `backend/internal/api/mcp_tools_memory_write_test.go` (append)

**Interfaces:**
- Produces (package `memory`):
  ```go
  type RetireRequest struct { CustomID, ReplacementCustomID string; AllowPipelineLearned bool }
  type RetireResult struct { Mode, Source string }
  const RetireModeInvalidated = "invalidated"; const RetireModeSuperseded = "superseded"
  var ErrDocumentNotFound, ErrPipelineLearnedMemory, ErrUnknownProvenance, ErrReplacementNotLive error
  // on Indexer:
  RetireDocument(ctx context.Context, req RetireRequest) (RetireResult, error)
  ```
  handler `retireMemory`, types `retireMemoryInput`, `retireMemoryOutput`.
- Consumes: `store.IsHumanAuthoredSource` (Task 9; `memory` already imports `store`), `memories` columns `metadata jsonb`, `invalidated_at`, `superseded_by`, `deleted_at`; `pgTestPool`, `discardLogger`, `NewPGIndexer(pool, embedder, installationID, dims, logger)`.

- [ ] **Step 1: Write the failing memory-package test**

Create `backend/internal/memory/pgindexer_retire_pg_test.go`:

```go
package memory

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func seedMemoryRow(t *testing.T, ctx context.Context, pool *pgxpool.Pool, install int64, customID, source string) {
	t.Helper()
	metadata := `{}`
	if source != "" {
		metadata = `{"source":"` + source + `"}`
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO memories (installation_id, container_tag, custom_id, type, content, metadata)
		VALUES ($1, 'x', $2, 'pattern', $2, $3::jsonb)`, install, customID, metadata); err != nil {
		t.Fatalf("seed memory %s: %v", customID, err)
	}
}

func isLive(t *testing.T, ctx context.Context, pool *pgxpool.Pool, install int64, customID string) bool {
	t.Helper()
	var live bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM live_memories WHERE installation_id=$1 AND custom_id=$2)`, install, customID).Scan(&live); err != nil {
		t.Fatal(err)
	}
	return live
}

func TestRetireDocument(t *testing.T) {
	pool, install := pgTestPool(t)
	ctx := context.Background()
	idx := NewPGIndexer(pool, nil, install, StorageDimensions, discardLogger())
	seedMemoryRow(t, ctx, pool, install, "human", "manual")
	seedMemoryRow(t, ctx, pool, install, "learned", "auto_learn")
	seedMemoryRow(t, ctx, pool, install, "orphan", "")
	seedMemoryRow(t, ctx, pool, install, "weird", "not_a_known_source")
	seedMemoryRow(t, ctx, pool, install, "replacement", "manual")
	seedMemoryRow(t, ctx, pool, install, "retired-replacement", "manual")
	if _, err := pool.Exec(ctx, `UPDATE memories SET invalidated_at = now() WHERE installation_id=$1 AND custom_id='retired-replacement'`, install); err != nil {
		t.Fatal(err)
	}

	// Human-authored: invalidated; repeat is idempotent.
	res, err := idx.RetireDocument(ctx, RetireRequest{CustomID: "human"})
	if err != nil || res.Mode != RetireModeInvalidated || res.Source != "manual" {
		t.Fatalf("human: res=%+v err=%v", res, err)
	}
	if isLive(t, ctx, pool, install, "human") {
		t.Fatal("retired memory still live")
	}
	if _, err := idx.RetireDocument(ctx, RetireRequest{CustomID: "human"}); err != nil {
		t.Fatalf("repeat must be idempotent: %v", err)
	}

	// Pipeline-learned: refused, still live; allowed with acknowledgment.
	if _, err := idx.RetireDocument(ctx, RetireRequest{CustomID: "learned"}); !errors.Is(err, ErrPipelineLearnedMemory) {
		t.Fatalf("learned: %v", err)
	}
	if !isLive(t, ctx, pool, install, "learned") {
		t.Fatal("refused retirement must leave the row live")
	}
	if res, err := idx.RetireDocument(ctx, RetireRequest{CustomID: "learned", AllowPipelineLearned: true}); err != nil || res.Source != "auto_learn" {
		t.Fatalf("learned acknowledged: res=%+v err=%v", res, err)
	}

	// Unknown provenance is an error even with the flag.
	for _, id := range []string{"orphan", "weird"} {
		if _, err := idx.RetireDocument(ctx, RetireRequest{CustomID: id, AllowPipelineLearned: true}); !errors.Is(err, ErrUnknownProvenance) {
			t.Fatalf("%s: %v", id, err)
		}
		if !isLive(t, ctx, pool, install, id) {
			t.Fatalf("%s must stay live", id)
		}
	}

	// Supersede: replacement must be distinct and live in this installation.
	seedMemoryRow(t, ctx, pool, install, "old", "manual")
	for _, bad := range []string{"old", "missing", "retired-replacement"} {
		if _, err := idx.RetireDocument(ctx, RetireRequest{CustomID: "old", ReplacementCustomID: bad}); !errors.Is(err, ErrReplacementNotLive) {
			t.Fatalf("replacement %q: %v", bad, err)
		}
		if !isLive(t, ctx, pool, install, "old") {
			t.Fatalf("failed supersede (%q) must leave the source live", bad)
		}
	}
	res, err = idx.RetireDocument(ctx, RetireRequest{CustomID: "old", ReplacementCustomID: "replacement"})
	if err != nil || res.Mode != RetireModeSuperseded {
		t.Fatalf("supersede: res=%+v err=%v", res, err)
	}
	if _, err := idx.RetireDocument(ctx, RetireRequest{CustomID: "old", ReplacementCustomID: "replacement"}); err != nil {
		t.Fatalf("repeat supersede must be idempotent: %v", err)
	}

	// Missing memory.
	if _, err := idx.RetireDocument(ctx, RetireRequest{CustomID: "nope", AllowPipelineLearned: true}); !errors.Is(err, ErrDocumentNotFound) {
		t.Fatalf("missing: %v", err)
	}
	// The pre-existing single-purpose methods now wrap the same sentinel.
	if err := idx.InvalidateDocument(ctx, "nope"); !errors.Is(err, ErrDocumentNotFound) {
		t.Fatalf("InvalidateDocument missing: %v", err)
	}
}
```

- [ ] **Step 2: Run to verify failure**

```bash
cd /Users/belazy/personal/argus/backend && go test ./internal/memory/ -run TestRetireDocument 2>&1 | tail -4
```

Expected: build failure — `RetireRequest`, `RetireDocument`, sentinels undefined.

- [ ] **Step 3: Add the seam types and PG implementation**

In `backend/internal/memory/indexer.go`, add to the `Indexer` interface after `SupersedeDocument`:

```go
	// RetireDocument is the guarded lifecycle transition for callers acting on
	// behalf of a person: it reads provenance from the memory row itself,
	// enforces the human-authored/acknowledged rule, validates a replacement,
	// and applies invalidate-or-supersede in ONE transaction. MCP must use this
	// rather than the unguarded methods above.
	RetireDocument(ctx context.Context, req RetireRequest) (RetireResult, error)
```

and after the `Source*` constants block:

```go
// RetireRequest names one memory to retire. ReplacementCustomID empty means
// plain invalidation; set, the memory is superseded by that replacement.
type RetireRequest struct {
	CustomID             string
	ReplacementCustomID  string
	AllowPipelineLearned bool
}

// RetireResult reports what happened and the provenance the guard read.
type RetireResult struct {
	Mode   string // RetireModeInvalidated | RetireModeSuperseded
	Source string
}

const (
	RetireModeInvalidated = "invalidated"
	RetireModeSuperseded  = "superseded"
)

var (
	// ErrDocumentNotFound: no live memory row for (installation, custom_id).
	ErrDocumentNotFound = errors.New("document not found")
	// ErrPipelineLearnedMemory: the row's source is not human-authored and the
	// caller did not acknowledge retiring pipeline-learned knowledge.
	ErrPipelineLearnedMemory = errors.New("memory was learned by the review pipeline")
	// ErrUnknownProvenance: metadata carries no source, or one this build does
	// not recognize. Refused even with acknowledgment — resolve provenance first.
	ErrUnknownProvenance = errors.New("memory provenance is unknown")
	// ErrReplacementNotLive: the replacement is missing, retired, deleted, in
	// another installation, or is the source itself.
	ErrReplacementNotLive = errors.New("replacement must be a distinct live memory in the same installation")
)

// knownMemorySources is every `source` value a writer in this codebase stamps
// into memory metadata. Retirement refuses a source outside this set because
// it cannot classify the knowledge. When adding a writer with a new source,
// add it here; verify with:
//
//	grep -rhoE 'Source: *"[a-z_]+"|source *[:=]+ *"[a-z_]+"' internal --include='*.go' | sort -u
var knownMemorySources = map[string]bool{
	"manual": true, "remember_command": true,
	"auto_learn": true, "org_learned": true, "convention_extraction": true,
	"synthesis": true, "scoring_confirmed": true, "pr_summary": true, "arch_summary": true,
	"pattern": true, "author": true, "dashboard": true, "vendors": true,
	SourceLegacyReplyFeedback: true, SourceTrustedReplyFeedback: true, SourceTrustedReplyLearning: true,
	SourceReactionFeedback: true, SourceAutomaticPraise: true,
}
```

Run the grep in that comment and add any literal it prints that is missing from the map. Ensure `"errors"` is imported in `indexer.go`.

In `backend/internal/memory/pgindexer.go`, hoist the two UPDATE statements into constants and use them in `InvalidateDocument`, `SupersedeDocument` and the new method:

```go
const invalidateMemorySQL = `
	UPDATE memories
	SET invalidated_at = COALESCE(invalidated_at, now()),
	    updated_at = CASE WHEN invalidated_at IS NULL THEN now() ELSE updated_at END
	WHERE installation_id = $1 AND custom_id = $2 AND deleted_at IS NULL`

const supersedeMemorySQL = `
	UPDATE memories AS source
	SET invalidated_at = COALESCE(source.invalidated_at, now()),
	    superseded_by = replacement.id,
	    updated_at = CASE
	      WHEN source.invalidated_at IS NULL OR source.superseded_by IS DISTINCT FROM replacement.id THEN now()
	      ELSE source.updated_at
	    END
	FROM live_memories AS replacement
	WHERE source.installation_id = $1
	  AND source.custom_id = $2
	  AND source.deleted_at IS NULL
	  AND (source.superseded_by IS NULL OR source.superseded_by = replacement.id)
	  AND replacement.installation_id = source.installation_id
	  AND replacement.custom_id = $3`
```

`InvalidateDocument` executes `invalidateMemorySQL` and on zero rows returns `fmt.Errorf("invalidating memory %s: %w", documentID, ErrDocumentNotFound)`; `SupersedeDocument` executes `supersedeMemorySQL` and on zero rows returns `fmt.Errorf("superseding memory %s with %s: %w", documentID, replacementID, ErrDocumentNotFound)`. Then add:

```go
// RetireDocument implements the guarded transition. Provenance comes from the
// locked memory row's metadata — never from the caller or a patterns row, so
// it works for memories the pipeline wrote directly — and the guard and the
// transition commit together, so a concurrent metadata change cannot slip a
// pipeline-learned row past an unacknowledged caller.
func (idx *PGIndexer) RetireDocument(ctx context.Context, req RetireRequest) (RetireResult, error) {
	if req.CustomID == "" {
		return RetireResult{}, fmt.Errorf("retiring memory: custom id is required")
	}
	tx, err := idx.pool.Begin(ctx)
	if err != nil {
		return RetireResult{}, fmt.Errorf("retiring memory %s: begin: %w", req.CustomID, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var source string
	err = tx.QueryRow(ctx, `
		SELECT COALESCE(metadata->>'source', '')
		FROM memories
		WHERE installation_id = $1 AND custom_id = $2 AND deleted_at IS NULL
		FOR UPDATE`, idx.installationID, req.CustomID).Scan(&source)
	if errors.Is(err, pgx.ErrNoRows) {
		return RetireResult{}, fmt.Errorf("retiring memory %s: %w", req.CustomID, ErrDocumentNotFound)
	}
	if err != nil {
		return RetireResult{}, fmt.Errorf("retiring memory %s: lock: %w", req.CustomID, err)
	}
	if !knownMemorySources[source] {
		return RetireResult{}, fmt.Errorf("retiring memory %s (source %q): %w", req.CustomID, source, ErrUnknownProvenance)
	}
	if !store.IsHumanAuthoredSource(source) && !req.AllowPipelineLearned {
		return RetireResult{}, fmt.Errorf("retiring memory %s (source %q): %w", req.CustomID, source, ErrPipelineLearnedMemory)
	}

	mode := RetireModeInvalidated
	if req.ReplacementCustomID != "" {
		if req.ReplacementCustomID == req.CustomID {
			return RetireResult{}, fmt.Errorf("retiring memory %s: %w", req.CustomID, ErrReplacementNotLive)
		}
		// Lock the replacement's live state for the rest of the transaction.
		var replacementID int64
		err := tx.QueryRow(ctx, `
			SELECT id FROM memories
			WHERE installation_id = $1 AND custom_id = $2
			  AND deleted_at IS NULL AND invalidated_at IS NULL AND superseded_by IS NULL
			FOR SHARE`, idx.installationID, req.ReplacementCustomID).Scan(&replacementID)
		if errors.Is(err, pgx.ErrNoRows) {
			return RetireResult{}, fmt.Errorf("retiring memory %s with %s: %w", req.CustomID, req.ReplacementCustomID, ErrReplacementNotLive)
		}
		if err != nil {
			return RetireResult{}, fmt.Errorf("retiring memory %s: replacement lookup: %w", req.CustomID, err)
		}
		tag, err := tx.Exec(ctx, supersedeMemorySQL, idx.installationID, req.CustomID, req.ReplacementCustomID)
		if err != nil {
			return RetireResult{}, fmt.Errorf("superseding memory %s with %s: %w", req.CustomID, req.ReplacementCustomID, err)
		}
		if tag.RowsAffected() == 0 {
			// Already superseded by a DIFFERENT memory: rewriting history needs
			// a separate policy decision, not a silent overwrite.
			return RetireResult{}, fmt.Errorf("retiring memory %s: already superseded by another memory: %w", req.CustomID, ErrReplacementNotLive)
		}
		mode = RetireModeSuperseded
	} else if _, err := tx.Exec(ctx, invalidateMemorySQL, idx.installationID, req.CustomID); err != nil {
		return RetireResult{}, fmt.Errorf("invalidating memory %s: %w", req.CustomID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return RetireResult{}, fmt.Errorf("retiring memory %s: commit: %w", req.CustomID, err)
	}
	return RetireResult{Mode: mode, Source: source}, nil
}
```

Ensure `pgindexer.go` imports `errors`, `github.com/jackc/pgx/v5`, and `github.com/BeLazy167/argus/backend/internal/store` (`memory` → `store` is an existing dependency direction via `mirror_worker.go`).

In `backend/internal/memory/memorytest/fake.go`, add to `Fake`:

```go
	RetireFn func(req memory.RetireRequest) (memory.RetireResult, error)
	Retired  []memory.RetireRequest // RetireDocument
```

and the method:

```go
func (f *Fake) RetireDocument(_ context.Context, req memory.RetireRequest) (memory.RetireResult, error) {
	f.mu.Lock()
	f.Retired = append(f.Retired, req)
	f.mu.Unlock()
	if f.RetireFn != nil {
		return f.RetireFn(req)
	}
	mode := memory.RetireModeInvalidated
	if req.ReplacementCustomID != "" {
		mode = memory.RetireModeSuperseded
	}
	return memory.RetireResult{Mode: mode, Source: "manual"}, nil
}
```

If Task 7 commented out `recordingIndexer.retireFn` / `RetireDocument` in `mcp_tools_memory_read_test.go`, uncomment them now.

```bash
cd /Users/belazy/personal/argus/backend && go build ./... && go vet ./internal/memory/... && go test ./internal/memory/ -run 'TestRetireDocument|TestInvalidate|TestSupersede' -count=1 -v 2>&1 | tail -10 && go test ./internal/pipeline/ -count=1 2>&1 | tail -2
```

Expected: build clean (every `memory.Indexer` implementer compiles — `PGIndexer`, `memorytest.Fake`; embedded-interface stubs are unaffected); the new test PASSes; pipeline tests still pass.

- [ ] **Step 4: Write the failing tool test**

Append to `backend/internal/api/mcp_tools_memory_write_test.go`:

```go
func TestRetireMemory(t *testing.T) {
	ctx, s, installID, _, idx, _ := newMemoryWriteFixture(t)
	tools := &mcpTools{srv: s, scope: writeScope(installID)}
	var got memory.RetireRequest
	idx.retireFn = func(req memory.RetireRequest) (memory.RetireResult, error) {
		got = req
		switch req.CustomID {
		case "human":
			return memory.RetireResult{Mode: memory.RetireModeInvalidated, Source: "manual"}, nil
		case "learned":
			if !req.AllowPipelineLearned {
				return memory.RetireResult{}, memory.ErrPipelineLearnedMemory
			}
			return memory.RetireResult{Mode: memory.RetireModeSuperseded, Source: "auto_learn"}, nil
		case "orphan":
			return memory.RetireResult{}, memory.ErrUnknownProvenance
		case "badrepl":
			return memory.RetireResult{}, memory.ErrReplacementNotLive
		}
		return memory.RetireResult{}, memory.ErrDocumentNotFound
	}

	if _, _, err := tools.retireMemory(ctx, nil, retireMemoryInput{InstallationID: installID, CustomID: "human"}); err == nil {
		t.Fatal("reason is required")
	}
	_, out, err := tools.retireMemory(ctx, nil, retireMemoryInput{InstallationID: installID, CustomID: "human", Reason: "team decision"})
	if err != nil || out.Mode != "invalidated" || out.Source != "manual" || out.CustomID != "human" || got.CustomID != "human" {
		t.Fatalf("human: out=%+v err=%v", out, err)
	}
	if _, _, err := tools.retireMemory(ctx, nil, retireMemoryInput{InstallationID: installID, CustomID: "learned", Reason: "r"}); err == nil || !strings.Contains(err.Error(), "confirm_pipeline_learned") {
		t.Fatalf("learned without acknowledgment: %v", err)
	}
	repl := "new"
	_, out, err = tools.retireMemory(ctx, nil, retireMemoryInput{InstallationID: installID, CustomID: "learned", ReplacedByCustomID: &repl, Reason: "r", ConfirmPipelineLearned: true})
	if err != nil || out.Mode != "superseded" || out.ReplacedBy == nil || *out.ReplacedBy != "new" || got.ReplacementCustomID != "new" || !got.AllowPipelineLearned {
		t.Fatalf("superseded: out=%+v req=%+v err=%v", out, got, err)
	}
	if _, _, err := tools.retireMemory(ctx, nil, retireMemoryInput{InstallationID: installID, CustomID: "orphan", Reason: "r", ConfirmPipelineLearned: true}); err == nil || !strings.Contains(err.Error(), "provenance") {
		t.Fatalf("unknown provenance: %v", err)
	}
	if _, _, err := tools.retireMemory(ctx, nil, retireMemoryInput{InstallationID: installID, CustomID: "badrepl", ReplacedByCustomID: &repl, Reason: "r"}); err == nil || !strings.Contains(err.Error(), "replacement") {
		t.Fatalf("bad replacement: %v", err)
	}
	if _, _, err := tools.retireMemory(ctx, nil, retireMemoryInput{InstallationID: installID, CustomID: "missing", Reason: "r"}); !errors.Is(err, errNotAccessible) {
		t.Fatalf("missing: %v", err)
	}
	if _, _, err := tools.retireMemory(ctx, nil, retireMemoryInput{InstallationID: installID + 99999, CustomID: "human", Reason: "r"}); !errors.Is(err, errNotAccessible) {
		t.Fatalf("foreign installation: %v", err)
	}
	s.indexers = nil
	if _, _, err := tools.retireMemory(ctx, nil, retireMemoryInput{InstallationID: installID, CustomID: "human", Reason: "r"}); !errors.Is(err, errMemoryUnavailable) {
		t.Fatalf("unwired: %v", err)
	}
	noWrite := &mcpTools{srv: s, scope: readScope(installID)}
	if _, _, err := noWrite.retireMemory(ctx, nil, retireMemoryInput{InstallationID: installID, CustomID: "human", Reason: "r", ConfirmPipelineLearned: true}); err == nil || err.Error() != errInsufficientScope(scopeMemoryWrite).Error() {
		t.Fatalf("read-only token: %v", err)
	}
}
```

- [ ] **Step 5: Run to verify failure**

```bash
cd /Users/belazy/personal/argus/backend && go test ./internal/api/ -run 'TestRetireMemory' 2>&1 | tail -4
```

Expected: build failure — `retireMemory`, `retireMemoryInput` undefined.

- [ ] **Step 6: Implement `retire_memory`**

Append to `backend/internal/api/mcp_tools_memory_write.go`:

```go
type retireMemoryInput struct {
	InstallationID         int64   `json:"installation_id" jsonschema:"local installation id from list_repos or search_memory"`
	CustomID               string  `json:"custom_id" jsonschema:"memory identity from search_memory or create_memory"`
	ReplacedByCustomID     *string `json:"replaced_by_custom_id,omitempty" jsonschema:"if set, the memory is superseded by this live memory (invalidate + forward pointer) instead of plainly invalidated"`
	Reason                 string  `json:"reason" jsonschema:"why this knowledge is being retired; recorded in the audit log"`
	ConfirmPipelineLearned bool    `json:"confirm_pipeline_learned,omitempty" jsonschema:"acknowledge retiring a memory the review pipeline learned; irreversible"`
}

type retireMemoryOutput struct {
	Mode       string  `json:"mode" jsonschema:"invalidated or superseded"`
	CustomID   string  `json:"custom_id"`
	Source     string  `json:"source" jsonschema:"the retired memory's recorded provenance"`
	ReplacedBy *string `json:"replaced_by,omitempty"`
}

// retireMemory makes a memory unretrievable. There is no un-invalidate, so a
// reason is required and logged. One tool covers both verbs: supersede is
// invalidate plus a forward pointer, and splitting them invites a caller to
// invalidate first and then fail to link. All checks and the transition run
// in RetireDocument's transaction.
func (t *mcpTools) retireMemory(ctx context.Context, _ *mcp.CallToolRequest, in retireMemoryInput) (*mcp.CallToolResult, retireMemoryOutput, error) {
	var zero retireMemoryOutput
	if err := t.requireScope(scopeMemoryWrite); err != nil {
		return nil, zero, err
	}
	if in.InstallationID == 0 || !containsID(t.scope.installationIDs, in.InstallationID) {
		return nil, zero, errNotAccessible
	}
	if strings.TrimSpace(in.CustomID) == "" {
		return nil, zero, errors.New("custom_id is required")
	}
	if strings.TrimSpace(in.Reason) == "" {
		return nil, zero, errors.New("reason is required: retirement cannot be undone")
	}
	idx := t.srv.resolveIndexer(ctx, in.InstallationID)
	if idx == nil {
		return nil, zero, errMemoryUnavailable
	}
	req := memory.RetireRequest{CustomID: in.CustomID, AllowPipelineLearned: in.ConfirmPipelineLearned}
	if in.ReplacedByCustomID != nil {
		req.ReplacementCustomID = strings.TrimSpace(*in.ReplacedByCustomID)
	}
	res, err := idx.RetireDocument(ctx, req)
	switch {
	case errors.Is(err, memory.ErrDocumentNotFound):
		return nil, zero, errNotAccessible
	case errors.Is(err, memory.ErrPipelineLearnedMemory):
		return nil, zero, fmt.Errorf("memory %s was learned by the review pipeline; pass confirm_pipeline_learned=true to acknowledge retiring it — this cannot be undone", in.CustomID)
	case errors.Is(err, memory.ErrUnknownProvenance):
		return nil, zero, fmt.Errorf("memory %s has unknown provenance; resolve its source before retiring it", in.CustomID)
	case errors.Is(err, memory.ErrReplacementNotLive):
		return nil, zero, errors.New("replacement must be a distinct live memory in the same installation")
	case err != nil:
		return nil, zero, t.internalErr(ctx, "retire_memory", err)
	}
	t.srv.logger.InfoContext(ctx, "mcp memory retired", "user", t.scope.userID, "org", t.scope.orgID, "installation_id", in.InstallationID,
		"custom_id", in.CustomID, "mode", res.Mode, "source", res.Source, "replaced_by", req.ReplacementCustomID, "reason", in.Reason)
	return nil, retireMemoryOutput{Mode: res.Mode, CustomID: in.CustomID, Source: res.Source, ReplacedBy: in.ReplacedByCustomID}, nil
}
```

Register:

```go
	mcp.AddTool(server, &mcp.Tool{
		Name:        "retire_memory",
		Description: "Stop a memory being retrieved (invalidate), or mark it superseded by another live memory when replaced_by_custom_id is given. This is the operation to use when knowledge should no longer influence reviews. Irreversible; a reason is required and logged. Refuses pipeline-learned memories unless confirm_pipeline_learned=true. Requires argus:memory:write.",
	}, t.retireMemory)
```

- [ ] **Step 7: Run the tests**

```bash
cd /Users/belazy/personal/argus/backend && go build ./... && go vet ./internal/api/ && go test ./internal/api/ -run 'TestRetireMemory|TestNewMCPServer' -count=1 -v 2>&1 | tail -8
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
cd /Users/belazy/personal/argus
git add backend/internal/memory/indexer.go backend/internal/memory/pgindexer.go backend/internal/memory/memorytest/fake.go backend/internal/memory/pgindexer_retire_pg_test.go backend/internal/api/mcp_tools_memory_write.go backend/internal/api/mcp_tools_memory_write_test.go backend/internal/api/mcp_tools_memory_read_test.go backend/internal/api/mcp_server.go
git commit -m "feat: guarded memory retirement on the indexer seam and the retire_memory MCP tool

InvalidateDocument and SupersedeDocument apply a transition and nothing
else. A person-facing caller needs the provenance check — is this
knowledge human-authored, or months of pipeline learning? — to happen
against the actual row, atomically with the transition, and to work for
memories the pipeline wrote with no patterns row at all.

RetireDocument locks the memory row, reads source from its metadata,
refuses unknown provenance even with acknowledgment, enforces the
human-authored rule, validates and locks a replacement's live state, and
applies the same UPDATE statements the single-purpose methods use, in one
transaction. Repeats are idempotent; a failed supersede leaves the source
live. The fake records requests and stubs results.

retire_memory maps each sentinel to a fixed error and logs the required
reason with the outcome."
```

---

### Task 11: `list_reviews` and `get_review_status`

**Files:**
- Create: `backend/internal/api/mcp_tools_review.go`
- Modify: `backend/internal/api/mcp_server.go` (`registerMCPTools`: two `AddTool`)
- Test: `backend/internal/api/mcp_tools_review_test.go`

**Interfaces:**
- Produces: `func githubPRURL(fullName string, pr int, githubReviewID *int64) string`, `func (t *mcpTools) scopedReview(ctx, rawID string) (*store.Review, *store.Repo, error)`, `func (t *mcpTools) reviewToolErr(ctx, tool string, err error) error`, handlers `listReviews`, `getReviewStatus`; test helpers `seedReview`, `newReviewFixture`.
- Consumes: `store.ListReviewsScoped(ctx, repoID, installationIDs, limit, offset)`, `store.ListAllReviewsScoped(ctx, installationIDs, limit, offset)`, `store.GetReview(ctx, uuid)`, `store.GetReviewAttemptGeneration(ctx, uuid)`, `store.GetLatestRunStateForReview` (Task 2), `scopedRepo` (Task 7).

- [ ] **Step 1: Write the failing tests**

Create `backend/internal/api/mcp_tools_review_test.go`:

```go
package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BeLazy167/argus/backend/internal/store"
)

func seedReview(t *testing.T, ctx context.Context, pool *pgxpool.Pool, repoID int64, pr int, status string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO reviews (repo_id, pr_number, pr_title, pr_author, head_sha, base_sha, status)
		VALUES ($1, $2, 'seed title', 'someone', 'head', 'base', $3) RETURNING id`, repoID, pr, status).Scan(&id); err != nil {
		t.Fatalf("seed review: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM pipeline_states WHERE review_id = $1`, id)
		_, _ = pool.Exec(bg, `DELETE FROM review_comments WHERE review_id = $1`, id)
		_, _ = pool.Exec(bg, `DELETE FROM reviews WHERE id = $1`, id)
	})
	return id
}

func newReviewFixture(t *testing.T) (context.Context, *pgxpool.Pool, *Server, int64, int64) {
	t.Helper()
	pool, ctx := architectureTestPool(t)
	installID, repoID := seedArchitectureRepo(t, ctx, pool)
	s := &Server{store: store.NewWithDB(pool), logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	return ctx, pool, s, installID, repoID
}

func TestListReviewsScopedAndClamped(t *testing.T) {
	ctx, pool, s, installID, repoID := newReviewFixture(t)
	_, foreignRepo := seedArchitectureRepo(t, ctx, pool)
	mine := seedReview(t, ctx, pool, repoID, 1, "completed")
	theirs := seedReview(t, ctx, pool, foreignRepo, 2, "completed")
	tools := &mcpTools{srv: s, scope: readScope(installID)}

	_, out, err := tools.listReviews(ctx, nil, listReviewsInput{Limit: 1000, Offset: -5})
	if err != nil {
		t.Fatal(err)
	}
	var sawMine, sawTheirs bool
	for _, r := range out.Reviews {
		sawMine = sawMine || r.ReviewID == mine.String()
		sawTheirs = sawTheirs || r.ReviewID == theirs.String()
	}
	if !sawMine || sawTheirs {
		t.Fatalf("sawMine=%v sawTheirs=%v — the unfiltered listing must exclude other installations", sawMine, sawTheirs)
	}
	if _, _, err := tools.listReviews(ctx, nil, listReviewsInput{RepoID: foreignRepo}); !errors.Is(err, errNotAccessible) {
		t.Fatalf("foreign repo_id: %v", err)
	}
	noRead := &mcpTools{srv: s, scope: tenantScope{installationIDs: []int64{installID}, grantedScopes: []string{scopeMemoryWrite}}}
	if _, _, err := noRead.listReviews(ctx, nil, listReviewsInput{}); err == nil || err.Error() != errInsufficientScope(scopeRead).Error() {
		t.Fatalf("missing read scope: %v", err)
	}
}

func TestGetReviewStatusIncludesStageAndGuardsTenant(t *testing.T) {
	ctx, pool, s, installID, repoID := newReviewFixture(t)
	id := seedReview(t, ctx, pool, repoID, 7, "in_progress")
	if _, err := pool.Exec(ctx, `INSERT INTO pipeline_states (review_id, state) VALUES ($1, 'synthesizing')`, id); err != nil {
		t.Fatal(err)
	}
	tools := &mcpTools{srv: s, scope: readScope(installID)}

	_, out, err := tools.getReviewStatus(ctx, nil, getReviewStatusInput{ReviewID: id.String()})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "in_progress" || out.Stage != "synthesizing" || out.PRNumber != 7 || out.RepoFullName == "" || out.GitHubPRURL == "" {
		t.Fatalf("out = %+v", out)
	}
	noRun := seedReview(t, ctx, pool, repoID, 8, "pending")
	if _, out, err := tools.getReviewStatus(ctx, nil, getReviewStatusInput{ReviewID: noRun.String()}); err != nil || out.Stage != "" {
		t.Fatalf("no-run review must omit stage, never guess it from status: out=%+v err=%v", out, err)
	}
	_, foreignRepo := seedArchitectureRepo(t, ctx, pool)
	foreign := seedReview(t, ctx, pool, foreignRepo, 9, "completed")
	if _, _, err := tools.getReviewStatus(ctx, nil, getReviewStatusInput{ReviewID: foreign.String()}); !errors.Is(err, errNotAccessible) {
		t.Fatalf("foreign review: %v", err)
	}
	if _, _, err := tools.getReviewStatus(ctx, nil, getReviewStatusInput{ReviewID: uuid.New().String()}); !errors.Is(err, errNotAccessible) {
		t.Fatalf("missing review must match foreign: %v", err)
	}
	if _, _, err := tools.getReviewStatus(ctx, nil, getReviewStatusInput{ReviewID: "not-a-uuid"}); err == nil || errors.Is(err, errNotAccessible) {
		t.Fatalf("malformed id should be a validation error, got %v", err)
	}
}

func TestGitHubPRURL(t *testing.T) {
	t.Parallel()
	if got := githubPRURL("acme/widgets", 42, nil); got != "https://github.com/acme/widgets/pull/42" {
		t.Fatalf("got %q", got)
	}
	rid := int64(99)
	if got := githubPRURL("acme/widgets", 42, &rid); got != "https://github.com/acme/widgets/pull/42#pullrequestreview-99" {
		t.Fatalf("got %q", got)
	}
}
```

- [ ] **Step 2: Run to verify failure**

```bash
cd /Users/belazy/personal/argus/backend && go test ./internal/api/ -run 'TestListReviews|TestGetReviewStatus|TestGitHubPRURL' 2>&1 | tail -4
```

Expected: build failure — `listReviews`, `getReviewStatus`, `githubPRURL` undefined.

- [ ] **Step 3: Implement**

Create `backend/internal/api/mcp_tools_review.go`:

```go
package api

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/BeLazy167/argus/backend/internal/store"
)

const (
	listReviewsDefaultLimit = 20
	listReviewsMaxLimit     = 100
)

var errReviewIDFormat = errors.New("review_id must be a UUID")

// githubPRURL matches the dashboard's builder: the PR page, anchored to the
// Argus review when one was posted.
func githubPRURL(fullName string, pr int, githubReviewID *int64) string {
	base := "https://github.com/" + fullName + "/pull/" + strconv.Itoa(pr)
	if githubReviewID != nil {
		return base + "#pullrequestreview-" + strconv.FormatInt(*githubReviewID, 10)
	}
	return base
}

// scopedReview is the authorization step for every review-taking tool: load
// the review, then GetRepoScoped on its repo — the ONLY tenant check, exactly
// as handlers_reviews.go getReview does. GetReview itself is unscoped.
func (t *mcpTools) scopedReview(ctx context.Context, rawID string) (*store.Review, *store.Repo, error) {
	id, err := uuid.Parse(rawID)
	if err != nil {
		return nil, nil, errReviewIDFormat
	}
	review, err := t.srv.store.GetReview(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, errNotAccessible
		}
		return nil, nil, err
	}
	repo, err := t.srv.store.GetRepoScoped(ctx, review.RepoID, t.scope.installationIDs)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, errNotAccessible
		}
		return nil, nil, err
	}
	return review, repo, nil
}

// reviewToolErr maps scopedReview's outcomes: caller-facing errors pass
// through, anything else is logged and replaced by the fixed failure string.
func (t *mcpTools) reviewToolErr(ctx context.Context, tool string, err error) error {
	if errors.Is(err, errNotAccessible) || errors.Is(err, errReviewIDFormat) {
		return err
	}
	return t.internalErr(ctx, tool, err)
}

func rfc3339(ts time.Time) string { return ts.UTC().Format(time.RFC3339) }

func rfc3339Ptr(ts *time.Time) *string {
	if ts == nil {
		return nil
	}
	s := rfc3339(*ts)
	return &s
}

type listReviewsInput struct {
	RepoID int64 `json:"repo_id,omitempty" jsonschema:"local repo id; omit for every repo the caller can access"`
	Limit  int   `json:"limit,omitempty" jsonschema:"default 20, max 100"`
	Offset int   `json:"offset,omitempty"`
}

type reviewSummary struct {
	ReviewID      string  `json:"review_id"`
	RepoID        int64   `json:"repo_id"`
	PRNumber      int     `json:"pr_number"`
	PRTitle       string  `json:"pr_title"`
	Status        string  `json:"status" jsonschema:"pending, in_progress, completed, failed, or cancelled"`
	Score         *int    `json:"score,omitempty"`
	DeepReview    bool    `json:"deep_review"`
	IsIncremental bool    `json:"is_incremental"`
	CreatedAt     string  `json:"created_at"`
	CompletedAt   *string `json:"completed_at,omitempty"`
}

type listReviewsOutput struct {
	Reviews []reviewSummary `json:"reviews"`
}

// listReviews projects only summary fields. List rows are not detail rows —
// token_usage, brief and review_contract are absent and diagrams is a
// synthesized "[]" — so returning store.Review here would look complete and
// not be.
func (t *mcpTools) listReviews(ctx context.Context, _ *mcp.CallToolRequest, in listReviewsInput) (*mcp.CallToolResult, listReviewsOutput, error) {
	var zero listReviewsOutput
	if err := t.requireScope(scopeRead); err != nil {
		return nil, zero, err
	}
	limit := in.Limit
	if limit <= 0 {
		limit = listReviewsDefaultLimit
	}
	if limit > listReviewsMaxLimit {
		limit = listReviewsMaxLimit
	}
	offset := max(in.Offset, 0)

	var reviews []store.Review
	var err error
	if in.RepoID != 0 {
		if _, _, scopeErr := t.scopedRepo(ctx, in.RepoID); scopeErr != nil {
			if errors.Is(scopeErr, errNotAccessible) {
				return nil, zero, scopeErr
			}
			return nil, zero, t.internalErr(ctx, "list_reviews", scopeErr)
		}
		reviews, err = t.srv.store.ListReviewsScoped(ctx, in.RepoID, t.scope.installationIDs, limit, offset)
	} else {
		reviews, err = t.srv.store.ListAllReviewsScoped(ctx, t.scope.installationIDs, limit, offset)
	}
	if err != nil {
		return nil, zero, t.internalErr(ctx, "list_reviews", err)
	}
	out := listReviewsOutput{Reviews: make([]reviewSummary, 0, len(reviews))}
	for _, r := range reviews {
		out.Reviews = append(out.Reviews, reviewSummary{
			ReviewID: r.ID.String(), RepoID: r.RepoID, PRNumber: r.PRNumber, PRTitle: r.PRTitle, Status: r.Status, Score: r.Score,
			DeepReview: r.DeepReview, IsIncremental: r.IsIncremental, CreatedAt: rfc3339(r.CreatedAt), CompletedAt: rfc3339Ptr(r.CompletedAt),
		})
	}
	return nil, out, nil
}

type getReviewStatusInput struct {
	ReviewID string `json:"review_id" jsonschema:"review UUID from list_reviews or the dashboard URL"`
}

type getReviewStatusOutput struct {
	ReviewID          string  `json:"review_id"`
	Status            string  `json:"status" jsonschema:"pending, in_progress, completed, failed, or cancelled"`
	Stage             string  `json:"stage,omitempty" jsonschema:"pipeline stage of the current run (triaging, reviewing, scoring, synthesizing, posting, ...); omitted when no run exists yet"`
	AttemptGeneration int     `json:"attempt_generation"`
	Score             *int    `json:"score,omitempty"`
	Error             *string `json:"error,omitempty"`
	DurationMs        *int    `json:"duration_ms,omitempty"`
	CreatedAt         string  `json:"created_at"`
	CompletedAt       *string `json:"completed_at,omitempty"`
	RepoFullName      string  `json:"repo_full_name"`
	PRNumber          int     `json:"pr_number"`
	GitHubPRURL       string  `json:"github_pr_url"`
}

// getReviewStatus is the cheap poll. Stage comes from pipeline_states and is
// never derived from status: the vocabularies differ (5 values vs 13).
func (t *mcpTools) getReviewStatus(ctx context.Context, _ *mcp.CallToolRequest, in getReviewStatusInput) (*mcp.CallToolResult, getReviewStatusOutput, error) {
	var zero getReviewStatusOutput
	if err := t.requireScope(scopeRead); err != nil {
		return nil, zero, err
	}
	review, repo, err := t.scopedReview(ctx, in.ReviewID)
	if err != nil {
		return nil, zero, t.reviewToolErr(ctx, "get_review_status", err)
	}
	gen, err := t.srv.store.GetReviewAttemptGeneration(ctx, review.ID)
	if err != nil {
		t.srv.logger.WarnContext(ctx, "get_review_status attempt generation", "error", err, "review_id", review.ID)
	}
	stage := ""
	if st, err := t.srv.store.GetLatestRunStateForReview(ctx, review.ID); err == nil {
		stage = st
	} else if !errors.Is(err, pgx.ErrNoRows) {
		t.srv.logger.WarnContext(ctx, "get_review_status run state", "error", err, "review_id", review.ID)
	}
	return nil, getReviewStatusOutput{
		ReviewID: review.ID.String(), Status: review.Status, Stage: stage, AttemptGeneration: gen,
		Score: review.Score, Error: review.Error, DurationMs: review.DurationMs,
		CreatedAt: rfc3339(review.CreatedAt), CompletedAt: rfc3339Ptr(review.CompletedAt),
		RepoFullName: repo.FullName, PRNumber: review.PRNumber,
		GitHubPRURL: githubPRURL(repo.FullName, review.PRNumber, review.GithubReviewID),
	}, nil
}
```

Register:

```go
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_reviews",
		Description: "List Argus reviews the caller can access, newest first, optionally for one repo. Returns review_id for get_review_status and get_review.",
	}, t.listReviews)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_review_status",
		Description: "Cheap poll for one review: status, current pipeline stage while running, score, error, timings and the GitHub PR link. Use get_review for findings.",
	}, t.getReviewStatus)
```

- [ ] **Step 4: Run the tests**

```bash
cd /Users/belazy/personal/argus/backend && go build ./... && go vet ./internal/api/ && go test ./internal/api/ -run 'TestListReviews|TestGetReviewStatus|TestGitHubPRURL|TestNewMCPServer' -count=1 -v 2>&1 | tail -14
```

Expected: PASS with `TEST_DATABASE_URL`.

- [ ] **Step 5: Commit**

```bash
cd /Users/belazy/personal/argus
git add backend/internal/api/mcp_tools_review.go backend/internal/api/mcp_tools_review_test.go backend/internal/api/mcp_server.go
git commit -m "feat(api): list_reviews and get_review_status MCP tools

Authorization mirrors getReview: load the review, then GetRepoScoped on
its repo — the only tenant check, since GetReview is unscoped. A foreign
review and a missing one get the same fixed error.

list_reviews projects summary fields only; list rows lack token_usage,
brief and review_contract and carry a synthesized '[]' for diagrams, so
returning store.Review would look complete and not be. get_review_status
reads the stage from pipeline_states and omits it when no run exists —
the 5-value review status and 13-value pipeline state vocabularies
differ, so stage is never derived from status."
```

---

### Task 12: `get_review`

**Files:**
- Modify: `backend/internal/api/mcp_tools_review.go` (append)
- Modify: `backend/internal/api/mcp_server.go` (`registerMCPTools`: one `AddTool`)
- Test: `backend/internal/api/mcp_tools_review_test.go` (append)

**Interfaces:**
- Consumes: `scopedReview`, `reviewToolErr` (Task 11); `store.GetReviewComments`, `GetReviewMinorNotes`, `ListPRReviewSummaries(ctx, repoID, pr)`, `ListPRAutoResolveEvents(ctx, repoID, pr)`, `ListReviewMemories(ctx, installationID, id, 0)`, `CountReviewMemoriesByType(ctx, installationID, id)`, `GetReviewAttemptGeneration`; `store.ReviewComment.Severity *string`, `.State string`.
- Produces: `getReview` handler with `Out = any`; `getReviewInput`, `getReviewOutput`, `findingCounts`, `nonNil`.

- [ ] **Step 1: Write the failing test**

Append to `backend/internal/api/mcp_tools_review_test.go`:

```go
func TestGetReviewPartitionsSuppressedAndCounts(t *testing.T) {
	ctx, pool, s, installID, repoID := newReviewFixture(t)
	id := seedReview(t, ctx, pool, repoID, 11, "completed")
	insert := func(path, sev, state string) {
		if _, err := pool.Exec(ctx, `
			INSERT INTO review_comments (review_id, file_path, body, severity, state, attempt_generation)
			VALUES ($1, $2, 'body', $3, $4, 1)`, id, path, sev, state); err != nil {
			t.Fatalf("seed comment: %v", err)
		}
	}
	insert("a.go", "high", "posted")
	insert("a.go", "low", "posted")
	insert("b.go", "high", "suppressed")
	tools := &mcpTools{srv: s, scope: readScope(installID)}

	_, raw, err := tools.getReview(ctx, nil, getReviewInput{ReviewID: id.String()})
	if err != nil {
		t.Fatal(err)
	}
	out, ok := raw.(getReviewOutput)
	if !ok {
		t.Fatalf("output type = %T", raw)
	}
	if len(out.Findings) != 2 || len(out.SuppressedFindings) != 0 {
		t.Fatalf("default: findings=%d suppressed=%d, want 2/0", len(out.Findings), len(out.SuppressedFindings))
	}
	if out.Counts.Total != 2 || out.Counts.BySeverity["high"] != 1 || out.Counts.BySeverity["low"] != 1 || out.Counts.ByFile["a.go"] != 2 {
		t.Fatalf("counts = %+v — must describe returned (non-suppressed) findings only", out.Counts)
	}
	if out.RepoFullName == "" || out.GitHubPRURL == "" || out.Review == nil || out.Review.ID != id || out.DegradedSections == nil {
		t.Fatalf("out = %+v", out)
	}
	_, raw, err = tools.getReview(ctx, nil, getReviewInput{ReviewID: id.String(), IncludeSuppressed: true})
	if err != nil {
		t.Fatal(err)
	}
	out = raw.(getReviewOutput)
	if len(out.SuppressedFindings) != 1 || out.SuppressedFindings[0].FilePath != "b.go" || out.Counts.Total != 2 {
		t.Fatalf("include_suppressed: %+v", out)
	}

	_, foreignRepo := seedArchitectureRepo(t, ctx, pool)
	foreign := seedReview(t, ctx, pool, foreignRepo, 12, "completed")
	if _, _, err := tools.getReview(ctx, nil, getReviewInput{ReviewID: foreign.String()}); !errors.Is(err, errNotAccessible) {
		t.Fatalf("foreign review: %v", err)
	}
	noRead := &mcpTools{srv: s, scope: tenantScope{installationIDs: []int64{installID}, grantedScopes: []string{scopeMemoryWrite}}}
	if _, _, err := noRead.getReview(ctx, nil, getReviewInput{ReviewID: id.String()}); err == nil || err.Error() != errInsufficientScope(scopeRead).Error() {
		t.Fatalf("missing read scope: %v", err)
	}
}
```

If `review_comments` has other `NOT NULL` columns without defaults on your migration set (check `\d review_comments`), add them to the `INSERT`; `file_path` and `body` are the known ones.

- [ ] **Step 2: Run to verify failure**

```bash
cd /Users/belazy/personal/argus/backend && go test ./internal/api/ -run 'TestGetReviewPartitions' 2>&1 | tail -4
```

Expected: build failure — `getReview`, `getReviewInput`, `getReviewOutput` undefined.

- [ ] **Step 3: Implement `get_review`**

Append to `backend/internal/api/mcp_tools_review.go`:

```go
type getReviewInput struct {
	ReviewID          string `json:"review_id" jsonschema:"review UUID from list_reviews or the dashboard URL"`
	IncludeSuppressed bool   `json:"include_suppressed,omitempty" jsonschema:"also return findings that were suppressed and never posted to the PR"`
}

type findingCounts struct {
	Total      int            `json:"total"`
	BySeverity map[string]int `json:"by_severity"`
	ByFile     map[string]int `json:"by_file"`
}

// getReviewOutput embeds store types (uuid.UUID, json.RawMessage), so the tool
// is registered with Out = any to skip output-schema inference. Content is
// still the JSON of this struct.
type getReviewOutput struct {
	Review             *store.Review              `json:"review"`
	RepoFullName       string                     `json:"repo_full_name"`
	GitHubPRURL        string                     `json:"github_pr_url"`
	AttemptGeneration  int                        `json:"attempt_generation"`
	Findings           []store.ReviewComment      `json:"findings"`
	SuppressedFindings []store.ReviewComment      `json:"suppressed_findings,omitempty"`
	MinorNotes         []store.ReviewMinorNote    `json:"minor_notes"`
	History            []store.PRReviewSummary    `json:"history"`
	AutoResolveEvents  []store.AutoResolveSummary `json:"auto_resolve_events"`
	Memories           []store.LearnedMemory      `json:"memories"`
	MemoryCounts       []store.LearnedMemoryCount `json:"memory_counts"`
	Counts             findingCounts              `json:"counts"`
	DegradedSections   []string                   `json:"degraded_sections"`
}

// getReview returns what the dashboard review page renders, in one call, with
// three deliberate differences from the REST handler it mirrors
// (handlers_reviews.go getReview): repo_full_name and the PR URL come from the
// authz check instead of a second round-trip; the four sidecars that degrade
// to nil on error are NAMED in degraded_sections instead of serializing as an
// indistinguishable null; and suppressed findings are partitioned before
// counting so counts describe what the caller received.
func (t *mcpTools) getReview(ctx context.Context, _ *mcp.CallToolRequest, in getReviewInput) (*mcp.CallToolResult, any, error) {
	if err := t.requireScope(scopeRead); err != nil {
		return nil, nil, err
	}
	review, repo, err := t.scopedReview(ctx, in.ReviewID)
	if err != nil {
		return nil, nil, t.reviewToolErr(ctx, "get_review", err)
	}
	comments, err := t.srv.store.GetReviewComments(ctx, review.ID)
	if err != nil {
		return nil, nil, t.internalErr(ctx, "get_review", err)
	}
	minorNotes, err := t.srv.store.GetReviewMinorNotes(ctx, review.ID)
	if err != nil {
		return nil, nil, t.internalErr(ctx, "get_review", err)
	}
	gen, err := t.srv.store.GetReviewAttemptGeneration(ctx, review.ID)
	if err != nil {
		t.srv.logger.WarnContext(ctx, "get_review attempt generation", "error", err, "review_id", review.ID)
	}

	degraded := []string{}
	history, err := t.srv.store.ListPRReviewSummaries(ctx, review.RepoID, review.PRNumber)
	if err != nil {
		t.srv.logger.WarnContext(ctx, "get_review history", "error", err, "review_id", review.ID)
		degraded, history = append(degraded, "history"), nil
	}
	autoResolves, err := t.srv.store.ListPRAutoResolveEvents(ctx, review.RepoID, review.PRNumber)
	if err != nil {
		t.srv.logger.WarnContext(ctx, "get_review auto-resolve events", "error", err, "review_id", review.ID)
		degraded, autoResolves = append(degraded, "auto_resolve_events"), nil
	}
	// Memory reads are scoped by the repo's installation — the tenant that
	// granted access — never by the request.
	memories, err := t.srv.store.ListReviewMemories(ctx, repo.InstallationID, review.ID, 0)
	if err != nil {
		t.srv.logger.WarnContext(ctx, "get_review memories", "error", err, "review_id", review.ID)
		degraded, memories = append(degraded, "memories"), nil
	}
	memoryCounts, err := t.srv.store.CountReviewMemoriesByType(ctx, repo.InstallationID, review.ID)
	if err != nil {
		t.srv.logger.WarnContext(ctx, "get_review memory counts", "error", err, "review_id", review.ID)
		degraded, memoryCounts = append(degraded, "memory_counts"), nil
	}

	findings := make([]store.ReviewComment, 0, len(comments))
	var suppressed []store.ReviewComment
	counts := findingCounts{BySeverity: map[string]int{}, ByFile: map[string]int{}}
	for _, c := range comments {
		if c.State == "suppressed" {
			if in.IncludeSuppressed {
				suppressed = append(suppressed, c)
			}
			continue
		}
		findings = append(findings, c)
		counts.Total++
		if c.Severity != nil {
			counts.BySeverity[*c.Severity]++
		}
		counts.ByFile[c.FilePath]++
	}
	return nil, getReviewOutput{
		Review: review, RepoFullName: repo.FullName,
		GitHubPRURL:       githubPRURL(repo.FullName, review.PRNumber, review.GithubReviewID),
		AttemptGeneration: gen, Findings: findings, SuppressedFindings: suppressed,
		MinorNotes: nonNil(minorNotes), History: nonNil(history), AutoResolveEvents: nonNil(autoResolves),
		Memories: nonNil(memories), MemoryCounts: nonNil(memoryCounts), Counts: counts, DegradedSections: degraded,
	}, nil
}

// nonNil turns a nil slice into an empty one so JSON carries [] not null:
// null is what a degraded section used to look like, and it is now named in
// degraded_sections instead.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
```

Register with explicit type parameters (because `Out` is `any`):

```go
	mcp.AddTool[getReviewInput, any](server, &mcp.Tool{
		Name:        "get_review",
		Description: "Everything the Argus review page shows for one review: the review record, findings (severity, file, line, body), minor notes, per-push history, auto-resolve events, what the review learned into memory, counts by severity and file, and which sections (if any) failed to load. Suppressed findings are returned only with include_suppressed=true.",
	}, t.getReview)
```

- [ ] **Step 4: Run the tests**

```bash
cd /Users/belazy/personal/argus/backend && go build ./... && go vet ./internal/api/ && go test ./internal/api/ -run 'TestGetReview|TestNewMCPServer' -count=1 -v 2>&1 | tail -10
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /Users/belazy/personal/argus
git add backend/internal/api/mcp_tools_review.go backend/internal/api/mcp_tools_review_test.go backend/internal/api/mcp_server.go
git commit -m "feat(api): get_review MCP tool — the review page in one call

Mirrors handlers_reviews.go getReview's read order and authorization, with
three deliberate differences. repo_full_name and the PR URL come from the
GetRepoScoped authz check, saving the dashboard's second round-trip. The
four sidecars getReview degrades to nil on error are named in
degraded_sections — as bare null, a failed history query and an empty one
are the same bytes, and an agent would report 'no findings' for a failure.
Suppressed findings are partitioned before counting, so counts describe
exactly what the caller received.

Registered with Out=any: the output embeds store types (uuid, RawMessage)
whose inferred JSON schema is not worth trusting."
```

---

### Task 13: End-to-end, selected-org and scope isolation, deletion timing, docs

**Files:**
- Test: `backend/internal/api/mcp_e2e_pg_test.go`
- Test: `backend/internal/memory/mirror_delete_timing_pg_test.go`
- Modify: `README.md` (repo root)
- Modify: `docs/superpowers/specs/2026-09-05-argus-mcp-server-design.md` (status line)

**Interfaces:**
- Consumes: everything above; SDK client `mcp.NewClient(&mcp.Implementation{...}, nil)`, `(*mcp.Client).Connect(ctx, &mcp.StreamableClientTransport{Endpoint, HTTPClient}, nil) (*mcp.ClientSession, error)`, `ListTools(ctx, nil)`, `CallTool(ctx, &mcp.CallToolParams{Name, Arguments})`, `Close()`; `memory.NewMirrorWorker(outbox, getIndexer, logger)` + `RunOnce(ctx, limit)`; `store.SetInstallationClerkOrgID`, `store.LinkUserInstallation`; test helpers `testJWKS`, `signTestJWT`, `accessTokenClaims`, `testIssuer`, `testResource` (Task 5), `seedReview` (Task 11), `recordingIndexer`/`stubIndexers`/`writeScope` (Tasks 6-7), `noKeysResolver` (Task 3).

- [ ] **Step 1: Write the end-to-end and isolation tests**

Create `backend/internal/api/mcp_e2e_pg_test.go`:

```go
package api

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/BeLazy167/argus/backend/internal/config"
	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/store"
)

type bearerTransport struct {
	token string
	base  http.RoundTripper
}

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(r)
}

type e2eFixture struct {
	srv                *httptest.Server
	key                *rsa.PrivateKey
	kid                string
	installA, installB int64
	repoA, repoB       int64
	orgA, orgB         string
}

// newE2EFixture mounts the real route on an httptest server with a JWKS the
// test controls, one user who belongs to two orgs/installations, and a token
// minter. Every test below goes through the full chain.
func newE2EFixture(t *testing.T) *e2eFixture {
	t.Helper()
	pool, ctx := architectureTestPool(t)
	installA, repoA := seedArchitectureRepo(t, ctx, pool)
	installB, repoB := seedArchitectureRepo(t, ctx, pool)
	st := store.NewWithDB(pool)
	orgA := "org_e2e_a_" + strconv.FormatInt(installA, 10)
	orgB := "org_e2e_b_" + strconv.FormatInt(installB, 10)
	for _, pair := range []struct {
		id  int64
		org string
	}{{installA, orgA}, {installB, orgB}} {
		if err := st.SetInstallationClerkOrgID(ctx, pair.id, pair.org); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []int64{installA, installB} {
		if _, err := st.LinkUserInstallation(ctx, e2eUser, id, "org:member"); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM user_installations WHERE clerk_user_id = $1`, e2eUser) })

	key, kid := testJWKS(t)
	s := &Server{
		store:  st,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		cfg:    &config.Config{MCPEnabled: true, ClerkIssuerURL: testIssuer, MCPResourceURL: testResource},
	}
	r := chi.NewRouter()
	s.registerMCPRoutes(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return &e2eFixture{srv: srv, key: key, kid: kid, installA: installA, installB: installB, repoA: repoA, repoB: repoB, orgA: orgA, orgB: orgB}
}

const e2eUser = "user_mcp_e2e"

// mint signs an access token for e2eUser in the given org with the given
// space-delimited scopes.
func (f *e2eFixture) mint(t *testing.T, org, scopes string) string {
	t.Helper()
	claims := accessTokenClaims()
	claims["sub"] = e2eUser
	claims["org_id"] = org
	claims["scope"] = scopes
	return signTestJWT(t, f.key, f.kid, claims)
}

func (f *e2eFixture) session(t *testing.T, token string) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	client := mcp.NewClient(&mcp.Implementation{Name: "e2e-test", Version: "0"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   f.srv.URL + "/mcp",
		HTTPClient: &http.Client{Transport: bearerTransport{token: token, base: http.DefaultTransport}},
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func decodeTool[T any](t *testing.T, res *mcp.CallToolResult) T {
	t.Helper()
	var out T
	if len(res.Content) == 0 {
		t.Fatal("no content")
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] = %T", res.Content[0])
	}
	if err := json.Unmarshal([]byte(text.Text), &out); err != nil {
		t.Fatalf("decode: %v\n%s", err, text.Text)
	}
	return out
}

const allScopes = "argus:read argus:memory:write user:org:read"

func TestMCPEndToEndSelectedOrgAndScopes(t *testing.T) {
	f := newE2EFixture(t)
	ctx := context.Background()

	// Authorized for org A: the whole tool set is advertised; list_repos shows
	// only A even though the user also belongs to B.
	sess := f.session(t, f.mint(t, f.orgA, allScopes))
	tools, err := sess.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"list_repos": false, "search_memory": false, "get_memory_briefing": false, "create_memory": false, "delete_memory": false, "retire_memory": false, "list_reviews": false, "get_review_status": false, "get_review": false}
	for _, tool := range tools.Tools {
		if _, ok := want[tool.Name]; ok {
			want[tool.Name] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("tool %s not advertised", name)
		}
	}
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "list_repos", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("list_repos: err=%v res=%+v", err, res)
	}
	out := decodeTool[listReposOutput](t, res)
	var sawA, sawB bool
	for _, repo := range out.Repos {
		sawA = sawA || repo.RepoID == f.repoA
		sawB = sawB || repo.RepoID == f.repoB
	}
	if !sawA || sawB {
		t.Fatalf("org A token saw A=%v B=%v; the user belongs to both installations but authorized only A", sawA, sawB)
	}
	// A tool call naming B's repo is not accessible through A's connection.
	res, err = sess.CallTool(ctx, &mcp.CallToolParams{Name: "list_reviews", Arguments: map[string]any{"repo_id": f.repoB}})
	if err != nil || !res.IsError {
		t.Fatalf("B's repo through A's token must be a tool error: err=%v res=%+v", err, res)
	}

	// Reauthorizing for B permits B only.
	sessB := f.session(t, f.mint(t, f.orgB, allScopes))
	res, err = sessB.CallTool(ctx, &mcp.CallToolParams{Name: "list_repos", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatal(err)
	}
	for _, repo := range decodeTool[listReposOutput](t, res).Repos {
		if repo.RepoID == f.repoA {
			t.Fatal("org B token saw A's repo")
		}
	}

	// A read-only grant cannot write, even with every acknowledgment flag set.
	ro := f.session(t, f.mint(t, f.orgA, "argus:read user:org:read"))
	res, err = ro.CallTool(ctx, &mcp.CallToolParams{Name: "create_memory", Arguments: map[string]any{
		"installation_id": f.installA, "repo_id": f.repoA, "content": "should not land", "confirm_duplicate": true, "confirm_shared": true,
	}})
	if err != nil || !res.IsError {
		t.Fatalf("read-only write must be a tool error: err=%v res=%+v", err, res)
	}
	if text := res.Content[0].(*mcp.TextContent).Text; !strings.Contains(text, "insufficient scope") {
		t.Fatalf("scope denial must use the fixed error, got %q", text)
	}

	// Raw HTTP checks of the auth chain on the mounted route.
	post := func(token, hint string) *http.Response {
		req, _ := http.NewRequest(http.MethodPost, f.srv.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if hint != "" {
			req.Header.Set("X-Installation-ID", hint)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp
	}
	if resp := post("", ""); resp.StatusCode != http.StatusUnauthorized || !strings.Contains(resp.Header.Get("WWW-Authenticate"), "resource_metadata") {
		t.Fatalf("no token: status=%d WWW-Authenticate=%q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}
	if resp := post(f.mint(t, f.orgA, allScopes), strconv.FormatInt(f.installB, 10)); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("hint for B on A's token: status=%d, want 403", resp.StatusCode)
	}
}

// Every tool that takes an id must answer errNotAccessible for a foreign one.
// Runs each handler directly so a regression in any one tool is named.
func TestMCPTenantIsolationSweep(t *testing.T) {
	pool, ctx := architectureTestPool(t)
	installA, _ := seedArchitectureRepo(t, ctx, pool)
	installB, repoB := seedArchitectureRepo(t, ctx, pool)
	reviewB := seedReview(t, ctx, pool, repoB, 1, "completed")
	st := store.NewWithDB(pool)
	src := "manual"
	cidB := "sm_sweep_b"
	patternB, err := st.CreatePattern(ctx, installB, &repoB, "theirs", nil, nil, &src, nil, nil, &cidB, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM memory_mirror_outbox WHERE installation_id = $1`, installB)
		_, _ = pool.Exec(bg, `DELETE FROM patterns WHERE installation_id = $1`, installB)
	})
	idx := &recordingIndexer{retireFn: func(memory.RetireRequest) (memory.RetireResult, error) {
		t.Fatal("retire must be rejected before reaching the indexer")
		return memory.RetireResult{}, nil
	}}
	s := &Server{store: st, logger: slog.New(slog.DiscardHandler), indexers: &stubIndexers{indexer: idx, available: true}}
	tools := &mcpTools{srv: s, scope: writeScope(installA)}

	cases := []struct {
		name string
		call func() error
	}{
		{"search_memory repo", func() error { _, _, e := tools.searchMemory(ctx, nil, searchMemoryInput{RepoID: repoB, Query: "q"}); return e }},
		{"search_memory shared", func() error { _, _, e := tools.searchMemory(ctx, nil, searchMemoryInput{InstallationID: installB, Scope: "shared", Query: "q"}); return e }},
		{"get_memory_briefing", func() error { _, _, e := tools.getMemoryBriefing(ctx, nil, getMemoryBriefingInput{RepoID: repoB, Query: "q"}); return e }},
		{"create_memory installation", func() error { _, _, e := tools.createMemory(ctx, nil, createMemoryInput{InstallationID: installB, RepoID: &repoB, Content: "x", ConfirmDuplicate: true}); return e }},
		{"create_memory repo", func() error { _, _, e := tools.createMemory(ctx, nil, createMemoryInput{InstallationID: installA, RepoID: &repoB, Content: "x", ConfirmDuplicate: true}); return e }},
		{"delete_memory", func() error { _, _, e := tools.deleteMemory(ctx, nil, deleteMemoryInput{PatternID: patternB.ID, ConfirmPipelineLearned: true}); return e }},
		{"retire_memory", func() error { _, _, e := tools.retireMemory(ctx, nil, retireMemoryInput{InstallationID: installB, CustomID: cidB, Reason: "r", ConfirmPipelineLearned: true}); return e }},
		{"list_reviews", func() error { _, _, e := tools.listReviews(ctx, nil, listReviewsInput{RepoID: repoB}); return e }},
		{"get_review_status", func() error { _, _, e := tools.getReviewStatus(ctx, nil, getReviewStatusInput{ReviewID: reviewB.String()}); return e }},
		{"get_review", func() error { _, _, e := tools.getReview(ctx, nil, getReviewInput{ReviewID: reviewB.String()}); return e }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); err == nil || err.Error() != errNotAccessible.Error() {
				t.Fatalf("%s: err = %v, want %q", tc.name, err, errNotAccessible)
			}
		})
	}
	if _, err := st.GetPattern(ctx, patternB.ID); err != nil {
		t.Fatalf("B's pattern must be untouched by the sweep: %v", err)
	}
	// list_repos has no id argument; its isolation is TestListReposIsScopedToCaller.
}
```

- [ ] **Step 2: Write the deletion-timing test**

Create `backend/internal/memory/mirror_delete_timing_pg_test.go`:

```go
package memory

import (
	"context"
	"testing"

	"github.com/BeLazy167/argus/backend/internal/store"
)

// The delete tool's result is a snapshot, not a completion claim: until the
// mirror worker drains, the memory is still searchable even with zero
// siblings; with a sibling, it stays after the drain.
func TestPatternDeleteTimingAgainstMirrorWorker(t *testing.T) {
	pool, install := pgTestPool(t)
	ctx := context.Background()
	st := store.NewWithDB(pool)
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM memory_mirror_outbox WHERE installation_id = $1`, install)
		_, _ = pool.Exec(bg, `DELETE FROM patterns WHERE installation_id = $1`, install)
	})
	reg := NewRegistry(discardLogger()).WithPostgresBackend(pool,
		NewEmbedderRegistry(noKeysResolver{}, PlatformEmbeddings{Dimensions: StorageDimensions}, discardLogger()))
	worker := NewMirrorWorker(st, func(ctx context.Context, id int64) MirrorIndexer {
		mi, _ := reg.GetIndexer(ctx, id).(MirrorIndexer)
		return mi
	}, discardLogger())
	drain := func() {
		for i := 0; i < 5; i++ {
			n, err := worker.RunOnce(ctx, 100)
			if err != nil {
				t.Fatalf("worker: %v", err)
			}
			if n == 0 {
				return
			}
		}
	}
	live := func(customID string) bool {
		var ok bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM live_memories WHERE installation_id=$1 AND custom_id=$2)`, install, customID).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		return ok
	}
	src := "manual"

	// Sole owner: delete → still live until drained → gone after.
	cid := SharedPatternCustomID("manual", "sole owner")
	p, err := st.CreatePattern(ctx, install, nil, "sole owner", nil, nil, &src, nil, nil, &cid, nil)
	if err != nil {
		t.Fatal(err)
	}
	drain()
	if !live(cid) {
		t.Fatal("mirror did not index the pattern")
	}
	res, err := st.DeletePatternGuarded(ctx, p.ID, []int64{install}, false)
	if err != nil || res.SiblingsAtDelete != 0 {
		t.Fatalf("delete: res=%+v err=%v", res, err)
	}
	if !live(cid) {
		t.Fatal("memory vanished before the worker ran — the delete result must not be read as completion, and here it would have been true by accident")
	}
	drain()
	if live(cid) {
		t.Fatal("memory still live after the worker drained a sole-owner delete")
	}

	// Sibling remains: delete one → still live after the drain.
	doc := "sm_timing_pair"
	a, err := st.CreatePattern(ctx, install, nil, "pair", &doc, nil, &src, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreatePattern(ctx, install, nil, "pair", &doc, nil, &src, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	drain()
	res, err = st.DeletePatternGuarded(ctx, a.ID, []int64{install}, false)
	if err != nil || res.SiblingsAtDelete != 1 {
		t.Fatalf("sibling delete: res=%+v err=%v", res, err)
	}
	drain()
	if !live(doc) {
		t.Fatal("memory removed although a sibling pattern still owns it")
	}
}
```

If the mirror's legacy-doc-id path indexes under a different custom id than `doc` (check `newPatternOutboxPayload` / `MirrorPattern` for how a legacy identity is mirrored), read the mirrored `custom_id` from `memory_mirror_outbox.custom_id` for pattern `a.ID` instead of assuming `doc`.

- [ ] **Step 3: Run everything as CI does**

```bash
cd /Users/belazy/personal/argus/backend && go build ./... && go vet ./... && go test -race -count=1 ./... 2>&1 | tail -12 && make sqlc-check && make tygo-check
```

Expected: all packages `ok`; both drift checks exit 0. Against the CI Postgres image when no local DB: `docker run -d --name ci-pg -e POSTGRES_PASSWORD=ci -e POSTGRES_DB=argus_test -p 5432:5432 <image from .github/workflows/ci.yml>`, `DATABASE_URL=... go run ./cmd/migrate`, then `TEST_DATABASE_URL=... go test ...`.

- [ ] **Step 4: Document client and Clerk setup**

Append to `README.md` (repo root), before any "License" section:

```markdown
## MCP: use Argus's memory and reviews from your own agent

Argus exposes its team memory and review results over the Model Context Protocol. Any MCP client that supports remote servers with OAuth (Claude Code, Cursor, Claude Desktop) can connect.

**Server:** `https://api.argus.reviews/mcp` (self-hosted: your `MCP_RESOURCE_URL`).

**Auth:** Clerk is the OAuth authorization server. The client discovers it from `/.well-known/oauth-protected-resource`, runs a browser login once, and asks you to pick **one organization** — the connection sees only that org's repos and memory. Switching orgs means authorizing again. Tokens refresh automatically; there are no API keys.

**Scopes:** `argus:read` for every read tool; `argus:memory:write` for `create_memory`, `delete_memory`, `retire_memory`. A read-only grant cannot mutate memory, whatever flags a call sets.

**Tools:** `list_repos` (call first — resolves the local `repo_id` / `installation_id` every other tool takes), `search_memory`, `get_memory_briefing`, `create_memory`, `delete_memory`, `retire_memory`, `list_reviews`, `get_review_status`, `get_review`.

**Guard rails:** deleting or retiring a memory the review pipeline learned requires `confirm_pipeline_learned=true`; writing an org-wide memory requires `confirm_shared=true`. Neither can be undone. To stop a memory influencing reviews, use `retire_memory` — `delete_memory` removes one contributing record and the memory may remain searchable.

**Self-hosting — backend env:** `MCP_ENABLED=true`, `CLERK_ISSUER_URL` (your Clerk Frontend API origin, e.g. `https://<slug>.clerk.accounts.dev`), `MCP_RESOURCE_URL` (the public `https://…/mcp` URL). `CLERK_JWKS_URL` must already be set. The route 404s when disabled.

**Self-hosting — Clerk dashboard:** under *OAuth applications*: create the custom scopes `argus:read` and `argus:memory:write` and assign them; enable *Advertise CIMD support* (preferred) or *Dynamic client registration* for clients that discover the server; keep the consent screen on; set *Default scopes* to `argus:read argus:memory:write user:org:read` for clients that omit `scope`; leave *Generate access tokens as JWTs* on (opaque tokens are rejected). Organizations must be enabled so `user:org:read` is available.

**Acceptance run before production:** with a staging Clerk instance and a real MCP client, complete discovery → registration → PKCE authorization with org selection → `tools/list` → `list_repos` → `create_memory` → `retire_memory` as a regular member; repeat with a read-only grant and with a second org. Record the client version and non-secret config used. If the token's scope claim is not `scope`/`scp`/`scopes`, adjust `grantedScopes` in `backend/internal/api/mcp_auth.go`.
```

Change the spec's status line (`docs/superpowers/specs/2026-09-05-argus-mcp-server-design.md:3`) to:

```
Status: implemented — see docs/superpowers/plans/2026-09-05-argus-mcp-server.md
```

- [ ] **Step 5: Commit**

```bash
cd /Users/belazy/personal/argus
git add backend/internal/api/mcp_e2e_pg_test.go backend/internal/memory/mirror_delete_timing_pg_test.go README.md docs/superpowers/specs/2026-09-05-argus-mcp-server-design.md
git commit -m "test: MCP end-to-end, selected-org and scope isolation, deletion timing; document setup

The end-to-end test drives the real SDK client through the mounted route
with tokens minted against a test JWKS: a user in two orgs authorized for
A sees only A, reauthorizing for B sees only B, a read-only grant is
denied the write tools with the fixed scope error, a missing token gets
the resource_metadata challenge, and a hint for B on A's token is a 403.

The isolation sweep runs every id-taking tool against another tenant's
ids and requires the single fixed 'not found or not accessible' answer.
The deletion-timing test shows the delete result is a snapshot: a
sole-owner delete leaves the memory searchable until the mirror worker
drains, and a sibling keeps it searchable after."
```

---

## Self-review notes

**Spec coverage.** Decisions table → org scope (Task 6 N5d), write permission for every member (no role check anywhere; scopes only), destructive writes (9, 10), org-wide writes (8), forgetting = retire (10 + tool descriptions in 9/10), duplicate writes (8 N10), rollout flag (1, 6). Architecture → mount point and middleware chain (6), N6 both directions incl. errors (4), request path (5, 6), OAuth configuration and permission policy (1, 5, 13 README), tenant scope in closure (6), id validation (every tool). Tool set → all nine (6, 7, 8, 9, 10, 11, 12) with the revised output shapes (`installation_id` + `custom_id` + `pattern_ids` in search; discriminated `status` in create; `pattern_deleted`/`siblings_at_delete`/`retention_expected` in delete; `source` in retire). Indexer access (3, 6, 10 N8). Write semantics table (8, 9, 10). New work N1–N10 (2, 9, 3, 3, 5, 5, 5, 6, 4, 1/6, 10, 7, 8). Hazards → each has a task above. Testing section → schema smoke (6), tenant isolation incl. unfiltered listings (13, 11), selected org (6, 13), member permissions and scopes (6, 7, 8, 9, 10, 11, 12, 13), create scope matrix (8), duplicate creation incl. concurrency (8), similar creation (8), mutation identity mapping (7), write guards incl. provenance without a pattern row and replacement validity (9, 10), deletion timing (13), HTTP log exclusion incl. errors and auth failures (4), degradation (7), OAuth acceptance (13 README, manual). Out of scope unchanged.

**Type consistency.** `tenantScope{userID, orgID, installationIDs, grantedScopes}`, `mcpTools`, `requireScope`, `errInsufficientScope`, `errNotAccessible`, `errMemoryUnavailable`, `internalErr`, `resolveIndexer`, `scopedRepo`, `scopedReview`, `reviewToolErr`, `githubPRURL`, `nonNil`, `readScope`/`writeScope`, `recordingIndexer`, `stubIndexers`, `seedReview`, `seedPatternWithSource`, `repoShortName`, `testJWKS`, `signTestJWT`, `accessTokenClaims`, `mcpTestServer`, `testIssuer`/`testResource`, `noKeysResolver` are each defined once and reused with the same signatures. Store: `patternIdentityExpr`, `findPatternIDByIdentity`/`FindPatternIDByIdentity`, `ListPatternIDsByIdentity`, `createPatternTx`, `CreateOrGetPattern`, `deletePatternTx`, `DeletePatternGuarded`, `PatternDeletion`, `IsHumanAuthoredSource`, `ErrPatternNotFound`, `ErrPipelineLearnedPattern`, `Pattern.MemoryCustomID`. Memory: `RetireRequest`, `RetireResult`, `RetireMode*`, `ErrDocumentNotFound`, `ErrPipelineLearnedMemory`, `ErrUnknownProvenance`, `ErrReplacementNotLive`, `knownMemorySources`, `invalidateMemorySQL`, `supersedeMemorySQL`.

**Ordering note.** `recordingIndexer` (Task 7 test file) references `memory.RetireRequest`/`RetireResult`, which Task 10 introduces; Task 7 comments those two members out and Task 10 uncomments them. Tasks 7-9 do not need them.

**Verified against the tree before commit:** `memory.pgTestPool`/`discardLogger`/`NewEmbedderRegistry`/`PlatformEmbeddings{APIKey,BaseURL,Model,Dimensions}`/`StorageDimensions`/`NewPGIndexer`; `memorytest.Fake` is the only non-embedding `Indexer` implementer besides `PGIndexer`; `store.Pool` set by `NewWithDB(pool)`; `lockMemoryMirrorCustomID`/`enqueueMemoryMirrorEvent` signatures; `CreatePattern`'s `ON CONFLICT … DO UPDATE` (the reason N10 short-circuits before it); `DeletePattern` sqlc RETURNING columns; `GetInstallationByClerkOrgID`/`SetInstallationClerkOrgID`/`LinkUserInstallation`; `resolveInstallationIDs` org branch; `ReviewComment.Severity *string`; `mcp.ServerOptions{Instructions, Logger}`, `mcp.Implementation.Title`; NOT NULL columns of `memories`, `pipeline_states`, `review_comments`, `user_installations`, `installations`; `live_memories` view; `memories.metadata->>'source'` is where writers stamp provenance; `NewMirrorWorker(outbox, getIndexer, logger)` + `RunOnce` and its app.go wiring; Clerk docs: JWT access tokens by default, `org_id` claim via `user:org:read`, custom scopes + default scopes + CIMD/DCR settings — scope claim name undocumented, handled by `grantedScopes`.
