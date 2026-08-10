package pipeline

import (
	"strings"
	"testing"
)

// The built-in list and ValidPersonas must describe the SAME set, minus custom.
//
// This is the drift guard the issue asks for. Adding a ninth persona today means
// a new const, a ValidPersonas entry, a PersonaPromptOverlay case and a
// PersonaSpecialistHint case — four edits that can each be forgotten
// independently. Once BuiltinPersonas seeds the database, a fifth way to forget
// appears: the row never gets written, so the dashboard cannot show or edit it
// and it silently stays code-only.
//
// Set equality in both directions is what makes that impossible to miss.
func TestBuiltinPersonas_MatchValidPersonasExactly(t *testing.T) {
	inList := make(map[Persona]bool, len(BuiltinPersonas()))
	for _, b := range BuiltinPersonas() {
		if inList[b.Slug] {
			t.Errorf("persona %q listed twice — slug is the row's identity, so a duplicate would fight itself on upsert", b.Slug)
		}
		inList[b.Slug] = true
	}

	for p := range ValidPersonas {
		// custom is deliberately excluded: its text comes from repo settings,
		// not from the compiled-in switch, so there is nothing to seed.
		if p == PersonaCustom {
			if inList[p] {
				t.Errorf("custom must NOT be seeded as a built-in: its overlay is per-repo settings text, and a shared row would hand one repo's custom prompt to every other repo in the installation")
			}
			continue
		}
		if !inList[p] {
			t.Errorf("persona %q is valid but missing from BuiltinPersonas — it would be selectable yet invisible in the dashboard", p)
		}
	}

	for _, b := range BuiltinPersonas() {
		if !ValidPersonas[b.Slug] {
			t.Errorf("BuiltinPersonas lists %q, which ValidPersonas rejects — resolution would refuse a persona the UI offers", b.Slug)
		}
	}
}

// The seeded text must BE the switch's text, not a copy of it.
//
// A copy is the failure this whole change is meant to remove: two sources for
// one prompt, drifting the first time somebody edits the switch and not the
// seed. Reading through the same functions the review path reads makes drift
// unrepresentable rather than merely tested.
func TestBuiltinPersonas_TextComesFromTheSwitch(t *testing.T) {
	for _, b := range BuiltinPersonas() {
		if got, want := b.PromptOverlay, PersonaPromptOverlay(b.Slug); got != want {
			t.Errorf("persona %q overlay is a copy, not the switch's text:\n got %q\nwant %q", b.Slug, got, want)
		}
		if got, want := b.SpecialistHint, PersonaSpecialistHint(b.Slug); got != want {
			t.Errorf("persona %q hint is a copy, not the switch's text:\n got %q\nwant %q", b.Slug, got, want)
		}
	}
}

// Every built-in needs a display name, because naming one is the entire point of
// the issue: today a custom persona renders as the literal string "custom".
func TestBuiltinPersonas_HaveDisplayNames(t *testing.T) {
	for _, b := range BuiltinPersonas() {
		if strings.TrimSpace(b.Name) == "" {
			t.Errorf("persona %q has no display name", b.Slug)
		}
		if b.Name == string(b.Slug) {
			t.Errorf("persona %q display name is the raw slug %q — the dashboard would show snake_case", b.Slug, b.Name)
		}
	}
}

// default carries no overlay, and that is correct: it is the absence of a
// persona, not a persona that says "be default". Asserting it explicitly stops
// somebody "fixing" the empty string by inventing prompt text for it, which
// would change every review that never chose a persona at all.
func TestBuiltinPersonas_DefaultCarriesNoOverlay(t *testing.T) {
	for _, b := range BuiltinPersonas() {
		if b.Slug != PersonaDefault {
			continue
		}
		if b.PromptOverlay != "" {
			t.Errorf("default persona must add no overlay, got %q — this text would join EVERY review that never picked a persona", b.PromptOverlay)
		}
		return
	}
	t.Fatal("default persona missing from BuiltinPersonas")
}
