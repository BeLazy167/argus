package api

import (
	"encoding/json"
	"testing"
)

func ptrOf[T any](v T) *T { return &v }

// TestOrgDefaultsRoundTrip guards the setOrgDefaults whitelist against drift:
// the five memory settings added to pipeline.repoSettings (thresholds +
// disable_shared_decay) must survive marshal into installations.default_settings
// so /settings/memory PUTs are not silently dropped.
func TestOrgDefaultsRoundTrip(t *testing.T) {
	t.Parallel()
	body := orgDefaultsBody{
		Persona:                  "strict",
		ThresholdFindingEnrich:   ptrOf(0.9),
		ThresholdSpecialistMin:   ptrOf(0.8),
		ThresholdScenarioTrigger: ptrOf(0.7),
		ThresholdScenarioDedupe:  ptrOf(0.85),
		DisableSharedDecay:       ptrOf(true),
	}
	if msg, ok := body.validate(); !ok {
		t.Fatalf("validate rejected valid body: %s", msg)
	}

	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Decoding back into the same whitelist shape mirrors how pipeline.repoSettings
	// reads installations.default_settings (identical JSON tags).
	var got orgDefaultsBody
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ThresholdFindingEnrich == nil || *got.ThresholdFindingEnrich != 0.9 {
		t.Fatalf("threshold_finding_enrich dropped: %v", got.ThresholdFindingEnrich)
	}
	if got.ThresholdSpecialistMin == nil || *got.ThresholdSpecialistMin != 0.8 {
		t.Fatalf("threshold_specialist_min dropped: %v", got.ThresholdSpecialistMin)
	}
	if got.ThresholdScenarioTrigger == nil || *got.ThresholdScenarioTrigger != 0.7 {
		t.Fatalf("threshold_scenario_trigger dropped: %v", got.ThresholdScenarioTrigger)
	}
	if got.ThresholdScenarioDedupe == nil || *got.ThresholdScenarioDedupe != 0.85 {
		t.Fatalf("threshold_scenario_dedupe dropped: %v", got.ThresholdScenarioDedupe)
	}
	if got.DisableSharedDecay == nil || !*got.DisableSharedDecay {
		t.Fatalf("disable_shared_decay dropped: %v", got.DisableSharedDecay)
	}
}

// TestOrgDefaultsExplicitZeroPreserved: an explicit threshold of 0 (filter
// disabled) must persist, not be stripped as "empty".
func TestOrgDefaultsExplicitZeroPreserved(t *testing.T) {
	t.Parallel()
	body := orgDefaultsBody{ThresholdFindingEnrich: ptrOf(0.0)}
	if msg, ok := body.validate(); !ok {
		t.Fatalf("validate rejected explicit 0: %s", msg)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if v, ok := m["threshold_finding_enrich"]; !ok || string(v) != "0" {
		t.Fatalf("explicit 0 not preserved, got key=%q present=%v", string(v), ok)
	}
}

// TestOrgDefaultsThresholdValidation: out-of-range thresholds are rejected with a
// field-specific message; in-range (incl. boundaries 0 and 1) pass.
func TestOrgDefaultsThresholdValidation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		body    orgDefaultsBody
		wantOK  bool
		wantMsg string
	}{
		{"above 1", orgDefaultsBody{ThresholdFindingEnrich: ptrOf(1.5)}, false, "threshold_finding_enrich must be between 0 and 1"},
		{"negative", orgDefaultsBody{ThresholdSpecialistMin: ptrOf(-0.1)}, false, "threshold_specialist_min must be between 0 and 1"},
		{"scenario_trigger above 1", orgDefaultsBody{ThresholdScenarioTrigger: ptrOf(2.0)}, false, "threshold_scenario_trigger must be between 0 and 1"},
		{"scenario_dedupe above 1", orgDefaultsBody{ThresholdScenarioDedupe: ptrOf(1.01)}, false, "threshold_scenario_dedupe must be between 0 and 1"},
		{"boundary 0", orgDefaultsBody{ThresholdFindingEnrich: ptrOf(0.0)}, true, ""},
		{"boundary 1", orgDefaultsBody{ThresholdFindingEnrich: ptrOf(1.0)}, true, ""},
		{"invalid persona", orgDefaultsBody{Persona: "nonsense"}, false, "invalid persona"},
		{"nil thresholds ok", orgDefaultsBody{}, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			msg, ok := tc.body.validate()
			if ok != tc.wantOK {
				t.Fatalf("validate ok=%v, want %v (msg=%q)", ok, tc.wantOK, msg)
			}
			if msg != tc.wantMsg {
				t.Fatalf("validate msg=%q, want %q", msg, tc.wantMsg)
			}
		})
	}
}
