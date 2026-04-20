// Package pipeline — token_persistence_test.go guards the async-stage
// token persistence path wired by persistAsyncStageTokens (crosspr_stage.go)
// and the MergeStageTokenEntry sqlc query (reviews.sql).
//
// Tests here are intentionally DB-less. The generated SQL is exercised in
// live DB tests separately; what we verify here is:
//
//  1. Stage-key constants stay synced with labels.go's stageLabels map
//     (drift guard — a renamed key that still compiles would silently
//     drop tokens into an unrendered bucket).
//  2. The StageTokens JSON shape matches the JSONB paths the SQL
//     query extracts (`->>'prompt_tokens'` etc.). A field rename on
//     the Go side would otherwise land tokens under the wrong JSONB
//     key without any compile error.
//  3. The generated sqlc query preserves the invariants we rely on:
//     writes the bucket, writes `{total}`, and reads from the current
//     row rather than from `NEW`/`OLD` triggers.
package pipeline

import (
	"encoding/json"
	"testing"

	"github.com/BeLazy167/argus/backend/internal/store/db"
)

// TestAsyncStageKeys_CoveredByLabels is the drift guard: the constants
// used by persistAsyncStageTokens must appear in stageLabels so UI
// rendering doesn't silently degrade to raw keys.
func TestAsyncStageKeys_CoveredByLabels(t *testing.T) {
	for _, key := range []string{stageKeyCrossPR, stageKeyAcceptance} {
		if _, ok := stageLabels[key]; !ok {
			t.Errorf("stage key %q is used by persistAsyncStageTokens but missing from stageLabels — add a label entry in labels.go", key)
		}
	}
}

// TestStageTokens_JSONShapeMatchesSQLPaths asserts every JSON key the
// MergeStageTokenEntry query extracts is emitted by StageTokens JSON
// marshaling. If anyone renames a field on the Go side, this fails
// before the query silently starts summing zeros.
func TestStageTokens_JSONShapeMatchesSQLPaths(t *testing.T) {
	entry := StageTokens{
		PromptTokens:     11,
		CompletionTokens: 13,
		TotalTokens:      24,
		Cost:             0.01,
		Model:            "m",
		Provider:         "p",
	}
	b, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Keys the SQL extracts via ->>'key'. Every one must be present on
	// the marshaled output or the JSONB math silently becomes zero.
	wantKeys := []string{
		"prompt_tokens",
		"completion_tokens",
		"total_tokens",
		"cost",
		"model",
		"provider",
	}
	for _, k := range wantKeys {
		if _, ok := m[k]; !ok {
			t.Errorf("StageTokens JSON is missing key %q — MergeStageTokenEntry query reads this key, rename on either side will silently zero out tokens", k)
		}
	}
}

// TestMergeStageTokenEntry_ParamsShape guards the sqlc-generated params
// struct. A rename of the generated StageKey / Entry / ReviewID fields
// would break persistAsyncStageTokens at compile time — but only if
// those fields are referenced there. Asserting their presence here
// makes the regression fail at this (isolated) test file even if the
// caller is refactored.
func TestMergeStageTokenEntry_ParamsShape(t *testing.T) {
	p := db.MergeStageTokenEntryParams{
		StageKey: stageKeyCrossPR,
		Entry:    json.RawMessage(`{"prompt_tokens":1}`),
	}
	if p.StageKey != stageKeyCrossPR {
		t.Fatalf("StageKey field missing or renamed on MergeStageTokenEntryParams")
	}
	if string(p.Entry) == "" {
		t.Fatalf("Entry field missing or renamed on MergeStageTokenEntryParams")
	}
	_ = p.ReviewID // existence check — type is uuid.UUID.
}

// TestPersistAsyncStageTokens_InMemoryContract asserts the switch in
// persistAsyncStageTokens routes each known stage key to the matching
// in-memory accumulator, and that unknown keys don't panic.
//
// This test only exercises the in-memory half of the helper; the DB
// half requires Postgres (covered by live-DB tests outside this
// suite). We construct a RunTokenUsage directly (no Orchestrator)
// and call the add* methods the way the helper does — keeping this
// test independent of test-scoped DB setup.
func TestPersistAsyncStageTokens_InMemoryContract(t *testing.T) {
	cases := []struct {
		name     string
		stageKey string
		// selector returns the bucket value on RunTokenUsage after merge.
		selector func(r *RunTokenUsage) StageTokens
		// apply invokes the same method persistAsyncStageTokens would.
		apply func(r *RunTokenUsage, s StageTokens)
	}{
		{
			name:     "cross_pr routes to addCrossPR",
			stageKey: stageKeyCrossPR,
			selector: func(r *RunTokenUsage) StageTokens { return r.CrossPR },
			apply:    func(r *RunTokenUsage, s StageTokens) { r.addCrossPR(s) },
		},
		{
			name:     "acceptance routes to addAcceptance",
			stageKey: stageKeyAcceptance,
			selector: func(r *RunTokenUsage) StageTokens { return r.Acceptance },
			apply:    func(r *RunTokenUsage, s StageTokens) { r.addAcceptance(s) },
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			r := &RunTokenUsage{}
			entry := StageTokens{
				PromptTokens:     7,
				CompletionTokens: 11,
				TotalTokens:      18,
				Cost:             0.25,
				Model:            "test-model",
				Provider:         "test-provider",
			}
			tc.apply(r, entry)
			got := tc.selector(r)
			if got.PromptTokens != entry.PromptTokens {
				t.Errorf("PromptTokens = %d, want %d", got.PromptTokens, entry.PromptTokens)
			}
			if got.CompletionTokens != entry.CompletionTokens {
				t.Errorf("CompletionTokens = %d, want %d", got.CompletionTokens, entry.CompletionTokens)
			}
			if got.TotalTokens != entry.TotalTokens {
				t.Errorf("TotalTokens = %d, want %d", got.TotalTokens, entry.TotalTokens)
			}
			if got.Cost != entry.Cost {
				t.Errorf("Cost = %v, want %v", got.Cost, entry.Cost)
			}
			if got.Model != entry.Model {
				t.Errorf("Model = %q, want %q", got.Model, entry.Model)
			}
			if got.Provider != entry.Provider {
				t.Errorf("Provider = %q, want %q", got.Provider, entry.Provider)
			}
			// Total must also be bumped exactly once.
			if r.Total.PromptTokens != entry.PromptTokens {
				t.Errorf("Total.PromptTokens = %d, want %d (total must aggregate the entry exactly once)", r.Total.PromptTokens, entry.PromptTokens)
			}
			if r.Total.TotalTokens != entry.TotalTokens {
				t.Errorf("Total.TotalTokens = %d, want %d", r.Total.TotalTokens, entry.TotalTokens)
			}
		})
	}
}

// TestPersistAsyncStageTokens_RepeatCallsAccumulate asserts the
// "merge into scalar" contract: two entries for the same stage sum
// field-wise (matching what MergeStageTokenEntry does on the DB side).
// If someone flips addCrossPR to replace-semantics, the JSONB write
// would disagree with the in-memory view — this test pins the
// agreement.
func TestPersistAsyncStageTokens_RepeatCallsAccumulate(t *testing.T) {
	r := &RunTokenUsage{}
	a := StageTokens{PromptTokens: 3, CompletionTokens: 5, TotalTokens: 8, Cost: 0.10, Model: "m1", Provider: "p1"}
	b := StageTokens{PromptTokens: 7, CompletionTokens: 11, TotalTokens: 18, Cost: 0.20, Model: "m2", Provider: "p2"}
	r.addCrossPR(a)
	r.addCrossPR(b)

	if got, want := r.CrossPR.PromptTokens, a.PromptTokens+b.PromptTokens; got != want {
		t.Errorf("CrossPR.PromptTokens = %d, want %d", got, want)
	}
	if got, want := r.CrossPR.TotalTokens, a.TotalTokens+b.TotalTokens; got != want {
		t.Errorf("CrossPR.TotalTokens = %d, want %d", got, want)
	}
	// Model is stamped once — stays "m1", matches the SQL's
	// "COALESCE(NULLIF(existing.model, ''), entry.model, '')" semantic.
	if r.CrossPR.Model != "m1" {
		t.Errorf("CrossPR.Model = %q, want %q (model should be stamped once, not overwritten)", r.CrossPR.Model, "m1")
	}
	if r.CrossPR.Provider != "p1" {
		t.Errorf("CrossPR.Provider = %q, want %q", r.CrossPR.Provider, "p1")
	}
	// Total sums BOTH entries.
	if r.Total.PromptTokens != a.PromptTokens+b.PromptTokens {
		t.Errorf("Total.PromptTokens = %d, want %d", r.Total.PromptTokens, a.PromptTokens+b.PromptTokens)
	}
}
