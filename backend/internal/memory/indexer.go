package memory

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BeLazy167/argus/backend/internal/util"
)

// ScenarioSearchResult holds a semantic search result with the parsed Postgres scenario ID.
type ScenarioSearchResult struct {
	ID         int64
	Content    string
	Similarity float64
}

// MemoryBlock is the structured result of specialistBlock: one synthesis hit
// (exact metadata match) plus a list of semantic matches from repo + shared
// containers. Briefing (assembleBriefing) dispatches these into typed sections.
//
// Concurrency invariant: MemoryBlock MUST remain write-partitioned. The three
// specialistBlock goroutines each write to a distinct field (Synthesis / Repo
// / Shared) — no shared slice, no shared map. Do not introduce any shared
// mutable state without synchronization; the happens-before edge is wg.Wait().
type MemoryBlock struct {
	Synthesis string         // file-scoped synthesis prose; empty if no match
	Repo      []PatternMatch // repo-scoped patterns/scenarios/feedback
	Shared    []PatternMatch // org-wide patterns
}

// Indexer is the domain-facing memory API. Callers build typed requests and
// the implementation handles container selection, metadata validation, and
// customID derivation. Every write lands in the unified `{repo}` / `_shared`
// container shape with typed metadata. The interface exposes typed reads only —
// no raw *Client escape hatch — so the retrieval + prompt-render seam lives
// entirely inside this module (see Briefing).
type Indexer interface {
	// Settings / lifecycle.
	DisableLLMFilter(ctx context.Context) error

	// Writers.
	IndexReviewCommentsBatch(ctx context.Context, owner, repo string, comments []ReviewMemory) error
	IndexRule(ctx context.Context, owner string, rule RuleMemory) error
	IndexPattern(ctx context.Context, repo string, pattern PatternMemory) (*AddResponse, error)
	IndexSharedPattern(ctx context.Context, pattern PatternMemory) (*AddResponse, error)
	IndexFeedbackSignal(ctx context.Context, owner, repo string, feedback FeedbackMemory) error
	IndexScenario(ctx context.Context, owner, repo string, scenarioID int64, description, severity string, files []string) error

	// Readers. The reader seam is two deep, error-honest methods: Search (typed
	// retrieval) + Briefing (assembled review-prompt block). Every value-level
	// read (pattern enrich, dismissal suppression, scenario dedup, triage/scoring
	// hints, rule lookup, agentic search) is a thin pure adapter over Search, so a
	// failed search is never silently indistinguishable from a genuine no-match.

	// Search runs a typed hybrid retrieval over the requested container scope and
	// returns the raw matches plus any search error verbatim. Callers choose the
	// failure policy: propagate (enrich novelty gating) or degrade via BestEffort
	// (briefing / hints / suppression). Owns its own 5s timeout; returns (nil,
	// nil) on a disabled indexer or an empty-repo repo scope. Per-call shaping
	// (top-1, truncation, id parsing) stays in the caller-side adapters. Defined
	// in reader.go.
	Search(ctx context.Context, q MemoryQuery) ([]PatternMatch, error)
	// Briefing assembles + renders the institutional-memory block for a review
	// prompt (specialist or single-pass, per q.Options.Profile), owning query
	// build, typed retrieval, polarity/type dispatch, per-section truncation, and
	// the per-call-site character cap, and returning any retrieval error so a
	// caller can degrade the block to empty. Callers embed the returned markdown
	// verbatim. Defined in briefing.go.
	Briefing(ctx context.Context, q BriefingQuery) (string, error)

	// Maintenance.
	DeleteDocument(ctx context.Context, documentID string) error
}

// indexerImpl is the concrete Indexer backed by a Supermemory Client.
type indexerImpl struct {
	client *Client
	logger *slog.Logger
}

// NewIndexer returns an Indexer writing to the unified container shape. A nil
// client produces an Indexer that silently no-ops every operation — used when
// an installation has no Supermemory key configured.
func NewIndexer(client *Client, logger *slog.Logger) Indexer {
	return &indexerImpl{client: client, logger: logger}
}

// DisableLLMFilter turns off Supermemory's server-side LLM filter. Argus
// pre-filters all content at the application layer, so the server-side filter
// only adds latency and non-determinism. Safe to call repeatedly; idempotent.
// Returns the underlying UpdateSettings error so the caller (Registry) can
// decide whether to retry on the next GetIndexer.
func (idx *indexerImpl) DisableLLMFilter(ctx context.Context) error {
	if idx.client == nil {
		return nil
	}
	// The LLM filter is an ACCOUNT-level setting on the customer's BYOK
	// Supermemory org, not container-scoped — PATCHing it mutates behavior for
	// every other tool sharing that key. Read the current value first and skip
	// the write when it's already disabled: makes repeated calls idempotent-cheap
	// and avoids re-mutating an account that's already configured. A read failure
	// is non-fatal — fall through to the PATCH so a genuinely-enabled filter still
	// gets disabled.
	if current, err := idx.client.GetSettings(ctx); err == nil {
		if filtering, ok := current["shouldLLMFilter"].(bool); ok && !filtering {
			return nil
		}
	}
	if err := idx.client.UpdateSettings(ctx, map[string]any{"shouldLLMFilter": false}); err != nil {
		return fmt.Errorf("disabling supermemory LLM filter: %w", err)
	}
	return nil
}

// PatternMatch is one search hit — content, similarity, Supermemory document
// ID, and raw metadata map. Callers read provenance fields (pr, pr_author,
// source, created_at) off Metadata, stamped at index time. RichContent carries
// summary + related-memory context and is populated only when the query set
// MemoryQuery.Enrich (the hint-render path); it is "" otherwise.
type PatternMatch struct {
	Content     string
	Score       float64
	ID          string
	Metadata    map[string]string
	RichContent string
}

// resultToPatternMatch converts a Supermemory SearchResult into the lighter
// PatternMatch shape. Unmarshals the raw metadata JSON into a map[string]string
// for cheap key lookups — metadata is always flat key/string values at index
// time (see Metadata.ToMap).
func resultToPatternMatch(r SearchResult) PatternMatch {
	pm := PatternMatch{
		Content: r.Content(),
		Score:   r.Similarity,
		ID:      r.ID,
	}
	if len(r.Metadata) > 0 {
		var md map[string]string
		if err := json.Unmarshal(r.Metadata, &md); err == nil {
			pm.Metadata = md
		}
	}
	return pm
}

var lineNumRegex = regexp.MustCompile(`(?i)\b(?:line|L)\s*\d+`)

// truncateIDWithSuffix caps a customId to 100 chars, preserving the suffix (hash/tag).
// Truncates the prefix to make room (rune-safe) rather than chopping the suffix.
func truncateIDWithSuffix(prefix, suffix string) string {
	sep := "--"
	maxPrefix := 100 - len(suffix) - len(sep)
	if maxPrefix < 0 {
		maxPrefix = 0
	}
	if len(prefix) > maxPrefix {
		// Walk backward to avoid splitting a multi-byte UTF-8 rune.
		cut := maxPrefix
		for cut > 0 && prefix[cut]&0xC0 == 0x80 {
			cut--
		}
		prefix = prefix[:cut]
	}
	return prefix + sep + suffix
}

// normalizeBody strips line numbers and excess whitespace for stable fingerprinting.
func normalizeBody(body string) string {
	s := lineNumRegex.ReplaceAllString(body, "")
	return strings.Join(strings.Fields(s), " ")
}

// FindingFingerprint produces a stable customId for a review finding.
// Format: {repo}--{sanitized-file}--{hash12} (max 100 chars).
// Returns empty string if repo or filePath is empty. owner is accepted for
// back-compat but ignored — under BYOK the installation key is the tenant.
func FindingFingerprint(owner, repo, filePath, category, body string) string {
	_ = owner
	if repo == "" || filePath == "" {
		return ""
	}
	h := sha256.Sum256([]byte(filePath + "|" + category + "|" + normalizeBody(body)))
	hash := hex.EncodeToString(h[:6]) // 12 hex chars
	prefix := fmt.Sprintf("%s--%s", repoIDSegment(repo), CustomIDSanitize(filePath))
	return truncateIDWithSuffix(prefix, hash)
}

// SynthesisCustomID returns a stable customId for a file synthesis document.
// owner accepted for back-compat; ignored.
//
// A hash of the RAW file path is ALWAYS appended and lives in the protected
// suffix. Always-present: CustomIDSanitize collapses '/' and '.' to '-', so
// distinct paths ("pkg/api-v1/x.go" vs "pkg/api/v1/x.go") sanitize identically;
// without a hash they would map to the same ID and clobber each other. In the
// suffix: truncateIDWithSuffix trims the readable prefix, never the suffix, so
// the disambiguator survives even for repo/path pairs that exceed 100 chars
// (a hash placed in the prefix would be chopped away for long names).
func SynthesisCustomID(owner, repo, filePath string) string {
	_ = owner
	h := sha256.Sum256([]byte(filePath))
	hash := hex.EncodeToString(h[:6])
	prefix := fmt.Sprintf("%s--%s", repoIDSegment(repo), CustomIDSanitize(filePath))
	return truncateIDWithSuffix(prefix, hash+"--synthesis")
}

// PRSummaryCustomID returns a stable customId for a PR summary document.
func PRSummaryCustomID(owner, repo string, prNumber int) string {
	_ = owner
	suffix := fmt.Sprintf("pr-%d-summary", prNumber)
	prefix := repoIDSegment(repo)
	return truncateIDWithSuffix(prefix, suffix)
}

// PatternCustomID returns a stable customId for a learned/confirmed pattern.
func PatternCustomID(owner, repo, source, content string) string {
	_ = owner
	h := sha256.Sum256([]byte(normalizeBody(content)))
	hash := hex.EncodeToString(h[:6])
	prefix := fmt.Sprintf("%s--%s", repoIDSegment(repo), CustomIDSanitize(source))
	return truncateIDWithSuffix(prefix, hash)
}

// SharedPatternCustomID returns a stable customId for a pattern written to the
// cross-repo `_shared` container (no repo segment).
func SharedPatternCustomID(source, content string) string {
	h := sha256.Sum256([]byte(normalizeBody(content)))
	hash := hex.EncodeToString(h[:6])
	prefix := fmt.Sprintf("shared--%s", CustomIDSanitize(source))
	return truncateIDWithSuffix(prefix, hash)
}

// TraceCustomID returns a stable customId for a decision trace. Hashes
// (file, trace_type, normalized_content) so identical traces dedupe across
// reruns, while semantically-different traces on the same file coexist.
func TraceCustomID(repo, filePath, traceType, content string) string {
	h := sha256.Sum256([]byte(filePath + "|" + traceType + "|" + normalizeBody(content)))
	hash := hex.EncodeToString(h[:6])
	prefix := fmt.Sprintf("%s--trace", repoIDSegment(repo))
	return truncateIDWithSuffix(prefix, hash)
}

// RuleCustomID returns a stable customId for a rule identified by its DB id.
func RuleCustomID(ruleID int64) string {
	return fmt.Sprintf("rule--%d", ruleID)
}

// DismissalCustomID returns a stable customId for a DISMISSED feedback signal.
// Keyed by category + semantic content only — deliberately file-path-free so
// the same dismissed finding recurring across files/PRs upserts one doc whose
// recurrence (not row count) is the suppression signal.
func DismissalCustomID(repo, category, body string) string {
	h := sha256.Sum256([]byte(category + "|" + normalizeBody(body)))
	hash := hex.EncodeToString(h[:6])
	prefix := fmt.Sprintf("%s--dismissal", repoIDSegment(repo))
	return truncateIDWithSuffix(prefix, hash)
}

// FeedbackCustomID returns a stable customId for a feedback signal on a finding.
// Includes `action` in the hash so confirmed and dismissed signals for the
// same finding coexist instead of silently overwriting each other.
func FeedbackCustomID(owner, repo, filePath, category, body, action string) string {
	_ = owner
	h := sha256.Sum256([]byte(filePath + "|" + category + "|" + normalizeBody(body) + "|" + action))
	hash := hex.EncodeToString(h[:6])
	prefix := fmt.Sprintf("%s--feedback", repoIDSegment(repo))
	return truncateIDWithSuffix(prefix, hash)
}

// ReviewMemory represents a review comment to be stored in Supermemory.
type ReviewMemory struct {
	ReviewID string
	PRNumber int
	FilePath string
	Body     string
	Severity string
	Category string
}

// RuleMemory represents a rule to be stored in Supermemory.
type RuleMemory struct {
	RuleID   int64
	Category string
	Priority int
	Content  string
}

// FeedbackMemory represents developer feedback on a review comment.
type FeedbackMemory struct {
	FilePath       string
	Category       string
	OriginalBody   string
	Action         string // "confirmed" | "dismissed" | "ignored"
	DeveloperReply string
	PRNumber       int
	// ChangeKind is the ReviewContract change class of the review that produced
	// the finding ("" = unknown/pre-contract). Stamped on dismissal metadata so
	// retrieval can ignore prototype-era dismissals during production review.
	ChangeKind string
	// Reason is the analyzer's distilled explanation for a dismissal ("" = none).
	Reason string
	// Repo is the repo short name, mirrored into dismissal metadata for
	// post-hoc audits (the container tag already scopes retrieval).
	Repo string
}

// IndexReviewCommentsBatch stores multiple review comments in one API call.
// Uses v3/documents/batch (max 600 per call, counts as 1 request for rate limiting).
func (idx *indexerImpl) IndexReviewCommentsBatch(ctx context.Context, owner, repo string, comments []ReviewMemory) error {
	if idx.client == nil || len(comments) == 0 {
		return nil
	}
	docs := make([]BatchDocument, 0, len(comments))
	skipped := 0
	for _, c := range comments {
		meta, err := Metadata{
			Type:     TypeReview,
			FilePath: c.FilePath,
			Severity: c.Severity,
			Category: c.Category,
			PRNumber: c.PRNumber,
			Extra:    map[string]string{"review_id": c.ReviewID},
		}.ToMap()
		if err != nil {
			idx.logger.Warn("skipping review comment with invalid metadata", "error", err, "file", c.FilePath)
			skipped++
			continue
		}
		docs = append(docs, BatchDocument{
			Content:  buildReviewContent(c),
			CustomID: FindingFingerprint(owner, repo, c.FilePath, c.Category, c.Body),
			Metadata: meta,
		})
	}
	// Surface all-dropped as an error so the caller can retry / alert. Silent
	// success on a fully-skipped batch masked reconcile-job data loss.
	if len(docs) == 0 {
		if skipped > 0 {
			return fmt.Errorf("batch indexing review comments: all %d docs dropped (invalid metadata)", skipped)
		}
		return nil
	}
	_, err := idx.client.AddMemoryBatch(ctx, BatchAddRequest{
		ContainerTag: RepoTagNew(repo),
		Documents:    docs,
	})
	if err != nil {
		return fmt.Errorf("batch indexing review comments: %w", err)
	}
	if skipped > 0 {
		idx.logger.Error("batch indexed review comments with drops", "repo", repo, "indexed", len(docs), "skipped", skipped)
	} else {
		idx.logger.Info("batch indexed review comments", "repo", repo, "count", len(docs))
	}
	return nil
}

// buildReviewContent keeps content pure-prose: the finding body only. No
// File:/Severity:/Category: prefix headers (those are metadata now) and no
// raw-diff Context suffix — retrieval matches on prose, and the diff only
// bloated the document.
func buildReviewContent(c ReviewMemory) string {
	return c.Body
}

// IndexRule stores an owner-scoped rule for semantic matching during review.
// Writes to `_shared` with type=rule. owner is accepted for back-compat.
func (idx *indexerImpl) IndexRule(ctx context.Context, owner string, rule RuleMemory) error {
	_ = owner
	if idx.client == nil {
		return nil
	}
	meta, err := Metadata{
		Type:     TypeRule,
		Category: rule.Category,
		Extra:    map[string]string{"rule_id": strconv.FormatInt(rule.RuleID, 10), "priority": strconv.Itoa(rule.Priority)},
	}.ToMap()
	if err != nil {
		return fmt.Errorf("rule metadata: %w", err)
	}
	_, err = idx.client.AddMemory(ctx, AddRequest{
		Content:       rule.Content,
		CustomID:      RuleCustomID(rule.RuleID),
		ContainerTags: []string{SharedTag},
		Metadata:      meta,
	})
	if err != nil {
		return fmt.Errorf("indexing rule: %w", err)
	}
	return nil
}

// PatternMemory is the typed input for IndexPattern / IndexSharedPattern. The
// writer derives the document Type from Source (synthesis, pr_summary and
// arch_summary re-type to their specific MemoryType; everything else stays
// type=pattern) and derives a deterministic customID when CustomID is empty so
// re-indexing upserts instead of duplicating.
type PatternMemory struct {
	Content  string
	CustomID string // optional; derived from Source+Content when empty
	Source   string
	Category string
	Severity string
	Subtype  string
	FilePath string
	PRNumber int
	PRAuthor string
	Score    int
	// Extra carries non-typed provenance (repo, choke_points, created_by,
	// origin_*). Keys colliding with a typed metadata field are rejected by
	// Metadata.ToMap, so callers keep provenance out of the reserved namespace.
	Extra map[string]string
}

// metadata builds the typed Metadata for a pattern write, mirroring the
// source-based re-typing so specialists can filter type=synthesis /
// type=pr_summary / type=topology precisely.
func (p PatternMemory) metadata() Metadata {
	m := Metadata{
		Type:     TypePattern,
		Subtype:  p.Subtype,
		Source:   p.Source,
		Category: p.Category,
		Severity: p.Severity,
		FilePath: p.FilePath,
		PRNumber: p.PRNumber,
		PRAuthor: p.PRAuthor,
		Score:    p.Score,
		Extra:    p.Extra,
	}
	switch p.Source {
	case "synthesis":
		m.Type = TypeSynthesis
	case "pr_summary":
		m.Type = TypePRSummary
	case "arch_summary":
		m.Type = TypeTopology
	}
	return m
}

// IndexPattern stores a pattern scoped to a specific repo. Writes to `{repo}`
// with the Source-derived type; a deterministic PatternCustomID is derived from
// source+content when CustomID is empty (upsert-dedup).
func (idx *indexerImpl) IndexPattern(ctx context.Context, repo string, p PatternMemory) (*AddResponse, error) {
	if idx.client == nil {
		return nil, nil
	}
	if p.CustomID == "" {
		source := p.Source
		if source == "" {
			source = "pattern"
		}
		p.CustomID = PatternCustomID("", repo, source, p.Content)
	}
	flat, err := p.metadata().ToMap()
	if err != nil {
		return nil, fmt.Errorf("pattern metadata: %w", err)
	}
	// Mirror the deterministic customId into searchable metadata under
	// "custom_id" so the per-finding enrich read can resolve a search hit back
	// to its patterns row by customId — /v4/search results carry metadata but no
	// top-level customId, and the result's own ID may be a chunk id that never
	// matches the stored supermemory_id.
	flat["custom_id"] = p.CustomID
	resp, err := idx.client.AddMemory(ctx, AddRequest{
		Content:       p.Content,
		CustomID:      p.CustomID,
		ContainerTags: []string{RepoTagNew(repo)},
		Metadata:      flat,
	})
	if err != nil {
		return nil, fmt.Errorf("indexing repo pattern: %w", err)
	}
	idx.logger.Info("indexed repo pattern", "repo", repo, "source", p.Source)
	return resp, nil
}

// IndexSharedPattern stores a pattern in the cross-repo `_shared` container.
// Every write pins confidence=1.00 — successful re-learning is the signal that
// the pattern is still live; the reconciler decays dormant docs and deletes
// below the retirement floor. A deterministic SharedPatternCustomID is derived
// from source+content when CustomID is empty. Source-trace provenance
// (origin_pr / origin_author) flows through Extra for post-hoc auditability.
func (idx *indexerImpl) IndexSharedPattern(ctx context.Context, p PatternMemory) (*AddResponse, error) {
	if idx.client == nil {
		return nil, nil
	}
	if p.CustomID == "" {
		source := p.Source
		if source == "" {
			source = "pattern"
		}
		p.CustomID = SharedPatternCustomID(source, p.Content)
	}
	m := p.metadata()
	// Copy Extra before pinning confidence so we never mutate the caller's map.
	extra := make(map[string]string, len(m.Extra)+1)
	for k, v := range m.Extra {
		extra[k] = v
	}
	extra["confidence"] = "1.00"
	m.Extra = extra
	flat, err := m.ToMap()
	if err != nil {
		return nil, fmt.Errorf("shared pattern metadata: %w", err)
	}
	// Mirror the customId into metadata (see IndexPattern) so shared-pattern
	// search hits are resolvable back to their patterns row by customId.
	flat["custom_id"] = p.CustomID
	resp, err := idx.client.AddMemory(ctx, AddRequest{
		Content:       p.Content,
		CustomID:      p.CustomID,
		ContainerTags: []string{SharedTag},
		Metadata:      flat,
	})
	if err != nil {
		return nil, fmt.Errorf("indexing shared pattern: %w", err)
	}
	idx.logger.Info("indexed shared pattern", "source", p.Source)
	return resp, nil
}

// IndexFeedbackSignal stores a single feedback event with action + polarity
// metadata. Confirmed, dismissed, and ignored share the same customID space via
// distinct `action` hashing, so all coexist per-finding.
//
// Returns an error on unrecognized Action — the valid set ("confirmed",
// "dismissed", "ignored") is small and stable; anything else is a caller bug
// that should surface, not silently drop.
func (idx *indexerImpl) IndexFeedbackSignal(ctx context.Context, owner, repo string, fb FeedbackMemory) error {
	if idx.client == nil {
		return nil
	}
	polarity, content, ok := feedbackShape(fb)
	if !ok {
		return fmt.Errorf("indexing feedback signal: unsupported action %q (want confirmed|dismissed|ignored)", fb.Action)
	}

	m := Metadata{
		Type:     TypeFeedback,
		FilePath: fb.FilePath,
		Category: fb.Category,
		Polarity: polarity,
		Action:   fb.Action,
		PRNumber: fb.PRNumber,
	}
	customID := FeedbackCustomID(owner, repo, fb.FilePath, fb.Category, fb.OriginalBody, fb.Action)
	// Dismissals are keyed by category + semantic content (file-path-free) and
	// carry change-kind provenance so retrieval can lifecycle-filter them.
	if fb.Action == "dismissed" {
		customID = DismissalCustomID(repo, fb.Category, fb.OriginalBody)
		extra := map[string]string{"repo": repo}
		if fb.ChangeKind != "" {
			extra["change_kind"] = fb.ChangeKind
		}
		if fb.Reason != "" {
			extra["reason"] = util.Truncate(fb.Reason, 300, false)
		}
		m.Extra = extra
	}
	meta, err := m.ToMap()
	if err != nil {
		return fmt.Errorf("feedback metadata: %w", err)
	}
	_, err = idx.client.AddMemory(ctx, AddRequest{
		Content:       content,
		CustomID:      customID,
		ContainerTags: []string{RepoTagNew(repo)},
		Metadata:      meta,
	})
	if err != nil {
		return fmt.Errorf("indexing feedback signal: %w", err)
	}
	idx.logger.Info("indexed feedback signal", "action", fb.Action, "repo", repo, "file", fb.FilePath)
	return nil
}

// feedbackShape derives polarity + content from a FeedbackMemory. Returns
// ok=false for unrecognized actions.
func feedbackShape(fb FeedbackMemory) (Polarity, string, bool) {
	switch fb.Action {
	case "confirmed":
		content := fb.OriginalBody
		if fb.DeveloperReply != "" {
			content += "\n\nDeveloper: " + util.Truncate(fb.DeveloperReply, 200, false)
		}
		return PolarityPositive, content, true
	case "dismissed":
		content := fb.OriginalBody
		if fb.DeveloperReply != "" {
			content += "\n\nDeveloper explanation: " + util.Truncate(fb.DeveloperReply, 200, false)
		}
		return PolarityNegative, content, true
	case "ignored":
		// Weak negative: the developer engaged (asked to clarify) but neither
		// confirmed nor dismissed the finding. Polarity is negative but the
		// `ignored` action keeps it distinguishable from an outright dismissal.
		content := fb.OriginalBody
		if fb.DeveloperReply != "" {
			content += "\n\nDeveloper (no resolution): " + util.Truncate(fb.DeveloperReply, 200, false)
		}
		return PolarityNegative, content, true
	default:
		return "", "", false
	}
}

// IndexScenario stores a scenario in the unified `{repo}` container with
// type=scenario and scenario_id in metadata (no more [scenario_id:N] prefix
// in content).
func (idx *indexerImpl) IndexScenario(ctx context.Context, owner, repo string, scenarioID int64, description, severity string, files []string) error {
	_ = owner
	if idx.client == nil {
		return nil
	}
	content := description
	if len(files) > 0 {
		content += "\n\nRelated files: " + strings.Join(files, ", ")
	}
	meta, err := Metadata{
		Type:       TypeScenario,
		ScenarioID: scenarioID,
		Severity:   severity,
	}.ToMap()
	if err != nil {
		return fmt.Errorf("scenario metadata: %w", err)
	}
	_, err = idx.client.AddMemory(ctx, AddRequest{
		Content:       content,
		CustomID:      ScenarioCustomID(repo, scenarioID),
		ContainerTags: []string{RepoTagNew(repo)},
		Metadata:      meta,
	})
	if err != nil {
		idx.logger.Warn("indexing scenario in supermemory", "error", err)
		return fmt.Errorf("indexing scenario: %w", err)
	}
	return nil
}

// specialistBlock fetches (1) file-scoped synthesis via exact metadata lookup,
// (2) repo-scoped semantic hits across patterns/scenarios/feedback, (3) shared
// semantic hits over patterns. Replaces the legacy 5-parallel-query block — cuts
// 5×N searches per PR to 2×N plus one list call. Owns its own 5s timeout;
// consumed only by Briefing (assembleBriefing) inside this module. Returns the
// first leg error so Briefing can surface a broken retrieval instead of silently
// serving a partial block; the repo (OR-of-types) and shared (numeric
// confidence) legs keep bespoke SearchRequests the single-type MemoryQuery does
// not model, but route through the shared error-honest runSearch core.
func (idx *indexerImpl) specialistBlock(ctx context.Context, repo, filePath, specialistQuery string, thresholds Thresholds) (MemoryBlock, error) {
	if idx.client == nil || repo == "" {
		return MemoryBlock{}, nil
	}
	thresholds = thresholds.WithDefaults()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var block MemoryBlock
	var synthErr, repoErr, sharedErr error
	var wg sync.WaitGroup
	wg.Add(3)

	// 1. Synthesis — metadata-filtered Search returns the body in r.Memory /
	// r.Chunk directly; avoids a List+GetDocument roundtrip (the Document
	// struct doesn't decode body fields). The query text is a placeholder —
	// the AND filter is what actually narrows results to this file's synthesis.
	go func() {
		defer wg.Done()
		if filePath == "" {
			return
		}
		var matches []PatternMatch
		matches, synthErr = idx.runSearch(ctx, SearchRequest{
			Query:        "file synthesis",
			ContainerTag: RepoTagNew(repo),
			SearchMode:   "hybrid",
			Limit:        1,
			Threshold:    0, // accept any hit — the metadata filter already pins it.
			Rerank:       false,
			Filters: &SearchFilters{AND: []FilterCondition{
				{Key: "type", Value: string(TypeSynthesis)},
				{Key: "file_path", Value: filePath},
			}},
		}, false)
		if len(matches) > 0 {
			block.Synthesis = matches[0].Content
		}
	}()

	// 2. Repo signal — semantic, type IN {pattern, scenario, feedback}.
	go func() {
		defer wg.Done()
		block.Repo, repoErr = idx.runSearch(ctx, SearchRequest{
			Query:        specialistQuery,
			ContainerTag: RepoTagNew(repo),
			SearchMode:   "hybrid",
			Limit:        5,
			Threshold:    thresholds.SpecialistMin,
			Rerank:       true,
			Filters: &SearchFilters{OR: []FilterCondition{
				{Key: "type", Value: string(TypePattern)},
				{Key: "type", Value: string(TypeScenario)},
				{Key: "type", Value: string(TypeFeedback)},
			}},
		}, false)
	}()

	// 3. Shared patterns — semantic against `_shared`. The AND filter excludes
	// already-fading docs (confidence < SharedConfidenceFloor) so decayed
	// patterns stop influencing reviews before the reconciler deletes them.
	// numeric compare required: FilterNumeric ensures supermemory interprets
	// the threshold as a float, not a lexicographic string.
	go func() {
		defer wg.Done()
		block.Shared, sharedErr = idx.runSearch(ctx, SearchRequest{
			Query:        specialistQuery,
			ContainerTag: SharedTag,
			SearchMode:   "hybrid",
			Limit:        3,
			Threshold:    thresholds.SpecialistMin,
			Rerank:       true,
			Filters: &SearchFilters{AND: []FilterCondition{
				{Key: "type", Value: string(TypePattern)},
				FilterNumeric("confidence", ">=", SharedConfidenceFloorStr),
			}},
		}, false)
	}()

	wg.Wait()
	// Per-leg degradation: keep whatever legs succeeded, Warn the failures,
	// and error only when ALL legs failed (nothing usable). Callers treat the
	// returned error as "no institutional memory available at all".
	failures := 0
	for _, l := range []struct {
		name string
		err  error
	}{{"specialist.synthesis", synthErr}, {"specialist.repo_patterns", repoErr}, {"specialist.shared_patterns", sharedErr}} {
		if l.err != nil {
			failures++
			idx.warnLeg(l.name, RepoTagNew(repo), 0, l.err)
		}
	}
	if failures == 3 {
		return MemoryBlock{}, cmp.Or(synthErr, repoErr, sharedErr)
	}
	return block, nil
}

// DeleteDocument removes a document from Supermemory by ID.
func (idx *indexerImpl) DeleteDocument(ctx context.Context, documentID string) error {
	if idx.client == nil {
		return nil
	}
	if err := idx.client.DeleteMemory(ctx, documentID); err != nil {
		return fmt.Errorf("deleting document: %w", err)
	}
	idx.logger.Debug("deleted document", "id", documentID)
	return nil
}

// FormatPositivePattern builds a structured positive pattern string from review data.
// Retained for back-compat with existing callers and tests. New code should
// pass structured fields directly to IndexFeedbackSignal where they land in
// typed metadata instead of being baked into content.
//
// Deprecated: use IndexFeedbackSignal with FeedbackMemory directly.
func FormatPositivePattern(category, filePath string, line int, body string) string {
	pattern := fmt.Sprintf("POSITIVE_PATTERN: [%s] %s:%d — %s", category, filePath, line, body)
	if len(pattern) > 200 {
		cut := 197
		for cut > 0 && pattern[cut]&0xC0 == 0x80 {
			cut--
		}
		pattern = pattern[:cut] + "..."
	}
	return pattern
}
