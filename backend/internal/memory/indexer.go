package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
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

// MemoryBlock is the structured result of SpecialistBlock: one synthesis hit
// (exact metadata match) plus a list of semantic matches from repo + shared
// containers. The caller formats these into a specialist prompt.
//
// Concurrency invariant: MemoryBlock MUST remain write-partitioned. The three
// SpecialistBlock goroutines each write to a distinct field (Synthesis / Repo
// / Shared) — no shared slice, no shared map. Do not introduce any shared
// mutable state without synchronization; the happens-before edge is wg.Wait().
type MemoryBlock struct {
	Synthesis string         // file-scoped synthesis prose; empty if no match
	Repo      []PatternMatch // repo-scoped patterns/scenarios/feedback
	Shared    []PatternMatch // org-wide patterns
}

// Indexer is the domain-facing memory API. Callers build typed requests and
// the implementation handles container selection, metadata validation, and
// customID derivation.
//
// Old-style methods (IndexRepoPattern, IndexOwnerPattern, IndexReviewComment,
// etc.) retain their existing signatures for incremental migration but now
// write to the unified `{repo}` / `_shared` container shape with typed
// metadata under the hood. Prefer the new typed methods (IndexPattern,
// IndexFeedback, SpecialistBlock) for new code.
type Indexer interface {
	Client() *Client

	// Settings / lifecycle.
	DisableLLMFilter(ctx context.Context) error
	SetRepoEntityContext(ctx context.Context, owner, repo, language, description string)

	// Writers — legacy signatures kept for call-site compatibility.
	IndexReviewComment(ctx context.Context, owner, repo string, comment ReviewMemory) error
	IndexReviewCommentsBatch(ctx context.Context, owner, repo string, comments []ReviewMemory) error
	IndexRule(ctx context.Context, owner string, rule RuleMemory) error
	IndexRepoPattern(ctx context.Context, owner, repo, content, customID string, metadata map[string]string) (*AddResponse, error)
	IndexOwnerPattern(ctx context.Context, owner, content, customID string, metadata map[string]string) (*AddResponse, error)
	IndexPositivePattern(ctx context.Context, owner, repo, content, customID string, metadata map[string]string) error
	IndexFeedbackSignal(ctx context.Context, owner, repo string, feedback FeedbackMemory) error
	IndexScenario(ctx context.Context, owner, repo string, scenarioID int64, description, severity string, files []string) error
	IndexDecisionTrace(ctx context.Context, owner, repo, filePath, traceType, content, severity string) error
	IndexSimulationResult(ctx context.Context, owner, repo string, prNumber int, changedFiles []string, passes bool, scenario string, confidence float64, rootCause, impact string) error

	// Readers.
	SearchPatternMatch(ctx context.Context, owner, repo, query string, thresholds Thresholds) PatternMatch
	SearchScenarios(ctx context.Context, owner, repo, query, severity string, limit int) []string
	SearchScenariosWithIDs(ctx context.Context, owner, repo, query, severity string, limit int) []ScenarioSearchResult
	SearchTraces(ctx context.Context, owner, repo, query, traceType string, limit int) []string
	SearchPatternsFiltered(ctx context.Context, owner, repo, query, category string, limit int) []string

	// Specialist retrieval — single call replaces the legacy 5-parallel-query
	// block. See plan doc for shape: 1 synthesis list + 1 repo semantic + 1
	// shared semantic. Thresholds controls the server-side similarity cutoff
	// used by the repo and shared semantic reads.
	SpecialistBlock(ctx context.Context, owner, repo, filePath, specialistQuery string, thresholds Thresholds) MemoryBlock

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

// Client returns the underlying Supermemory client.
func (idx *indexerImpl) Client() *Client { return idx.client }

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

// SetRepoEntityContext sets per-repo context that guides memory extraction.
// Under the unified container shape this touches only the single `{repo}` tag.
// Owner arg is accepted but unused (BYOK: key is tenant).
func (idx *indexerImpl) SetRepoEntityContext(ctx context.Context, owner, repo, language, description string) {
	_ = owner
	if idx.client == nil {
		return
	}
	tag := RepoTagNew(repo)
	entityCtx := fmt.Sprintf("Code review data for repo %s", repo)
	if language != "" {
		entityCtx += fmt.Sprintf(" (primary language: %s)", language)
	}
	if description != "" {
		entityCtx += ". " + description
	}
	if err := idx.client.UpdateEntityContext(ctx, tag, entityCtx); err != nil {
		idx.logger.Debug("failed to set entity context", "tag", tag, "error", err)
	}
}

// PatternMatch is the full result of SearchPatternMatch — content, similarity,
// Supermemory document ID, and raw metadata map. Callers unmarshal Metadata to
// read provenance fields (pr, pr_author, source, created_at) stamped at index
// time.
type PatternMatch struct {
	Content  string
	Score    float64
	ID       string
	Metadata map[string]string
}

// SearchPatternMatch searches for the best matching pattern across the repo
// container and the shared container. Returns the higher-scoring match.
// Dual-reads include a fallback against the legacy `{owner}--{repo}--patterns`
// container so live reviews continue to see pre-migration data; remove the
// legacy branch once migration completes.
func (idx *indexerImpl) SearchPatternMatch(ctx context.Context, owner, repo, query string, thresholds Thresholds) PatternMatch {
	if idx.client == nil || repo == "" {
		return PatternMatch{}
	}
	thresholds = thresholds.WithDefaults()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var repoRes, sharedRes, legacyRepoRes, legacyOwnerRes PatternMatch
	var wg sync.WaitGroup
	wg.Add(4)

	patternFilter := &SearchFilters{AND: []FilterCondition{{Key: "type", Value: string(TypePattern)}}}

	go func() {
		defer wg.Done()
		repoRes = idx.topMatch(ctx, query, RepoTagNew(repo), patternFilter, thresholds.FindingEnrich)
	}()
	go func() {
		defer wg.Done()
		sharedRes = idx.topMatch(ctx, query, SharedTag, patternFilter, thresholds.FindingEnrich)
	}()
	// Legacy dual-read — removed in post-migration cleanup PR. Legacy
	// `{owner}--{repo}--patterns` held mixed writes (synthesis, pr_summary,
	// arch_summary, feedback) so a non-pattern doc could silently outrank a
	// real pattern. Post-filter by metadata.source to exclude known non-pattern
	// writes; if the top-1 legacy hit is a non-pattern, treat as zero match.
	go func() {
		defer wg.Done()
		if owner == "" {
			return
		}
		legacyRepoRes = idx.topMatchExcludingSources(ctx, query, RepoTag(owner, repo, "patterns"), legacyNonPatternSources, thresholds.FindingEnrich)
	}()
	go func() {
		defer wg.Done()
		if owner == "" {
			return
		}
		legacyOwnerRes = idx.topMatchExcludingSources(ctx, query, OwnerTag(owner, "patterns"), legacyNonPatternSources, thresholds.FindingEnrich)
	}()

	wg.Wait()
	return bestMatch(repoRes, sharedRes, legacyRepoRes, legacyOwnerRes)
}

// legacyNonPatternSources enumerates metadata.source values that legacy writes
// stored in `--patterns` containers without being actual patterns. Used to
// exclude them from legacy pattern-match results during migration.
var legacyNonPatternSources = map[string]struct{}{
	"synthesis":          {},
	"pr_summary":         {},
	"arch_summary":       {},
	"feedback_confirmed": {},
	"feedback_dismissed": {},
}

// topMatchExcludingSources fetches up to 3 hybrid results, filters out any
// whose metadata.source is in `excluded`, and returns the best remaining.
// Returns zero PatternMatch if everything in the top slice was excluded.
func (idx *indexerImpl) topMatchExcludingSources(ctx context.Context, query, containerTag string, excluded map[string]struct{}, threshold float64) PatternMatch {
	resp, err := idx.client.Search(ctx, SearchRequest{
		Query:        query,
		ContainerTag: containerTag,
		SearchMode:   "hybrid",
		Limit:        3, // take top-3 so we still have candidates after filtering
		Threshold:    threshold,
		Rerank:       true,
	})
	if err != nil || resp == nil {
		return PatternMatch{}
	}
	for _, r := range resp.Results {
		pm := resultToPatternMatch(r)
		if _, skip := excluded[pm.Metadata["source"]]; skip {
			continue
		}
		return pm
	}
	return PatternMatch{}
}

// topMatch runs a one-result hybrid search on the given container + filter.
// Returns zero PatternMatch if no hit, error, or empty query. threshold is
// the server-side similarity cutoff (callers pass thresholds.FindingEnrich).
func (idx *indexerImpl) topMatch(ctx context.Context, query, containerTag string, filters *SearchFilters, threshold float64) PatternMatch {
	resp, err := idx.client.Search(ctx, SearchRequest{
		Query:        query,
		ContainerTag: containerTag,
		SearchMode:   "hybrid",
		Limit:        1,
		Threshold:    threshold,
		Rerank:       true,
		Filters:      filters,
	})
	if err != nil || resp == nil || len(resp.Results) == 0 {
		return PatternMatch{}
	}
	return resultToPatternMatch(resp.Results[0])
}

// bestMatch returns the PatternMatch with the highest Score from the given candidates.
func bestMatch(candidates ...PatternMatch) PatternMatch {
	var best PatternMatch
	for _, c := range candidates {
		if c.Score > best.Score {
			best = c
		}
	}
	return best
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

// SimulationCustomID returns a stable customId for a simulation result.
// Same PR + same scenario always maps to the same doc — idempotent re-runs.
func SimulationCustomID(repo string, prNumber int, scenario string) string {
	h := sha256.Sum256([]byte(scenario))
	hash := hex.EncodeToString(h[:6])
	suffix := fmt.Sprintf("sim-%d-%s", prNumber, hash)
	return truncateIDWithSuffix(repoIDSegment(repo), suffix)
}

// TopologyCustomID returns the stable single-doc customId for a repo's
// topology summary.
func TopologyCustomID(repo string) string {
	return truncateIDWithSuffix(repoIDSegment(repo), "topology")
}

// RuleCustomID returns a stable customId for a rule identified by its DB id.
func RuleCustomID(ruleID int64) string {
	return fmt.Sprintf("rule--%d", ruleID)
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
	ReviewID    string
	PRNumber    int
	FilePath    string
	Body        string
	Severity    string
	Category    string
	DiffContext string
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
	Action         string // "confirmed" or "dismissed"
	DeveloperReply string
	PRNumber       int
}

// IndexReviewComment stores a single review comment. Writes to the unified
// `{repo}` container with `type=review` metadata. Upserts via stable customID.
func (idx *indexerImpl) IndexReviewComment(ctx context.Context, owner, repo string, comment ReviewMemory) error {
	if idx.client == nil {
		return nil
	}
	content := buildReviewContent(comment)
	meta, err := Metadata{
		Type:     TypeReview,
		FilePath: comment.FilePath,
		Severity: comment.Severity,
		Category: comment.Category,
		PRNumber: comment.PRNumber,
		Extra:    map[string]string{"review_id": comment.ReviewID},
	}.ToMap()
	if err != nil {
		return fmt.Errorf("review metadata: %w", err)
	}
	_, err = idx.client.AddMemory(ctx, AddRequest{
		Content:       content,
		CustomID:      FindingFingerprint(owner, repo, comment.FilePath, comment.Category, comment.Body),
		ContainerTags: []string{RepoTagNew(repo)},
		Metadata:      meta,
	})
	if err != nil {
		return fmt.Errorf("indexing review comment: %w", err)
	}
	idx.logger.Info("indexed review comment", "repo", repo, "file", comment.FilePath)
	return nil
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

// buildReviewContent keeps content pure-prose: finding body + diff context.
// No File:/Severity:/Category: prefix headers — those are metadata now.
func buildReviewContent(c ReviewMemory) string {
	if c.DiffContext == "" {
		return c.Body
	}
	return c.Body + "\n\nContext:\n" + c.DiffContext
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

// IndexRepoPattern stores a pattern scoped to a specific repo. Writes to
// `{repo}` with type=pattern; stable customID is required (upsert-dedup).
// owner is accepted for back-compat and ignored.
func (idx *indexerImpl) IndexRepoPattern(ctx context.Context, owner, repo, content, customID string, metadata map[string]string) (*AddResponse, error) {
	if idx.client == nil {
		return nil, nil
	}
	if customID == "" {
		source := metadata["source"]
		if source == "" {
			source = "pattern"
		}
		customID = PatternCustomID(owner, repo, source, content)
	}
	meta := patternMetadataFromLegacy(metadata, idx.logger)
	flat, err := meta.ToMap()
	if err != nil {
		return nil, fmt.Errorf("pattern metadata: %w", err)
	}
	resp, err := idx.client.AddMemory(ctx, AddRequest{
		Content:       content,
		CustomID:      customID,
		ContainerTags: []string{RepoTagNew(repo)},
		Metadata:      flat,
	})
	if err != nil {
		return nil, fmt.Errorf("indexing repo pattern: %w", err)
	}
	idx.logger.Info("indexed repo pattern", "repo", repo, "source", meta.Source)
	return resp, nil
}

// IndexOwnerPattern stores a pattern at owner scope — now written to the
// shared container under the installation. Adds confidence=1.0 on every
// write (Bundle 5); the reconciler decays this over time for dormant docs
// and deletes below the retirement floor. Source-trace fields (origin_pr /
// origin_author) flow through metadata.Extra for post-hoc auditability —
// operators can grep bad patterns back to the PR + developer that seeded them.
func (idx *indexerImpl) IndexOwnerPattern(ctx context.Context, owner, content, customID string, metadata map[string]string) (*AddResponse, error) {
	_ = owner
	if idx.client == nil {
		return nil, nil
	}
	if customID == "" {
		source := metadata["source"]
		if source == "" {
			source = "pattern"
		}
		customID = SharedPatternCustomID(source, content)
	}
	meta := patternMetadataFromLegacy(metadata, idx.logger)
	// Every _shared write resets confidence to 1.0 — successful re-learning
	// is the signal that the pattern is still live. Dormant patterns that
	// never re-upsert will decay via the reconciler's nightly sweep.
	if meta.Extra == nil {
		meta.Extra = make(map[string]string, 4)
	}
	meta.Extra["confidence"] = "1.00"
	// Preserve origin_* fields from the caller when present (auto_learn /
	// reply_feedback both pass these); they're the audit trail for how a
	// shared pattern made it into the corpus.
	for _, k := range []string{"origin_pr", "origin_author", "origin_model_decision"} {
		if v, ok := metadata[k]; ok && v != "" {
			meta.Extra[k] = v
		}
	}
	flat, err := meta.ToMap()
	if err != nil {
		return nil, fmt.Errorf("shared pattern metadata: %w", err)
	}
	resp, err := idx.client.AddMemory(ctx, AddRequest{
		Content:       content,
		CustomID:      customID,
		ContainerTags: []string{SharedTag},
		Metadata:      flat,
	})
	if err != nil {
		return nil, fmt.Errorf("indexing shared pattern: %w", err)
	}
	idx.logger.Info("indexed shared pattern", "source", meta.Source)
	return resp, nil
}

// patternMetadataFromLegacy translates the old map[string]string call sites
// used to pass into IndexRepoPattern / IndexOwnerPattern into a typed
// Metadata. Keys honored: source, category, pr (alias pr_number), pr_author,
// score (parsed to int), file (alias file_path). Unknown keys flow through
// Extra so callers keep any custom provenance (e.g. choke_points).
//
// logger is optional — when non-nil, unparseable numeric values log a Warn
// so callers can see legacy maps with garbage numeric strings instead of
// silently getting zero-valued typed fields.
func patternMetadataFromLegacy(legacy map[string]string, logger *slog.Logger) Metadata {
	m := Metadata{Type: TypePattern}
	m.Subtype = legacy["subtype"]
	m.Source = legacy["source"]
	m.Category = legacy["category"]
	m.Severity = legacy["severity"]
	m.PRAuthor = legacy["pr_author"]
	warnParse := func(key, val string, err error) {
		if logger != nil {
			logger.Warn("legacy metadata parse", "key", key, "value", val, "error", err)
		}
	}
	if v := legacy["pr"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			m.PRNumber = n
		} else {
			warnParse("pr", v, err)
		}
	}
	if v := legacy["pr_number"]; v != "" && m.PRNumber == 0 {
		if n, err := strconv.Atoi(v); err == nil {
			m.PRNumber = n
		} else {
			warnParse("pr_number", v, err)
		}
	}
	if v := legacy["score"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			m.Score = n
		} else {
			warnParse("score", v, err)
		}
	}
	if v := legacy["file"]; v != "" {
		m.FilePath = v
	}
	if v := legacy["file_path"]; v != "" {
		m.FilePath = v
	}
	// If source is one of the "unstructured" summary kinds, re-type accordingly
	// so specialists can filter `type=synthesis` / `type=pr_summary` precisely.
	switch m.Source {
	case "synthesis":
		m.Type = TypeSynthesis
	case "pr_summary":
		m.Type = TypePRSummary
	case "arch_summary":
		m.Type = TypeTopology
	}
	// Pass through any unrecognized keys (e.g. choke_points) via Extra.
	reserved := map[string]struct{}{
		"subtype": {}, "source": {}, "category": {}, "severity": {},
		"pr": {}, "pr_number": {}, "pr_author": {}, "score": {},
		"file": {}, "file_path": {},
	}
	for k, v := range legacy {
		if _, skip := reserved[k]; skip {
			continue
		}
		if _, reserved := reservedMetadataKeys[k]; reserved {
			continue
		}
		if m.Extra == nil {
			m.Extra = map[string]string{}
		}
		m.Extra[k] = v
	}
	return m
}

// IndexPositivePattern is a compatibility shim around IndexFeedbackSignal —
// positive patterns are feedback with action=confirmed, polarity=positive.
// New code should call IndexFeedbackSignal directly.
//
// Deprecated: construct a FeedbackMemory with Action="confirmed" and call
// IndexFeedbackSignal.
func (idx *indexerImpl) IndexPositivePattern(ctx context.Context, owner, repo, content, customID string, metadata map[string]string) error {
	_ = customID
	if idx.client == nil {
		return nil
	}
	return idx.IndexFeedbackSignal(ctx, owner, repo, FeedbackMemory{
		FilePath:       metadata["file_path"],
		Category:       metadata["category"],
		OriginalBody:   content,
		Action:         "confirmed",
		DeveloperReply: "",
		PRNumber:       parseIntOr(metadata["pr"], parseIntOr(metadata["pr_number"], 0)),
	})
}

func parseIntOr(s string, fallback int) int {
	if s == "" {
		return fallback
	}
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return fallback
}

// IndexFeedbackSignal stores a single feedback event with action + polarity
// metadata. Confirmed vs dismissed share the same customID space via distinct
// `action` hashing, so both coexist per-finding.
//
// Returns an error on unrecognized Action — the valid set ("confirmed",
// "dismissed") is small and stable; anything else is a caller bug that
// should surface, not silently drop.
func (idx *indexerImpl) IndexFeedbackSignal(ctx context.Context, owner, repo string, fb FeedbackMemory) error {
	if idx.client == nil {
		return nil
	}
	polarity, content, ok := feedbackShape(fb)
	if !ok {
		return fmt.Errorf("indexing feedback signal: unsupported action %q (want confirmed|dismissed)", fb.Action)
	}

	meta, err := Metadata{
		Type:     TypeFeedback,
		FilePath: fb.FilePath,
		Category: fb.Category,
		Polarity: polarity,
		Action:   fb.Action,
		PRNumber: fb.PRNumber,
	}.ToMap()
	if err != nil {
		return fmt.Errorf("feedback metadata: %w", err)
	}
	_, err = idx.client.AddMemory(ctx, AddRequest{
		Content:       content,
		CustomID:      FeedbackCustomID(owner, repo, fb.FilePath, fb.Category, fb.OriginalBody, fb.Action),
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

// IndexDecisionTrace stores a decision trace in the unified `{repo}` container
// with type=trace metadata. Uses a content-hash customID so exact duplicates
// dedupe while semantically-different traces on the same file coexist.
func (idx *indexerImpl) IndexDecisionTrace(ctx context.Context, owner, repo, filePath, traceType, content, severity string) error {
	_ = owner
	if idx.client == nil {
		return nil
	}
	meta, err := Metadata{
		Type:     TypeTrace,
		Subtype:  traceType,
		FilePath: filePath,
		Severity: severity,
	}.ToMap()
	if err != nil {
		return fmt.Errorf("trace metadata: %w", err)
	}
	_, err = idx.client.AddMemory(ctx, AddRequest{
		Content:       content,
		CustomID:      TraceCustomID(repo, filePath, traceType, content),
		ContainerTags: []string{RepoTagNew(repo)},
		Metadata:      meta,
	})
	if err != nil {
		idx.logger.Warn("indexing trace in supermemory", "error", err)
		return fmt.Errorf("indexing decision trace: %w", err)
	}
	return nil
}

// SearchScenarios performs semantic search over scenarios with reranking.
// Returns content strings. Dual-reads legacy container for transition.
func (idx *indexerImpl) SearchScenarios(ctx context.Context, owner, repo, query, severity string, limit int) []string {
	results := idx.SearchScenariosWithIDs(ctx, owner, repo, query, severity, limit)
	out := make([]string, 0, len(results))
	for _, r := range results {
		out = append(out, r.Content)
	}
	return out
}

// SearchScenariosWithIDs performs semantic search over scenarios and returns
// results with scenario IDs pulled from `metadata.scenario_id` (falling back
// to the legacy `[scenario_id:N]` content prefix for pre-migration docs).
func (idx *indexerImpl) SearchScenariosWithIDs(ctx context.Context, owner, repo, query, severity string, limit int) []ScenarioSearchResult {
	if idx.client == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	filters := &SearchFilters{AND: []FilterCondition{{Key: "type", Value: string(TypeScenario)}}}
	if severity != "" {
		filters.AND = append(filters.AND, FilterCondition{Key: "severity", Value: severity})
	}

	newResp, err := idx.client.Search(ctx, SearchRequest{
		Query:        query,
		ContainerTag: RepoTagNew(repo),
		SearchMode:   "hybrid",
		Rerank:       true,
		Limit:        limit,
		Filters:      filters,
	})
	if err != nil {
		idx.logger.Warn("searching scenarios in supermemory", "error", err)
	}

	var legacyResp *SearchResponse
	if owner != "" {
		legacyReq := SearchRequest{
			Query:        query,
			ContainerTag: RepoTag(owner, repo, "scenarios"),
			SearchMode:   "hybrid",
			Rerank:       true,
			Limit:        limit,
		}
		if severity != "" {
			legacyReq.Filters = &SearchFilters{AND: []FilterCondition{{Key: "severity", Value: severity}}}
		}
		lResp, lerr := idx.client.Search(ctx, legacyReq)
		if lerr != nil {
			idx.logger.Debug("legacy scenarios search failed", "error", lerr, "tag", legacyReq.ContainerTag)
		}
		legacyResp = lResp
	}

	return idx.mergeScenarioResults(newResp, legacyResp, limit)
}

var scenarioIDContentRegex = regexp.MustCompile(`\[scenario_id:(\d+)\]`)

// mergeScenarioResults unions results from the new and legacy containers,
// dedupes by scenario_id (NOT Supermemory doc ID — same scenario across
// containers has different doc IDs), prefers metadata.scenario_id over the
// legacy content-prefix parse, and returns up to `limit` sorted by similarity
// descending so the rerank budget isn't wasted on lower-scoring duplicates.
func (idx *indexerImpl) mergeScenarioResults(newResp, legacyResp *SearchResponse, limit int) []ScenarioSearchResult {
	seenScenarioIDs := map[int64]struct{}{}
	var out []ScenarioSearchResult

	add := func(r SearchResult, preferMeta bool) {
		content := r.Content()
		var id int64
		if preferMeta && len(r.Metadata) > 0 {
			var md map[string]string
			if err := json.Unmarshal(r.Metadata, &md); err == nil {
				if v, ok := md["scenario_id"]; ok {
					if parsed, perr := strconv.ParseInt(v, 10, 64); perr == nil {
						id = parsed
					}
				}
			}
		}
		if id == 0 {
			if m := scenarioIDContentRegex.FindStringSubmatch(content); len(m) == 2 {
				if parsed, perr := strconv.ParseInt(m[1], 10, 64); perr == nil {
					id = parsed
				}
			}
		}
		if id == 0 {
			idx.logger.Warn("scenario missing id", "doc_id", r.ID, "content_head", util.Truncate(content, 80, false))
			return
		}
		if _, dup := seenScenarioIDs[id]; dup {
			return
		}
		seenScenarioIDs[id] = struct{}{}
		out = append(out, ScenarioSearchResult{ID: id, Content: content, Similarity: r.Similarity})
	}

	if newResp != nil {
		for _, r := range newResp.Results {
			add(r, true)
		}
	}
	if legacyResp != nil {
		for _, r := range legacyResp.Results {
			add(r, false)
		}
	}

	// Sort merged by similarity desc so the cap keeps the best hits across both sources.
	sort.SliceStable(out, func(i, j int) bool { return out[i].Similarity > out[j].Similarity })

	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// IndexSimulationResult indexes a simulation result as a trace with a stable
// customID. Previously used v4/memories (no dedup); now uses v3 upsert so
// re-running simulation on the same PR doesn't accumulate duplicates.
func (idx *indexerImpl) IndexSimulationResult(ctx context.Context, owner, repo string, prNumber int, changedFiles []string, passes bool, scenario string, confidence float64, rootCause, impact string) error {
	_ = owner
	if idx.client == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	status := "PASS"
	if !passes {
		status = "FAIL"
	}
	// Pure prose content — structured fields live in metadata.
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Simulation %s on PR #%d for scenario: %s (%.0f%% confidence).",
		status, prNumber, util.Truncate(scenario, 200, true), confidence*100))
	if rootCause != "" {
		sb.WriteString("\nRoot cause: ")
		sb.WriteString(rootCause)
	}
	if impact != "" {
		sb.WriteString("\nImpact: ")
		sb.WriteString(impact)
	}
	if len(changedFiles) > 0 {
		sb.WriteString("\nChanged files: ")
		sb.WriteString(strings.Join(changedFiles, ", "))
	}

	meta, err := Metadata{
		Type:     TypeTrace,
		Subtype:  "simulation_result",
		PRNumber: prNumber,
		Extra: map[string]string{
			"passes":     fmt.Sprintf("%t", passes),
			"confidence": fmt.Sprintf("%.2f", confidence),
		},
	}.ToMap()
	if err != nil {
		return fmt.Errorf("simulation metadata: %w", err)
	}
	_, err = idx.client.AddMemory(ctx, AddRequest{
		Content:       sb.String(),
		CustomID:      SimulationCustomID(repo, prNumber, scenario),
		ContainerTags: []string{RepoTagNew(repo)},
		Metadata:      meta,
	})
	if err != nil {
		return fmt.Errorf("indexing simulation result: %w", err)
	}
	return nil
}

// SearchTraces performs semantic search over decision traces. Trace type
// filter narrows results (e.g., only "review_finding"). Dual-reads legacy
// `{owner}--{repo}--traces` container for transition.
func (idx *indexerImpl) SearchTraces(ctx context.Context, owner, repo, query, traceType string, limit int) []string {
	if idx.client == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	filters := &SearchFilters{AND: []FilterCondition{{Key: "type", Value: string(TypeTrace)}}}
	if traceType != "" {
		filters.AND = append(filters.AND, FilterCondition{Key: "subtype", Value: traceType})
	}

	newResp, err := idx.client.Search(ctx, SearchRequest{
		Query:        query,
		ContainerTag: RepoTagNew(repo),
		SearchMode:   "hybrid",
		Rerank:       true,
		Limit:        limit,
		Filters:      filters,
	})
	if err != nil {
		idx.logger.Warn("searching traces in supermemory", "error", err)
	}

	var legacyResp *SearchResponse
	if owner != "" {
		legacyReq := SearchRequest{
			Query:        query,
			ContainerTag: RepoTag(owner, repo, "traces"),
			SearchMode:   "hybrid",
			Rerank:       true,
			Limit:        limit,
		}
		if traceType != "" {
			legacyReq.Filters = &SearchFilters{AND: []FilterCondition{{Key: "trace_type", Value: traceType}}}
		}
		lResp, lerr := idx.client.Search(ctx, legacyReq)
		if lerr != nil {
			idx.logger.Debug("legacy traces search failed", "error", lerr, "tag", legacyReq.ContainerTag)
		}
		legacyResp = lResp
	}

	return contentsFromResponses(newResp, legacyResp, limit)
}

// SearchPatternsFiltered performs semantic search over patterns with metadata
// filtering. category narrows results.
func (idx *indexerImpl) SearchPatternsFiltered(ctx context.Context, owner, repo, query, category string, limit int) []string {
	if idx.client == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	filters := &SearchFilters{AND: []FilterCondition{{Key: "type", Value: string(TypePattern)}}}
	if category != "" {
		filters.AND = append(filters.AND, FilterCondition{Key: "category", Value: category})
	}

	newResp, err := idx.client.Search(ctx, SearchRequest{
		Query:        query,
		ContainerTag: RepoTagNew(repo),
		SearchMode:   "hybrid",
		Rerank:       true,
		Limit:        limit,
		Filters:      filters,
	})
	if err != nil {
		idx.logger.Warn("searching patterns filtered", "error", err)
	}

	var legacyResp *SearchResponse
	if owner != "" {
		legacyReq := SearchRequest{
			Query:        query,
			ContainerTag: RepoTag(owner, repo, "patterns"),
			SearchMode:   "hybrid",
			Rerank:       true,
			Limit:        limit,
		}
		if category != "" {
			legacyReq.Filters = &SearchFilters{AND: []FilterCondition{{Key: "category", Value: category}}}
		}
		lResp, lerr := idx.client.Search(ctx, legacyReq)
		if lerr != nil {
			idx.logger.Debug("legacy patterns search failed", "error", lerr, "tag", legacyReq.ContainerTag)
		}
		legacyResp = lResp
	}

	return contentsFromResponses(newResp, legacyResp, limit)
}

// contentsFromResponses merges results from new + legacy containers, dedupes
// by Supermemory doc ID AND normalized content, sorts by similarity descending,
// then caps at limit and returns content strings. Sorting matters during the
// dual-read window: if we truncated in insertion order instead, a high-scoring
// legacy hit would be dropped in favor of a low-scoring new-container hit once
// the merged set exceeded `limit` — silently degrading retrieval for any
// installation not yet migrated. Mirrors mergeScenarioResults.
//
// Content dedup (not just doc ID) is required during the dual-read window: the
// same logical doc re-indexed into the new container while its legacy copy
// still exists has a DIFFERENT Supermemory doc ID (and a different customID —
// the legacy owner segment was dropped), so ID-only dedup would surface it
// twice and push a distinct hit out of the capped result. The search API does
// not return customID, so normalized content is the available join key.
func contentsFromResponses(newResp, legacyResp *SearchResponse, limit int) []string {
	type scored struct {
		content    string
		similarity float64
	}
	seen := map[string]struct{}{}
	seenContent := map[string]struct{}{}
	var merged []scored
	consume := func(resp *SearchResponse) {
		if resp == nil {
			return
		}
		for _, r := range resp.Results {
			if _, dup := seen[r.ID]; dup {
				continue
			}
			seen[r.ID] = struct{}{}
			content := r.Content()
			key := normalizeBody(content)
			if _, dup := seenContent[key]; dup {
				continue
			}
			seenContent[key] = struct{}{}
			merged = append(merged, scored{content: content, similarity: r.Similarity})
		}
	}
	consume(newResp)
	consume(legacyResp)
	sort.SliceStable(merged, func(i, j int) bool {
		return merged[i].similarity > merged[j].similarity
	})
	if limit > 0 && len(merged) > limit {
		merged = merged[:limit]
	}
	out := make([]string, len(merged))
	for i, m := range merged {
		out[i] = m.content
	}
	return out
}

// SpecialistBlock fetches (1) file-scoped synthesis via exact metadata lookup,
// (2) repo-scoped semantic hits across patterns/scenarios/feedback, (3)
// shared semantic hits over patterns. Replaces the legacy 5-parallel-query
// block — cuts 5×N searches per PR to 2×N plus one list call.
func (idx *indexerImpl) SpecialistBlock(ctx context.Context, owner, repo, filePath, specialistQuery string, thresholds Thresholds) MemoryBlock {
	if idx.client == nil || repo == "" {
		return MemoryBlock{}
	}
	thresholds = thresholds.WithDefaults()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var block MemoryBlock
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
		resp, err := idx.client.Search(ctx, SearchRequest{
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
		})
		if err != nil {
			idx.logger.Debug("specialist synthesis search failed", "error", err, "repo", repo, "file", filePath)
			return
		}
		if resp == nil || len(resp.Results) == 0 {
			return
		}
		block.Synthesis = resp.Results[0].Content()
	}()

	// 2. Repo signal — semantic, type IN {pattern, scenario, feedback}.
	go func() {
		defer wg.Done()
		block.Repo = idx.searchMatches(ctx, SearchRequest{
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
		})
	}()

	// 3. Shared patterns — semantic against `_shared`. The AND filter excludes
	// already-fading docs (confidence < SharedConfidenceFloor) so decayed
	// patterns stop influencing reviews before the reconciler deletes them.
	// numeric compare required: FilterNumeric ensures supermemory interprets
	// the threshold as a float, not a lexicographic string.
	go func() {
		defer wg.Done()
		block.Shared = idx.searchMatches(ctx, SearchRequest{
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
		})
	}()

	wg.Wait()

	// Legacy dual-read fallback — removed in the post-migration cleanup PR
	// alongside SearchPatternMatch's legacy branch. Fires ONLY when a new-shape
	// leg came back empty and we still know the owner: pre-migration data lives
	// in the owner-prefixed `{owner}--{repo}--patterns` / `{owner}--patterns`
	// containers, which are empty for freshly-migrated installs but hold months
	// of history for existing ones. A fully-migrated install pays nothing (the
	// new legs are non-empty). Mirrors SearchPatternMatch's machinery so cleanup
	// strips both at once.
	if owner != "" {
		idx.specialistLegacyFallback(ctx, owner, repo, filePath, specialistQuery, thresholds, &block)
	}
	return block
}

// specialistLegacyFallback backfills empty MemoryBlock legs from the
// pre-migration owner-prefixed containers. Dual-read-window code: delete with
// the other legacy branches in the cleanup PR. Each leg runs only when its
// new-shape counterpart returned nothing, and each writes a distinct MemoryBlock
// field, preserving the write-partition invariant documented on MemoryBlock.
func (idx *indexerImpl) specialistLegacyFallback(ctx context.Context, owner, repo, filePath, specialistQuery string, thresholds Thresholds, block *MemoryBlock) {
	legacyRepoTag := RepoTag(owner, repo, "patterns")
	var wg sync.WaitGroup

	// Synthesis — legacy synthesis docs were written to `{owner}--{repo}--patterns`
	// with source=synthesis and the file path under the `file` metadata key
	// (see origin/main synthesizeFileMemories). Exact-match both to pin this
	// file's synthesis.
	if block.Synthesis == "" && filePath != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := idx.client.Search(ctx, SearchRequest{
				Query:        "file synthesis " + filePath,
				ContainerTag: legacyRepoTag,
				SearchMode:   "hybrid",
				Limit:        1,
				Threshold:    0,
				Rerank:       false,
				Filters: &SearchFilters{AND: []FilterCondition{
					{Key: "source", Value: "synthesis"},
					{Key: "file", Value: filePath},
				}},
			})
			if err == nil && resp != nil && len(resp.Results) > 0 {
				block.Synthesis = resp.Results[0].Content()
			}
		}()
	}

	// Repo signal — legacy repo patterns / feedback lived in
	// `{owner}--{repo}--patterns` mixed with non-pattern writes (synthesis,
	// pr_summary, feedback), so post-filter by metadata.source.
	if len(block.Repo) == 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			block.Repo = idx.matchesExcludingSources(ctx, specialistQuery, legacyRepoTag, legacyNonPatternSources, thresholds.SpecialistMin, 5)
		}()
	}

	// Shared / org signal — legacy org-wide patterns lived in the
	// `{owner}--patterns` container (SharedTag's pre-migration equivalent).
	if len(block.Shared) == 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			block.Shared = idx.matchesExcludingSources(ctx, specialistQuery, OwnerTag(owner, "patterns"), legacyNonPatternSources, thresholds.SpecialistMin, 3)
		}()
	}

	wg.Wait()
}

// matchesExcludingSources fetches hybrid results from a legacy container, drops
// any whose metadata.source is a known non-pattern write, and returns up to
// `limit` PatternMatch. List-returning sibling of topMatchExcludingSources;
// dual-read-window helper removed in the cleanup PR.
func (idx *indexerImpl) matchesExcludingSources(ctx context.Context, query, containerTag string, excluded map[string]struct{}, threshold float64, limit int) []PatternMatch {
	resp, err := idx.client.Search(ctx, SearchRequest{
		Query:        query,
		ContainerTag: containerTag,
		SearchMode:   "hybrid",
		Limit:        limit + len(excluded), // headroom so filtering doesn't starve the cap
		Threshold:    threshold,
		Rerank:       true,
	})
	if err != nil || resp == nil {
		return nil
	}
	out := make([]PatternMatch, 0, limit)
	for _, r := range resp.Results {
		pm := resultToPatternMatch(r)
		if _, skip := excluded[pm.Metadata["source"]]; skip {
			continue
		}
		out = append(out, pm)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// searchMatches runs a search and translates the result list into
// []PatternMatch with metadata already unmarshaled. Returns nil on error.
func (idx *indexerImpl) searchMatches(ctx context.Context, req SearchRequest) []PatternMatch {
	resp, err := idx.client.Search(ctx, req)
	if err != nil || resp == nil {
		return nil
	}
	out := make([]PatternMatch, 0, len(resp.Results))
	for _, r := range resp.Results {
		out = append(out, resultToPatternMatch(r))
	}
	return out
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
