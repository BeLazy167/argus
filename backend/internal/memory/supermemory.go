package memory

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"golang.org/x/time/rate"
)

// SearchInclude controls which extra fields the v4 search API returns.
type SearchInclude struct {
	Documents       bool `json:"documents,omitempty"`
	RelatedMemories bool `json:"relatedMemories,omitempty"`
	Summaries       bool `json:"summaries,omitempty"`
}

// RelatedMemory represents a memory related to a search result.
type RelatedMemory struct {
	Memory   string `json:"memory"`
	Relation string `json:"relation"`
	Version  int    `json:"version"`
}

// DocumentLink represents a linked document returned by v4 search.
type DocumentLink struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Type  string `json:"type"`
}

const baseURL = "https://api.supermemory.ai"

// Client wraps the Supermemory REST API for memory storage and retrieval.
// Holds an optional rate limiter (per-installation token bucket) and an
// optional backoff policy. Nil limiter or policy disables that feature —
// NewClient wires sensible defaults for production; tests can pass no-op
// values without ceremony.
type Client struct {
	apiKey  string
	client  *http.Client
	limiter *rate.Limiter
	backoff BackoffPolicy
}

// ClientOption is a functional option for NewClient. Kept minimal — the only
// tunables today are rate limits (per-installation QPS/burst) and the retry
// policy used by doRequest.
type ClientOption func(*Client)

// WithLimiter attaches a rate limiter. Calls block on Wait before every HTTP
// call, bounded by MaxWaitForToken so a saturated bucket fails fast rather
// than stalling the pipeline.
func WithLimiter(l *rate.Limiter) ClientOption {
	return func(c *Client) { c.limiter = l }
}

// WithBackoff overrides the default retry policy. Use for tests that want
// no retries (pass BackoffPolicy{MaxAttempts: 1}) or non-default timing.
func WithBackoff(p BackoffPolicy) ClientOption {
	return func(c *Client) { c.backoff = p }
}

// NewClient constructs a Supermemory client with the given API key. Applies
// DefaultBackoff; caller attaches a rate limiter via WithLimiter when BYOK
// usage requires it (the Registry does this in production).
func NewClient(apiKey string, opts ...ClientOption) *Client {
	c := &Client{
		apiKey:  apiKey,
		client:  &http.Client{Timeout: 30 * time.Second},
		backoff: DefaultBackoff,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// doRequest sends a request with the given method/path and decodes the response.
// Wraps the actual HTTP call in a retry loop that honors Retry-After for 429s,
// falls back to exponential backoff with jitter for 502/503/504, and short-
// circuits all other non-2xx responses. Every call blocks on the rate limiter
// (if configured) before the first attempt AND before each retry — a 429 that
// triggers a retry also consumes a token.
func (c *Client) doRequest(ctx context.Context, method, path string, reqBody, result any) error {
	return retryWithBackoff(ctx, c.backoff, func(attemptCtx context.Context) error {
		if err := waitForToken(attemptCtx, c.limiter); err != nil {
			return err
		}
		return c.doOnce(attemptCtx, method, path, reqBody, result)
	})
}

// doOnce performs a single HTTP round-trip. Returns *retryableError for status
// codes the backoff policy retries (429/502/503/504); other non-2xx become
// plain errors that short-circuit retry. Body marshaling is redone on every
// attempt — bodyReader cannot be rewound safely across retries, and doJSON is
// usually called with small bodies.
func (c *Client) doOnce(ctx context.Context, method, path string, reqBody, result any) error {
	var bodyReader io.Reader
	if reqBody != nil {
		body, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("marshaling request: %w", err)
		}
		bodyReader = bytes.NewReader(body)
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, baseURL+path, bodyReader)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	c.setHeaders(httpReq)

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		if isRetryableStatus(resp.StatusCode) {
			return &retryableError{
				StatusCode: resp.StatusCode,
				Body:       respBody,
				RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
			}
		}
		return fmt.Errorf("supermemory %s %s error (status %d): %s", method, path, resp.StatusCode, string(respBody))
	}

	if result != nil {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("unmarshaling response: %w", err)
		}
	}
	return nil
}

// doJSON is a convenience wrapper that calls doRequest with POST method.
func (c *Client) doJSON(ctx context.Context, path string, reqBody, result any) error {
	return c.doRequest(ctx, "POST", path, reqBody, result)
}

// AddMemory stores a new memory via v3/documents. Supports customId upserts but documents
// are queued for processing (not immediately searchable).
func (c *Client) AddMemory(ctx context.Context, req AddRequest) (*AddResponse, error) {
	var result AddResponse
	if err := c.doJSON(ctx, "/v3/documents", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// BatchDocument is a single document in a batch add request.
type BatchDocument struct {
	Content  string            `json:"content"`
	CustomID string            `json:"customId,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// BatchAddRequest is the request body for POST /v3/documents/batch.
type BatchAddRequest struct {
	ContainerTag string            `json:"containerTag,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	Documents    []BatchDocument   `json:"documents"`
}

// BatchResult is a single per-document result in a batch-add response.
// The server returns one entry per input document, in the same order, with an
// empty `id` when that document failed (see `error`/`details`).
type BatchResult struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Error   string `json:"error,omitempty"`
	Details string `json:"details,omitempty"`
}

// BatchAddResponse is the response from POST /v3/documents/batch. The live API
// returns `{results:[{id,status,error}], success, failed}`; `IDs` is retained
// only for backward compatibility with any older `{ids:[…]}` shape. Callers that
// need the created document ids should use DocIDs(), which prefers Results.
type BatchAddResponse struct {
	Results []BatchResult `json:"results"`
	Success int           `json:"success"`
	Failed  int           `json:"failed"`
	IDs     []string      `json:"ids"`
}

// DocIDs returns the created document ids aligned to the input document order.
// Prefers the documented `results[].id` shape; falls back to the legacy `ids`
// array. A failed document contributes an empty string at its position so the
// slice stays index-aligned with the request for write-back.
func (r *BatchAddResponse) DocIDs() []string {
	if len(r.Results) > 0 {
		ids := make([]string, len(r.Results))
		for i, res := range r.Results {
			ids[i] = res.ID
		}
		return ids
	}
	return r.IDs
}

// AddMemoryBatch stores multiple documents in a single API call via v3/documents/batch.
// Max 600 documents per call. Counts as 1 request for rate limiting.
func (c *Client) AddMemoryBatch(ctx context.Context, req BatchAddRequest) (*BatchAddResponse, error) {
	var result BatchAddResponse
	if err := c.doJSON(ctx, "/v3/documents/batch", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetSettings retrieves current org-level Supermemory settings.
func (c *Client) GetSettings(ctx context.Context) (map[string]any, error) {
	var result map[string]any
	if err := c.doRequest(ctx, "GET", "/v3/settings", nil, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// UpdateSettings updates org-level Supermemory settings (filter prompt, chunk size, etc).
func (c *Client) UpdateSettings(ctx context.Context, settings map[string]any) error {
	return c.doRequest(ctx, "PATCH", "/v3/settings", settings, &struct{}{})
}

// UpdateEntityContext sets per-container-tag context that guides memory extraction.
func (c *Client) UpdateEntityContext(ctx context.Context, containerTag, entityContext string) error {
	return c.doRequest(ctx, "PATCH", "/v3/container-tags/"+containerTag, map[string]string{
		"entityContext": entityContext,
	}, &struct{}{})
}

// Search performs semantic search across memories.
func (c *Client) Search(ctx context.Context, req SearchRequest) (*SearchResponse, error) {
	var result SearchResponse
	if err := c.doJSON(ctx, "/v4/search", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// DeleteMemory removes a document by ID.
func (c *Client) DeleteMemory(ctx context.Context, documentID string) error {
	return c.doRequest(ctx, "DELETE", "/v3/documents/"+documentID, nil, nil)
}

// ListDocuments lists documents filtered by containerTags with pagination.
func (c *Client) ListDocuments(ctx context.Context, req ListRequest) (*ListResponse, error) {
	var result ListResponse
	if err := c.doJSON(ctx, "/v3/documents/list", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// BulkDelete removes multiple documents by IDs or containerTags. At least one
// of IDs or ContainerTags must be non-empty. Returns the API's deletedCount so
// callers can report how many docs each call removed.
func (c *Client) BulkDelete(ctx context.Context, req BulkDeleteRequest) (*BulkDeleteResponse, error) {
	if len(req.IDs) == 0 && len(req.ContainerTags) == 0 {
		return nil, fmt.Errorf("BulkDelete: at least one of IDs or ContainerTags required")
	}
	var result BulkDeleteResponse
	if err := c.doRequest(ctx, "DELETE", "/v3/documents/bulk", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetDocument retrieves a single document by ID.
func (c *Client) GetDocument(ctx context.Context, documentID string) (*Document, error) {
	var result Document
	if err := c.doRequest(ctx, "GET", "/v3/documents/"+documentID, nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
}

// tagSanitizer replaces chars invalid in Supermemory container tags.
var tagSanitizer = strings.NewReplacer(":", "-", "/", "-", "~", "-", ".", "-")

// idSanitizerRe matches any character NOT in Supermemory's allowed set for
// `customId` values. Per Supermemory's 400-response message the allowed set is
// alphanumeric plus `_`, `-`, and `:`. Everything else — notably `(`, `)`,
// `[`, `]`, `/`, `.`, `~`, and spaces — must be replaced.
//
// The simpler tagSanitizer above was missing parens and brackets, which broke
// Next.js route-group paths like `src/app/(auth)/oauth/page.tsx` and dynamic
// segments like `[slug]/page.tsx`. Reviews on acmeorg-account#331 logged 7×
// HTTP 400 errors for this reason.
var idSanitizerRe = regexp.MustCompile(`[^a-zA-Z0-9_:-]`)

// CustomIDSanitize makes an arbitrary string safe for use as a Supermemory
// `customId`. Replaces any disallowed character with `-`. Idempotent on
// already-safe input. Callers assembling multi-segment IDs should join with
// `--` AFTER per-segment sanitization — sanitizing the joined string would
// also collapse the `--` separator (`--` is allowed, just redundant).
func CustomIDSanitize(s string) string {
	return idSanitizerRe.ReplaceAllString(s, "-")
}

// SharedTag is the container tag for cross-repo patterns under one installation.
// BYOK makes the Supermemory API key the tenant, so there is no owner segment.
const SharedTag = "_shared"

// repoNameHash returns the first 6 hex chars of sha256(raw repo name). Used as
// a deterministic disambiguator so two repo names that sanitize to the same
// string never share a container or a customID.
func repoNameHash(repo string) string {
	sum := sha256.Sum256([]byte(repo))
	return hex.EncodeToString(sum[:3])
}

// repoNameIsLossy reports whether the repo name cannot be represented safely as
// a bare container tag / customID segment without risking a collision: either
// sanitization changes it (so "sdk.js" and "sdk-js" would map to the same
// token) or the sanitized tag form equals the reserved SharedTag (so a repo
// literally named "_shared" would land in the cross-repo container). When true,
// callers append repoNameHash to keep distinct repos distinct.
func repoNameIsLossy(repo string) bool {
	tag := tagSanitizer.Replace(repo)
	return tag != repo || CustomIDSanitize(repo) != repo || tag == SharedTag
}

// RepoTagNew returns a container tag for a single repo under the installation.
// Under BYOK each installation has its own Supermemory key, so owner is
// implicit; the repo name alone identifies the container.
//
// Collision guard: tagSanitizer maps '.', '/', ':', '~' to '-', so "sdk.js"
// and "sdk-js" would otherwise share one container, and a repo named "_shared"
// would collide with SharedTag. When the raw name is lossy under sanitization
// (or collides with SharedTag) a short deterministic hash of the RAW name is
// appended so distinct repos never merge. Safe names are returned unchanged so
// the common case keeps stable, human-readable tags. Nothing new-shape is
// deployed yet, so this scheme is free to change.
func RepoTagNew(repo string) string {
	tag := tagSanitizer.Replace(repo)
	if repoNameIsLossy(repo) {
		return tag + "-" + repoNameHash(repo)
	}
	return tag
}

// repoIDSegment returns the collision-safe {repo} segment used inside customID
// builders. It mirrors RepoTagNew's disambiguation so a repo's container tag
// and its customIDs stay consistent: both append the same raw-name hash when
// the name is lossy under sanitization. Without this, "sdk.js" and "sdk-js"
// would produce identical customIDs and clobber each other's docs.
func repoIDSegment(repo string) string {
	seg := CustomIDSanitize(repo)
	if repoNameIsLossy(repo) {
		return seg + "-" + repoNameHash(repo)
	}
	return seg
}

type AddRequest struct {
	Content       string            `json:"content"`
	CustomID      string            `json:"customId,omitempty"`
	ContainerTags []string          `json:"containerTags"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

type AddResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type SearchRequest struct {
	Query        string         `json:"q"`
	ContainerTag string         `json:"containerTag"`
	SearchMode   string         `json:"searchMode,omitempty"` // "hybrid" recommended
	Limit        int            `json:"limit,omitempty"`
	Threshold    float64        `json:"threshold,omitempty"`
	Rerank       bool           `json:"rerank,omitempty"`
	RewriteQuery bool           `json:"rewriteQuery,omitempty"`
	Filters      *SearchFilters `json:"filters,omitempty"`
	Include      *SearchInclude `json:"include,omitempty"`
}

// SearchFilters supports AND/OR metadata filtering per Supermemory docs.
type SearchFilters struct {
	AND []FilterCondition `json:"AND,omitempty"`
	OR  []FilterCondition `json:"OR,omitempty"`
}

type FilterCondition struct {
	Key             string `json:"key"`
	Value           string `json:"value"`
	FilterType      string `json:"filterType,omitempty"`      // "string_contains", "numeric", "array_contains"
	NumericOperator string `json:"numericOperator,omitempty"` // ">=", "<=", ">", "<", "="
	Negate          bool   `json:"negate,omitempty"`
}

// FilterNumeric returns a FilterCondition configured for numeric comparison.
// Supermemory metadata values are always strings in the API; without filterType
// "numeric" the server does lexicographic string comparison, which is wrong for
// fields like pr_number ("99" > "100" lexicographically but 99 < 100 numerically).
//
// op is the numericOperator: ">=", "<=", ">", "<", "=".
func FilterNumeric(key, op, value string) FilterCondition {
	return FilterCondition{
		Key:             key,
		Value:           value,
		FilterType:      "numeric",
		NumericOperator: op,
	}
}

// BuildFiltersJSON marshals SearchFilters to the JSON-string envelope that
// POST /v3/documents/list requires. v4/search accepts *SearchFilters nested
// directly in the request body; this helper is ONLY for ListRequest.
//
// Nil input (or filters with no AND/OR conditions) returns empty string and
// nil error — the caller should treat that as "no filter applied".
//
// Rejects AND+OR both populated — Supermemory docs don't define precedence
// and real behavior would be non-deterministic across calls.
func BuildFiltersJSON(f *SearchFilters) (string, error) {
	if f == nil || (len(f.AND) == 0 && len(f.OR) == 0) {
		return "", nil
	}
	if len(f.AND) > 0 && len(f.OR) > 0 {
		return "", fmt.Errorf("filters: AND and OR are mutually exclusive")
	}
	b, err := json.Marshal(f)
	if err != nil {
		return "", fmt.Errorf("marshaling filters: %w", err)
	}
	return string(b), nil
}

type SearchResponse struct {
	Results []SearchResult `json:"results"`
	Timing  int            `json:"timing"`
	Total   int            `json:"total"`
}

type SearchResult struct {
	ID              string          `json:"id"`
	Memory          string          `json:"memory,omitempty"`
	Chunk           string          `json:"chunk,omitempty"`
	Similarity      float64         `json:"similarity"`
	Metadata        json.RawMessage `json:"metadata,omitempty"`
	Summary         string          `json:"summary,omitempty"`
	RelatedMemories []RelatedMemory `json:"relatedMemories,omitempty"`
	Documents       []DocumentLink  `json:"documents,omitempty"`
}

// Content returns the best-available text from a search result,
// preferring Memory over Chunk.
func (r SearchResult) Content() string {
	if r.Memory != "" {
		return r.Memory
	}
	if r.Chunk != "" {
		return r.Chunk
	}
	return ""
}

// RichContent returns the primary content plus related memories and summary for fuller context.
func (r SearchResult) RichContent(maxRelated int) string {
	var sb strings.Builder
	sb.WriteString(r.Content())
	if r.Summary != "" {
		sb.WriteString("\nSummary: ")
		sb.WriteString(r.Summary)
	}
	for i, rm := range r.RelatedMemories {
		if i >= maxRelated {
			break
		}
		sb.WriteString("\nRelated: ")
		sb.WriteString(rm.Memory)
	}
	return sb.String()
}

type ListRequest struct {
	Limit         int      `json:"limit,omitempty"`
	Page          int      `json:"page,omitempty"`
	ContainerTags []string `json:"containerTags,omitempty"`
	// Filters is a JSON-encoded string per Supermemory v3 docs — NOT a nested
	// object. v4/search accepts nested SearchFilters directly; v3/documents/list
	// requires the stringified envelope. Use BuildFiltersJSON to construct.
	Filters string `json:"filters,omitempty"`
	Sort    string `json:"sort,omitempty"`
	Order   string `json:"order,omitempty"`
	// IncludeContent asks the API to return the full `content` field per doc.
	// Off by default — content bloats list responses; the migration count-verify
	// only needs pagination.totalItems, so it leaves this false.
	IncludeContent bool `json:"includeContent,omitempty"`
}

// ListPagination is the pagination envelope on POST /v3/documents/list. Limit is
// capped at 200 server-side. TotalItems is the count the migration count-verify
// gate compares between legacy and unified containers.
type ListPagination struct {
	CurrentPage int `json:"currentPage"`
	TotalItems  int `json:"totalItems"`
	TotalPages  int `json:"totalPages"`
	Limit       int `json:"limit"`
}

type ListResponse struct {
	Memories   []Document      `json:"memories"`
	Pagination *ListPagination `json:"pagination,omitempty"`
}

// Document is the /v3/documents/{id} response shape. Metadata + timestamps
// are needed by the reconciler's `_shared` decay phase (Bundle 5): it reads
// confidence from metadata and age from UpdatedAt to decide whether to
// decay, retire, or skip each doc.
type Document struct {
	ID        string            `json:"id"`
	CustomID  string            `json:"customId,omitempty"`
	Title     string            `json:"title,omitempty"`
	Status    string            `json:"status,omitempty"`
	Content   string            `json:"content,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	CreatedAt string            `json:"createdAt,omitempty"`
	UpdatedAt string            `json:"updatedAt,omitempty"`
}

type BulkDeleteRequest struct {
	IDs           []string `json:"ids,omitempty"`
	ContainerTags []string `json:"containerTags,omitempty"`
}

// BulkDeleteResponse is the response from DELETE /v3/documents/bulk. deletedCount
// is the total number of memories removed across the requested containerTags.
type BulkDeleteResponse struct {
	Success       bool     `json:"success"`
	DeletedCount  int      `json:"deletedCount"`
	ContainerTags []string `json:"containerTags,omitempty"`
}
