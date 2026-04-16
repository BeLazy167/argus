package pipeline

import (
	"strings"
	"testing"

	"github.com/BeLazy167/argus/backend/pkg/diff"
)

// TestAssessPRScope covers the scope-warning heuristic: large file count,
// multi-area (distinct top-level dir count), and the reasonable-PR happy path.
// Thresholds tested: >= 25 files (large), >= 5 top-level dirs (multi-area).
func TestAssessPRScope(t *testing.T) {
	mkFile := func(path string) diff.FileDiff { return diff.FileDiff{NewName: path} }
	mkRun := func(paths ...string) *PipelineRun {
		files := make([]diff.FileDiff, len(paths))
		for i, p := range paths {
			files[i] = mkFile(p)
		}
		return &PipelineRun{Diff: &diff.PatchSet{Files: files}}
	}

	// 25 files in a single area — triggers the file-count branch only.
	largePRSingleArea := make([]string, 25)
	for i := range largePRSingleArea {
		largePRSingleArea[i] = "backend/file.go"
	}

	// 24 files across 5 areas — triggers multi-area without file-count.
	multiArea := []string{
		"backend/a.go", "web/b.ts", "scripts/c.sh", "docs/d.md", "config/e.yml",
		"backend/a2.go", "web/b2.ts", "scripts/c2.sh", "docs/d2.md", "config/e2.yml",
		"backend/a3.go", "web/b3.ts", "scripts/c3.sh", "docs/d3.md", "config/e3.yml",
		"backend/a4.go", "web/b4.ts", "scripts/c4.sh", "docs/d4.md", "config/e4.yml",
		"backend/a5.go", "web/b5.ts", "scripts/c5.sh", "docs/d5.md",
	}

	// 25 files across 5 areas — triggers both.
	both := append([]string{}, multiArea...)
	both = append(both, "config/extra.yml")

	tests := []struct {
		name       string
		run        *PipelineRun
		wantEmpty  bool
		wantSubstr []string // all must be present when wantEmpty is false
	}{
		{
			name:      "nil run",
			run:       nil,
			wantEmpty: true,
		},
		{
			name:      "nil diff",
			run:       &PipelineRun{Diff: nil},
			wantEmpty: true,
		},
		{
			name:      "empty file list",
			run:       &PipelineRun{Diff: &diff.PatchSet{Files: nil}},
			wantEmpty: true,
		},
		{
			name:      "small focused PR",
			run:       mkRun("backend/server.go", "backend/server_test.go"),
			wantEmpty: true,
		},
		{
			name:      "24 files single area — below file threshold, single area",
			run:       mkRun(largePRSingleArea[:24]...),
			wantEmpty: true,
		},
		{
			name:       "25 files single area — hits file threshold only",
			run:        mkRun(largePRSingleArea...),
			wantEmpty:  false,
			wantSubstr: []string{"Scope concern", "25 files"},
		},
		{
			name:       "multi-area below file threshold",
			run:        mkRun(multiArea...),
			wantEmpty:  false,
			wantSubstr: []string{"Scope concern", "5 top-level areas", "backend", "web"},
		},
		{
			name:       "both triggers",
			run:        mkRun(both...),
			wantEmpty:  false,
			wantSubstr: []string{"Scope concern", "25 files", "5 top-level areas"},
		},
		{
			name:       "falls back to OldName when NewName empty (deleted files)",
			run:        &PipelineRun{Diff: &diff.PatchSet{Files: make25DeletedFiles()}},
			wantEmpty:  false,
			wantSubstr: []string{"25 files"},
		},
		{
			name:      "files without a directory prefix don't count toward areas",
			run:       mkRun("README.md", "LICENSE", "Dockerfile", "main.go", "go.mod"),
			wantEmpty: true, // no dirs, only 5 files → under both thresholds
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := assessPRScope(tc.run)
			if tc.wantEmpty {
				if got != "" {
					t.Fatalf("expected empty warning, got: %q", got)
				}
				return
			}
			for _, sub := range tc.wantSubstr {
				if !strings.Contains(got, sub) {
					t.Errorf("warning missing substring %q\nfull output: %s", sub, got)
				}
			}
		})
	}
}

// make25DeletedFiles returns 25 FileDiff entries with only OldName set,
// exercising the NewName-empty fallback branch.
func make25DeletedFiles() []diff.FileDiff {
	files := make([]diff.FileDiff, 25)
	for i := range files {
		files[i] = diff.FileDiff{OldName: "backend/removed.go", Status: diff.FileDeleted}
	}
	return files
}
