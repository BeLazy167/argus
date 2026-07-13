// Package pipeline — retry_run_test.go guards buildRetryRun, the helper that
// reconstructs a fresh PipelineRun when a terminal review is retried.
//
// A terminal run (failed/cancelled) can't be resumed — StateMachine.Run's
// loop exits immediately on a terminal state — so RetryReview rebuilds a fresh
// run for the SAME review and drives it from the initial state. These tests
// are DB-less: buildRetryRun is pure.
package pipeline

import (
	"testing"

	"github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/pkg/diff"
	"github.com/google/uuid"
)

// terminalRun builds a plausible persisted run in a terminal state with
// intermediate results populated, so tests can assert they're dropped.
func terminalRun() *PipelineRun {
	return &PipelineRun{
		ID:       uuid.New(),
		ReviewID: uuid.New(),
		State:    StateFailed,
		Error:    "stage reviewing failed: boom",
		PREvent: github.PREvent{
			RepoFullName: "acme/widgets",
			PRNumber:     42,
			HeadRef:      "feature/x",
			PRTitle:      "Add widget",
		},
		DBInstallationID:    7,
		DBRepoID:            9,
		TraceID:             "trace-123",
		Diff:                &diff.PatchSet{Files: []diff.FileDiff{{NewName: "a.go"}}},
		RawDiff:             "diff --git a/a.go b/a.go",
		Persona:             Persona("security"),
		CustomPersonaPrompt: "be paranoid",
		DeepReview:          true,
		IsIncremental:       true,
		FileSynthesis:       true,
		Prompts:             map[string]string{"review": "custom"},
		// Intermediate results that MUST be dropped on rebuild.
		TriageResults: []TriageResult{{File: "a.go"}},
		FileReviews:   []FileReview{{Path: "a.go"}},
		Synthesis:     &SynthesisResult{Score: 5},
	}
}

func TestBuildRetryRun_FreshRunFromInitialState(t *testing.T) {
	prev := terminalRun()

	fresh, err := buildRetryRun(prev)
	if err != nil {
		t.Fatalf("buildRetryRun: unexpected error: %v", err)
	}

	if fresh.State != StatePending {
		t.Errorf("State = %q, want %q (must start from the initial state)", fresh.State, StatePending)
	}
	if fresh.State.IsTerminal() {
		t.Errorf("fresh run is terminal (%q) — Run would no-op", fresh.State)
	}
	// The initial state must have a transition so Run actually executes stages.
	if next := transitions()[StatePending]; next != StateTriaging {
		t.Errorf("transitions()[StatePending] = %q, want %q", next, StateTriaging)
	}
	if fresh.ID == prev.ID {
		t.Error("run ID must be new, got the prior run's ID")
	}
	if fresh.ReviewID != prev.ReviewID {
		t.Errorf("ReviewID = %s, want %s (retry is the SAME review)", fresh.ReviewID, prev.ReviewID)
	}
	if fresh.Error != "" {
		t.Errorf("Error = %q, want empty (terminal error must be cleared)", fresh.Error)
	}
	if fresh.EventBus != nil {
		t.Error("EventBus must be nil (caller wires it after build)")
	}
}

func TestBuildRetryRun_CarriesSettings(t *testing.T) {
	prev := terminalRun()

	fresh, err := buildRetryRun(prev)
	if err != nil {
		t.Fatalf("buildRetryRun: %v", err)
	}

	if fresh.PREvent.RepoFullName != prev.PREvent.RepoFullName || fresh.PREvent.PRNumber != prev.PREvent.PRNumber {
		t.Errorf("PREvent not carried: got %+v", fresh.PREvent)
	}
	if fresh.DBInstallationID != prev.DBInstallationID {
		t.Errorf("DBInstallationID = %d, want %d", fresh.DBInstallationID, prev.DBInstallationID)
	}
	if fresh.DBRepoID != prev.DBRepoID {
		t.Errorf("DBRepoID = %d, want %d", fresh.DBRepoID, prev.DBRepoID)
	}
	if fresh.TraceID != prev.TraceID {
		t.Errorf("TraceID = %q, want %q", fresh.TraceID, prev.TraceID)
	}
	if fresh.RawDiff != prev.RawDiff {
		t.Errorf("RawDiff = %q, want %q", fresh.RawDiff, prev.RawDiff)
	}
	if fresh.Diff != prev.Diff {
		t.Error("Diff not carried")
	}
	if fresh.Persona != prev.Persona {
		t.Errorf("Persona = %q, want %q", fresh.Persona, prev.Persona)
	}
	if fresh.CustomPersonaPrompt != prev.CustomPersonaPrompt {
		t.Errorf("CustomPersonaPrompt = %q, want %q", fresh.CustomPersonaPrompt, prev.CustomPersonaPrompt)
	}
	if !fresh.DeepReview {
		t.Error("DeepReview not carried")
	}
	if !fresh.IsIncremental {
		t.Error("IsIncremental not carried")
	}
	if !fresh.FileSynthesis {
		t.Error("FileSynthesis not carried")
	}
	if fresh.Prompts["review"] != "custom" {
		t.Errorf("Prompts not carried: %+v", fresh.Prompts)
	}
}

func TestBuildRetryRun_NormalizesThresholds(t *testing.T) {
	prev := terminalRun()
	// PipelineRun.Thresholds is json:"-", so a run loaded from a persisted
	// terminal state carries the zero value. buildRetryRun must normalize it at
	// this single ingress so downstream value-level readers never see a zero
	// Thresholds (which would zero every similarity gate).
	fresh, err := buildRetryRun(prev)
	if err != nil {
		t.Fatalf("buildRetryRun: %v", err)
	}
	if fresh.Thresholds.IsZero() {
		t.Error("Thresholds left zero — retry hands zeroed similarity gates to readers")
	}
	if fresh.Thresholds.FindingEnrich == 0 {
		t.Errorf("FindingEnrich = 0, want a resolved default; Thresholds=%+v", fresh.Thresholds)
	}
}

func TestBuildRetryRun_DropsIntermediateResults(t *testing.T) {
	prev := terminalRun()

	fresh, err := buildRetryRun(prev)
	if err != nil {
		t.Fatalf("buildRetryRun: %v", err)
	}

	if len(fresh.TriageResults) != 0 {
		t.Errorf("TriageResults carried over (%d) — must start clean", len(fresh.TriageResults))
	}
	if len(fresh.FileReviews) != 0 {
		t.Errorf("FileReviews carried over (%d) — must start clean", len(fresh.FileReviews))
	}
	if fresh.Synthesis != nil {
		t.Error("Synthesis carried over — must start clean")
	}
	if fresh.Tokens.Total.TotalTokens != 0 {
		t.Errorf("Tokens carried over (%d) — must start clean", fresh.Tokens.Total.TotalTokens)
	}
}

func TestBuildRetryRun_RecomputesContract(t *testing.T) {
	prev := terminalRun()
	prev.Contract = nil // json:"-": never survives persistence
	prev.PREvent.Draft = true

	fresh, err := buildRetryRun(prev)
	if err != nil {
		t.Fatalf("buildRetryRun: %v", err)
	}

	if fresh.Contract == nil {
		t.Fatal("Contract nil — must be recomputed from the carried PR event")
	}
	// A draft PR downgrades depth to skim; proves the contract was recomputed
	// from PREvent rather than copied (prev.Contract was nil).
	if fresh.Contract.Depth != DepthSkim {
		t.Errorf("Contract.Depth = %q, want %q (draft PR)", fresh.Contract.Depth, DepthSkim)
	}
}

func TestBuildRetryRun_NilDiffDoesNotPanic(t *testing.T) {
	prev := terminalRun()
	prev.Diff = nil

	fresh, err := buildRetryRun(prev)
	if err != nil {
		t.Fatalf("buildRetryRun with nil Diff: %v", err)
	}
	if fresh.Contract == nil {
		t.Error("Contract should still compute with a nil diff")
	}
}

func TestBuildRetryRun_CarriesPriorComments(t *testing.T) {
	prev := terminalRun()
	prev.IsIncremental = true
	prev.PreviousReviewID = func() *uuid.UUID { u := uuid.New(); return &u }()
	prev.PriorComments = map[string][]PriorComment{
		"a.go": {{FilePath: "a.go", Line: 10, EndLine: 10, Body: "prior finding", Severity: "warning"}},
	}

	fresh, err := buildRetryRun(prev)
	if err != nil {
		t.Fatalf("buildRetryRun: %v", err)
	}
	if !fresh.IsIncremental {
		t.Error("IsIncremental not carried")
	}
	if fresh.PreviousReviewID == nil || *fresh.PreviousReviewID != *prev.PreviousReviewID {
		t.Error("PreviousReviewID not carried")
	}
	got := fresh.PriorComments["a.go"]
	if len(got) != 1 || got[0].Body != "prior finding" {
		t.Errorf("PriorComments not carried: %+v (an incremental retry must not re-post prior findings)", fresh.PriorComments)
	}
}

func TestBuildRetryRun_GuardsUnusablePREvent(t *testing.T) {
	cases := []struct {
		name string
		mut  func(r *PipelineRun)
	}{
		{"empty repo", func(r *PipelineRun) { r.PREvent.RepoFullName = "" }},
		{"zero pr number", func(r *PipelineRun) { r.PREvent.PRNumber = 0 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prev := terminalRun()
			tc.mut(prev)
			fresh, err := buildRetryRun(prev)
			if err == nil {
				t.Fatal("want error for unusable PR event, got nil")
			}
			if fresh != nil {
				t.Error("want nil run on guard failure")
			}
		})
	}
}
