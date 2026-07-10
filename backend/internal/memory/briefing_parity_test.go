package memory

import (
	"fmt"
	"strings"
	"testing"

	"github.com/BeLazy167/argus/backend/internal/util"
)

// This file locks BYTE-FOR-BYTE parity between the new Briefing render path
// (briefingSections + renderSpecialist / renderReview) and the pre-refactor
// pipeline functions formatSpecialistBlock / reviewMemoryBlock, whose exact
// string-building logic is frozen below as reference implementations. If a
// future edit changes a header, footer, ordering, truncation cap, or the
// cap-vs-footer boundary, one of these assertions fails.
//
// The references are verbatim copies of the pre-seam pipeline code (git blame:
// internal/pipeline/specialists.go before refactor/memory-briefing-module).

// referenceFormatSpecialistBlock is the frozen pre-seam formatSpecialistBlock.
func referenceFormatSpecialistBlock(block MemoryBlock, filePath string, emphasizeFalsePositives bool) string {
	var sb strings.Builder

	if block.Synthesis != "" {
		sb.WriteString("\n\n## Memory Briefing: " + filePath + "\n\n")
		sb.WriteString("### File History\n")
		sb.WriteString(util.Truncate(block.Synthesis, 500, true) + "\n")
	}

	var patterns, negatives, positives []PatternMatch
	for _, m := range block.Repo {
		mt := m.Metadata["type"]
		if mt == string(TypeFeedback) {
			switch Polarity(m.Metadata["polarity"]) {
			case PolarityNegative:
				negatives = append(negatives, m)
				continue
			case PolarityPositive:
				positives = append(positives, m)
				continue
			}
		}
		patterns = append(patterns, m)
	}
	patterns = append(patterns, block.Shared...)

	if len(patterns) > 0 {
		if block.Synthesis == "" {
			sb.WriteString("\n\n## Repo Memory (patterns from past reviews)\n\n")
		} else {
			sb.WriteString("\n### Repo Patterns\n")
		}
		for i, m := range patterns {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, util.Truncate(m.Content, 500, true)))
		}
	}

	if emphasizeFalsePositives && len(negatives) > 0 {
		sb.WriteString("\n## Known False Positives (DO NOT re-flag these patterns)\n")
		for i, m := range negatives {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, util.Truncate(m.Content, 500, true)))
		}
	}

	if len(positives) > 0 {
		sb.WriteString("\n## Approved Patterns (do not flag code following these)\n")
		for i, m := range positives {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, util.Truncate(m.Content, 500, true)))
		}
	}

	if sb.Len() == 0 {
		return ""
	}
	footer := "\nUse this context to inform your review — issues matching known patterns are higher priority.\nWhen a finding matches a known pattern above, add a tag at the end of your comment: *[Matches pattern: <pattern description>]*. Only tag when there is a clear match — do not fabricate references."

	result := sb.String()
	if len(result) > 2400 {
		result = util.Truncate(result, 2400, true)
	}
	return result + footer
}

// referenceFormatMemoryBlock is the frozen pre-seam formatMemoryBlock.
func referenceFormatMemoryBlock(header, footer string, results []string) string {
	if len(results) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(header)
	for i, r := range results {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, r))
	}
	sb.WriteString(footer)
	return sb.String()
}

// referenceReviewRender is the frozen pre-seam reviewMemoryBlock render, split
// out from its (now module-internal) side-searches: rules + pastReviews are
// passed in exactly as searchMemoryRichTyped would have returned them.
func referenceReviewRender(block MemoryBlock, rules, pastReviews []string) string {
	var synthResults []string
	if block.Synthesis != "" {
		synthResults = []string{util.Truncate(block.Synthesis, 500, true)}
	}
	var repoPatterns, orgPatterns, negResults, posResults []string
	for _, m := range block.Repo {
		mt := m.Metadata["type"]
		content := util.Truncate(m.Content, 500, true)
		if mt == string(TypeFeedback) {
			switch Polarity(m.Metadata["polarity"]) {
			case PolarityNegative:
				negResults = append(negResults, content)
				continue
			case PolarityPositive:
				posResults = append(posResults, content)
				continue
			}
		}
		repoPatterns = append(repoPatterns, content)
	}
	for _, m := range block.Shared {
		orgPatterns = append(orgPatterns, util.Truncate(m.Content, 500, true))
	}

	var sb strings.Builder

	if len(synthResults) > 0 {
		sb.WriteString("\n\n## File History\n")
		for _, r := range synthResults {
			sb.WriteString("- " + r + "\n")
		}
	}

	if len(rules) > 0 {
		sb.WriteString("\n## Review Rules\n")
		for i, r := range rules {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, r))
		}
	}

	allPatterns := append(repoPatterns, orgPatterns...)
	if len(allPatterns) > 0 {
		sb.WriteString("\n## Established Patterns\n")
		for i, r := range allPatterns {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, r))
		}
	}

	if len(pastReviews) > 0 {
		sb.WriteString("\n## Past Review Findings (avoid re-raising the same issue)\n")
		for i, r := range pastReviews {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, r))
		}
	}

	if blk := referenceFormatMemoryBlock("\n## Known False Positives (DO NOT re-flag these patterns)\n", "", negResults); blk != "" {
		sb.WriteString(blk)
	}

	if blk := referenceFormatMemoryBlock("\n## Approved Patterns (do not flag code following these)\n", "", posResults); blk != "" {
		sb.WriteString(blk)
	}

	if sb.Len() == 0 {
		return ""
	}
	sb.WriteString("\nApply these patterns and past findings when reviewing. When a finding matches a known pattern above, add a tag at the end of your comment: *[Matches pattern: <pattern description>]*. Only tag when there is a clear match — do not fabricate references.\n")

	result := sb.String()
	if len(result) > 3200 {
		result = util.Truncate(result, 3200, true)
	}
	return result
}

func fb(content string, polarity Polarity) PatternMatch {
	return PatternMatch{Content: content, Metadata: map[string]string{"type": string(TypeFeedback), "polarity": string(polarity)}}
}

func pat(content string) PatternMatch {
	return PatternMatch{Content: content, Metadata: map[string]string{"type": string(TypePattern)}}
}

// briefingParityCases spans the render's decision points: empty, synthesis-only,
// patterns-with/without-synthesis (header switch), feedback polarity routing,
// unknown polarity fall-through, shared patterns, over-cap truncation (both the
// 2400 specialist cap-before-footer and the 3200 review cap-including-footer),
// and multibyte truncation.
func briefingParityCases() map[string]struct {
	block             MemoryBlock
	rules, pastReview []string
} {
	long := strings.Repeat("alpha beta gamma delta ", 40)   // ~920 chars, forces per-item 500 trunc
	huge := strings.Repeat("lorem ipsum dolor sit amet ", 300) // forces the block-level cap
	multibyte := strings.Repeat("héllo wörld café ", 40)       // multibyte near the 500 boundary
	return map[string]struct {
		block             MemoryBlock
		rules, pastReview []string
	}{
		"empty": {block: MemoryBlock{}},
		"synthesis only": {block: MemoryBlock{Synthesis: "This file handles auth token refresh."}},
		"patterns no synthesis": {block: MemoryBlock{Repo: []PatternMatch{pat("always validate JWT exp"), pat("rate-limit login")}}},
		"synthesis and patterns": {block: MemoryBlock{
			Synthesis: "Auth module.",
			Repo:      []PatternMatch{pat("validate exp"), pat("check aud")},
		}},
		"full mix": {block: MemoryBlock{
			Synthesis: "Payments module summary.",
			Repo: []PatternMatch{
				pat("use idempotency keys"),
				fb("dismissed: this nil-check is unreachable", PolarityNegative),
				fb("confirmed: always wrap in a tx", PolarityPositive),
				pat("log settlement failures"),
			},
			Shared: []PatternMatch{pat("org rule: no floats for money"), pat("org rule: audit every write")},
		}},
		"unknown polarity falls to patterns": {block: MemoryBlock{
			Repo: []PatternMatch{{Content: "weird feedback", Metadata: map[string]string{"type": string(TypeFeedback), "polarity": "sideways"}}},
		}},
		"long items truncated to 500": {block: MemoryBlock{
			Synthesis: long,
			Repo:      []PatternMatch{pat(long), fb(long, PolarityNegative)},
			Shared:    []PatternMatch{pat(long)},
		}},
		"over cap": {block: MemoryBlock{
			Synthesis: huge,
			Repo:      []PatternMatch{pat(huge), pat(huge), fb(huge, PolarityNegative), fb(huge, PolarityPositive)},
			Shared:    []PatternMatch{pat(huge), pat(huge)},
		}},
		"multibyte": {block: MemoryBlock{
			Synthesis: multibyte,
			Repo:      []PatternMatch{pat(multibyte)},
		}},
		"review side searches": {block: MemoryBlock{
			Synthesis: "Cache layer.",
			Repo:      []PatternMatch{pat("warm on boot")},
			Shared:    []PatternMatch{pat("org: cap TTL at 1h")},
		}, rules: []string{"Rule: no unbounded caches", "Rule: log evictions"}, pastReview: []string{"Prior: flagged stampede here", "Prior: TTL too high"}},
	}
}

func TestBriefingSpecialistParity(t *testing.T) {
	t.Parallel()
	const file = "internal/auth/token.go"
	for name, tc := range briefingParityCases() {
		tc := tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			want := referenceFormatSpecialistBlock(tc.block, file, true)
			got := briefingSections(tc.block).renderSpecialist(file, 2400, true)
			if got != want {
				t.Errorf("specialist render drift\n--- got  (%d bytes) ---\n%q\n--- want (%d bytes) ---\n%q", len(got), got, len(want), want)
			}
		})
	}
}

func TestBriefingSpecialistNoEmphasisParity(t *testing.T) {
	t.Parallel()
	const file = "x.go"
	block := MemoryBlock{Repo: []PatternMatch{pat("p"), fb("dismissed thing", PolarityNegative), fb("approved thing", PolarityPositive)}}
	want := referenceFormatSpecialistBlock(block, file, false)
	got := briefingSections(block).renderSpecialist(file, 2400, false)
	if got != want {
		t.Errorf("specialist (no FP emphasis) drift\ngot:  %q\nwant: %q", got, want)
	}
	if strings.Contains(got, "Known False Positives") {
		t.Errorf("emphasizeFalsePositives=false must drop the FP section, got: %q", got)
	}
}

func TestBriefingReviewParity(t *testing.T) {
	t.Parallel()
	for name, tc := range briefingParityCases() {
		tc := tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			want := referenceReviewRender(tc.block, tc.rules, tc.pastReview)
			b := briefingSections(tc.block)
			b.Rules = tc.rules
			b.PastReviews = tc.pastReview
			got := b.renderReview(3200)
			if got != want {
				t.Errorf("review render drift\n--- got  (%d bytes) ---\n%q\n--- want (%d bytes) ---\n%q", len(got), got, len(want), want)
			}
		})
	}
}

// TestBriefingCapBoundaries pins the subtle cap-vs-footer difference between the
// two profiles: specialist caps the body BEFORE appending the footer (final
// length may exceed 2400), review caps the whole string INCLUDING the footer.
func TestBriefingCapBoundaries(t *testing.T) {
	t.Parallel()
	// Each item is per-item-truncated to 500 first, so exceeding the block cap
	// takes many items, not a few huge ones. 12×~500 ≈ 6000 chars of body.
	big := strings.Repeat("x", 5000)
	var repo []PatternMatch
	for i := 0; i < 12; i++ {
		repo = append(repo, pat(big))
	}
	block := MemoryBlock{Repo: repo}

	const specFooterTail = "do not fabricate references."
	const reviewInstruction = "Apply these patterns and past findings"

	spec := briefingSections(block).renderSpecialist("f.go", 2400, true)
	// Specialist: body truncated to the 2400 cap, THEN the footer is appended —
	// so the footer always survives and the total exceeds 2400.
	if len(spec) <= 2400 {
		t.Errorf("specialist should exceed 2400 (body cap + appended footer), got %d", len(spec))
	}
	if !strings.HasSuffix(spec, specFooterTail) {
		t.Errorf("specialist footer must always survive (appended after cap), got tail: %q", spec[max(0, len(spec)-40):])
	}

	rev := briefingSections(block).renderReview(3200)
	// Review: the trailing instruction is written INTO the buffer, then the whole
	// string is capped — util.Truncate adds a 3-byte ellipsis outside maxLen, so
	// the ceiling is 3200+3. When far over cap the instruction is truncated away.
	if len(rev) > 3203 {
		t.Errorf("review must be <=3203 (3200 cap + ellipsis), got %d", len(rev))
	}
	if strings.Contains(rev, reviewInstruction) {
		t.Errorf("review instruction should be swallowed by the cap when far over budget, but it survived")
	}
}
