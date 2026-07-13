package pipeline

import (
	"context"
	"io"
	"log/slog"
	"testing"
)

// discardOrchestrator builds a minimal Orchestrator whose only wired dependency
// is a no-op logger — enough to exercise dedupStage's pure control flow without
// the full pipeline harness.
func discardOrchestrator() *Orchestrator {
	return &Orchestrator{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

// TestDedupStage_NonDeepIncrementalDropsPriorDuplicate proves the ungate: a
// free-tier (DeepReview=false) incremental re-review still structurally drops a
// finding that duplicates a prior comment. Before the ungate, dedupStage
// early-returned for non-deep runs and re-posted the reworded duplicate.
func TestDedupStage_NonDeepIncrementalDropsPriorDuplicate(t *testing.T) {
	o := discardOrchestrator()
	run := &PipelineRun{
		DeepReview:    false, // free tier — the ungated path
		IsIncremental: true,
		PriorComments: map[string][]PriorComment{
			"a.go": {{Line: 10, Category: "bug"}},
		},
		FileReviews: []FileReview{
			{Path: "a.go", Comments: []FileComment{
				{Line: 12, Category: "bug"},  // reworded prior duplicate (within ±10) → dropped
				{Line: 200, Category: "bug"}, // novel finding → kept
			}},
		},
	}

	if err := o.dedupStage(context.Background(), run); err != nil {
		t.Fatalf("dedupStage: %v", err)
	}

	got := run.FileReviews[0].Comments
	if len(got) != 1 {
		t.Fatalf("non-deep incremental: expected 1 surviving comment after prior-dup drop, got %d: %+v", len(got), got)
	}
	if got[0].Line != 200 {
		t.Fatalf("wrong comment survived: line %d, want 200", got[0].Line)
	}
}

// TestDedupStage_NonDeepNonIncrementalIsNoop guards the ungate's scope: a
// non-deep, NON-incremental run (e.g. a first review) must not touch findings —
// there are no priors to dedup against.
func TestDedupStage_NonDeepNonIncrementalIsNoop(t *testing.T) {
	o := discardOrchestrator()
	run := &PipelineRun{
		DeepReview:    false,
		IsIncremental: false,
		FileReviews: []FileReview{
			{Path: "a.go", Comments: []FileComment{{Line: 10, Category: "bug"}}},
		},
	}
	if err := o.dedupStage(context.Background(), run); err != nil {
		t.Fatalf("dedupStage: %v", err)
	}
	if n := len(run.FileReviews[0].Comments); n != 1 {
		t.Fatalf("first review must not drop findings: got %d comments, want 1", n)
	}
}

// TestDropPriorDuplicates verifies the structural dedup against prior reviews.
// The matching rule is: same file + same Category (case-insensitive) + line
// within ±10. String similarity is intentionally not considered — LLMs reword
// findings across reviews, so structural matching is the only reliable signal.
func TestDropPriorDuplicates(t *testing.T) {
	mkRun := func(priors map[string][]PriorComment, files []FileReview) *PipelineRun {
		return &PipelineRun{
			IsIncremental: true,
			PriorComments: priors,
			FileReviews:   files,
		}
	}

	tests := []struct {
		name           string
		run            *PipelineRun
		wantDropped    int
		wantRemaining  map[string]int // file path -> expected comment count after dedup
	}{
		{
			name:          "nil run — no-op",
			run:           nil,
			wantDropped:   0,
			wantRemaining: nil,
		},
		{
			name: "no prior comments — nothing dropped",
			run: mkRun(nil, []FileReview{
				{Path: "a.go", Comments: []FileComment{{Line: 10, Category: "bug"}}},
			}),
			wantDropped:   0,
			wantRemaining: map[string]int{"a.go": 1},
		},
		{
			name: "prior on different file — not dropped",
			run: mkRun(
				map[string][]PriorComment{"other.go": {{Line: 10, Category: "bug"}}},
				[]FileReview{{Path: "a.go", Comments: []FileComment{{Line: 10, Category: "bug"}}}},
			),
			wantDropped:   0,
			wantRemaining: map[string]int{"a.go": 1},
		},
		{
			name: "exact line+category match — dropped",
			run: mkRun(
				map[string][]PriorComment{"a.go": {{Line: 10, Category: "bug"}}},
				[]FileReview{{Path: "a.go", Comments: []FileComment{{Line: 10, Category: "bug"}}}},
			),
			wantDropped:   1,
			wantRemaining: map[string]int{"a.go": 0},
		},
		{
			name: "line within +10 — dropped (accommodates line shift on re-push)",
			run: mkRun(
				map[string][]PriorComment{"a.go": {{Line: 10, Category: "bug"}}},
				[]FileReview{{Path: "a.go", Comments: []FileComment{{Line: 20, Category: "bug"}}}},
			),
			wantDropped:   1,
			wantRemaining: map[string]int{"a.go": 0},
		},
		{
			name: "line beyond ±10 — kept",
			run: mkRun(
				map[string][]PriorComment{"a.go": {{Line: 10, Category: "bug"}}},
				[]FileReview{{Path: "a.go", Comments: []FileComment{{Line: 25, Category: "bug"}}}},
			),
			wantDropped:   0,
			wantRemaining: map[string]int{"a.go": 1},
		},
		{
			name: "different category on same line — kept (different issue)",
			run: mkRun(
				map[string][]PriorComment{"a.go": {{Line: 10, Category: "bug"}}},
				[]FileReview{{Path: "a.go", Comments: []FileComment{{Line: 10, Category: "security"}}}},
			),
			wantDropped:   0,
			wantRemaining: map[string]int{"a.go": 1},
		},
		{
			name: "category case-insensitive match — dropped",
			run: mkRun(
				map[string][]PriorComment{"a.go": {{Line: 10, Category: "BUG"}}},
				[]FileReview{{Path: "a.go", Comments: []FileComment{{Line: 12, Category: "bug"}}}},
			),
			wantDropped:   1,
			wantRemaining: map[string]int{"a.go": 0},
		},
		{
			name: "multiple priors — first match wins, new comment dropped",
			run: mkRun(
				map[string][]PriorComment{"a.go": {
					{Line: 50, Category: "security"},
					{Line: 100, Category: "bug"},
				}},
				[]FileReview{{Path: "a.go", Comments: []FileComment{{Line: 98, Category: "bug"}}}},
			),
			wantDropped:   1,
			wantRemaining: map[string]int{"a.go": 0},
		},
		{
			name: "mix: drops duplicate, keeps novel on same file",
			run: mkRun(
				map[string][]PriorComment{"a.go": {{Line: 10, Category: "bug"}}},
				[]FileReview{{Path: "a.go", Comments: []FileComment{
					{Line: 12, Category: "bug"},    // dropped (near prior)
					{Line: 200, Category: "bug"},   // kept (far from prior)
					{Line: 10, Category: "style"},  // kept (different category)
				}}},
			),
			wantDropped:   1,
			wantRemaining: map[string]int{"a.go": 2},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := dropPriorDuplicates(tc.run)
			if got != tc.wantDropped {
				t.Errorf("dropped = %d, want %d", got, tc.wantDropped)
			}
			if tc.run != nil && tc.wantRemaining != nil {
				seen := make(map[string]bool, len(tc.wantRemaining))
				for _, fr := range tc.run.FileReviews {
					want, ok := tc.wantRemaining[fr.Path]
					if !ok {
						continue
					}
					seen[fr.Path] = true
					if len(fr.Comments) != want {
						t.Errorf("file %s: %d comments remain, want %d", fr.Path, len(fr.Comments), want)
					}
				}
				// Catch regressions where an expected file was removed from
				// FileReviews entirely: every wantRemaining key must be seen.
				for path := range tc.wantRemaining {
					if !seen[path] {
						t.Errorf("file %s: expected in FileReviews with %d comments, but not present", path, tc.wantRemaining[path])
					}
				}
			}
		})
	}
}
