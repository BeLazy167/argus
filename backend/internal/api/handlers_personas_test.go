package api

import "testing"

// A persona's overlay is appended to the review system prompt on EVERY review
// the repo runs, so these bounds are a spend control as much as a validation
// rule: an unbounded overlay is an unbounded per-review token cost that no
// budget limit catches, because the budget measures the diff, not the prompt.
func TestValidatePersonaInput(t *testing.T) {
	long := func(n int) string {
		b := make([]byte, n)
		for i := range b {
			b[i] = 'a'
		}
		return string(b)
	}

	cases := []struct {
		name    string
		in      personaInput
		wantErr bool
		reason  string
	}{
		{
			name: "ordinary persona",
			in:   personaInput{Slug: "house_style", Name: "House Style", PromptOverlay: "Prefer table-driven tests."},
		},
		{
			name:    "missing slug",
			in:      personaInput{Name: "X", PromptOverlay: "y"},
			wantErr: true, reason: "slug is the row identity and the value stored in repo settings",
		},
		{
			name:    "missing name",
			in:      personaInput{Slug: "x", PromptOverlay: "y"},
			wantErr: true, reason: "a nameless persona is the exact bug the issue reported",
		},
		{
			name:    "missing overlay",
			in:      personaInput{Slug: "x", Name: "X"},
			wantErr: true, reason: "a persona with no overlay silently does nothing",
		},
		{
			name:    "slug with spaces",
			in:      personaInput{Slug: "house style", Name: "X", PromptOverlay: "y"},
			wantErr: true, reason: "slug travels in settings JSON and URL paths",
		},
		{
			name:    "slug with path traversal",
			in:      personaInput{Slug: "../admin", Name: "X", PromptOverlay: "y"},
			wantErr: true, reason: "slug is a URL path segment",
		},
		{
			name:    "uppercase slug",
			in:      personaInput{Slug: "HouseStyle", Name: "X", PromptOverlay: "y"},
			wantErr: true, reason: "case-only variants would look identical in the UI but be different rows",
		},
		{
			name: "slug may shadow a built-in",
			in:   personaInput{Slug: "security_auditor", Name: "Our Security", PromptOverlay: "y"},
			// Deliberately allowed: shadowing is the supported way to retune a
			// built-in without losing the name everyone already selected.
		},
		{
			name:    "overlay past the cap",
			in:      personaInput{Slug: "x", Name: "X", PromptOverlay: long(maxPersonaOverlay + 1)},
			wantErr: true, reason: "unbounded prompt cost on every review",
		},
		{
			name: "overlay exactly at the cap",
			in:   personaInput{Slug: "x", Name: "X", PromptOverlay: long(maxPersonaOverlay)},
		},
		{
			name:    "hint past the cap",
			in:      personaInput{Slug: "x", Name: "X", PromptOverlay: "y", SpecialistHint: long(maxPersonaHint + 1)},
			wantErr: true, reason: "the hint is appended once per specialist, so it multiplies",
		},
		{
			name:    "name past the cap",
			in:      personaInput{Slug: "x", Name: long(maxPersonaName + 1), PromptOverlay: "y"},
			wantErr: true, reason: "the name renders in the PR progress comment",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePersonaInput(tc.in)
			if tc.wantErr && err == nil {
				t.Errorf("expected rejection (%s), got none", tc.reason)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("expected acceptance, got %v", err)
			}
		})
	}
}
