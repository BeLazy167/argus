package pipeline

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestAddToTotal_NewBuckets pushes distinct-prime token counts through
// addToTotal for each new bucket (LeadAgent, Acceptance, CrossPR, Reply,
// and two Simulation entries) and asserts Total sums field-wise.
// Distinct primes make off-by-one and field-mix bugs localizable.
func TestAddToTotal_NewBuckets(t *testing.T) {
	lead := StageTokens{PromptTokens: 11, CompletionTokens: 13, TotalTokens: 24, Cost: 0.01}
	acc := StageTokens{PromptTokens: 17, CompletionTokens: 19, TotalTokens: 36, Cost: 0.02}
	xpr := StageTokens{PromptTokens: 23, CompletionTokens: 29, TotalTokens: 52, Cost: 0.03}
	rep := StageTokens{PromptTokens: 31, CompletionTokens: 37, TotalTokens: 68, Cost: 0.04}
	sim0 := StageTokens{PromptTokens: 41, CompletionTokens: 43, TotalTokens: 84, Cost: 0.05}
	sim1 := StageTokens{PromptTokens: 47, CompletionTokens: 53, TotalTokens: 100, Cost: 0.06}

	r := RunTokenUsage{
		LeadAgent:  lead,
		Acceptance: acc,
		CrossPR:    xpr,
		Reply:      rep,
		Simulation: []StageTokens{sim0, sim1},
	}
	r.addToTotal(r.LeadAgent)
	r.addToTotal(r.Acceptance)
	r.addToTotal(r.CrossPR)
	r.addToTotal(r.Reply)
	for _, s := range r.Simulation {
		r.addToTotal(s)
	}

	wantP := 11 + 17 + 23 + 31 + 41 + 47
	wantC := 13 + 19 + 29 + 37 + 43 + 53
	wantT := 24 + 36 + 52 + 68 + 84 + 100
	wantCost := 0.01 + 0.02 + 0.03 + 0.04 + 0.05 + 0.06

	if r.Total.PromptTokens != wantP {
		t.Errorf("PromptTokens = %d, want %d", r.Total.PromptTokens, wantP)
	}
	if r.Total.CompletionTokens != wantC {
		t.Errorf("CompletionTokens = %d, want %d", r.Total.CompletionTokens, wantC)
	}
	if r.Total.TotalTokens != wantT {
		t.Errorf("TotalTokens = %d, want %d", r.Total.TotalTokens, wantT)
	}
	if diff := r.Total.Cost - wantCost; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("Cost = %v, want %v", r.Total.Cost, wantCost)
	}
}

// TestRunTokenUsage_JSONMarshal_NewFields asserts the 5 new fields emit when
// populated and are omitted (omitempty) when zero-valued.
func TestRunTokenUsage_JSONMarshal_NewFields(t *testing.T) {
	t.Run("populated_keys_present", func(t *testing.T) {
		r := RunTokenUsage{
			LeadAgent:  StageTokens{TotalTokens: 1},
			Acceptance: StageTokens{TotalTokens: 2},
			CrossPR:    StageTokens{TotalTokens: 3},
			Reply:      StageTokens{TotalTokens: 4},
			Simulation: []StageTokens{{TotalTokens: 5}},
		}
		b, err := json.Marshal(&r)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		out := string(b)
		for _, key := range []string{`"lead_agent"`, `"acceptance"`, `"cross_pr"`, `"simulation"`, `"reply"`} {
			if !strings.Contains(out, key) {
				t.Errorf("missing key %s in output:\n%s", key, out)
			}
		}
	})

	t.Run("zero_slice_fields_omitted", func(t *testing.T) {
		// Go's encoding/json honours omitempty on slice/map/pointer zero values
		// but NOT on zero-valued structs — that's a known limitation and matches
		// the behaviour of pre-existing buckets like Scoring/Synthesis. Only the
		// slice-typed Simulation (and FileSynthesis) can actually be omitted.
		r := RunTokenUsage{}
		b, err := json.Marshal(&r)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		out := string(b)
		if strings.Contains(out, `"simulation"`) {
			t.Errorf("simulation (nil slice) should be omitted:\n%s", out)
		}
		if strings.Contains(out, `"file_synthesis"`) {
			t.Errorf("file_synthesis (nil slice) should be omitted:\n%s", out)
		}
		// Struct fields (lead_agent/acceptance/cross_pr/reply) always emit even
		// at zero value — don't assert omission for them.
	})
}
