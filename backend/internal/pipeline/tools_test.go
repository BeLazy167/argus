package pipeline

import (
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
	th := NewToolHandler(nil, nil, repo)

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
	th := NewToolHandler(nil, nil, repo)

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
}
