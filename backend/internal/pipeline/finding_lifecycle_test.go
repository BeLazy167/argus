// Package pipeline — finding_lifecycle_test.go pins the FindingLifecycle (#165)
// state machine over BOTH the DB ledger and the GitHub thread, against a fake
// ledger + fake GitHub client (no pipeline, no DB). The whole point of the
// module is being testable in isolation and proving the ledger state always
// matches the GitHub thread state.
package pipeline

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/store"
	"github.com/google/uuid"
)

func lifecycleTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fakeFindingLedger is an in-memory findingLedger. UpdateFindingStateFrom honors
// the per-event allowedFrom set exactly as the store's SQL WHERE clause does, so
// (from, event) → (changed?, new state) is realistic.
type fakeFindingLedger struct {
	state       map[uuid.UUID]store.FindingState
	links       map[uuid.UUID]*store.ThreadLink
	comments    map[int64]*store.ReviewComment // github_comment_id → finding (TransitionThread)
	resolvedSHA map[uuid.UUID]string           // stamp-once resolved_sha (#167)
	updateErr   error
}

// SetFindingResolvedSHA mimics the store's stamp-once UPDATE: the first SHA wins,
// later stamps are silent no-ops (WHERE resolved_sha IS NULL).
func (f *fakeFindingLedger) SetFindingResolvedSHA(_ context.Context, id uuid.UUID, sha string) error {
	if f.resolvedSHA == nil {
		f.resolvedSHA = map[uuid.UUID]string{}
	}
	if sha == "" {
		return nil
	}
	if _, ok := f.resolvedSHA[id]; ok {
		return nil // stamp-once
	}
	f.resolvedSHA[id] = sha
	return nil
}

func (f *fakeFindingLedger) UpdateFindingStateFrom(_ context.Context, id uuid.UUID, to store.FindingState, allowedFrom []store.FindingState) (bool, error) {
	if f.updateErr != nil {
		return false, f.updateErr
	}
	cur := f.state[id]
	if cur == "" {
		cur = store.FindingStatePosted
	}
	if cur == to {
		return false, nil // idempotent replay
	}
	allowed := false
	for _, s := range allowedFrom {
		if s == cur {
			allowed = true
			break
		}
	}
	if !allowed {
		return false, nil // disallowed source (e.g. heuristic addressed from dismissed)
	}
	f.state[id] = to
	return true, nil
}

func (f *fakeFindingLedger) GetThreadLinkForComment(_ context.Context, id uuid.UUID) (*store.ThreadLink, error) {
	l, ok := f.links[id]
	if !ok {
		return nil, errors.New("no rows")
	}
	return l, nil
}

func (f *fakeFindingLedger) GetCommentByGithubID(_ context.Context, ghID int64) (*store.ReviewComment, error) {
	c, ok := f.comments[ghID]
	if !ok {
		return nil, errors.New("no rows")
	}
	return c, nil
}

// fakeThreadGH is an in-memory threadResolver recording which thread node ids
// were resolved, so a test can prove Transition targeted exactly the right one.
type fakeThreadGH struct {
	resolved     []string
	resolveErr   error
	threads      []ghpkg.ReviewThread
	findThreadID string
	findErr      error
}

func (f *fakeThreadGH) ResolveReviewThread(_ context.Context, _ int64, threadID string) error {
	if f.resolveErr != nil {
		return f.resolveErr
	}
	f.resolved = append(f.resolved, threadID)
	return nil
}

func (f *fakeThreadGH) FindThreadForComment(_ context.Context, _ int64, _, _ string, _ int, _ string) (string, error) {
	return f.findThreadID, f.findErr
}

func (f *fakeThreadGH) ListReviewThreads(_ context.Context, _ int64, _, _ string, _ int) ([]ghpkg.ReviewThread, error) {
	return f.threads, nil
}

// TestFindingLifecycle_Transition is the table-driven core: (from-state, event)
// → (new-state, thread-resolved?, ledger-changed?) plus WHICH thread was
// resolved. It proves the ledger and the GitHub thread never disagree.
func TestFindingLifecycle_Transition(t *testing.T) {
	const storedNode = "PRRT_stored"
	rest := int64(555)

	cases := []struct {
		name string
		from store.FindingState
		link *store.ThreadLink
		req  FindingTransition // FindingID injected by the runner
		gh   fakeThreadGH

		wantState     store.FindingState
		wantStored    store.FindingState // ledger row after the call
		wantChanged   bool
		wantResolved  bool
		wantResolveID string // "" = expect nothing resolved
		wantThreadErr bool
	}{
		{
			name:          "posted + dismissed → dismissed, resolves stored thread",
			from:          store.FindingStatePosted,
			link:          &store.ThreadLink{ThreadNodeID: ptrStr(storedNode)},
			req:           FindingTransition{Event: EventDismissed, Owner: "o", Repo: "r", PRNumber: 1},
			wantState:     store.FindingStateDismissed,
			wantStored:    store.FindingStateDismissed,
			wantChanged:   true,
			wantResolved:  true,
			wantResolveID: storedNode,
		},
		{
			name:          "posted + addressed → addressed, resolves stored thread",
			from:          store.FindingStatePosted,
			link:          &store.ThreadLink{ThreadNodeID: ptrStr(storedNode)},
			req:           FindingTransition{Event: EventAddressed, Owner: "o", Repo: "r", PRNumber: 1},
			wantState:     store.FindingStateAddressed,
			wantStored:    store.FindingStateAddressed,
			wantChanged:   true,
			wantResolved:  true,
			wantResolveID: storedNode,
		},
		{
			name:         "posted + deferred → deferred, NO thread resolution",
			from:         store.FindingStatePosted,
			link:         &store.ThreadLink{ThreadNodeID: ptrStr(storedNode)},
			req:          FindingTransition{Event: EventDeferred},
			wantState:    store.FindingStateDeferred,
			wantStored:   store.FindingStateDeferred,
			wantChanged:  true,
			wantResolved: false,
		},
		{
			name:          "posted + resolved-manually via caller node id",
			from:          store.FindingStatePosted,
			req:           FindingTransition{Event: EventResolvedManually, ThreadNodeID: "PRRT_manual", Owner: "o", Repo: "r", PRNumber: 1},
			wantState:     store.FindingStateResolved,
			wantStored:    store.FindingStateResolved,
			wantChanged:   true,
			wantResolved:  true,
			wantResolveID: "PRRT_manual",
		},
		{
			name:          "heuristic addressed + human dismissed → dismissed, thread resolved (human overrides)",
			from:          store.FindingStateAddressed,
			link:          &store.ThreadLink{ThreadNodeID: ptrStr(storedNode)},
			req:           FindingTransition{Event: EventDismissed, Owner: "o", Repo: "r", PRNumber: 1},
			wantState:     store.FindingStateDismissed,
			wantStored:    store.FindingStateDismissed,
			wantChanged:   true,
			wantResolved:  true,
			wantResolveID: storedNode,
		},
		{
			// should-fix #3: a HEURISTIC addressed (auto-resolve) must NOT overwrite
			// an explicit human dismissal. The thread still closes (its lines moved),
			// but the ledger stays 'dismissed'.
			name:          "dismissed + HEURISTIC addressed → ledger stays dismissed (no credit for a fix that was a rejection)",
			from:          store.FindingStateDismissed,
			link:          &store.ThreadLink{ThreadNodeID: ptrStr(storedNode)},
			req:           FindingTransition{Event: EventAddressed, Owner: "o", Repo: "r", PRNumber: 1},
			wantState:     store.FindingStateAddressed, // policy target (attempted)
			wantStored:    store.FindingStateDismissed, // human dismissal preserved
			wantChanged:   false,
			wantResolved:  true,
			wantResolveID: storedNode,
		},
		{
			// The reply's human evidence ("you were right, I fixed it") MAY override
			// a prior dismissal — this is the only addressed path allowed to.
			name:          "dismissed + addressed-BY-REPLY → addressed (human evidence overrides)",
			from:          store.FindingStateDismissed,
			link:          &store.ThreadLink{ThreadNodeID: ptrStr(storedNode)},
			req:           FindingTransition{Event: EventAddressedByReply, Owner: "o", Repo: "r", PRNumber: 1},
			wantState:     store.FindingStateAddressed,
			wantStored:    store.FindingStateAddressed,
			wantChanged:   true,
			wantResolved:  true,
			wantResolveID: storedNode,
		},
		{
			// BLOCKING #1(b): a mistaken manual resolve is recoverable by an explicit
			// human reply-dismissal — 'resolved' is not an irreversible trap.
			name:          "resolved + reply dismissal → dismissed (recovers a mistaken resolve)",
			from:          store.FindingStateResolved,
			link:          &store.ThreadLink{ThreadNodeID: ptrStr(storedNode)},
			req:           FindingTransition{Event: EventDismissed, Owner: "o", Repo: "r", PRNumber: 1},
			wantState:     store.FindingStateDismissed,
			wantStored:    store.FindingStateDismissed,
			wantChanged:   true,
			wantResolved:  true,
			wantResolveID: storedNode,
		},
		{
			// should-fix #3 via the gauge's ledger-only heuristic: also cannot leave
			// a human dismissal.
			name:         "dismissed + addressed-at-merge → ledger stays dismissed (heuristic, no thread)",
			from:         store.FindingStateDismissed,
			link:         &store.ThreadLink{ThreadNodeID: ptrStr(storedNode)},
			req:          FindingTransition{Event: EventAddressedAtMerge},
			wantState:    store.FindingStateAddressed,
			wantStored:   store.FindingStateDismissed,
			wantChanged:  false,
			wantResolved: false,
		},
		{
			name:          "no stored node, exact REST-id join fallback",
			from:          store.FindingStatePosted,
			link:          &store.ThreadLink{RestCommentID: &rest}, // node id not hydrated
			req:           FindingTransition{Event: EventDismissed, Owner: "o", Repo: "r", PRNumber: 1},
			gh:            fakeThreadGH{threads: []ghpkg.ReviewThread{{ID: "PRRT_join", FirstCommentID: rest}}},
			wantState:     store.FindingStateDismissed,
			wantStored:    store.FindingStateDismissed,
			wantChanged:   true,
			wantResolved:  true,
			wantResolveID: "PRRT_join",
		},
		{
			name:          "no link at all, comment-node-id fallback (reply path)",
			from:          store.FindingStatePosted,
			link:          nil,
			req:           FindingTransition{Event: EventDismissed, Owner: "o", Repo: "r", PRNumber: 1, CommentNodeID: "MDcomment"},
			gh:            fakeThreadGH{findThreadID: "PRRT_find"},
			wantState:     store.FindingStateDismissed,
			wantStored:    store.FindingStateDismissed,
			wantChanged:   true,
			wantResolved:  true,
			wantResolveID: "PRRT_find",
		},
		{
			// should-fix #4, DECISION half: a reply DISMISSAL records the human
			// decision even if the resolve 502s — the decision is true regardless of
			// thread state, and comment_outcomes already holds it.
			name:          "reply dismissal + resolve error → ledger STILL records dismissal (decision), ThreadErr reported",
			from:          store.FindingStatePosted,
			link:          &store.ThreadLink{ThreadNodeID: ptrStr(storedNode)},
			req:           FindingTransition{Event: EventDismissed, Owner: "o", Repo: "r", PRNumber: 1},
			gh:            fakeThreadGH{resolveErr: errors.New("403 forbidden")},
			wantState:     store.FindingStateDismissed,
			wantStored:    store.FindingStateDismissed, // decision recorded despite the failure
			wantChanged:   true,
			wantResolved:  false,
			wantThreadErr: true,
		},
		{
			// should-fix #4, ASSERTION half: an addressed ASSERTION claims the thread
			// closed, so a failed resolve must NOT advance the ledger (no over-claim).
			name:          "addressed assertion + resolve error → ledger stays posted (no over-claim)",
			from:          store.FindingStatePosted,
			link:          &store.ThreadLink{ThreadNodeID: ptrStr(storedNode)},
			req:           FindingTransition{Event: EventAddressed, Owner: "o", Repo: "r", PRNumber: 1},
			gh:            fakeThreadGH{resolveErr: errors.New("502 bad gateway")},
			wantState:     store.FindingStateAddressed, // policy target (attempted)
			wantStored:    store.FindingStatePosted,    // stays retryable — thread still open
			wantChanged:   false,
			wantResolved:  false,
			wantThreadErr: true,
		},
		{
			name:          "reaction dismissed → LEDGER ONLY, zero thread resolution",
			from:          store.FindingStatePosted,
			link:          &store.ThreadLink{ThreadNodeID: ptrStr(storedNode)},
			req:           FindingTransition{Event: EventReactionDismissed},
			wantState:     store.FindingStateDismissed,
			wantStored:    store.FindingStateDismissed,
			wantChanged:   true,
			wantResolved:  false,
			wantResolveID: "", // untrusted reaction must NOT resolve the thread
		},
		{
			name:          "gauge addressed-at-merge → LEDGER ONLY, no thread resolution",
			from:          store.FindingStatePosted,
			link:          &store.ThreadLink{ThreadNodeID: ptrStr(storedNode)},
			req:           FindingTransition{Event: EventAddressedAtMerge},
			wantState:     store.FindingStateAddressed,
			wantStored:    store.FindingStateAddressed,
			wantChanged:   true,
			wantResolved:  false,
			wantResolveID: "",
		},
		{
			name:          "reopened thread, finding already dismissed → re-resolve, ledger no-op",
			from:          store.FindingStateDismissed,
			req:           FindingTransition{Event: EventDismissed, ThreadNodeID: storedNode, Owner: "o", Repo: "r", PRNumber: 1},
			wantState:     store.FindingStateDismissed,
			wantStored:    store.FindingStateDismissed,
			wantChanged:   false, // already dismissed — idempotent
			wantResolved:  true,  // but the OPEN thread is still re-closed
			wantResolveID: storedNode,
		},
		{
			name:          "reopened thread, finding already resolved → re-resolve (manual), ledger no-op",
			from:          store.FindingStateResolved,
			req:           FindingTransition{Event: EventResolvedManually, ThreadNodeID: storedNode, Owner: "o", Repo: "r", PRNumber: 1},
			wantState:     store.FindingStateResolved,
			wantStored:    store.FindingStateResolved,
			wantChanged:   false,
			wantResolved:  true,
			wantResolveID: storedNode,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fid := uuid.New()
			ledger := &fakeFindingLedger{
				state: map[uuid.UUID]store.FindingState{fid: tc.from},
				links: map[uuid.UUID]*store.ThreadLink{},
			}
			if tc.link != nil {
				tc.link.CommentID = fid
				ledger.links[fid] = tc.link
			}
			gh := tc.gh // copy
			lc := NewFindingLifecycle(ledger, &gh, lifecycleTestLogger())

			req := tc.req
			req.FindingID = fid
			res, err := lc.Transition(context.Background(), req)
			if err != nil {
				t.Fatalf("Transition returned error: %v", err)
			}
			if res.NewState != tc.wantState {
				t.Errorf("NewState = %q, want %q", res.NewState, tc.wantState)
			}
			if res.LedgerChanged != tc.wantChanged {
				t.Errorf("LedgerChanged = %v, want %v", res.LedgerChanged, tc.wantChanged)
			}
			if res.ThreadResolved != tc.wantResolved {
				t.Errorf("ThreadResolved = %v, want %v", res.ThreadResolved, tc.wantResolved)
			}
			if (res.ThreadErr != nil) != tc.wantThreadErr {
				t.Errorf("ThreadErr = %v, want error: %v", res.ThreadErr, tc.wantThreadErr)
			}
			if tc.wantResolveID == "" {
				if len(gh.resolved) != 0 {
					t.Errorf("resolved threads = %v, want none", gh.resolved)
				}
			} else {
				if len(gh.resolved) != 1 || gh.resolved[0] != tc.wantResolveID {
					t.Errorf("resolved threads = %v, want exactly [%s]", gh.resolved, tc.wantResolveID)
				}
			}
			// The ledger row after the call — pins resolve-first honesty: a failed
			// resolve must leave the prior state, never advance to the terminal one.
			if ledger.state[fid] != tc.wantStored {
				t.Errorf("stored ledger state = %q, want %q", ledger.state[fid], tc.wantStored)
			}
		})
	}
}

// TestFindingLifecycle_ResolvedSHAStamp pins the resolved-by-commit breadcrumb
// write (#167): the SHA is stamped ONLY when the event actually moves the finding
// to addressed/resolved, never when a provenance guard blocks the move or on a
// decision event — so the breadcrumb can never misattribute a commit.
func TestFindingLifecycle_ResolvedSHAStamp(t *testing.T) {
	const sha = "deadbeefcafef00d"
	cases := []struct {
		name    string
		from    store.FindingState
		req     FindingTransition
		wantSHA string // "" = expect NO stamp
	}{
		{
			name:    "auto-resolve addresses an open finding → stamped",
			from:    store.FindingStatePosted,
			req:     FindingTransition{Event: EventAddressed, ResolvedSHA: sha, ThreadNodeID: "PRRT_1", Owner: "o", Repo: "r", PRNumber: 1},
			wantSHA: sha,
		},
		{
			name:    "manual resolve of an open finding with a SHA → stamped (resolved is resolved-ish)",
			from:    store.FindingStatePosted,
			req:     FindingTransition{Event: EventResolvedManually, ResolvedSHA: sha, ThreadNodeID: "PRRT_2", Owner: "o", Repo: "r", PRNumber: 1},
			wantSHA: sha,
		},
		{
			name:    "heuristic addressed BLOCKED by prior human dismissal → NOT stamped (no misattribution)",
			from:    store.FindingStateDismissed,
			req:     FindingTransition{Event: EventAddressed, ResolvedSHA: sha, ThreadNodeID: "PRRT_3", Owner: "o", Repo: "r", PRNumber: 1},
			wantSHA: "", // ledger stayed dismissed → no stamp
		},
		{
			name:    "addressed with empty SHA (no known commit) → NOT stamped",
			from:    store.FindingStatePosted,
			req:     FindingTransition{Event: EventAddressed, ResolvedSHA: "", ThreadNodeID: "PRRT_4", Owner: "o", Repo: "r", PRNumber: 1},
			wantSHA: "",
		},
		{
			name:    "dismissed decision carrying a SHA → NOT stamped (dismissed is not resolved-ish)",
			from:    store.FindingStatePosted,
			req:     FindingTransition{Event: EventDismissed, ResolvedSHA: sha, ThreadNodeID: "PRRT_5", Owner: "o", Repo: "r", PRNumber: 1},
			wantSHA: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fid := uuid.New()
			ledger := &fakeFindingLedger{
				state:       map[uuid.UUID]store.FindingState{fid: tc.from},
				links:       map[uuid.UUID]*store.ThreadLink{},
				resolvedSHA: map[uuid.UUID]string{},
			}
			lc := NewFindingLifecycle(ledger, &fakeThreadGH{}, lifecycleTestLogger())
			req := tc.req
			req.FindingID = fid
			if _, err := lc.Transition(context.Background(), req); err != nil {
				t.Fatalf("Transition: %v", err)
			}
			if got := ledger.resolvedSHA[fid]; got != tc.wantSHA {
				t.Errorf("resolved_sha = %q, want %q", got, tc.wantSHA)
			}
		})
	}

	t.Run("stamp-once: a second resolving push does not overwrite the first commit", func(t *testing.T) {
		fid := uuid.New()
		ledger := &fakeFindingLedger{
			state:       map[uuid.UUID]store.FindingState{fid: store.FindingStatePosted},
			links:       map[uuid.UUID]*store.ThreadLink{},
			resolvedSHA: map[uuid.UUID]string{},
		}
		lc := NewFindingLifecycle(ledger, &fakeThreadGH{}, lifecycleTestLogger())
		base := FindingTransition{FindingID: fid, Event: EventAddressed, ThreadNodeID: "PRRT_x", Owner: "o", Repo: "r", PRNumber: 1}
		first := base
		first.ResolvedSHA = "sha_first"
		if _, err := lc.Transition(context.Background(), first); err != nil {
			t.Fatalf("Transition first: %v", err)
		}
		// Second push: finding already addressed → ledger no-op (changed=false), so
		// even though the store guards stamp-once, the lifecycle also skips the call.
		second := base
		second.ResolvedSHA = "sha_second"
		if _, err := lc.Transition(context.Background(), second); err != nil {
			t.Fatalf("Transition second: %v", err)
		}
		if got := ledger.resolvedSHA[fid]; got != "sha_first" {
			t.Errorf("resolved_sha = %q, want the first commit %q", got, "sha_first")
		}
	})
}

// TestFindingLifecycle_SameLineDismissalTargetsOwnThread is the #171 fold-forward
// invariant at the lifecycle seam: two findings on the SAME path+line (different
// categories), each bound 1:1 to its OWN thread. Dismissing B must resolve ONLY
// B's thread and must not touch A's ledger row.
func TestFindingLifecycle_SameLineDismissalTargetsOwnThread(t *testing.T) {
	fa, fb := uuid.New(), uuid.New()
	line := 42
	const nodeA, nodeB = "PRRT_A", "PRRT_B"

	ledger := &fakeFindingLedger{
		state: map[uuid.UUID]store.FindingState{
			fa: store.FindingStatePosted,
			fb: store.FindingStatePosted,
		},
		links: map[uuid.UUID]*store.ThreadLink{
			fa: {CommentID: fa, FilePath: "pay.go", EndLine: &line, ThreadNodeID: ptrStr(nodeA)},
			fb: {CommentID: fb, FilePath: "pay.go", EndLine: &line, ThreadNodeID: ptrStr(nodeB)},
		},
	}
	gh := &fakeThreadGH{}
	lc := NewFindingLifecycle(ledger, gh, lifecycleTestLogger())

	if _, err := lc.Transition(context.Background(), FindingTransition{
		FindingID: fb, Event: EventDismissed, Owner: "o", Repo: "r", PRNumber: 1,
	}); err != nil {
		t.Fatalf("Transition: %v", err)
	}

	if len(gh.resolved) != 1 || gh.resolved[0] != nodeB {
		t.Fatalf("dismissing B resolved %v, want exactly [%s] — same-line thread bled", gh.resolved, nodeB)
	}
	if ledger.state[fa] != store.FindingStatePosted {
		t.Fatalf("finding A moved to %q; B's dismissal leaked into A's ledger row", ledger.state[fa])
	}
	if ledger.state[fb] != store.FindingStateDismissed {
		t.Fatalf("finding B state = %q, want dismissed", ledger.state[fb])
	}
}

// TestFindingLifecycle_UnknownEvent guards the one error path.
func TestFindingLifecycle_UnknownEvent(t *testing.T) {
	ledger := &fakeFindingLedger{state: map[uuid.UUID]store.FindingState{}, links: map[uuid.UUID]*store.ThreadLink{}}
	lc := NewFindingLifecycle(ledger, &fakeThreadGH{}, lifecycleTestLogger())
	if _, err := lc.Transition(context.Background(), FindingTransition{FindingID: uuid.New(), Event: "bogus"}); err == nil {
		t.Fatal("want error for unknown event, got nil")
	}
}

// TestFindingLifecycle_ReactionNeverResolvesThread is the security invariant: a
// 👎 reaction dismisses the ledger but must issue ZERO ResolveReviewThread calls,
// even though the finding has a perfectly resolvable stored thread link. A
// reaction is untrusted (any user, swept every PR event) and must not clear a
// finding from the merge-gate view via the app's write token.
func TestFindingLifecycle_ReactionNeverResolvesThread(t *testing.T) {
	fid := uuid.New()
	ledger := &fakeFindingLedger{
		state: map[uuid.UUID]store.FindingState{fid: store.FindingStatePosted},
		links: map[uuid.UUID]*store.ThreadLink{
			fid: {CommentID: fid, ThreadNodeID: ptrStr("PRRT_reactable")},
		},
	}
	gh := &fakeThreadGH{}
	lc := NewFindingLifecycle(ledger, gh, lifecycleTestLogger())

	res, err := lc.Transition(context.Background(), FindingTransition{FindingID: fid, Event: EventReactionDismissed})
	if err != nil {
		t.Fatalf("Transition: %v", err)
	}
	if len(gh.resolved) != 0 {
		t.Fatalf("reaction resolved threads %v — an untrusted reaction must NOT touch GitHub", gh.resolved)
	}
	if !res.LedgerChanged || ledger.state[fid] != store.FindingStateDismissed {
		t.Fatalf("reaction should still dismiss the ledger: changed=%v state=%q", res.LedgerChanged, ledger.state[fid])
	}
}

// TestFindingLifecycle_ReopenedTerminalThreadReResolved is the regression guard:
// a thread a human REOPENED (ledger already terminal, GitHub thread open again)
// must be re-closed by the resolving callers. Those callers list threads and
// invoke Transition only for unresolved ones, so Transition must resolve the
// thread regardless of the (already-terminal) ledger state. Covers both the
// @argus-resolve event and the auto-resolve event.
func TestFindingLifecycle_ReopenedTerminalThreadReResolved(t *testing.T) {
	cases := []struct {
		name  string
		from  store.FindingState
		event LifecycleEvent
	}{
		{"@argus resolve on reopened resolved finding", store.FindingStateResolved, EventResolvedManually},
		{"auto-resolve on reopened addressed finding", store.FindingStateAddressed, EventAddressed},
		{"auto-resolve on reopened dismissed finding", store.FindingStateDismissed, EventAddressed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fid := uuid.New()
			ledger := &fakeFindingLedger{
				state: map[uuid.UUID]store.FindingState{fid: tc.from},
				links: map[uuid.UUID]*store.ThreadLink{},
			}
			gh := &fakeThreadGH{}
			lc := NewFindingLifecycle(ledger, gh, lifecycleTestLogger())

			// The caller (auto-resolve / @argus resolve) passes the node id of a
			// thread it already confirmed open.
			res, err := lc.Transition(context.Background(), FindingTransition{
				FindingID: fid, Event: tc.event, ThreadNodeID: "PRRT_reopened",
				Owner: "o", Repo: "r", PRNumber: 1,
			})
			if err != nil {
				t.Fatalf("Transition: %v", err)
			}
			if !res.ThreadResolved || len(gh.resolved) != 1 || gh.resolved[0] != "PRRT_reopened" {
				t.Fatalf("reopened thread not re-resolved: resolved=%v (a terminal ledger state must not block re-resolving an open thread)", gh.resolved)
			}
		})
	}
}

// TestFindingLifecycle_TransitionThread covers the bulk-resolver glue the
// rewired auto-resolve + @argus-resolve loops now share: map thread→finding and
// either route through Transition (ledger + thread) or resolve directly.
func TestFindingLifecycle_TransitionThread(t *testing.T) {
	t.Run("tracked finding routes through Transition (ledger + thread)", func(t *testing.T) {
		fid := uuid.New()
		rest := int64(9001)
		ledger := &fakeFindingLedger{
			state:    map[uuid.UUID]store.FindingState{fid: store.FindingStatePosted},
			links:    map[uuid.UUID]*store.ThreadLink{},
			comments: map[int64]*store.ReviewComment{rest: {ID: fid}},
		}
		gh := &fakeThreadGH{}
		lc := NewFindingLifecycle(ledger, gh, lifecycleTestLogger())

		res, err := lc.TransitionThread(context.Background(), ThreadTransition{
			Event: EventResolvedManually, ThreadNodeID: "PRRT_x", RestCommentID: rest,
			Owner: "o", Repo: "r", PRNumber: 1,
		})
		if err != nil {
			t.Fatalf("TransitionThread: %v", err)
		}
		if !res.ThreadResolved || len(gh.resolved) != 1 || gh.resolved[0] != "PRRT_x" {
			t.Fatalf("thread not resolved via lifecycle: resolved=%v", gh.resolved)
		}
		if ledger.state[fid] != store.FindingStateResolved {
			t.Fatalf("tracked finding ledger not moved: %q, want resolved", ledger.state[fid])
		}
	})

	t.Run("untracked thread resolves directly, no ledger", func(t *testing.T) {
		ledger := &fakeFindingLedger{comments: map[int64]*store.ReviewComment{}} // no mapping
		gh := &fakeThreadGH{}
		lc := NewFindingLifecycle(ledger, gh, lifecycleTestLogger())

		res, err := lc.TransitionThread(context.Background(), ThreadTransition{
			Event: EventResolvedManually, ThreadNodeID: "PRRT_legacy", RestCommentID: 404,
		})
		if err != nil {
			t.Fatalf("TransitionThread: %v", err)
		}
		if !res.ThreadResolved || len(gh.resolved) != 1 || gh.resolved[0] != "PRRT_legacy" {
			t.Fatalf("untracked thread not resolved directly: resolved=%v", gh.resolved)
		}
	})

	t.Run("untracked resolve failure surfaces ThreadErr for the caller", func(t *testing.T) {
		ledger := &fakeFindingLedger{comments: map[int64]*store.ReviewComment{}}
		gh := &fakeThreadGH{resolveErr: errors.New("boom")}
		lc := NewFindingLifecycle(ledger, gh, lifecycleTestLogger())

		res, _ := lc.TransitionThread(context.Background(), ThreadTransition{
			Event: EventResolvedManually, ThreadNodeID: "PRRT_x", RestCommentID: 1,
		})
		// The @argus-resolve loop turns (!ThreadResolved) into its "thread not
		// resolved" failure count; ThreadErr must be non-nil for classification.
		if res.ThreadResolved || res.ThreadErr == nil {
			t.Fatalf("want unresolved + ThreadErr, got resolved=%v err=%v", res.ThreadResolved, res.ThreadErr)
		}
	})
}

// TestEventPolicies_AllowedFromRespectStructuralLegality asserts every event's
// allowedFrom is a strict subset of the store's structural transition DAG — the
// per-event provenance restrictions may only NARROW legality, never invent an
// illegal edge or list a redundant self-source.
func TestEventPolicies_AllowedFromRespectStructuralLegality(t *testing.T) {
	for ev, pol := range eventPolicies {
		for _, from := range pol.allowedFrom {
			if from == pol.state {
				t.Errorf("event %q: allowedFrom lists its own target %q (redundant)", ev, pol.state)
			}
			if !store.CanTransitionFindingState(from, pol.state) {
				t.Errorf("event %q: %q→%q is not structurally legal", ev, from, pol.state)
			}
		}
	}
}

// TestPairCommentsToRows_SameLineDistinctRows proves the 1:1 backfill: two
// findings on the SAME (path, line) bind to DISTINCT rows, and the binding
// follows the exact body — not input order (comments arrive reversed here).
func TestPairCommentsToRows_SameLineDistinctRows(t *testing.T) {
	ra, rb := uuid.New(), uuid.New()
	rows := []unboundCommentRow{
		{ID: ra, Path: "pay.go", Line: 42, Body: "**Bug**: off-by-one"},
		{ID: rb, Path: "pay.go", Line: 42, Body: "**Security**: missing authz"},
	}
	comments := []postedComment{
		{GithubID: 1002, Path: "pay.go", Line: 42, Body: "**Security**: missing authz"},
		{GithubID: 1001, Path: "pay.go", Line: 42, Body: "**Bug**: off-by-one"},
	}
	got := pairCommentsToRows(rows, comments)
	if len(got) != 2 {
		t.Fatalf("want 2 distinct bindings, got %d: %v", len(got), got)
	}
	if got[ra] != 1001 {
		t.Errorf("row A bound to %d, want 1001 (body match, not order)", got[ra])
	}
	if got[rb] != 1002 {
		t.Errorf("row B bound to %d, want 1002 (body match, not order)", got[rb])
	}
}

// TestPairCommentsToRows_IdenticalBodiesStillDistinct covers the degenerate case
// (two same-line findings with identical bodies): the pairing must still assign
// each comment its own row rather than collapsing both onto one.
func TestPairCommentsToRows_IdenticalBodiesStillDistinct(t *testing.T) {
	ra, rb := uuid.New(), uuid.New()
	rows := []unboundCommentRow{
		{ID: ra, Path: "a.go", Line: 5, Body: "same"},
		{ID: rb, Path: "a.go", Line: 5, Body: "same"},
	}
	comments := []postedComment{
		{GithubID: 10, Path: "a.go", Line: 5, Body: "same"},
		{GithubID: 11, Path: "a.go", Line: 5, Body: "same"},
	}
	got := pairCommentsToRows(rows, comments)
	if len(got) != 2 {
		t.Fatalf("want 2 distinct bindings, got %d: %v", len(got), got)
	}
	if got[ra] == got[rb] {
		t.Fatalf("identical-body findings collapsed onto one github id %d", got[ra])
	}
}

// TestPairCommentsToRows_NoRowForComment: a comment whose (path, line) has no
// unbound row is skipped (leaves nothing bound), mirroring the old backfill's
// no-match behaviour.
func TestPairCommentsToRows_NoRowForComment(t *testing.T) {
	ra := uuid.New()
	rows := []unboundCommentRow{{ID: ra, Path: "a.go", Line: 5, Body: "x"}}
	comments := []postedComment{{GithubID: 99, Path: "b.go", Line: 9, Body: "y"}}
	got := pairCommentsToRows(rows, comments)
	if len(got) != 0 {
		t.Fatalf("want no bindings for a comment with no matching row, got %v", got)
	}
}
