package pipeline

import (
	"strconv"
	"strings"
)

// Anchor is the location fingerprint of a review finding or a prior review
// thread: the file it sits on, its (1-based) line, and its category. The three
// re-review call sites — incremental prior-dedup, thread auto-resolve, and the
// cross-PR auto-resolved-thread filter — all answer the same question ("is this
// new finding the same location as a prior one?") and share this shape so the
// proximity rule can no longer drift between them.
type Anchor struct {
	Path     string
	Line     int
	Category string
}

// Matcher decides whether two Anchors name the same finding location under a
// site-specific policy. It is the single predicate that replaces three formerly
// divergent proximity rules:
//
//   - incremental prior-dedup:    Matcher{Proximity: 10, UseCategory: true}
//     Wide window absorbs line shifts introduced by the re-push; the category
//     guard keeps distinct issues on the same line from collapsing together.
//   - thread auto-resolve:        Matcher{Proximity: 3, UseCategory: false}
//     Tight window; a modified line near the thread resolves it regardless of
//     category (review threads carry no category).
//   - cross-PR resolved filter:   Matcher{Proximity: 0, UseCategory: false}
//     Exact "<path>:<line>" identity — the persisted migration-041 join key.
type Matcher struct {
	// Proximity is the maximum absolute line delta (inclusive) at which two
	// anchors on the same path still count as the same location. 0 requires the
	// lines to be equal.
	Proximity int
	// UseCategory, when true, additionally requires the two anchors to share a
	// category (case-insensitive). Sites whose anchors carry no category leave
	// this false.
	UseCategory bool
}

// Matches reports whether a and b name the same finding location under m's
// policy: same path, category agreement when required, and a line delta within
// the proximity window.
func (m Matcher) Matches(a, b Anchor) bool {
	if a.Path != b.Path {
		return false
	}
	if m.UseCategory && !strings.EqualFold(a.Category, b.Category) {
		return false
	}
	delta := a.Line - b.Line
	if delta < 0 {
		delta = -delta
	}
	return delta <= m.Proximity
}

// parseAnchorKey splits a "<path>:<line>" join key (the shape produced by
// findingKey / the auto-resolve joinKey) back into an Anchor so the cross-PR
// filter can route it through the shared Matcher. It splits on the LAST colon
// so paths containing colons round-trip. Returns ok=false for colon-less or
// non-numeric-line keys (defensive against sqlc array-scanning quirks) — such
// keys simply never match, preserving the prior set-membership behaviour where
// a malformed key could never equal a "<path>:<int>" finding key.
func parseAnchorKey(key string) (Anchor, bool) {
	idx := strings.LastIndex(key, ":")
	if idx < 0 {
		return Anchor{}, false
	}
	line, err := strconv.Atoi(key[idx+1:])
	if err != nil {
		return Anchor{}, false
	}
	return Anchor{Path: key[:idx], Line: line}, true
}
