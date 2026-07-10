package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPipelineHasNoDirectMemoryClient is the grep-of-record enforcing the
// memory-briefing refactor's core invariant: the pipeline talks to memory ONLY
// through the memory.Indexer interface (Briefing / SearchHints / SearchRuleContent
// / SearchScored / the writers), never a raw *memory.Client. The retrieval +
// prompt-render seam lives inside internal/memory; a new `*memory.Client` field,
// `.Client()` escape hatch, or direct `.Search(` here would re-open it.
func TestPipelineHasNoDirectMemoryClient(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	// Substrings that only appear when a raw Supermemory client leaks in.
	banned := []string{"memory.Client", ".Client()"}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		// This guard file names the banned tokens in prose; skip itself.
		if name == "memory_seam_test.go" {
			continue
		}
		data, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		src := string(data)
		for _, tok := range banned {
			if strings.Contains(src, tok) {
				t.Errorf("%s uses %q — the pipeline must reach memory only through the memory.Indexer interface, not a raw *memory.Client", name, tok)
			}
		}
	}
}
