package memory

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/BeLazy167/argus/backend/internal/util"
	"github.com/google/uuid"
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
	// Writers.
	IndexReviewCommentsBatch(ctx context.Context, owner, repo string, comments []ReviewMemory) error
	IndexRule(ctx context.Context, owner string, rule RuleMemory) error
	IndexPattern(ctx context.Context, repo string, pattern PatternMemory) (*IndexResult, error)
	IndexSharedPattern(ctx context.Context, pattern PatternMemory) (*IndexResult, error)
	IndexFeedbackSignal(ctx context.Context, owner, repo string, feedback FeedbackMemory) error
	IndexScenario(ctx context.Context, owner, repo string, scenarioID int64, description, severity string, files []string) error

	// ForReview returns an Indexer that attributes everything it writes to one
	// review run (memories.review_id, migration 070). It is what lets a
	// finished review say what it learned; without it a memory row is
	// identifiable only down to the pull request, which every re-review of
	// that PR overwrites. Returns the receiver unchanged for the nil UUID.
	// Attribution is a property of the WRITER, not of each document, because
	// one run's writes all belong to the same review.
	ForReview(reviewID uuid.UUID) Indexer

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

const (
	// SourceLegacyReplyFeedback identifies reply learnings written before the
	// author authorization boundary existed. Readers quarantine this source
	// non-destructively because those rows carry no provenance to audit trust.
	SourceLegacyReplyFeedback = "reply_feedback"
	// SourceTrustedReplyFeedback identifies reply learnings whose author was
	// authorized before the write.
	SourceTrustedReplyFeedback = "trusted_reply_feedback"
)

// IndexResult identifies the row a write landed on. ID is the deterministic
// customID: in the Postgres store the document id and the customID are the
// same value by construction, so a caller can mirror it into patterns.
// memory_doc_id and later resolve a search hit straight back to its row.
type IndexResult struct {
	ID string
}

// PatternMatch is one search hit — content, similarity, memory document
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

// The customID builders below stay EXPORTED deliberately: the deterministic
// customId is a cross-package contract, not a test affordance. The pipeline
// computes it to mirror memory_doc_id into the patterns/scenarios tables and to
// resolve a search hit back to its row; cmd/migrate-memory and
// cmd/reconcile-memory reconstruct the SAME id to back-fill / sync legacy docs;
// internal/api derives it to delete a rule's doc. Un-exporting any of these
// would break those callers, so their ID invariants are pinned by write→read
// round-trip tests through the Indexer (see dismissal_id_test.go) rather than by
// keeping a builder package-visible for a unit test. Builders with NO
// cross-package caller (dismissalCustomID) are unexported.

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

// RuleCustomID returns a stable customId for a rule identified by its DB id.
func RuleCustomID(ruleID int64) string {
	return fmt.Sprintf("rule--%d", ruleID)
}

// dismissalCustomID returns a stable customId for a DISMISSED feedback signal.
// Keyed by category + semantic content only — deliberately file-path-free so
// the same dismissed finding recurring across files/PRs upserts one doc whose
// recurrence (not row count) is the suppression signal. Unexported: the only
// writer is IndexFeedbackSignal below; its invariants are round-tripped in
// dismissal_id_test.go through that write path.
func dismissalCustomID(repo, category, body string) string {
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

// ReviewMemory represents a review comment to be stored in memory.
type ReviewMemory struct {
	ReviewID string
	PRNumber int
	FilePath string
	Body     string
	Severity string
	Category string
}

// RuleMemory represents a rule to be stored in memory.
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

// buildReviewContent keeps content pure-prose: the finding body only. No
// File:/Severity:/Category: prefix headers (those are metadata now) and no
// raw-diff Context suffix — retrieval matches on prose, and the diff only
// bloated the document.
func buildReviewContent(c ReviewMemory) string {
	return c.Body
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

// specialistBlockWith is the shared specialist-block orchestration over a
// transport core: three concurrent legs whose REQUESTS are identical across
// backends by construction (same filters, floors, limits), with the shared
// keep-what-succeeded degradation policy. The legs fetch (1) file-scoped
// synthesis via exact metadata lookup, (2) repo-scoped semantic hits across
// patterns/scenarios/feedback, (3) shared semantic hits over patterns —
// replacing the legacy 5-parallel-query block (5xN searches per PR down to
// 2xN plus one list call). Owns its own 5s timeout; consumed only by
// assembleBriefingWith. The repo (OR-of-types) and shared (numeric
// confidence) legs keep bespoke SearchRequests the single-type MemoryQuery
// does not model.
func specialistBlockWith(ctx context.Context, run runSearchFn, logger *slog.Logger, repo, filePath, specialistQuery string, thresholds Thresholds) (MemoryBlock, error) {
	if repo == "" {
		// Every leg is repo-scoped. briefingWith already guards this for the
		// briefing path; keeping it here too means a direct caller cannot
		// issue three searches against RepoTagNew("") and then read the
		// all-legs-failed error as "no institutional memory available".
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
		matches, synthErr = run(ctx, SearchRequest{
			Query:        "file synthesis",
			ContainerTag: RepoTagNew(repo),
			Limit:        1,
			Threshold:    0,    // accept any hit — the metadata filter already pins it.
			PointLookup:  true, // (type, file_path) pins at most one synthesis doc
			Filters: &SearchFilters{AND: []FilterCondition{
				{Key: "type", Value: string(TypeSynthesis)},
				{Key: "file_path", Value: filePath},
			}},
		})
		if len(matches) > 0 {
			block.Synthesis = matches[0].Content
		}
	}()

	// 2. Repo signal — semantic, type IN {pattern, scenario, feedback}.
	go func() {
		defer wg.Done()
		block.Repo, repoErr = run(ctx, SearchRequest{
			Query:        specialistQuery,
			ContainerTag: RepoTagNew(repo),
			Limit:        5,
			Threshold:    thresholds.SpecialistMin,
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
	// numeric compare required: FilterNumeric ensures the reader interprets
	// the threshold as a float, not a lexicographic string.
	go func() {
		defer wg.Done()
		block.Shared, sharedErr = run(ctx, SearchRequest{
			Query:        specialistQuery,
			ContainerTag: SharedTag,
			Limit:        3,
			Threshold:    thresholds.SpecialistMin,
			Filters: &SearchFilters{AND: []FilterCondition{
				{Key: "type", Value: string(TypePattern)},
				FilterNumeric("confidence", ">=", SharedConfidenceFloorStr),
			}},
		})
	}()

	wg.Wait()
	block.Repo = retrievableMatches(block.Repo)
	block.Shared = retrievableMatches(block.Shared)
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
			logDegrade(logger, l.name, RepoTagNew(repo), 0, l.err)
		}
	}
	if failures == 3 {
		return MemoryBlock{}, cmp.Or(synthErr, repoErr, sharedErr)
	}
	return block, nil
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
