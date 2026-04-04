package memory

import "testing"

func TestNegativePatternTag(t *testing.T) {
	got := NegativePatternTag("acme", "widget")
	want := "acme--widget--negative_patterns"
	if got != want {
		t.Errorf("NegativePatternTag = %q, want %q", got, want)
	}
}

func TestNegativePatternTag_Sanitizes(t *testing.T) {
	got := NegativePatternTag("acme/org", "my.repo")
	want := "acme-org--my-repo--negative_patterns"
	if got != want {
		t.Errorf("NegativePatternTag with special chars = %q, want %q", got, want)
	}
}

func TestPositivePatternTag(t *testing.T) {
	got := PositivePatternTag("acme", "widget")
	want := "acme--widget--positive_patterns"
	if got != want {
		t.Errorf("PositivePatternTag = %q, want %q", got, want)
	}
}

func TestPositivePatternTag_Sanitizes(t *testing.T) {
	got := PositivePatternTag("org:team", "repo~v2")
	want := "org-team--repo-v2--positive_patterns"
	if got != want {
		t.Errorf("PositivePatternTag with special chars = %q, want %q", got, want)
	}
}
