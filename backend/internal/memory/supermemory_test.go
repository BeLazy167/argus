package memory

import (
	"strings"
	"testing"
)

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

func TestFormatPositivePattern(t *testing.T) {
	got := FormatPositivePattern("bug", "api/handler.go", 42, "Good edge-case handling for empty input")
	want := "POSITIVE_PATTERN: [bug] api/handler.go:42 — Good edge-case handling for empty input"
	if got != want {
		t.Errorf("FormatPositivePattern = %q, want %q", got, want)
	}
}

func TestFormatPositivePattern_Truncates(t *testing.T) {
	longBody := strings.Repeat("x", 300)
	got := FormatPositivePattern("security", "file.go", 1, longBody)
	if len(got) > 200 {
		t.Errorf("FormatPositivePattern len = %d, want <= 200", len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("FormatPositivePattern should end with ellipsis, got %q", got[len(got)-10:])
	}
	if !strings.HasPrefix(got, "POSITIVE_PATTERN: [security]") {
		t.Errorf("FormatPositivePattern should start with prefix, got %q", got[:40])
	}
}

func TestFormatPositivePattern_ShortBody(t *testing.T) {
	got := FormatPositivePattern("style", "f.go", 1, "ok")
	if strings.HasSuffix(got, "...") {
		t.Errorf("short body should not be truncated: %q", got)
	}
}
