// Package pipeline — incremental_test.go exercises IncrementalResolver.Resolve
// against in-memory fakes (no DB, no GitHub). It pins the four re-review
// decisions the resolver owns: no-prior → full, parse-failure → full + a
// surfaced fallback signal, retry carries priors (aggregated + deduped across
// all completed reviews), and a clean inter-diff → incremental patch.
package pipeline

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// fakeIncrementalStore is an in-memory incrementalStore.
type fakeIncrementalStore struct {
	prev      *store.Review
	prevErr   error
	comments  []store.ReviewComment
	commErr   error
	prevCalls int
	commCalls int
}

func (f *fakeIncrementalStore) GetLastCompletedReview(_ context.Context, _ int64, _ int) (*store.Review, error) {
	f.prevCalls++
	return f.prev, f.prevErr
}

func (f *fakeIncrementalStore) GetPRCompletedReviewComments(_ context.Context, _ int64, _ int) ([]store.ReviewComment, error) {
	f.commCalls++
	return f.comments, f.commErr
}

// fakeInterDiffFetcher is an in-memory interDiffFetcher.
type fakeInterDiffFetcher struct {
	diff  string
	err   error
	calls int
}

func (f *fakeInterDiffFetcher) GetCompareCommitsDiff(_ context.Context, _ int64, _, _, _, _ string) (string, error) {
	f.calls++
	return f.diff, f.err
}

func newBufferResolver(st incrementalStore, gh interDiffFetcher) (*IncrementalResolver, *bytes.Buffer) {
	var buf bytes.Buffer
	// Debug level so InfoContext + WarnContext + ErrorContext records are all captured.
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return NewIncrementalResolver(st, gh, logger), &buf
}

const validInterDiff = `diff --git a/f.go b/f.go
--- a/f.go
+++ b/f.go
@@ -1,3 +1,4 @@
 line1
 line2
+added
 line3
`

func syncEvent() ghpkg.PREvent {
	return ghpkg.PREvent{
		Action:         "synchronize",
		InstallationID: 42,
		RepoFullName:   "acme/widgets",
		PRNumber:       7,
		HeadSHA:        "newhead",
	}
}

// No prior completed review ⇒ full review, no fallback, and the inter-diff is
// never fetched (the expensive GitHub round-trip is skipped entirely).
func TestResolve_NoPrior_FullReview(t *testing.T) {
	st := &fakeIncrementalStore{prev: nil, prevErr: pgx.ErrNoRows}
	gh := &fakeInterDiffFetcher{}
	r, _ := newBufferResolver(st, gh)

	plan := r.Resolve(context.Background(), 1, syncEvent())

	if plan.IsIncremental {
		t.Error("IsIncremental = true, want false for first push")
	}
	if plan.Fallback {
		t.Error("Fallback = true, want false — a first push is not a fallback")
	}
	if plan.PreviousReviewID != nil {
		t.Errorf("PreviousReviewID = %v, want nil", plan.PreviousReviewID)
	}
	if plan.InterDiffPatch != nil {
		t.Error("InterDiffPatch non-nil, want nil")
	}
	if gh.calls != 0 {
		t.Errorf("GetCompareCommitsDiff called %d times, want 0 (no prior to compare against)", gh.calls)
	}
}

// An unparseable inter-diff ⇒ full review WITH a surfaced fallback signal: the
// log record carries event=incremental.fallback at ERROR level (parser
// regression), not a silent Warn.
func TestResolve_ParseFailure_FullReviewWithSignal(t *testing.T) {
	prev := &store.Review{ID: uuid.New(), HeadSHA: "oldhead"}
	st := &fakeIncrementalStore{prev: prev}
	// `@@ -x,1 +y,1 @@` has a non-numeric hunk range → diff.Parse errors.
	gh := &fakeInterDiffFetcher{diff: "diff --git a/f.go b/f.go\n@@ -x,1 +y,1 @@\n+foo\n"}
	r, buf := newBufferResolver(st, gh)

	plan := r.Resolve(context.Background(), 1, syncEvent())

	if plan.IsIncremental {
		t.Error("IsIncremental = true, want false on parse failure")
	}
	if !plan.Fallback {
		t.Fatal("Fallback = false, want true on parse failure")
	}
	if plan.FallbackReason != "inter_diff_parse_failed" {
		t.Errorf("FallbackReason = %q, want inter_diff_parse_failed", plan.FallbackReason)
	}
	logged := buf.String()
	if !strings.Contains(logged, "event=incremental.fallback") {
		t.Errorf("fallback not surfaced as an event; log = %q", logged)
	}
	if !strings.Contains(logged, "level=ERROR") {
		t.Errorf("parse-failure fallback not logged at ERROR; log = %q", logged)
	}
}

// A GetCompareCommitsDiff error ⇒ full review WITH a surfaced fallback signal
// at WARN level (operational, not a parser regression).
func TestResolve_FetchError_FullReviewWithSignal(t *testing.T) {
	prev := &store.Review{ID: uuid.New(), HeadSHA: "oldhead"}
	st := &fakeIncrementalStore{prev: prev}
	gh := &fakeInterDiffFetcher{err: errors.New("502 from github")}
	r, buf := newBufferResolver(st, gh)

	plan := r.Resolve(context.Background(), 1, syncEvent())

	if plan.IsIncremental {
		t.Error("IsIncremental = true, want false on fetch error")
	}
	if !plan.Fallback || plan.FallbackReason != "inter_diff_fetch_failed" {
		t.Errorf("Fallback=%v reason=%q, want true / inter_diff_fetch_failed", plan.Fallback, plan.FallbackReason)
	}
	if !strings.Contains(buf.String(), "event=incremental.fallback") {
		t.Errorf("fetch-error fallback not surfaced as an event; log = %q", buf.String())
	}
}

// Retry path: ResolvePriors carries priors — aggregated and deduped across all
// completed reviews on the PR — WITHOUT fetching an inter-diff or emitting a
// fallback signal, even though the prior review is at a DIFFERENT head (which
// under the full Resolve would fire a compare and, on empty/error, a fallback).
func TestResolvePriors_RetryCarriesDedupedPriors_NoCompare(t *testing.T) {
	prev := &store.Review{ID: uuid.New(), HeadSHA: "oldhead"}
	// Same (file, range, body) posted on two different completed reviews plus a
	// distinct second finding: the duplicate must collapse, the distinct stay.
	dup := store.ReviewComment{FilePath: "a.go", StartLine: intPtr(10), EndLine: intPtr(10), Body: "nil deref"}
	other := store.ReviewComment{FilePath: "b.go", StartLine: intPtr(3), EndLine: intPtr(3), Body: "unused var"}
	st := &fakeIncrementalStore{
		prev:     prev,
		comments: []store.ReviewComment{dup, dup, other},
	}
	gh := &fakeInterDiffFetcher{diff: validInterDiff} // must never be consulted
	r, buf := newBufferResolver(st, gh)

	plan := r.ResolvePriors(context.Background(), 1, 7)

	if gh.calls != 0 {
		t.Errorf("GetCompareCommitsDiff called %d times, want 0 on the retry/priors path", gh.calls)
	}
	if plan.IsIncremental {
		t.Error("IsIncremental = true, want false — ResolvePriors never produces an inter-diff")
	}
	if plan.Fallback {
		t.Error("Fallback = true, want false — a retry must not emit a fallback signal")
	}
	if plan.PreviousReviewID == nil || *plan.PreviousReviewID != prev.ID {
		t.Errorf("PreviousReviewID = %v, want %v", plan.PreviousReviewID, prev.ID)
	}
	if got := len(plan.PriorComments["a.go"]); got != 1 {
		t.Errorf("a.go prior comments = %d, want 1 (duplicate not deduped)", got)
	}
	if got := len(plan.PriorComments["b.go"]); got != 1 {
		t.Errorf("b.go prior comments = %d, want 1", got)
	}
	if strings.Contains(buf.String(), "event=incremental.fallback") {
		t.Errorf("retry path wrongly surfaced a fallback; log = %q", buf.String())
	}
}

// Push-path replay: an empty compare where the head is UNCHANGED (duplicate /
// replayed synchronize webhook) is quiet — not incremental, not a fallback.
func TestResolve_UnchangedHead_QuietNoFallback(t *testing.T) {
	prev := &store.Review{ID: uuid.New(), HeadSHA: "samehead"}
	st := &fakeIncrementalStore{prev: prev}
	gh := &fakeInterDiffFetcher{diff: ""}
	event := syncEvent()
	event.HeadSHA = "samehead"
	r, buf := newBufferResolver(st, gh)

	plan := r.Resolve(context.Background(), 1, event)

	if plan.IsIncremental {
		t.Error("IsIncremental = true, want false when head is unchanged")
	}
	if plan.Fallback {
		t.Error("Fallback = true, want false — an unchanged head is not an anomaly")
	}
	if strings.Contains(buf.String(), "event=incremental.fallback") {
		t.Errorf("unchanged head wrongly surfaced a fallback; log = %q", buf.String())
	}
}

// A clean inter-diff between two distinct commits ⇒ incremental review whose
// InterDiffPatch is the parsed compare, with the previous-review pointer set.
func TestResolve_Incremental_ProducesInterDiffPatch(t *testing.T) {
	prev := &store.Review{ID: uuid.New(), HeadSHA: "oldhead"}
	st := &fakeIncrementalStore{prev: prev}
	gh := &fakeInterDiffFetcher{diff: validInterDiff}
	r, _ := newBufferResolver(st, gh)

	plan := r.Resolve(context.Background(), 1, syncEvent())

	if !plan.IsIncremental {
		t.Fatal("IsIncremental = false, want true for a clean inter-diff")
	}
	if plan.Fallback {
		t.Error("Fallback = true, want false on a clean inter-diff")
	}
	if plan.InterDiffRaw != validInterDiff {
		t.Error("InterDiffRaw does not match the fetched compare diff")
	}
	if plan.InterDiffPatch == nil || len(plan.InterDiffPatch.Files) != 1 {
		t.Fatalf("InterDiffPatch = %v, want a 1-file patch", plan.InterDiffPatch)
	}
	if got := plan.InterDiffPatch.Files[0].NewName; got != "f.go" {
		t.Errorf("patched file = %q, want f.go", got)
	}
	if plan.PreviousReviewID == nil || *plan.PreviousReviewID != prev.ID {
		t.Errorf("PreviousReviewID = %v, want %v", plan.PreviousReviewID, prev.ID)
	}
	if gh.calls != 1 {
		t.Errorf("GetCompareCommitsDiff called %d times, want exactly 1", gh.calls)
	}
}
