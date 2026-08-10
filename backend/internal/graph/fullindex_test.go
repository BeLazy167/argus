package graph

import (
	"fmt"
	"strings"
	"testing"
)

// A full index accumulates per-file symbol and edge slices across EVERY file so
// the cross-file edge-resolution pass can see them all. That is the part which
// does not stream, and it is why the previous full-index caller OOM'd a 512 MB
// VM on an 890-file repo and was deleted rather than fixed.
//
// The cap is the thing that makes re-wiring it safe. These tests pin its two
// non-obvious properties: it must be deterministic, and it must be loud.
func TestSelectIndexableFiles(t *testing.T) {
	mk := func(n int) []string {
		out := make([]string, 0, n)
		for i := range n {
			out = append(out, fmt.Sprintf("src/pkg%03d/file.go", i))
		}
		return out
	}

	t.Run("under the cap, everything is indexed and nothing is reported dropped", func(t *testing.T) {
		in := mk(10)
		got, dropped, _ := selectIndexableFiles(in, 100, 0)
		if len(got) != 10 || dropped != 0 {
			t.Errorf("got %d files and %d dropped, want 10 and 0", len(got), dropped)
		}
	})

	t.Run("over the cap, the excess is reported rather than silently lost", func(t *testing.T) {
		in := mk(150)
		got, dropped, _ := selectIndexableFiles(in, 100, 0)
		if len(got) != 100 {
			t.Errorf("indexed %d files, want the cap of 100", len(got))
		}
		// A cap that truncates silently reads, from every dashboard above it, as
		// "this repo is fully indexed" — the exact false confidence the whole
		// issue is about. The count is what lets the caller log it.
		if dropped != 50 {
			t.Errorf("reported %d dropped, want 50", dropped)
		}
	})

	t.Run("the same offset always names the same window", func(t *testing.T) {
		in := mk(150)
		first, _, _ := selectIndexableFiles(in, 100, 40)
		second, _, _ := selectIndexableFiles(in, 100, 40)
		for i := range first {
			if first[i] != second[i] {
				t.Fatalf("window differs between runs at %d: %q vs %q", i, first[i], second[i])
			}
		}
	})

	// The property that matters most, and the one a fixed prefix fails: over
	// successive runs every file must eventually be indexed. A cap that always
	// took sorted[:100] would exclude the same alphabetical tail forever — on a
	// monorepo where web/ sorts after backend/, an entire top-level tree would
	// never enter the graph while the repo reported as indexed.
	t.Run("the window rotates until the whole repo is covered", func(t *testing.T) {
		in := mk(250)
		seen := map[string]bool{}
		offset := 0
		for range 10 { // more than enough cycles at 100 per run
			var window []string
			window, _, offset = selectIndexableFiles(in, 100, offset)
			for _, f := range window {
				seen[f] = true
			}
			if len(seen) == len(in) {
				break
			}
		}
		if len(seen) != len(in) {
			t.Errorf("after repeated runs only %d/%d files were ever indexed — the rest are permanently invisible to the graph", len(seen), len(in))
		}
	})

	t.Run("an out-of-range offset restarts rather than indexing nothing", func(t *testing.T) {
		in := mk(150)
		got, _, _ := selectIndexableFiles(in, 100, 9999)
		if len(got) != 100 {
			t.Errorf("indexed %d files from a stale offset, want a full window", len(got))
		}
	})

	t.Run("a zero or negative cap means no cap", func(t *testing.T) {
		in := mk(50)
		got, dropped, _ := selectIndexableFiles(in, 0, 0)
		if len(got) != 50 || dropped != 0 {
			t.Errorf("got %d/%d, want all 50 indexed with none dropped", len(got), dropped)
		}
	})
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
