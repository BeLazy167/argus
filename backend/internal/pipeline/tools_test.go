package pipeline

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/BeLazy167/argus/backend/internal/memory"
)

// TestToolHandlerTagAllowed pins the agentic search_memory access check. The
// deep-review system prompt advertises exactly the new-shape repo + shared
// containers, so the handler MUST accept those and reject everything else —
// including the now-deleted legacy owner-prefixed containers and any other
// repo's container — otherwise a prompt-injected PR could pivot search_memory
// into repo Y's memory.
func TestToolHandlerTagAllowed(t *testing.T) {
	t.Parallel()
	const repo = "myrepo"
	th := NewToolHandler(nil, nil, repo, memory.NewThresholds())

	allowed := []string{
		memory.RepoTagNew(repo), // this repo's unified container
		memory.SharedTag,        // cross-repo patterns + org rules
	}
	for _, tag := range allowed {
		if !th.tagAllowed(tag) {
			t.Errorf("tagAllowed(%q) = false, want true", tag)
		}
	}

	denied := []string{
		memory.RepoTagNew("otherrepo"), // another repo's new-shape container
		"acme--patterns",               // legacy owner-scoped (deleted; no longer reachable)
		"acme--myrepo--reviews",        // legacy repo-scoped (deleted; no longer reachable)
		"evilorg--patterns",            // another owner's legacy container
		"",                             // empty tag
	}
	for _, tag := range denied {
		if th.tagAllowed(tag) {
			t.Errorf("tagAllowed(%q) = true, want false", tag)
		}
	}
}

// TestAgenticMemoryTagsMatchPrompt guards against prompt/validator drift: every
// container the deep-review system prompt advertises must pass the handler's
// access check, so the LLM can never receive "access denied" for a tag the
// prompt itself told it to use.
func TestAgenticMemoryTagsMatchPrompt(t *testing.T) {
	t.Parallel()
	const owner, repo = "acme", "myrepo"
	th := NewToolHandler(nil, nil, repo, memory.NewThresholds())

	tags := agenticMemoryTags(repo)
	if len(tags) == 0 {
		t.Fatal("agenticMemoryTags returned no tags")
	}

	prompt := buildAgenticSystemPrompt(owner, repo)
	for _, tag := range tags {
		if !th.tagAllowed(tag) {
			t.Errorf("prompt advertises %q but tagAllowed rejects it", tag)
		}
		if !strings.Contains(prompt, tag) {
			t.Errorf("agentic prompt missing advertised tag %q", tag)
		}
	}

	// Every advertised type filter must be accepted by the handler and appear
	// in the prompt, so the LLM never gets "invalid type" for a type the prompt
	// told it to use.
	for _, mt := range agenticMemoryTypes() {
		if !memoryTypeAllowed(mt.Value) {
			t.Errorf("prompt advertises type=%s but memoryTypeAllowed rejects it", mt.Value)
		}
		if !strings.Contains(prompt, "type="+mt.Value) {
			t.Errorf("agentic prompt missing advertised type=%s", mt.Value)
		}
	}

	// Reverse guard: every type= token the prompt advertises must be queryable —
	// catches a phantom filter (e.g. a stale type=topology or file_path claim)
	// slipping back into the prose.
	for _, m := range regexp.MustCompile(`type=([a-z_]+)`).FindAllStringSubmatch(prompt, -1) {
		if !memoryTypeAllowed(m[1]) {
			t.Errorf("agentic prompt advertises unqueryable type=%s", m[1])
		}
	}

	// The tool-schema enum must equal the helper, so the schema the LLM sees and
	// the validator share one source.
	props := memoryTools(repo)[0].Function.Parameters["properties"].(map[string]any)
	enum := props["type"].(map[string]any)["enum"].([]string)
	if !reflect.DeepEqual(enum, agenticMemoryTypeValues()) {
		t.Errorf("search_memory type enum %v != agenticMemoryTypeValues %v", enum, agenticMemoryTypeValues())
	}
}

// TestComposeReviewSystemPromptKeepsBriefing pins the core fix: the deterministic
// memory briefing must survive into the agentic system prompt — previously the
// agentic branch overwrote systemBase and discarded it. Both paths must carry
// the briefing, and the agentic path must still carry its search_memory
// instructions and (for specialists) the specialist overlay.
func TestComposeReviewSystemPromptKeepsBriefing(t *testing.T) {
	t.Parallel()
	const owner, repo = "acme", "myrepo"
	const briefing = "\n## Known False Positives (DO NOT re-flag these patterns)\n1. SENTINEL_BRIEFING\n"
	const base = "BASE_SYSTEM_PROMPT"
	const extra = "\nPERSONA_EXTRA"

	t.Run("agentic keeps briefing", func(t *testing.T) {
		t.Parallel()
		got := composeReviewSystemPrompt(base, owner, repo, "", true, briefing, extra)
		if !strings.Contains(got, "SENTINEL_BRIEFING") {
			t.Error("agentic prompt dropped the memory briefing")
		}
		if !strings.Contains(got, "## Memory Access") {
			t.Error("agentic prompt missing search_memory instructions")
		}
		if !strings.Contains(got, extra) {
			t.Error("agentic prompt dropped promptExtra")
		}
	})

	t.Run("agentic specialist keeps overlay and briefing", func(t *testing.T) {
		t.Parallel()
		got := composeReviewSystemPrompt(base, owner, repo, SpecialistBugHunter, true, briefing, extra)
		if !strings.Contains(got, "SENTINEL_BRIEFING") {
			t.Error("agentic specialist prompt dropped the memory briefing")
		}
		if !strings.Contains(got, specialistOverlay(SpecialistBugHunter)) {
			t.Error("agentic specialist prompt dropped the specialist overlay")
		}
	})

	t.Run("non-agentic keeps base and briefing", func(t *testing.T) {
		t.Parallel()
		got := composeReviewSystemPrompt(base, owner, repo, "", false, briefing, extra)
		if !strings.HasPrefix(got, base) {
			t.Error("non-agentic prompt should start with systemBase")
		}
		if !strings.Contains(got, "SENTINEL_BRIEFING") {
			t.Error("non-agentic prompt dropped the memory briefing")
		}
		if strings.Contains(got, "## Memory Access") {
			t.Error("non-agentic prompt should not carry agentic search_memory instructions")
		}
	})
}
