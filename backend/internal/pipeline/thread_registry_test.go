// Package pipeline — thread_registry_test.go pins the ThreadRegistry (#162)
// lookup contract: dismissing finding X must target exactly X's own GitHub
// review thread, not a neighbouring finding's picked by line proximity.
//
// The whole point of the registry is to retire the fuzzy "<path>:<line>"
// matching: two findings on adjacent lines in the same file are indistinguishable
// to a proximity match but resolve to distinct thread node ids here. The fake
// store below returns each finding's own stored link, and the test proves the
// resolver returns X's node id — never its neighbour's — and reports "no thread"
// (so the caller falls back) for unhydrated / suppressed findings.
package pipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/BeLazy167/argus/backend/internal/store"
	"github.com/google/uuid"
)

// fakeThreadLinkReader is an in-memory threadLinkReader keyed by finding id.
// A missing key models an unknown finding (store returns ErrNoRows); a present
// entry with a nil ThreadNodeID models a persisted-but-unhydrated finding.
type fakeThreadLinkReader struct {
	links map[uuid.UUID]*store.ThreadLink
}

func (f fakeThreadLinkReader) GetThreadLinkForComment(_ context.Context, commentID uuid.UUID) (*store.ThreadLink, error) {
	link, ok := f.links[commentID]
	if !ok {
		return nil, errors.New("no rows")
	}
	return link, nil
}

func ptrStr(s string) *string { return &s }

func TestThreadNodeIDForFinding_TargetsExactlyThatFinding(t *testing.T) {
	// Two findings on adjacent lines of the SAME file — the case a proximity
	// match cannot disambiguate. Each carries its own distinct thread node id.
	findingA := uuid.New() // auth.go:10
	findingB := uuid.New() // auth.go:12
	line10, line12 := 10, 12
	restA, restB := int64(1001), int64(1002)

	reader := fakeThreadLinkReader{links: map[uuid.UUID]*store.ThreadLink{
		findingA: {
			CommentID:     findingA,
			FilePath:      "auth.go",
			EndLine:       &line10,
			RestCommentID: &restA,
			ThreadNodeID:  ptrStr("PRRT_nodeA"),
		},
		findingB: {
			CommentID:     findingB,
			FilePath:      "auth.go",
			EndLine:       &line12,
			RestCommentID: &restB,
			ThreadNodeID:  ptrStr("PRRT_nodeB"),
		},
	}}

	gotA, okA := threadNodeIDForFinding(context.Background(), reader, findingA)
	if !okA || gotA != "PRRT_nodeA" {
		t.Fatalf("finding A → (%q, %v), want (\"PRRT_nodeA\", true)", gotA, okA)
	}
	gotB, okB := threadNodeIDForFinding(context.Background(), reader, findingB)
	if !okB || gotB != "PRRT_nodeB" {
		t.Fatalf("finding B → (%q, %v), want (\"PRRT_nodeB\", true)", gotB, okB)
	}
	// The invariant: A's lookup never bleeds into B's thread despite the 2-line
	// gap that a proximity match (±3) would collapse.
	if gotA == gotB {
		t.Fatalf("adjacent findings resolved to the same thread node id %q — proximity ambiguity leaked into the registry", gotA)
	}
}

func TestThreadNodeIDForFinding_FallsBackWhenUnhydrated(t *testing.T) {
	posted := uuid.New()  // stored row, node id not yet hydrated
	unknown := uuid.New() // no row at all
	empty := uuid.New()   // hydrated to an empty string (defensive)
	line := 7

	reader := fakeThreadLinkReader{links: map[uuid.UUID]*store.ThreadLink{
		posted: {CommentID: posted, FilePath: "db.go", EndLine: &line, ThreadNodeID: nil},
		empty:  {CommentID: empty, FilePath: "db.go", EndLine: &line, ThreadNodeID: ptrStr("")},
	}}

	for name, id := range map[string]uuid.UUID{
		"unhydrated row": posted,
		"unknown finding": unknown,
		"empty node id":   empty,
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := threadNodeIDForFinding(context.Background(), reader, id)
			if ok || got != "" {
				t.Fatalf("%s → (%q, %v), want (\"\", false) so the caller falls back to proximity", name, got, ok)
			}
		})
	}
}
