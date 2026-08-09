package main

import "testing"

// TestShortNameReducesToRepo: container tags are built from the repo segment,
// so a full name that still carries its owner would produce a tag no writer
// ever used — exporting an empty container while the real one goes untouched.
func TestShortNameReducesToRepo(t *testing.T) {
	cases := map[string]string{
		"acme/widget":      "widget",
		"widget":           "widget",
		"acme/sub/widget":  "widget", // defensive: last segment wins
		"":                 "",
		"acme/":            "",
		"/widget":          "widget",
		"Acme/Widget-Repo": "Widget-Repo",
		"acme/_shared":     "_shared", // RepoTagNew disambiguates this one
	}
	for in, want := range cases {
		if got := shortName(in); got != want {
			t.Errorf("shortName(%q) = %q, want %q", in, got, want)
		}
	}
}
