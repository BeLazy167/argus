package pipeline

import (
	"strings"
	"testing"

	"github.com/BeLazy167/argus/backend/internal/store"
)

// prRepo is the DB id of the repository the pull request is in; sibling is
// another repository inside the SAME installation, which the blast-radius walk
// reaches since #221 item 2.
const (
	prRepo  int64 = 11
	sibling int64 = 22
)

// TestDependentFetchPathsExcludesSiblingRepos pins the one rule every caller
// depends on: a path is only fetchable if it belongs to the pull request's own
// repository, because that is the only repository the caller has a ref for.
//
// Both callers pass owner/repo and HeadSHA from THIS pull request. Hand them a
// sibling repository's path and one of two things happens, neither detectable
// afterwards: the same path exists here too (`src/index.ts` is the routine case)
// so the fetch succeeds and an unrelated file is presented to the model as the
// dependent's source, or it does not exist and the dependent is dropped without
// a trace.
func TestDependentFetchPathsExcludesSiblingRepos(t *testing.T) {
	tests := []struct {
		name    string
		nodes   []store.CodeNode
		changed map[string]bool
		want    []string
	}{
		{
			name: "sibling repo dependent is never fetched, even on a colliding path",
			nodes: []store.CodeNode{
				{Name: "JobsPage", FilePath: "src/index.ts", RepoID: sibling, Depth: 1},
				{Name: "Router", FilePath: "api/router.go", RepoID: prRepo, Depth: 1},
			},
			want: []string{"api/router.go"},
		},
		{
			name: "only depth 1",
			nodes: []store.CodeNode{
				{FilePath: "api/seed.go", RepoID: prRepo, Depth: 0},
				{FilePath: "api/router.go", RepoID: prRepo, Depth: 1},
				{FilePath: "api/far.go", RepoID: prRepo, Depth: 2},
			},
			want: []string{"api/router.go"},
		},
		{
			name: "paths already in the diff are skipped",
			nodes: []store.CodeNode{
				{FilePath: "api/router.go", RepoID: prRepo, Depth: 1},
				{FilePath: "api/handlers/jobs.go", RepoID: prRepo, Depth: 1},
			},
			changed: map[string]bool{"api/handlers/jobs.go": true},
			want:    []string{"api/router.go"},
		},
		{
			name: "one entry per path, order preserved",
			nodes: []store.CodeNode{
				{Name: "A", FilePath: "api/router.go", RepoID: prRepo, Depth: 1},
				{Name: "B", FilePath: "api/router.go", RepoID: prRepo, Depth: 1},
				{Name: "C", FilePath: "api/store.go", RepoID: prRepo, Depth: 1},
			},
			want: []string{"api/router.go", "api/store.go"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changed := tt.changed
			if changed == nil {
				changed = map[string]bool{}
			}
			got := dependentFetchPaths(tt.nodes, prRepo, changed)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("got %v, want %v", got, tt.want)
				}
			}
		})
	}
}

// TestFormatBlastRadiusMarksSiblingRepos pins the prompt text. The listing is
// paths only, and two repositories in one installation routinely hold the same
// path — unlabelled, a sibling's `src/index.ts` reads as a file in the
// repository under review, and a finding can be raised against a path that means
// something else there.
func TestFormatBlastRadiusMarksSiblingRepos(t *testing.T) {
	out := FormatBlastRadius([]store.CodeNode{
		{Name: "JobsPage", Kind: "function", FilePath: "src/index.ts", RepoID: sibling, Depth: 1},
		{Name: "Router", Kind: "function", FilePath: "api/router.go", RepoID: prRepo, Depth: 1},
	}, nil, prRepo)

	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.Contains(line, "JobsPage"):
			if !strings.Contains(line, "another repository") {
				t.Errorf("sibling-repo dependent is not marked as such: %q", line)
			}
		case strings.Contains(line, "Router"):
			if strings.Contains(line, "another repository") {
				t.Errorf("same-repo dependent is marked as foreign: %q", line)
			}
		}
	}
}
