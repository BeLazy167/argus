package pipeline

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/BeLazy167/argus/backend/internal/store"
)

type fakePersonaReader struct {
	row  *store.Persona
	err  error
	slug string
}

func (f *fakePersonaReader) GetPersona(_ context.Context, _ int64, slug string) (*store.Persona, error) {
	f.slug = slug
	return f.row, f.err
}

// The migration is behaviour-preserving: before any row exists, a review reads
// exactly the text it read when these lived in a switch.
func TestResolvePersona_FallsBackToCompiledInText(t *testing.T) {
	got := resolvePersona(context.Background(), &fakePersonaReader{}, 42, PersonaSecurityAuditor, "")

	if got.Overlay != PersonaPromptOverlay(PersonaSecurityAuditor) {
		t.Errorf("overlay is not the compiled-in text:\n%s", got.Overlay)
	}
	if got.SpecialistHint != PersonaSpecialistHint(PersonaSecurityAuditor) {
		t.Errorf("specialist hint is not the compiled-in text: %q", got.SpecialistHint)
	}
}

// A stored persona shadows the built-in of the same slug — the supported way to
// retune one without losing the name a team already uses.
func TestResolvePersona_StoredRowWins(t *testing.T) {
	r := &fakePersonaReader{row: &store.Persona{
		Slug: "security_auditor", PromptOverlay: "OUR OWN SECURITY RULES", SpecialistHint: "ours",
	}}

	got := resolvePersona(context.Background(), r, 42, PersonaSecurityAuditor, "")

	if got.Overlay != "OUR OWN SECURITY RULES" {
		t.Errorf("stored overlay ignored: %q", got.Overlay)
	}
	if r.slug != "security_auditor" {
		t.Errorf("looked up %q, want the persona's slug", r.slug)
	}
}

// A read failure must not stop a review. The compiled-in text is always valid,
// so degrading to it is strictly better than failing the run.
func TestResolvePersona_ReadErrorFallsBack(t *testing.T) {
	r := &fakePersonaReader{err: errors.New("db down")}

	got := resolvePersona(context.Background(), r, 42, PersonaMentor, "")

	if got.Overlay != PersonaPromptOverlay(PersonaMentor) {
		t.Error("a read error must fall back, not blank the persona")
	}
}

// Custom keeps its own path: its text lives in settings, not in a row, and the
// reader must not be consulted for it.
func TestResolvePersona_CustomUsesSettingsText(t *testing.T) {
	r := &fakePersonaReader{row: &store.Persona{PromptOverlay: "should not be used"}}

	got := resolvePersona(context.Background(), r, 42, PersonaCustom, "review like a paranoid SRE")

	if !strings.Contains(got.Overlay, "paranoid SRE") {
		t.Errorf("custom persona lost its settings text: %q", got.Overlay)
	}
	if r.slug != "" {
		t.Errorf("custom must not hit the personas table, looked up %q", r.slug)
	}
}

// A hand-constructed pipeline (tests, and any embedder without a store) must
// still resolve rather than panic.
func TestResolvePersona_NilReader(t *testing.T) {
	got := resolvePersona(context.Background(), nil, 42, PersonaArchitect, "")
	if got.Overlay != PersonaPromptOverlay(PersonaArchitect) {
		t.Error("nil reader must fall back to the compiled-in overlay")
	}
}
