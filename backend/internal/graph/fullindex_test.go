package graph

import (
	"slices"
	"strings"
	"testing"
)

// Capped generations must retry failures promptly and never revisit ready files.
func TestSelectPendingFiles(t *testing.T) {
	files := []string{"c.go", "a.go", "b.go"}
	tests := []struct {
		name          string
		ready         map[string]struct{}
		cap           int
		want          []string
		wantRemaining int
	}{
		{name: "uncapped sorts all", cap: 0, want: []string{"a.go", "b.go", "c.go"}},
		{name: "cap reports remaining", cap: 2, want: []string{"a.go", "b.go"}, wantRemaining: 1},
		{name: "ready files are skipped", ready: map[string]struct{}{"a.go": {}}, cap: 2, want: []string{"b.go", "c.go"}},
		{name: "failed file is absent from ready and retried first", ready: map[string]struct{}{"b.go": {}, "c.go": {}}, cap: 1, want: []string{"a.go"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, remaining := selectPendingFiles(files, tt.ready, tt.cap)
			if !slices.Equal(got, tt.want) || remaining != tt.wantRemaining {
				t.Fatalf("selected/remaining = %v/%d, want %v/%d", got, remaining, tt.want, tt.wantRemaining)
			}
		})
	}
}

// Only source files are candidates. The tree of a real repo is mostly not code,
// and every non-source entry that slipped through would cost one GitHub API
// call to fetch and parse into nothing.
func TestSourceFileFilter(t *testing.T) {
	in := []string{
		"main.go", "web/app.tsx", "README.md", "go.sum",
		"assets/logo.png", "src/x.py", "vendor.lock",
	}
	got := filterSourceFiles(in)

	for _, want := range []string{"main.go", "web/app.tsx", "src/x.py"} {
		if !contains(got, want) {
			t.Errorf("dropped source file %q", want)
		}
	}
	for _, unwanted := range []string{"README.md", "go.sum", "assets/logo.png", "vendor.lock"} {
		if contains(got, unwanted) {
			t.Errorf("kept non-source file %q — one wasted API fetch per entry", unwanted)
		}
	}
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if strings.EqualFold(h, needle) {
			return true
		}
	}
	return false
}

func TestFullGenerationUsesFileSymbolPhysicalLOC(t *testing.T) {
	tests := []struct {
		name, content      string
		wantStart, wantEnd int
	}{
		{name: "empty", content: "", wantStart: 0, wantEnd: 0},
		{name: "one line no newline", content: "package p", wantStart: 1, wantEnd: 1},
		{name: "trailing newline terminates line", content: "package p\n", wantStart: 1, wantEnd: 1},
		{name: "multiple lines", content: "package p\nfunc F() {}\n", wantStart: 1, wantEnd: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fileSymbol("a.go", tt.content)
			if got.Kind != "file" || got.Name != "a.go" || got.FilePath != "a.go" || got.LineStart != tt.wantStart || got.LineEnd != tt.wantEnd {
				t.Fatalf("file symbol = %+v, want lines %d-%d", got, tt.wantStart, tt.wantEnd)
			}
		})
	}
}

func TestDescribeNodeResolutionKeepsAmbiguityExplicit(t *testing.T) {
	keys := map[string]int64{nodeKey("same.go", "Local"): 1}
	names := map[string][]int64{
		"Local":  {1, 2},
		"Unique": {3},
		"Dup":    {4, 5},
	}
	tests := []struct {
		name, sourceFile, target string
		wantID                   int64
		wantStatus               nodeResolution
	}{
		{name: "same file wins", sourceFile: "same.go", target: "Local", wantID: 1, wantStatus: resolutionResolved},
		{name: "unique repo target resolves", sourceFile: "same.go", target: "Unique", wantID: 3, wantStatus: resolutionResolved},
		{name: "duplicate stays ambiguous", sourceFile: "same.go", target: "Dup", wantStatus: resolutionAmbiguous},
		{name: "missing stays unresolved", sourceFile: "same.go", target: "Missing", wantStatus: resolutionUnresolved},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, status := describeNodeResolution(tt.sourceFile, tt.target, keys, names)
			if id != tt.wantID || status != tt.wantStatus {
				t.Fatalf("resolution = %d/%s, want %d/%s", id, status, tt.wantID, tt.wantStatus)
			}
		})
	}
}
