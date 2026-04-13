package pipeline

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPersonaPromptOverlay(t *testing.T) {
	tests := []struct {
		name      string
		persona   Persona
		wantEmpty bool
		wantSnip  string // substring expected in output
	}{
		{"security auditor", PersonaSecurityAuditor, false, "Security Auditor"},
		{"performance engineer", PersonaPerformanceEngineer, false, "Performance Engineer"},
		{"mentor", PersonaMentor, false, "Mentor"},
		{"architect", PersonaArchitect, false, "Architect"},
		{"strict", PersonaStrict, false, "Strict Reviewer"},
		{"adversarial", PersonaAdversarial, false, "Adversarial Reviewer"},
		{"fresh eyes", PersonaFreshEyes, false, "Fresh Eyes"},
		{"default returns empty", PersonaDefault, true, ""},
		{"unknown returns empty", Persona("nonexistent"), true, ""},
		{"empty string returns empty", Persona(""), true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PersonaPromptOverlay(tt.persona)
			if tt.wantEmpty && got != "" {
				t.Errorf("expected empty, got %q", got)
			}
			if !tt.wantEmpty && got == "" {
				t.Errorf("expected non-empty overlay for %q", tt.persona)
			}
			if tt.wantSnip != "" && !strings.Contains(got, tt.wantSnip) {
				t.Errorf("expected overlay to contain %q, got %q", tt.wantSnip, got)
			}
		})
	}
}

func TestPersonaPromptOverlayCustom(t *testing.T) {
	tests := []struct {
		name   string
		prompt string
		want   string
	}{
		{"empty returns empty", "", ""},
		{"non-empty wraps prompt", "Be nice", "\n\n## Persona: Custom\nBe nice"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PersonaPromptOverlayCustom(tt.prompt)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPersonaSpecialistHint(t *testing.T) {
	tests := []struct {
		name      string
		persona   Persona
		wantEmpty bool
		wantSnip  string
	}{
		{"security", PersonaSecurityAuditor, false, "security-first"},
		{"performance", PersonaPerformanceEngineer, false, "performance-focused"},
		{"mentor", PersonaMentor, false, "mentor"},
		{"architect", PersonaArchitect, false, "architect"},
		{"strict", PersonaStrict, false, "strict"},
		{"adversarial", PersonaAdversarial, false, "adversarial"},
		{"fresh eyes", PersonaFreshEyes, false, "fresh eyes"},
		{"default returns empty", PersonaDefault, true, ""},
		{"unknown returns empty", Persona("nope"), true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PersonaSpecialistHint(tt.persona)
			if tt.wantEmpty && got != "" {
				t.Errorf("expected empty, got %q", got)
			}
			if !tt.wantEmpty && got == "" {
				t.Errorf("expected non-empty hint for %q", tt.persona)
			}
			if tt.wantSnip != "" && !strings.Contains(got, tt.wantSnip) {
				t.Errorf("expected hint to contain %q, got %q", tt.wantSnip, got)
			}
		})
	}
}

func TestPersonaSpecialistHintCustom(t *testing.T) {
	tests := []struct {
		name   string
		prompt string
		want   string
	}{
		{"empty returns empty", "", ""},
		{"short prompt", "Be strict", "\nPersona lens (custom): Be strict"},
		{"long prompt truncated at 150", longString("abcde", 200), "\nPersona lens (custom): " + longString("abcde", 200)[:150] + "..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PersonaSpecialistHintCustom(tt.prompt)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseRepoSettings(t *testing.T) {
	tests := []struct {
		name    string
		input   json.RawMessage
		wantOK  bool
		wantStr string // persona field value if wantOK
	}{
		{"nil input", nil, false, ""},
		{"empty input", json.RawMessage{}, false, ""},
		{"invalid json", json.RawMessage(`{bad`), false, ""},
		{"empty object", json.RawMessage(`{}`), true, ""},
		{"with persona", json.RawMessage(`{"persona":"mentor"}`), true, "mentor"},
		{"with deep review", json.RawMessage(`{"deep_review":true}`), true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, ok := parseRepoSettings(tt.input)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && s.Persona != tt.wantStr {
				t.Errorf("Persona = %q, want %q", s.Persona, tt.wantStr)
			}
		})
	}
}

func TestLoadPersona(t *testing.T) {
	tests := []struct {
		name  string
		input json.RawMessage
		want  Persona
	}{
		{"nil settings", nil, PersonaDefault},
		{"empty settings", json.RawMessage(`{}`), PersonaDefault},
		{"empty persona field", json.RawMessage(`{"persona":""}`), PersonaDefault},
		{"valid persona", json.RawMessage(`{"persona":"mentor"}`), PersonaMentor},
		{"unknown persona falls back", json.RawMessage(`{"persona":"wizard"}`), PersonaDefault},
		{"invalid json", json.RawMessage(`{bad`), PersonaDefault},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := loadPersona(tt.input)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLoadCustomPersonaPrompt(t *testing.T) {
	tests := []struct {
		name  string
		input json.RawMessage
		want  string
	}{
		{"nil settings", nil, ""},
		{"empty object", json.RawMessage(`{}`), ""},
		{"present", json.RawMessage(`{"custom_persona_prompt":"be kind"}`), "be kind"},
		{"invalid json", json.RawMessage(`{bad`), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := loadCustomPersonaPrompt(tt.input)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsDeepReviewEnabled(t *testing.T) {
	tests := []struct {
		name  string
		input json.RawMessage
		want  bool
	}{
		{"nil defaults false", nil, false},
		{"empty object defaults false", json.RawMessage(`{}`), false},
		{"explicit true", json.RawMessage(`{"deep_review":true}`), true},
		{"explicit false", json.RawMessage(`{"deep_review":false}`), false},
		{"invalid json", json.RawMessage(`{bad`), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isDeepReviewEnabled(tt.input)
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// boolSettingTest is a reusable test case for bool-pointer settings that default true.
type boolSettingTest struct {
	name  string
	input json.RawMessage
	want  bool
}

// defaultTrueBoolTests returns standard test cases for settings that default to true.
func defaultTrueBoolTests(field string) []boolSettingTest {
	bTrue := `{"` + field + `":true}`
	bFalse := `{"` + field + `":false}`
	return []boolSettingTest{
		{"nil defaults true", nil, true},
		{"empty object defaults true", json.RawMessage(`{}`), true},
		{"explicit true", json.RawMessage(bTrue), true},
		{"explicit false", json.RawMessage(bFalse), false},
		{"invalid json", json.RawMessage(`{bad`), true},
	}
}

func TestIsCrossFileContextEnabled(t *testing.T) {
	for _, tt := range defaultTrueBoolTests("cross_file_context") {
		t.Run(tt.name, func(t *testing.T) {
			if got := isCrossFileContextEnabled(tt.input); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsBlastRadiusEnabled(t *testing.T) {
	for _, tt := range defaultTrueBoolTests("blast_radius") {
		t.Run(tt.name, func(t *testing.T) {
			if got := isBlastRadiusEnabled(tt.input); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsScenarioMemoryEnabled(t *testing.T) {
	for _, tt := range defaultTrueBoolTests("scenario_memory") {
		t.Run(tt.name, func(t *testing.T) {
			if got := isScenarioMemoryEnabled(tt.input); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsCodeSimulationEnabled(t *testing.T) {
	for _, tt := range defaultTrueBoolTests("code_simulation") {
		t.Run(tt.name, func(t *testing.T) {
			if got := isCodeSimulationEnabled(tt.input); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsPREnrichmentEnabled(t *testing.T) {
	for _, tt := range defaultTrueBoolTests("pr_enrichment") {
		t.Run(tt.name, func(t *testing.T) {
			if got := isPREnrichmentEnabled(tt.input); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsLearnPatternsEnabled(t *testing.T) {
	for _, tt := range defaultTrueBoolTests("learn_patterns") {
		t.Run(tt.name, func(t *testing.T) {
			if got := isLearnPatternsEnabled(tt.input); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsLearnConventionsEnabled(t *testing.T) {
	for _, tt := range defaultTrueBoolTests("learn_conventions") {
		t.Run(tt.name, func(t *testing.T) {
			if got := isLearnConventionsEnabled(tt.input); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsFileSynthesisEnabled(t *testing.T) {
	for _, tt := range defaultTrueBoolTests("file_synthesis") {
		t.Run(tt.name, func(t *testing.T) {
			if got := isFileSynthesisEnabled(tt.input); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsArchitectureGraphEnabled(t *testing.T) {
	for _, tt := range defaultTrueBoolTests("architecture_graph") {
		t.Run(tt.name, func(t *testing.T) {
			if got := isArchitectureGraphEnabled(tt.input); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidPersonas(t *testing.T) {
	expected := []Persona{
		PersonaDefault, PersonaSecurityAuditor, PersonaPerformanceEngineer,
		PersonaMentor, PersonaArchitect, PersonaStrict, PersonaAdversarial,
		PersonaFreshEyes, PersonaCustom,
	}
	for _, p := range expected {
		if !ValidPersonas[p] {
			t.Errorf("ValidPersonas missing %q", p)
		}
	}
	if ValidPersonas[Persona("bogus")] {
		t.Error("ValidPersonas should not contain bogus persona")
	}
}

