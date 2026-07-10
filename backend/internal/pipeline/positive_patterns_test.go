package pipeline

import (
	"context"
	"log/slog"
	"testing"

	"github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/memory/memorytest"
)

func TestLearnPositivePatterns_NilIndexer(t *testing.T) {
	o := &Orchestrator{logger: slog.Default()}
	run := &PipelineRun{LearnPatterns: true}
	got := o.learnPositivePatterns(context.Background(), run, "owner", "repo")
	if got != 0 {
		t.Errorf("nil indexer: expected 0, got %d", got)
	}
}

func TestLearnPositivePatterns_Disabled(t *testing.T) {
	o := &Orchestrator{logger: slog.Default()}
	fake := &memorytest.Fake{}
	run := &PipelineRun{
		LearnPatterns: false,
		Indexer:       fake,
	}
	got := o.learnPositivePatterns(context.Background(), run, "owner", "repo")
	if got != 0 {
		t.Errorf("disabled: expected 0, got %d", got)
	}
	if len(fake.Feedback) != 0 {
		t.Errorf("disabled path must write nothing, got %d feedback signals", len(fake.Feedback))
	}
}

func TestLearnPositivePatterns_NoPraiseComments(t *testing.T) {
	o := &Orchestrator{logger: slog.Default()}
	fake := &memorytest.Fake{}
	run := &PipelineRun{
		LearnPatterns: true,
		Indexer:       fake,
		PREvent:       github.PREvent{PRNumber: 1, RepoFullName: "owner/repo"},
		FileReviews: []FileReview{
			{
				Path: "main.go",
				Comments: []FileComment{
					{Severity: SeverityCritical, Category: CategoryBug, Line: 10, Body: "nil deref"},
					{Severity: SeverityWarning, Category: CategoryStyle, Line: 20, Body: "naming"},
				},
			},
		},
	}
	got := o.learnPositivePatterns(context.Background(), run, "owner", "repo")
	if got != 0 {
		t.Errorf("no praise: expected 0, got %d", got)
	}
	if len(fake.Feedback) != 0 {
		t.Errorf("non-praise comments must never be indexed, got %d feedback signals", len(fake.Feedback))
	}
}

func TestLearnPositivePatterns_CollectsPraiseOnly(t *testing.T) {
	o := &Orchestrator{logger: slog.Default()}
	fake := &memorytest.Fake{}
	run := &PipelineRun{
		LearnPatterns: true,
		Indexer:       fake,
		PREvent:       github.PREvent{PRNumber: 42, RepoFullName: "acme/widget"},
		FileReviews: []FileReview{
			{
				Path: "handler.go",
				Comments: []FileComment{
					{Severity: SeverityCritical, Category: CategoryBug, Line: 10, Body: "nil deref"},
					{Severity: SeverityPraise, Category: CategoryBug, Line: 15, Body: "Good edge-case handling"},
				},
			},
			{
				Path: "util.go",
				Comments: []FileComment{
					{Severity: SeverityPraise, Category: CategoryErrorHandling, Line: 5, Body: "Proper error wrapping"},
				},
			},
		},
	}
	// Should collect exactly 2 praise comments (not the 1 critical)
	got := o.learnPositivePatterns(context.Background(), run, "acme", "widget")
	if got != 2 {
		t.Errorf("expected 2 praise patterns, got %d", got)
	}
	// The fake records the actual writes: only the two praise comments, each as a
	// confirmed (positive) feedback signal on its own file — never the critical.
	if len(fake.Feedback) != 2 {
		t.Fatalf("expected 2 feedback signals indexed, got %d", len(fake.Feedback))
	}
	byFile := map[string]memory.FeedbackMemory{}
	for _, fb := range fake.Feedback {
		byFile[fb.FilePath] = fb
		if fb.Action != "confirmed" {
			t.Errorf("praise must be indexed with action=confirmed, got %q for %s", fb.Action, fb.FilePath)
		}
		if fb.PRNumber != 42 {
			t.Errorf("feedback PRNumber = %d, want 42", fb.PRNumber)
		}
	}
	if h := byFile["handler.go"]; h.OriginalBody != "Good edge-case handling" || h.Category != string(CategoryBug) {
		t.Errorf("handler.go feedback = %+v", h)
	}
	if u := byFile["util.go"]; u.OriginalBody != "Proper error wrapping" || u.Category != string(CategoryErrorHandling) {
		t.Errorf("util.go feedback = %+v", u)
	}
}

func TestLearnPositivePatterns_UsesAllFileReviews(t *testing.T) {
	o := &Orchestrator{logger: slog.Default()}
	fake := &memorytest.Fake{}
	run := &PipelineRun{
		LearnPatterns: true,
		Indexer:       fake,
		PREvent:       github.PREvent{PRNumber: 10, RepoFullName: "org/repo"},
		FileReviews:   nil,
		AllFileReviews: []FileReview{
			{
				Path: "api.go",
				Comments: []FileComment{
					{Severity: SeverityPraise, Category: CategorySecurity, Line: 1, Body: "Input validated"},
				},
			},
		},
	}
	// AllFileReviews should be preferred over empty FileReviews
	got := o.learnPositivePatterns(context.Background(), run, "org", "repo")
	if got != 1 {
		t.Errorf("AllFileReviews path: expected 1 praise pattern, got %d", got)
	}
	if len(fake.Feedback) != 1 || fake.Feedback[0].FilePath != "api.go" || fake.Feedback[0].Category != string(CategorySecurity) {
		t.Errorf("expected the AllFileReviews praise indexed once on api.go, got %+v", fake.Feedback)
	}
}
