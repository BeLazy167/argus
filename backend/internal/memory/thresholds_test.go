package memory

import "testing"

// TestWithDefaults_ExplicitZeroSurvives pins idx 28: an operator's explicit 0
// (filter disabled per the docs) must not be silently coerced back to the
// default. A resolved struct carries defaults in the fields the operator didn't
// touch, so it's never all-zero — WithDefaults must return it verbatim.
func TestWithDefaults_ExplicitZeroSurvives(t *testing.T) {
	// Simulate parseThresholds output: NewThresholds defaults, then finding_enrich
	// explicitly overridden to 0.
	resolved := NewThresholds()
	resolved.FindingEnrich = 0

	got := resolved.WithDefaults()
	if got.FindingEnrich != 0 {
		t.Errorf("explicit FindingEnrich=0 coerced to %v, want 0", got.FindingEnrich)
	}
	// Untouched fields keep their resolved defaults.
	if got.SpecialistMin != DefaultThresholdSpecialistMin {
		t.Errorf("SpecialistMin = %v, want default %v", got.SpecialistMin, DefaultThresholdSpecialistMin)
	}
}

// TestWithDefaults_ZeroValueGetsDefaults pins the defensive path: a caller that
// passes the zero value (never resolved from settings) still gets defaults.
func TestWithDefaults_ZeroValueGetsDefaults(t *testing.T) {
	got := Thresholds{}.WithDefaults()
	want := NewThresholds()
	if got != want {
		t.Errorf("Thresholds{}.WithDefaults() = %+v, want %+v", got, want)
	}
}

// TestWithDefaults_PartialResolvedPreservesZero covers a mixed struct: one
// explicit 0 alongside a non-default value — neither should be rewritten.
func TestWithDefaults_PartialResolvedPreservesZero(t *testing.T) {
	in := NewThresholds()
	in.SpecialistMin = 0    // operator disabled the specialist gate
	in.FindingEnrich = 0.95 // and tightened enrichment
	got := in.WithDefaults()
	if got.SpecialistMin != 0 {
		t.Errorf("SpecialistMin = %v, want 0 (explicit)", got.SpecialistMin)
	}
	if got.FindingEnrich != 0.95 {
		t.Errorf("FindingEnrich = %v, want 0.95", got.FindingEnrich)
	}
}

func TestThresholds_IsZero(t *testing.T) {
	if !(Thresholds{}).IsZero() {
		t.Error("zero-value Thresholds should report IsZero")
	}
	if (Thresholds{SpecialistMin: 0.6}).IsZero() {
		t.Error("Thresholds with a set field should not report IsZero")
	}
}
