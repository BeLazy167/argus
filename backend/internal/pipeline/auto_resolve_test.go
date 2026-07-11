// Package pipeline — auto_resolve_test.go pins the per-thread decision rule
// used by autoResolveStaleComments (orchestrator.go). The decision logic is
// extracted into decideAutoResolveThread so we can exercise it without a
// GitHub fake; the surrounding counter bookkeeping + ResolveReviewThread call
// live in autoResolveStaleComments and are covered by integration tests.
//
// Why a dedicated test: the resolved-key format ("<path>:<line>") is the
// join key migration 041 uses to dedupe prior-review findings in the async
// cross-PR stage. A drift on either side (writer or reader) would silently
// stop the filter from applying — caught here by pinning the exact format,
// the ±3-line proximity window, and the file-level-fallback exclusion.
package pipeline

import (
	"testing"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
)

// TestAutoResolveStaleComments_EmitsResolvedKeys covers the invariants the
// migration-041 consumer depends on. The input shape mirrors what arrives
// from diff.PatchSet.ChangedLines (per-file changed-line set) and the
// ReviewThread rows ListReviewThreads returns.
func TestAutoResolveStaleComments_EmitsResolvedKeys(t *testing.T) {
	const lineProximity = 3

	cases := []struct {
		name             string
		thread           ghpkg.ReviewThread
		changedFiles     map[string]bool
		fileChangedLines map[string]map[int]bool
		wantKind         autoResolveDecisionKind
		wantJoinKey      string
	}{
		{
			name: "file-level thread (line=0) → file-level fallback, no join key",
			thread: ghpkg.ReviewThread{
				Path: "auth.go",
				Line: 0,
			},
			changedFiles: map[string]bool{"auth.go": true},
			// no changed lines for auth.go (or empty set) triggers fallback.
			fileChangedLines: map[string]map[int]bool{"auth.go": {}},
			wantKind:         autoResolveFileHit,
			wantJoinKey:      "",
		},
		{
			name: "line-level thread within ±3 → line-hit + join key uses path:line exactly",
			thread: ghpkg.ReviewThread{
				Path: "auth.go",
				Line: 10,
			},
			changedFiles: map[string]bool{"auth.go": true},
			// Changed line 13 is exactly at the proximity boundary (10+3).
			fileChangedLines: map[string]map[int]bool{"auth.go": {13: true}},
			wantKind:         autoResolveLineHit,
			wantJoinKey:      "auth.go:10",
		},
		{
			name: "line-level thread outside ±3 → skip (no resolve, no join key)",
			thread: ghpkg.ReviewThread{
				Path: "auth.go",
				Line: 10,
			},
			changedFiles: map[string]bool{"auth.go": true},
			// Changed line 14 is one past the proximity window (10+4).
			fileChangedLines: map[string]map[int]bool{"auth.go": {14: true}},
			wantKind:         autoResolveSkip,
			wantJoinKey:      "",
		},
		{
			name: "thread in unchanged file → skip regardless of line proximity",
			thread: ghpkg.ReviewThread{
				Path: "unchanged.go",
				Line: 10,
			},
			changedFiles:     map[string]bool{"auth.go": true},
			fileChangedLines: map[string]map[int]bool{"auth.go": {10: true}},
			wantKind:         autoResolveSkip,
			wantJoinKey:      "",
		},
		{
			name: "line-level thread, no parsed hunks → file-level fallback",
			thread: ghpkg.ReviewThread{
				Path: "auth.go",
				Line: 50,
			},
			changedFiles: map[string]bool{"auth.go": true},
			// File is known-changed (e.g. GH metadata says so) but we have
			// no hunk data — fall back to file-level.
			fileChangedLines: map[string]map[int]bool{"auth.go": nil},
			wantKind:         autoResolveFileHit,
			wantJoinKey:      "",
		},
		{
			name: "line-level thread at exact changed line → line-hit",
			thread: ghpkg.ReviewThread{
				Path: "auth.go",
				Line: 17,
			},
			changedFiles:     map[string]bool{"auth.go": true},
			fileChangedLines: map[string]map[int]bool{"auth.go": {17: true}},
			wantKind:         autoResolveLineHit,
			wantJoinKey:      "auth.go:17",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := decideAutoResolveThread(tc.thread, tc.changedFiles, tc.fileChangedLines, lineProximity)
			if got.kind != tc.wantKind {
				t.Errorf("kind = %v, want %v", got.kind, tc.wantKind)
			}
			if got.joinKey != tc.wantJoinKey {
				t.Errorf("joinKey = %q, want %q", got.joinKey, tc.wantJoinKey)
			}
		})
	}
}

// TestAutoResolveDecision_FileLevelFallbackHasNoJoinKey pins the migration-041
// invariant: file-level resolves MUST NOT append to resolvedKeys. A "path:0"
// entry would never match any Finding (which is always line-addressed) and
// would only bloat the array stored in auto_resolve_events.resolved_thread_keys.
func TestAutoResolveDecision_FileLevelFallbackHasNoJoinKey(t *testing.T) {
	got := decideAutoResolveThread(
		ghpkg.ReviewThread{Path: "a.go", Line: 0},
		map[string]bool{"a.go": true},
		map[string]map[int]bool{"a.go": {}},
		3,
	)
	if got.kind != autoResolveFileHit {
		t.Fatalf("kind = %v, want autoResolveFileHit", got.kind)
	}
	if got.joinKey != "" {
		t.Fatalf("file-level fallback leaked a joinKey (%q); migration 041 would bloat with unmatchable entries", got.joinKey)
	}
}

// TestResolvedByReplyBody pins the convergence breadcrumb posted on a thread
// before auto-resolving it: short-sha rendering plus the empty-SHA fallback.
func TestResolvedByReplyBody(t *testing.T) {
	tests := []struct {
		name string
		sha  string
		want string
	}{
		{"full sha shortened to 7", "abcdef0123456789", "✅ Resolved by `abcdef0` — the flagged lines were modified in this push."},
		{"short sha kept verbatim", "abc12", "✅ Resolved by `abc12` — the flagged lines were modified in this push."},
		{"empty sha degrades gracefully", "", "✅ Resolved by a newer push — the flagged lines were modified."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolvedByReplyBody(tt.sha); got != tt.want {
				t.Errorf("resolvedByReplyBody(%q) = %q, want %q", tt.sha, got, tt.want)
			}
		})
	}
}
