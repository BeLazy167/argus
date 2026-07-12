package pipeline

import (
	"fmt"
	"strings"
	"testing"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/pkg/diff"
	"github.com/google/uuid"
)

// --- fixtures -------------------------------------------------------------

// mkDiffFile builds a FileDiff whose ValidCommentLines() covers the given
// new-file line numbers, so comments on those lines render inline (not folded).
func mkDiffFile(path string, validLines ...int) diff.FileDiff {
	lines := make([]diff.DiffLine, len(validLines))
	for i, ln := range validLines {
		lines[i] = diff.DiffLine{Type: diff.LineAdded, NewNum: ln}
	}
	return diff.FileDiff{NewName: path, Status: diff.FileModified, Hunks: []diff.Hunk{{Lines: lines}}}
}

// mkComment builds a minimal FileComment. A distinct `what` keeps
// formatCommentBody output distinct enough to survive postSelectionDedup.
func mkComment(line int, sev Severity, score int, what string) FileComment {
	return FileComment{
		Line:     line,
		Severity: sev,
		Score:    score,
		What:     what,
		Why:      what + " — impact and rationale unique to this finding",
		Category: CategoryBug,
	}
}

// composeRun assembles a PipelineRun ready for Compose. Contract is nil
// (production/full defaults), DeepReview off (single-pass reviewer label).
func composeRun(reviews []FileReview, diffFiles []diff.FileDiff) *PipelineRun {
	return &PipelineRun{
		ReviewID:    uuid.Nil,
		PREvent:     ghpkg.PREvent{HeadSHA: "headsha0123456789"},
		Synthesis:   &SynthesisResult{Score: 8, Headline: "looks solid", Brief: "This PR does a thing."},
		Diff:        &diff.PatchSet{Files: diffFiles},
		FileReviews: reviews,
	}
}

func inlineLines(sub ReviewSubmission) []int {
	out := make([]int, len(sub.GitHub.Comments))
	for i, c := range sub.GitHub.Comments {
		out[i] = c.Line
	}
	return out
}

// --- tests ----------------------------------------------------------------

// TestComposeSeverityOrdering locks the inline ordering contract: severity rank
// descending first (critical > warning > suggestion > praise), score descending
// as the tiebreaker — blocking findings always lead the round.
func TestComposeSeverityOrdering(t *testing.T) {
	// Suggestion/praise live on a NON-blocking file (b.go) so they are not
	// demoted — this isolates pure severity-then-score ordering, which sorts
	// globally across files.
	reviews := []FileReview{
		{Path: "a.go", Comments: []FileComment{
			mkComment(20, SeverityCritical, 10, "critical bravo lowscore blocker"),
			mkComment(30, SeverityWarning, 50, "warning charlie midrange caution"),
			mkComment(40, SeverityCritical, 80, "critical delta strong blocker"),
		}},
		{Path: "b.go", Comments: []FileComment{
			mkComment(10, SeveritySuggestion, 90, "suggestion foxtrot golf hotel india"),
			mkComment(50, SeverityPraise, 95, "praise juliett kilo lima mike november"),
		}},
	}
	sub := Compose(composeRun(reviews, []diff.FileDiff{
		mkDiffFile("a.go", 20, 30, 40),
		mkDiffFile("b.go", 10, 50),
	}))

	got := inlineLines(sub)
	want := []int{40, 20, 30, 10, 50}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("inline order by line = %v, want %v", got, want)
	}
	if sub.FoldedImportant != 0 || sub.FoldedMinor != 0 {
		t.Errorf("folded counts = (%d,%d), want (0,0) — all comments are on valid lines",
			sub.FoldedImportant, sub.FoldedMinor)
	}
}

// TestComposeCapAndOverflow locks the 10-comment inline cap and the single
// "plus N similar" overflow line (findings beyond the cap are summarized, never
// posted as extra comments).
func TestComposeCapAndOverflow(t *testing.T) {
	const total = 15
	comments := make([]FileComment, total)
	validLines := make([]int, total)
	for i := 0; i < total; i++ {
		ln := i + 1
		validLines[i] = ln
		// Descending score so ordering is deterministic. Each `what` carries six
		// index-tagged word tokens (length > 3, non-numeric) so tokenize keeps
		// them distinct and postSelectionDedup does not collapse the set.
		comments[i] = mkComment(ln, SeveritySuggestion, 1000-i,
			fmt.Sprintf("alpha%d bravo%d charlie%d delta%d echo%d foxtrot%d", i, i, i, i, i, i))
	}
	sub := Compose(composeRun([]FileReview{{Path: "big.go", Comments: comments}},
		[]diff.FileDiff{mkDiffFile("big.go", validLines...)}))

	if n := len(sub.GitHub.Comments); n != 10 {
		t.Fatalf("inline comment count = %d, want 10 (cap)", n)
	}
	if !strings.Contains(sub.GitHub.Summary, "plus 5 similar findings not shown inline") {
		t.Errorf("summary missing overflow line for 5 hidden findings:\n%s", sub.GitHub.Summary)
	}
	// Observability counts surfaced for post()'s consolidated log line.
	if sub.InlineCandidates != total {
		t.Errorf("InlineCandidates = %d, want %d (pre-cap)", sub.InlineCandidates, total)
	}
	if sub.CapOverflow != 5 {
		t.Errorf("CapOverflow = %d, want 5", sub.CapOverflow)
	}
	if sub.DedupRemoved != 0 {
		t.Errorf("DedupRemoved = %d, want 0 (all bodies distinct)", sub.DedupRemoved)
	}
}

// TestComposeDedupRemovedCount locks the post-selection-dedup observability
// count: two identical-body findings collapse to one, and DedupRemoved reports
// the drop for post()'s log.
func TestComposeDedupRemovedCount(t *testing.T) {
	dup := "identical duplicate finding alpha bravo charlie delta"
	reviews := []FileReview{{Path: "a.go", Comments: []FileComment{
		mkComment(1, SeverityWarning, 80, dup),
		mkComment(2, SeverityWarning, 70, dup),
	}}}
	sub := Compose(composeRun(reviews, []diff.FileDiff{mkDiffFile("a.go", 1, 2)}))

	if len(sub.GitHub.Comments) != 1 {
		t.Fatalf("inline count = %d, want 1 (near-identical deduped)", len(sub.GitHub.Comments))
	}
	if sub.InlineCandidates != 2 {
		t.Errorf("InlineCandidates = %d, want 2", sub.InlineCandidates)
	}
	if sub.DedupRemoved != 1 {
		t.Errorf("DedupRemoved = %d, want 1", sub.DedupRemoved)
	}
	if sub.CapOverflow != 0 {
		t.Errorf("CapOverflow = %d, want 0 (under cap)", sub.CapOverflow)
	}
}

// TestComposeNitDemotionOnBlockingFile locks one-round ordering: on a file that
// carries a blocking (critical) finding, its nit/praise findings are demoted to
// Minor notes instead of posting inline next to the blocker. Warnings stay.
func TestComposeNitDemotionOnBlockingFile(t *testing.T) {
	reviews := []FileReview{{Path: "hot.go", Comments: []FileComment{
		mkComment(5, SeverityCritical, 90, "critical blocker in hot file must fix"),
		mkComment(6, SeveritySuggestion, 40, "suggestion nit demoted off hot file"),
		mkComment(7, SeverityPraise, 30, "praise demoted off hot file"),
		mkComment(8, SeverityWarning, 60, "warning stays inline on hot file"),
	}}}
	sub := Compose(composeRun(reviews, []diff.FileDiff{mkDiffFile("hot.go", 5, 6, 7, 8)}))

	got := inlineLines(sub)
	want := []int{5, 8} // critical then warning; suggestion + praise demoted
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("inline lines = %v, want %v (nit + praise demoted)", got, want)
	}
	if !strings.Contains(sub.GitHub.Summary, "Minor notes (2)") {
		t.Errorf("summary missing 'Minor notes (2)' for demoted findings:\n%s", sub.GitHub.Summary)
	}
}

// TestComposeMinorNotesRendering locks the collapsed Minor-notes block: total in
// the summary label, 15-item render cap, and per-note bullet format.
func TestComposeMinorNotesRendering(t *testing.T) {
	run := composeRun(nil, nil)
	for i := 0; i < 20; i++ {
		run.MinorNotes = append(run.MinorNotes, MinorNote{
			Path:     fmt.Sprintf("pkg/f%d.go", i),
			Line:     i + 1,
			Severity: SeveritySuggestion,
			Title:    fmt.Sprintf("minornote-%02d", i),
		})
	}
	sub := Compose(run)
	s := sub.GitHub.Summary

	if !strings.Contains(s, "<details><summary>Minor notes (20)</summary>") {
		t.Errorf("summary missing 'Minor notes (20)' label:\n%s", s)
	}
	if !strings.Contains(s, "- `pkg/f0.go:L1` [suggestion] minornote-00") {
		t.Errorf("summary missing first minor-note bullet:\n%s", s)
	}
	if !strings.Contains(s, "minornote-14") {
		t.Errorf("summary should render the 15th note (index 14)")
	}
	if strings.Contains(s, "minornote-15") {
		t.Errorf("summary should cap Minor notes at 15 rendered items; note 15 leaked")
	}
}

// TestComposeSuppressedCountLine locks the single team-feedback suppression
// audit line (never per-finding noise). Suppressed findings never post inline.
func TestComposeSuppressedCountLine(t *testing.T) {
	reviews := []FileReview{{Path: "a.go", Comments: []FileComment{
		mkComment(1, SeverityWarning, 70, "warning visible finding posted inline"),
		func() FileComment {
			c := mkComment(2, SeverityCritical, 80, "suppressed one")
			c.Suppressed = true
			return c
		}(),
		func() FileComment {
			c := mkComment(3, SeverityCritical, 80, "suppressed two")
			c.Suppressed = true
			return c
		}(),
	}}}
	sub := Compose(composeRun(reviews, []diff.FileDiff{mkDiffFile("a.go", 1, 2, 3)}))
	s := sub.GitHub.Summary

	if !strings.Contains(s, "2 findings suppressed by team feedback") {
		t.Errorf("summary missing suppression audit line:\n%s", s)
	}
	if len(sub.GitHub.Comments) != 1 {
		t.Errorf("inline count = %d, want 1 (suppressed findings never post)", len(sub.GitHub.Comments))
	}
	if !strings.Contains(s, "2 suppressed by team feedback") { // glass-box footer tally
		t.Errorf("glass-box footer missing suppressed tally:\n%s", s)
	}
}

// TestComposeScoringNoticeNotDuplicated locks the double-append hazard: the
// unconfigured-scoring notice is appended ONCE at synthesis (it rides in on
// run.Synthesis.Brief). Compose writes Brief verbatim and must NOT re-append —
// even when the run's ScoringUnconfigured flags are set.
func TestComposeScoringNoticeNotDuplicated(t *testing.T) {
	const notice = "> ⚠️ Findings were not score-filtered — set org default models in Settings → Org Defaults."
	run := composeRun(
		[]FileReview{{Path: "a.go", Comments: []FileComment{mkComment(1, SeverityWarning, 60, "a warning that survives")}}},
		[]diff.FileDiff{mkDiffFile("a.go", 1)},
	)
	run.Synthesis.Brief = "This PR does a thing.\n\n" + notice
	// Set the flags that would drive scoringSkippedNotice — Compose must ignore them.
	run.ScoringSkipped = true
	run.ScoringUnconfigured = true

	sub := Compose(run)
	if n := strings.Count(sub.GitHub.Summary, "were not score-filtered"); n != 1 {
		t.Errorf("scoring notice appears %d times, want exactly 1 (no re-append in Compose)", n)
	}
}

// TestComposeGlassBoxFooter locks the glass-box footer + dashboard link, present
// on every posted review regardless of findings.
func TestComposeGlassBoxFooter(t *testing.T) {
	sub := Compose(composeRun(nil, nil))
	s := sub.GitHub.Summary
	for _, want := range []string{
		"Contract: production/full", // nil-contract default
		"single-pass review",        // checkedReviewers default
		"[Dashboard →](https://argus.reviews/reviews/",
		"React 👎 to dismiss",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("footer missing %q:\n%s", want, s)
		}
	}
}

// TestComposeEmptyFindings locks the clean-PR shape: header + brief + footer,
// no inline comments, no findings pill, no folded sections.
func TestComposeEmptyFindings(t *testing.T) {
	sub := Compose(composeRun(nil, nil))
	s := sub.GitHub.Summary

	if len(sub.GitHub.Comments) != 0 {
		t.Errorf("inline count = %d, want 0", len(sub.GitHub.Comments))
	}
	if sub.FoldedImportant != 0 || sub.FoldedMinor != 0 {
		t.Errorf("folded = (%d,%d), want (0,0)", sub.FoldedImportant, sub.FoldedMinor)
	}
	if !strings.Contains(s, "8/10") || !strings.Contains(s, "looks solid") {
		t.Errorf("summary missing header verdict:\n%s", s)
	}
	if !strings.Contains(s, "This PR does a thing.") {
		t.Errorf("summary missing brief body:\n%s", s)
	}
	if strings.Contains(s, " inline · ") {
		t.Errorf("clean PR should omit the findings pill:\n%s", s)
	}
	if strings.Contains(s, "Affected code outside the diff") || strings.Contains(s, "Minor notes (") {
		t.Errorf("clean PR should have no folded/minor sections:\n%s", s)
	}
}
