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

// TestSplitHeadlineAndBody pins the parse of the synthesis LLM response that
// encodes the H2 one-liner as a `**Headline:** …` line. Any drift in how the
// prompt asks for this field (or how the LLM emits it) should surface here.
func TestSplitHeadlineAndBody(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		in           string
		wantHeadline string
		wantBody     string
	}{
		{
			name: "happy_path",
			in: "**Headline:** Ships the partner auth flow cleanly\n\n" +
				"**Verdict:** Looks good.",
			wantHeadline: "Ships the partner auth flow cleanly",
			wantBody:     "**Verdict:** Looks good.",
		},
		{
			name: "lowercase_label_tolerated",
			in: "**headline:** drifted casing still parsed\n" +
				"**Verdict:** rest.",
			wantHeadline: "drifted casing still parsed",
			wantBody:     "**Verdict:** rest.",
		},
		{
			name:         "no_headline_returns_empty_headline",
			in:           "**Verdict:** no headline line at all.",
			wantHeadline: "",
			wantBody:     "**Verdict:** no headline line at all.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, b := splitHeadlineAndBody(tc.in)
			if h != tc.wantHeadline {
				t.Errorf("headline = %q, want %q", h, tc.wantHeadline)
			}
			if b != tc.wantBody {
				t.Errorf("body = %q, want %q", b, tc.wantBody)
			}
		})
	}
}

// TestExtractHeadline_Fallback covers the path we hit when the LLM drops the
// required **Headline:** line. Checks: leading bold prefix stripped, result
// truncated at a word boundary, never mid-word.
func TestExtractHeadline_Fallback(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{
			name: "strips_verdict_prefix",
			in:   "**Verdict:** The code looks good to ship.",
			max:  100,
			want: "The code looks good to ship.",
		},
		{
			// The literal acmeorg-account#331 failure. With 80-char truncation
			// it cut as "dependency/"; the new 100-char + word-boundary path
			// should end on a whole word and append an ellipsis.
			name: "word_boundary_truncation",
			in: "**Verdict:** This PR moves the partner auth flow, prerender fix, " +
				"and dependency/security updates forward.",
			max:  60,
			want: "This PR moves the partner auth flow, prerender fix, and…",
		},
		{
			name: "short_enough_no_truncation",
			in:   "All good.",
			max:  100,
			want: "All good.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractHeadline(tc.in, tc.max)
			if got != tc.want {
				t.Errorf("extractHeadline(...) =\n  got: %q\n want: %q", got, tc.want)
			}
		})
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
