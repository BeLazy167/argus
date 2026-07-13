package memory

import (
	"regexp"
	"strings"
	"testing"
)

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

// TestCustomIDSanitize pins the character-class rule Supermemory enforces on
// customId: alphanumeric + underscore + hyphen + colon, everything else → `-`.
// Every case here maps to a real failure observed in production logs.
func TestCustomIDSanitize(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "next_route_group_parens",
			in:   "src/app/(auth)/oauth/page.tsx",
			want: "src-app--auth--oauth-page-tsx",
		},
		{
			name: "next_dynamic_segment_brackets",
			in:   "src/app/(dashboard)/explore/projects/[slug]/page.tsx",
			want: "src-app--dashboard--explore-projects--slug--page-tsx",
		},
		{
			name: "owner_slash_repo",
			in:   "acme/webapp",
			want: "acme-webapp",
		},
		{
			name: "already_safe_idempotent",
			in:   "already_safe:id-123",
			want: "already_safe:id-123",
		},
		{
			name: "spaces_to_hyphens",
			in:   "with spaces in the middle",
			want: "with-spaces-in-the-middle",
		},
		{
			name: "empty_input_empty_output",
			in:   "",
			want: "",
		},
		{
			name: "preserves_alphanumeric_and_colon",
			in:   "review:2026-04-18:PR331",
			want: "review:2026-04-18:PR331",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CustomIDSanitize(tc.in); got != tc.want {
				t.Errorf("CustomIDSanitize(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// supermemoryAllowedRe codifies Supermemory's allowed char set so test
// assertions don't drift from production's error text.
var supermemoryAllowedRe = regexp.MustCompile(`^[a-zA-Z0-9_:-]+$`)

// TestSynthesisCustomID_NoForbiddenChars is the regression guard: every
// customId builder must produce output that passes Supermemory's char-class
// check. Prior bug produced IDs with `(`, `)`, `[`, `]`, `/`, `.`.
func TestSynthesisCustomID_NoForbiddenChars(t *testing.T) {
	cases := []struct {
		name  string
		owner string
		repo  string
		path  string
	}{
		{"next_auth_route", "acme", "webapp", "src/app/(auth)/oauth/page.tsx"},
		{"next_dynamic_slug", "acme", "webapp", "src/app/(dashboard)/explore/projects/[slug]/page.tsx"},
		{"deeply_nested", "org", "repo", "a/b/c/d/e/f/g/h.tsx"},
		{"with_dots", "org", "repo", "lib/file.test.tsx"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SynthesisCustomID(tc.owner, tc.repo, tc.path)
			if !supermemoryAllowedRe.MatchString(got) {
				t.Errorf("SynthesisCustomID(%q,%q,%q) = %q; contains forbidden chars", tc.owner, tc.repo, tc.path, got)
			}
		})
	}
}

// TestSynthesisCustomID_HashDisambiguates pins idx 24: distinct file paths that
// sanitize to the same string (`/` and `.` both -> `-`) must NOT collapse to the
// same customID, because SynthesisCustomID's short path previously had no hash.
func TestSynthesisCustomID_HashDisambiguates(t *testing.T) {
	a := SynthesisCustomID("org", "repo", "pkg/api-v1/client.go")
	b := SynthesisCustomID("org", "repo", "pkg/api/v1/client.go")
	if a == b {
		t.Errorf("distinct paths collide: both -> %q", a)
	}
	if len(a) > 100 || len(b) > 100 {
		t.Errorf("customID exceeds 100 chars: %d / %d", len(a), len(b))
	}
	// Deterministic.
	if a != SynthesisCustomID("org", "repo", "pkg/api-v1/client.go") {
		t.Error("SynthesisCustomID not deterministic")
	}
}

// TestSynthesisCustomID_LongRepoKeepsHash pins idx 23: when the ID exceeds 100
// chars the readable prefix is truncated but the disambiguating hash (living in
// the protected suffix) must survive, so two files under a long-named repo don't
// truncate to the same customID.
func TestSynthesisCustomID_LongRepoKeepsHash(t *testing.T) {
	longRepo := strings.Repeat("r", 88) // GitHub allows up to 100-char repo names
	a := SynthesisCustomID("o", longRepo, "src/aaaaaaaaaaaaaaaaaaaa/one.go")
	b := SynthesisCustomID("o", longRepo, "src/bbbbbbbbbbbbbbbbbbbb/two.go")
	if len(a) > 100 || len(b) > 100 {
		t.Fatalf("customID exceeds 100 chars: %d / %d", len(a), len(b))
	}
	if a == b {
		t.Errorf("long-repo distinct files collide after truncation: both -> %q", a)
	}
	if !strings.HasSuffix(a, "--synthesis") || !strings.HasSuffix(b, "--synthesis") {
		t.Errorf("synthesis suffix lost: %q / %q", a, b)
	}
}
