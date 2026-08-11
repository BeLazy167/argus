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
