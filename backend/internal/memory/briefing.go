package memory

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

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

// Briefing assembles the institutional-memory block for a review prompt and
// renders it to markdown, returning the string the caller embeds verbatim. It
// owns the whole retrieval → dispatch → truncation → render path: query is the
// semantic query for the repo/shared/past-review reads; opts.Profile selects
// the render shape and which side-searches run. Returns "" on nil client or
// empty repo. Each underlying read owns its own 5s timeout + non-fatal Warn.
func (idx *indexerImpl) Briefing(ctx context.Context, owner, repo, filePath, query string, opts BriefingOptions) string {
	if idx.client == nil || repo == "" {
		return ""
	}
	b := idx.assembleBriefing(ctx, owner, repo, filePath, query, opts)
	if opts.Profile == ProfileReview {
		return b.renderReview(opts.CharCap)
	}
	return b.renderSpecialist(filePath, opts.CharCap, opts.EmphasizeFalsePositives)
}

// assembleBriefing runs the typed reads and dispatches results into sections.
// The specialistBlock legs (synthesis + repo + shared) serve both profiles; the
// review profile adds the rules + past-review side-searches specialists skip.
// All per-item content is truncated to 500 chars here so the render stays pure.
func (idx *indexerImpl) assembleBriefing(ctx context.Context, owner, repo, filePath, query string, opts BriefingOptions) Briefing {
	if opts.Profile != ProfileReview {
		// Specialist profile has no side-searches — one specialistBlock (own 5s).
		return briefingSections(idx.specialistBlock(ctx, owner, repo, filePath, query, opts.Thresholds))
	}

	// Review profile: the three legs — specialistBlock (synthesis/repo/shared),
	// the rules side-search, and the past-review side-search — are mutually
	// independent, so run ALL THREE concurrently. Each owns its own 5s timeout,
	// so the worst-case ceiling is ~5s, not specialistBlock(~5s) THEN
	// side-searches(~5s) in series (~10s). Both side-searches are non-fatal: a
	// failure or empty result just omits the respective section.
	//
	// Write-partitioned: each goroutine writes a distinct variable; wg.Wait() is
	// the happens-before edge before they are read.
	var block MemoryBlock
	var rules, pastReviews []string
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		block = idx.specialistBlock(ctx, owner, repo, filePath, query, opts.Thresholds)
	}()
	go func() {
		defer wg.Done()
		rules = idx.SearchHints(ctx, "review rules conventions", SharedTag, 3, TypeRule)
	}()
	go func() {
		defer wg.Done()
		pastReviews = idx.SearchHints(ctx, query, RepoTagNew(repo), 2, TypeReview)
	}()
	wg.Wait()

	b := briefingSections(block)
	b.Rules = rules
	b.PastReviews = pastReviews
	return b
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

// SearchHints runs a rerank + related/summary-enriched hybrid search pinned to a
// metadata type and returns the RichContent(2) of each hit truncated to 500
// chars. Non-fatal: returns nil on nil client or any search error. Owns its own
// 5s timeout. typ="" leaves the search untyped. This is the workhorse read for
// triage/scoring hints and the review-profile rules/past-review side-searches.
func (idx *indexerImpl) SearchHints(ctx context.Context, query, containerTag string, limit int, typ MemoryType) []string {
	if idx.client == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req := SearchRequest{
		Query:        query,
		ContainerTag: containerTag,
		SearchMode:   "hybrid",
		Limit:        limit,
		Threshold:    0.5,
		Rerank:       true,
		Include: &SearchInclude{
			RelatedMemories: true,
			Summaries:       true,
		},
	}
	if typ != "" {
		req.Filters = &SearchFilters{AND: []FilterCondition{{Key: "type", Value: string(typ)}}}
	}
	resp, err := idx.client.Search(ctx, req)
	if err != nil {
		if ctx.Err() == nil {
			idx.logger.Warn("memory search failed", "error", err, "tag", containerTag)
		}
		return nil
	}
	results := make([]string, 0, len(resp.Results))
	for _, r := range resp.Results {
		content := r.RichContent(2)
		if content != "" {
			results = append(results, util.Truncate(content, 500, true))
		}
	}
	return results
}

// SearchRuleContent returns the top org-rule (_shared, type=rule) semantically
// matching query, truncated to 300 chars, or "" on nil client / no hit / error.
// Owns its own 5s timeout. Distinct from the review-profile rules leg
// (SearchHints): this is a single un-reranked lookup used at finding enrichment.
func (idx *indexerImpl) SearchRuleContent(ctx context.Context, query string) string {
	if idx.client == nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	resp, err := idx.client.Search(ctx, SearchRequest{
		Query:        query,
		ContainerTag: SharedTag,
		SearchMode:   "hybrid",
		Limit:        1,
		Threshold:    0.5,
		Filters:      &SearchFilters{AND: []FilterCondition{{Key: "type", Value: string(TypeRule)}}},
	})
	if err != nil {
		if ctx.Err() == nil {
			idx.logger.Warn("memory search failed", "error", err, "tag", SharedTag)
		}
		return ""
	}
	for _, r := range resp.Results {
		if c := r.Content(); c != "" {
			return util.Truncate(c, 300, true)
		}
	}
	return ""
}

// SearchScored runs a plain hybrid search (no rerank, no includes) and returns
// the raw score + content of each hit. Powers the agentic search_memory tool,
// which needs per-result scores and the untruncated body, and must distinguish a
// search error (surfaced to the LLM) from an empty result set — hence the error
// return. Returns (nil, nil) on nil client. typ="" leaves the search untyped.
func (idx *indexerImpl) SearchScored(ctx context.Context, query, containerTag string, typ MemoryType, limit int) ([]PatternMatch, error) {
	if idx.client == nil {
		return nil, nil
	}
	req := SearchRequest{
		Query:        query,
		ContainerTag: containerTag,
		SearchMode:   "hybrid",
		Limit:        limit,
		Threshold:    0.5,
	}
	if typ != "" {
		req.Filters = &SearchFilters{AND: []FilterCondition{{Key: "type", Value: string(typ)}}}
	}
	resp, err := idx.client.Search(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, nil
	}
	out := make([]PatternMatch, 0, len(resp.Results))
	for _, r := range resp.Results {
		out = append(out, resultToPatternMatch(r))
	}
	return out, nil
}
