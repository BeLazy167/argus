package pipeline

import (
	"strings"
	"testing"
)

// TestNormalizeVerdict covers both the LLM-emitted-verdict pass-through and the derived-fallback
// path. When the LLM returns a valid verdict we trust it; otherwise we map from (passes, confidence).
func TestNormalizeVerdict(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		raw        string
		passes     bool
		confidence float64
		want       string
	}{
		{"direct broken", "broken", false, 0.9, "broken"},
		{"direct fixed", "Fixed", true, 0.9, "fixed"},
		{"direct partial mixed-case", "  Partial ", false, 0.6, "partial"},
		{"direct unclear", "unclear", false, 0.2, "unclear"},
		{"invalid falls back to fixed when passes", "nonsense", true, 0.9, "fixed"},
		{"invalid + fail + high conf → broken", "", false, 0.95, "broken"},
		{"invalid + fail + medium conf → partial", "", false, 0.6, "partial"},
		{"invalid + fail + low conf → unclear", "", false, 0.3, "unclear"},
		// Contradiction handling — LLM sometimes emits labels that disagree with the structured
		// `passes` boolean. Trust `passes` and derive, rather than letting a "fixed" label hide
		// a genuinely broken scenario.
		{"contradict: fail + fixed label → derived broken", "fixed", false, 0.95, "broken"},
		{"contradict: fail + fixed label + med conf → derived partial", "fixed", false, 0.6, "partial"},
		{"contradict: pass + broken label → derived fixed", "broken", true, 0.9, "fixed"},
		{"contradict: pass + partial label → derived fixed", "partial", true, 0.9, "fixed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := normalizeVerdict(tc.raw, tc.passes, tc.confidence); got != tc.want {
				t.Fatalf("normalizeVerdict(%q, %v, %v) = %q, want %q", tc.raw, tc.passes, tc.confidence, got, tc.want)
			}
		})
	}
}

// TestFirstSentence — the backfill path used when the LLM omitted the one-line Why/Fix.
func TestFirstSentence(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"Single sentence no period", "Single sentence no period"},
		{"First. Second.", "First."},
		{"   padded.   Second part.", "padded."},
	}
	for _, tc := range cases {
		if got := firstSentence(tc.in); got != tc.want {
			t.Fatalf("firstSentence(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestFormatSimulationResults_Format ensures the new 4-line block renders with plain labels
// and filters out low-confidence + fixed verdicts.
func TestFormatSimulationResults_Format(t *testing.T) {
	t.Parallel()
	results := []SimulationResult{
		{
			Scenario:   "private-host blocklist misses RFC1918",
			Verdict:    "broken",
			Confidence: 0.97,
			Why:        "No new validation added; same code still runs.",
			Fix:        "Add IP parse + deny private ranges before fetch.",
			Passes:     false,
		},
		{
			Scenario:   "passes — should NOT render",
			Verdict:    "fixed",
			Confidence: 0.9,
			Passes:     true,
		},
		{
			Scenario:   "low confidence — should NOT render",
			Verdict:    "broken",
			Confidence: 0.2,
			Passes:     false,
		},
	}
	out := FormatSimulationResults(results)

	mustContain := []string{
		"Tested 3 scenarios",
		"1 potential issues found",
		"**Scenario:** private-host blocklist misses RFC1918",
		"**Verdict:** Broken (97% sure)",
		"**Why:** No new validation added; same code still runs.",
		"**Fix:** Add IP parse + deny private ranges before fetch.",
	}
	for _, needle := range mustContain {
		if !strings.Contains(out, needle) {
			t.Errorf("FormatSimulationResults output missing %q\n---\n%s", needle, out)
		}
	}

	mustOmit := []string{
		"passes — should NOT render",
		"low confidence — should NOT render",
		"**Root cause:**",  // old-format label; should be gone
		"**Impact:**",      // old-format label; should be gone
		"**Suggestion:**",  // old-format label; should be gone
	}
	for _, needle := range mustOmit {
		if strings.Contains(out, needle) {
			t.Errorf("FormatSimulationResults should NOT contain %q\n---\n%s", needle, out)
		}
	}
}

// TestFormatSimulationResults_AllPass — when every scenario is fixed we emit the short
// summary line instead of the failures block.
func TestFormatSimulationResults_AllPass(t *testing.T) {
	t.Parallel()
	results := []SimulationResult{
		{Verdict: "fixed", Confidence: 0.9, Passes: true},
		{Verdict: "fixed", Confidence: 0.95, Passes: true},
	}
	out := FormatSimulationResults(results)
	if !strings.Contains(out, "Simulated 2 scenarios — all pass.") {
		t.Errorf("unexpected all-pass output: %q", out)
	}
}

// TestFormatSimulationResults_Empty — zero results = empty string (no header).
func TestFormatSimulationResults_Empty(t *testing.T) {
	t.Parallel()
	if got := FormatSimulationResults(nil); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

// TestParseSimulationResponse_BackfillsWhyAndFix ensures we still render something sensible
// when the LLM emits only the long-form root_cause / suggestion fields.
func TestParseSimulationResponse_BackfillsWhyAndFix(t *testing.T) {
	t.Parallel()
	jsonBody := `{
		"passes": false,
		"confidence": 0.9,
		"root_cause": "Timing-equal compare fails when lengths differ. Secondary detail follows.",
		"suggestion": "Guard length before timingSafeEqual. Use Buffer.byteLength."
	}`
	res, err := parseSimulationResponse(jsonBody, "token check throws on malformed")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if res.Verdict != "broken" {
		t.Errorf("verdict = %q, want broken", res.Verdict)
	}
	if !strings.HasPrefix(res.Why, "Timing-equal compare fails") {
		t.Errorf("Why not backfilled from root_cause: %q", res.Why)
	}
	if !strings.HasPrefix(res.Fix, "Guard length") {
		t.Errorf("Fix not backfilled from suggestion: %q", res.Fix)
	}
}
