package pipeline

import (
	"context"
	"log/slog"
	"testing"

	"github.com/BeLazy167/argus/internal/github"
	"github.com/BeLazy167/argus/internal/memory"
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
	o := &Orchestrator{
		indexer: memory.NewIndexer(nil, slog.Default()),
		logger:  slog.Default(),
	}
	run := &PipelineRun{LearnPatterns: false}
	got := o.learnPositivePatterns(context.Background(), run, "owner", "repo")
	if got != 0 {
		t.Errorf("disabled: expected 0, got %d", got)
	}
}

func TestLearnPositivePatterns_NoPraiseComments(t *testing.T) {
	o := &Orchestrator{
		indexer: memory.NewIndexer(nil, slog.Default()),
		logger:  slog.Default(),
	}
	run := &PipelineRun{
		LearnPatterns: true,
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
}

func TestLearnPositivePatterns_CollectsPraiseOnly(t *testing.T) {
	o := &Orchestrator{
		indexer: memory.NewIndexer(nil, slog.Default()),
		logger:  slog.Default(),
	}
	run := &PipelineRun{
		LearnPatterns: true,
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
}

func TestLearnPositivePatterns_UsesAllFileReviews(t *testing.T) {
	o := &Orchestrator{
		indexer: memory.NewIndexer(nil, slog.Default()),
		logger:  slog.Default(),
	}
	run := &PipelineRun{
		LearnPatterns: true,
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
}
