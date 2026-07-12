package memory

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/BeLazy167/argus/backend/internal/util"
)

// BriefingProfile selects which prompt block a Briefing renders. The two
// profiles differ in section set, headers, footer, and character cap — the
// deep-review specialist block vs the single-pass reviewer block.
type BriefingProfile int

const (
	// ProfileSpecialist renders the deep-review specialist block: synthesis,
	// combined repo+shared patterns, false positives, approved patterns.
	ProfileSpecialist BriefingProfile = iota
	// ProfileReview renders the single-pass reviewer block, which additionally
	// pulls org rules + past-review context that specialists intentionally skip.
	ProfileReview
)

// BriefingOptions tunes a Briefing assembly + render. CharCap is per-call-site
// (specialist 2400, review 3200) so each prompt keeps the exact byte budget it
// had before this seam moved into the module. EmphasizeFalsePositives splits
// dismissed-finding feedback into its own call-out (specialist path).
type BriefingOptions struct {
	Profile                 BriefingProfile
	Thresholds              Thresholds
	CharCap                 int
	EmphasizeFalsePositives bool
}

// Briefing is the typed, per-section memory block assembled for a review
// prompt. Every section carries already-truncated prose (each item ≤500 chars);
// the render methods add headers, numbering, the trailing instruction, and the
// per-profile character cap. Splitting retrieval (assembleBriefing) from
// rendering keeps the byte-sensitive markdown pure and unit-testable.
type Briefing struct {
	// Synthesis is the file-scoped review-history prose (≤500 chars). Empty if
	// no synthesis doc matched.
	Synthesis string
	// Patterns is repo-scoped patterns/scenarios (non-feedback) followed by
	// shared org patterns, in that order, each ≤500 chars.
	Patterns []string
	// FalsePositives is type=feedback polarity=negative content (dismissals).
	FalsePositives []string
	// Approved is type=feedback polarity=positive content (confirmations).
	Approved []string
	// Rules is org-wide review rules (ProfileReview only).
	Rules []string
	// PastReviews is prior review findings on this repo (ProfileReview only).
	PastReviews []string
}

// BriefingQuery bundles the inputs for a Briefing assembly + render: the repo
// coordinates, the file under review, the semantic query driving the
// repo/shared/past-review reads, and the profile/threshold/cap options.
type BriefingQuery struct {
	Owner    string
	Repo     string
	FilePath string
	Query    string
	Options  BriefingOptions
}

// Briefing assembles the institutional-memory block for a review prompt and
// renders it to markdown, returning the string the caller embeds verbatim. It
// owns the whole retrieval → dispatch → truncation → render path: q.Query is the
// semantic query for the repo/shared/past-review reads; q.Options.Profile
// selects the render shape and which side-searches run. Returns ("", nil) on nil
// client or empty repo, and ("", err) when any underlying retrieval failed so a
// caller can degrade the block (via BestEffort) instead of embedding a silently
// partial one. Each underlying read owns its own 5s timeout.
func (idx *indexerImpl) Briefing(ctx context.Context, q BriefingQuery) (string, error) {
	if idx.client == nil || q.Repo == "" {
		return "", nil
	}
	b, err := idx.assembleBriefing(ctx, q)
	if err != nil {
		return "", err
	}
	if q.Options.Profile == ProfileReview {
		return b.renderReview(q.Options.CharCap), nil
	}
	return b.renderSpecialist(q.FilePath, q.Options.CharCap, q.Options.EmphasizeFalsePositives), nil
}

// assembleBriefing runs the typed reads and dispatches results into sections.
// The specialistBlock legs (synthesis + repo + shared) serve both profiles; the
// review profile adds the rules + past-review side-searches specialists skip.
// All per-item content is truncated to 500 chars here so the render stays pure.
// Any leg error is returned so Briefing degrades the whole block rather than
// serving a partial one.
func (idx *indexerImpl) assembleBriefing(ctx context.Context, q BriefingQuery) (Briefing, error) {
	if q.Options.Profile != ProfileReview {
		// Specialist profile has no side-searches — one specialistBlock (own 5s).
		block, err := idx.specialistBlock(ctx, q.Repo, q.FilePath, q.Query, q.Options.Thresholds)
		if err != nil {
			return Briefing{}, err
		}
		return briefingSections(block), nil
	}

	// Review profile: the three legs — specialistBlock (synthesis/repo/shared),
	// the rules side-search, and the past-review side-search — are mutually
	// independent, so run ALL THREE concurrently. Each owns its own 5s timeout,
	// so the worst-case ceiling is ~5s, not specialistBlock(~5s) THEN
	// side-searches(~5s) in series (~10s).
	//
	// Write-partitioned: each goroutine writes a distinct variable; wg.Wait() is
	// the happens-before edge before they are read.
	var block MemoryBlock
	var rules, pastReviews []string
	var blockErr, rulesErr, pastErr error
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		block, blockErr = idx.specialistBlock(ctx, q.Repo, q.FilePath, q.Query, q.Options.Thresholds)
	}()
	go func() {
		defer wg.Done()
		var m []PatternMatch
		m, rulesErr = idx.Search(ctx, MemoryQuery{
			Query: "review rules conventions", Scope: ScopeShared, Type: TypeRule,
			Limit: 3, Threshold: 0.5, Rerank: true, Enrich: true,
		})
		rules = HintStrings(m)
	}()
	go func() {
		defer wg.Done()
		var m []PatternMatch
		m, pastErr = idx.Search(ctx, MemoryQuery{
			Query: q.Query, Repo: q.Repo, Scope: ScopeRepo, Type: TypeReview,
			Limit: 2, Threshold: 0.5, Rerank: true, Enrich: true,
		})
		pastReviews = HintStrings(m)
	}()
	wg.Wait()
	// Per-leg degradation: the specialist block (file history + patterns) is
	// the CORE — if it failed there is nothing usable and the error propagates.
	// The rules and past-review side-searches are OPTIONAL: a failed leg is
	// Warn-logged and its section omitted, so a transient single-leg error
	// never blanks the whole briefing (the #147 gate's resilience finding).
	if blockErr != nil {
		return Briefing{}, blockErr
	}
	if rulesErr != nil {
		idx.warnLeg("briefing.rules", SharedTag, len(q.Query), rulesErr)
		rules = nil
	}
	if pastErr != nil {
		idx.warnLeg("briefing.past_reviews", RepoTagNew(q.Repo), len(q.Query), pastErr)
		pastReviews = nil
	}

	b := briefingSections(block)
	b.Rules = rules
	b.PastReviews = pastReviews
	return b, nil
}

// warnLeg is the per-leg sibling of BestEffort: same "memory read degraded"
// Warn shape, used where a multi-leg assembly keeps its successful legs
// instead of zeroing the whole result.
func (idx *indexerImpl) warnLeg(caller, container string, queryLen int, err error) {
	if idx.logger != nil {
		idx.logger.Warn("memory read degraded",
			"caller", caller, "container", container, "query_len", queryLen, "error", err)
	}
}

// briefingSections splits a MemoryBlock into the typed prose sections shared by
// both render profiles, truncating each item to 500 chars. A type=feedback doc
// routes to FalsePositives (polarity=negative) or Approved (polarity=positive);
// everything else — repo patterns/scenarios then shared org patterns, in that
// order — lands in Patterns. Pure (no client), so the parity tests can drive it.
func briefingSections(block MemoryBlock) Briefing {
	var b Briefing
	if block.Synthesis != "" {
		b.Synthesis = util.Truncate(block.Synthesis, 500, true)
	}
	for _, m := range block.Repo {
		content := util.Truncate(m.Content, 500, true)
		if m.Metadata["type"] == string(TypeFeedback) {
			switch Polarity(m.Metadata["polarity"]) {
			case PolarityNegative:
				b.FalsePositives = append(b.FalsePositives, content)
				continue
			case PolarityPositive:
				b.Approved = append(b.Approved, content)
				continue
			}
		}
		b.Patterns = append(b.Patterns, content)
	}
	for _, m := range block.Shared {
		b.Patterns = append(b.Patterns, util.Truncate(m.Content, 500, true))
	}
	return b
}

// renderSpecialist renders the deep-review specialist block. charCap bounds the
// body BEFORE the trailing instruction is appended (so the final string may
// exceed charCap by the footer length) — preserving the pre-seam behavior.
func (b Briefing) renderSpecialist(filePath string, charCap int, emphasizeFalsePositives bool) string {
	var sb strings.Builder

	if b.Synthesis != "" {
		sb.WriteString("\n\n## Memory Briefing: " + filePath + "\n\n")
		sb.WriteString("### File History\n")
		sb.WriteString(b.Synthesis + "\n")
	}

	if len(b.Patterns) > 0 {
		if b.Synthesis == "" {
			sb.WriteString("\n\n## Repo Memory (patterns from past reviews)\n\n")
		} else {
			sb.WriteString("\n### Repo Patterns\n")
		}
		for i, m := range b.Patterns {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, m))
		}
	}

	if emphasizeFalsePositives && len(b.FalsePositives) > 0 {
		sb.WriteString("\n## Known False Positives (DO NOT re-flag these patterns)\n")
		for i, m := range b.FalsePositives {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, m))
		}
	}

	if len(b.Approved) > 0 {
		sb.WriteString("\n## Approved Patterns (do not flag code following these)\n")
		for i, m := range b.Approved {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, m))
		}
	}

	if sb.Len() == 0 {
		return ""
	}
	footer := "\nUse this context to inform your review — issues matching known patterns are higher priority.\nWhen a finding matches a known pattern above, add a tag at the end of your comment: *[Matches pattern: <pattern description>]*. Only tag when there is a clear match — do not fabricate references."

	result := sb.String()
	if len(result) > charCap {
		result = util.Truncate(result, charCap, true)
	}
	return result + footer
}

// renderReview renders the single-pass reviewer block. charCap bounds the whole
// string INCLUDING the trailing instruction — matching the pre-seam behavior,
// which differs subtly from the specialist path (cap-before-footer).
func (b Briefing) renderReview(charCap int) string {
	var sb strings.Builder

	if b.Synthesis != "" {
		sb.WriteString("\n\n## File History\n")
		sb.WriteString("- " + b.Synthesis + "\n")
	}

	if len(b.Rules) > 0 {
		sb.WriteString("\n## Review Rules\n")
		for i, r := range b.Rules {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, r))
		}
	}

	if len(b.Patterns) > 0 {
		sb.WriteString("\n## Established Patterns\n")
		for i, r := range b.Patterns {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, r))
		}
	}

	if len(b.PastReviews) > 0 {
		sb.WriteString("\n## Past Review Findings (avoid re-raising the same issue)\n")
		for i, r := range b.PastReviews {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, r))
		}
	}

	if blk := numberedBlock("\n## Known False Positives (DO NOT re-flag these patterns)\n", b.FalsePositives); blk != "" {
		sb.WriteString(blk)
	}

	if blk := numberedBlock("\n## Approved Patterns (do not flag code following these)\n", b.Approved); blk != "" {
		sb.WriteString(blk)
	}

	if sb.Len() == 0 {
		return ""
	}
	sb.WriteString("\nApply these patterns and past findings when reviewing. When a finding matches a known pattern above, add a tag at the end of your comment: *[Matches pattern: <pattern description>]*. Only tag when there is a clear match — do not fabricate references.\n")

	result := sb.String()
	if len(result) > charCap {
		result = util.Truncate(result, charCap, true)
	}
	return result
}

// numberedBlock renders header + a 1-indexed numbered list of items, or "" when
// items is empty. Mirrors the pipeline's formatMemoryBlock with an empty footer.
func numberedBlock(header string, items []string) string {
	if len(items) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(header)
	for i, r := range items {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, r))
	}
	return sb.String()
}
