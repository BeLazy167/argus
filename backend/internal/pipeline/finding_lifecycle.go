// Package pipeline — finding_lifecycle.go: the FindingLifecycle module (#165).
//
// A posted finding has three verbs in its life — dismissed, addressed, and
// closed (deferred at merge / manually resolved) — that used to be smeared
// across ~6 files that never called each other, so the GitHub thread state and
// the DB ledger permanently disagreed:
//
//   - GitHub-thread resolution lived in the orchestrator's auto-resolve and in
//     reply.go, calling client.ResolveReviewThread directly.
//   - review_comments.state was written independently by reply.go / reactions.go.
//   - merge outcomes landed in a SEPARATE table (comment_outcomes) via the gauge.
//
// FindingLifecycle collapses those into ONE surface, Transition(ctx, req), which
// is the ONLY writer of review_comments.state and the ONLY caller of GitHub
// thread resolution for a finding's lifecycle. Every writer (reply, reaction,
// auto-resolve, @argus resolve, PR-closed) routes through it, so the ledger and
// GitHub can no longer drift.
//
// Two-ledger reconciliation (review_comments.state vs comment_outcomes):
//
//   - review_comments.state is the SOURCE OF TRUTH for a finding's terminal
//     lifecycle state: posted → {addressed, dismissed, deferred, resolved}
//     (or suppressed at insert). Written ONLY here.
//   - comment_outcomes is the gauge's finer, multi-signal axis
//     (confirmed / dismissed / ignored / not_applicable_change_kind /
//     addressed_human / addressed_agent / deferred), keyed (comment, outcome)
//     and consumed by vw_review_gauge for the human-weighted address rate. It
//     keeps the human-vs-agent and confirmed/ignored granularity a single
//     ledger state cannot express.
//
// They cannot silently disagree because every trigger that writes a terminal
// comment_outcomes row ALSO drives the matching ledger state through Transition:
// reply/reaction dismissals write comment_outcomes.dismissed + state=dismissed;
// the gauge writes addressed_* + state=addressed and deferred + state=deferred.
// The split is deliberate: comment_outcomes answers "how was it addressed / what
// signal did we get"; state answers "what is the finding's terminal status".
//
// Thread resolution is a privileged action (it clears a finding from the
// unresolved-conversations / merge-gate view), so only TRUSTED, deliberate
// triggers do it: auto-resolve (a push that modified the anchored lines), a
// reply and `@argus resolve` — both gated on author_association ∈
// owner/member/collaborator at their call sites (a review-comment replier and an
// issue commenter are the same untrusted population as a reactor). REACTIONS are
// explicitly ledger-only (EventReactionDismissed): a 👎 is an untrusted,
// low-effort signal swept on every PR event from any user, and must never drive
// ResolveReviewThread. The gauge is ledger-only too — it runs post-close and
// never checks thread openness.
//
// Two safety rules keep the ledger honest (see Transition + eventPolicies):
//   - Ordering by event class: a DECISION (dismissed/deferred) is recorded first
//     then the thread best-effort resolved (a 502 can't drop a human decision an
//     comment_outcomes already holds); an ASSERTION (addressed/resolved) resolves
//     first and only asserts the closed state on success (never over-claims).
//   - Provenance: a HEURISTIC 'addressed' (auto-resolve/gauge proximity) may not
//     overwrite an explicit human 'dismissed'; only a reply's human evidence
//     (EventAddressedByReply) or a manual resolve may leave 'dismissed'. A
//     mistaken manual 'resolved' is itself recoverable by a human reply-dismissal.
//
// ACCEPTED drift corners (intentional — so "the ledger and GitHub never disagree"
// is precise, not absolute):
//   - A 👎-reaction-dismissed finding is ledger-only, so its GitHub thread stays
//     OPEN while state=dismissed. If a later push modifies the anchored lines,
//     auto-resolve resolves the thread but the provenance rule keeps the ledger
//     at 'dismissed' (a heuristic addressed never overrides a human dismissal) —
//     so that finding ends thread=resolved / state=dismissed, by design.
//   - An UNTRUSTED reply that the analyzer read as a dismissal still records the
//     soft learning signal (comment_outcomes.dismissed) but writes NO terminal
//     state, so state stays 'posted' while comment_outcomes holds 'dismissed' —
//     the soft signal informs pattern suppression without an untrusted user
//     authoritatively closing the finding's lifecycle.
package pipeline

import (
	"context"
	"fmt"
	"log/slog"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/store"
	"github.com/google/uuid"
)

// LifecycleEvent is the input vocabulary of the finding state machine. Multiple
// call sites may raise the same event (a 👎 reaction and a reply both raise
// EventDismissed); each event maps to exactly one terminal ledger state.
type LifecycleEvent string

const (
	// EventReactionDismissed — a 👎-dominant reaction dismissed the finding.
	// LEDGER-ONLY: it must NOT resolve the GitHub thread. Reactions are an
	// untrusted, low-effort signal swept on every PR event from ANY user
	// (including fork contributors with no write/triage), so letting a reaction
	// drive ResolveReviewThread would let anyone clear a finding from the
	// unresolved-conversations / merge-gate view using the app's write token.
	// State=dismissed is kept for suppression memory (the pre-#165 behavior).
	EventReactionDismissed LifecycleEvent = "reaction_dismissed"
	// EventDismissed — the developer rejected the finding in a REPLY (resolve-
	// with-learning / not-applicable). A DECISION (records a human judgement true
	// regardless of thread state), so it writes the ledger first, then best-effort
	// resolves the thread: a reply is a deliberate authored act by someone who can
	// comment (pre-#165 the reply path resolved the thread). May recover a
	// mistaken manual resolve (resolved → dismissed).
	EventDismissed LifecycleEvent = "dismissed"
	// EventAddressed — a HEURISTIC fix signal: a push modified the anchored lines
	// (auto-resolve on synchronize). An ASSERTION that the thread is closed, so it
	// resolves first and only asserts state=addressed on success. Being heuristic,
	// it may NOT override an explicit human 'dismissed' (only posted/deferred →
	// addressed). NOTE: real "was it fixed?" judging is slice #166.
	EventAddressed LifecycleEvent = "addressed"
	// EventAddressedByReply — a reply in which the developer CONFIRMED the finding
	// and said they fixed it (human evidence, not a proximity heuristic). Like
	// EventAddressed it asserts the thread closed, but its human evidence MAY leave
	// an explicit 'dismissed' (dev fixed the code after first arguing).
	EventAddressedByReply LifecycleEvent = "addressed_by_reply"
	// EventAddressedAtMerge — the gauge detected the fix only at merge time
	// (first-vs-last compare). LEDGER-ONLY: it reconciles state=addressed with the
	// comment_outcomes addressed_* signal, but does NOT resolve the thread — the
	// gauge does not check thread openness, and thread resolution during the PR's
	// life is auto-resolve's job (the PR is now closed, the thread moot). Heuristic,
	// so it may NOT override 'dismissed' either.
	EventAddressedAtMerge LifecycleEvent = "addressed_at_merge"
	// EventDeferred — the PR closed without merging; the finding was acknowledged
	// but not fixed. A DECISION; ledger-only (no live thread worth resolving on a
	// closed-unmerged PR).
	EventDeferred LifecycleEvent = "deferred"
	// EventResolvedManually — a maintainer ran `@argus resolve` to close all open
	// Argus threads. AUTHORIZATION is enforced at the command (author_association
	// ∈ owner/member/collaborator) BEFORE this event is raised. An ASSERTION that
	// the thread closed; recorded in its own terminal state (neither addressed nor
	// dismissed).
	EventResolvedManually LifecycleEvent = "resolved_manually"
)

// Per-event allowed source states (subsets of the structural legality DAG in
// store.findingStateTransitions). The KEY invariant (should-fix #3): a HEURISTIC
// addressed event (auto-resolve, gauge) may move only posted/deferred → addressed
// and can NEVER overwrite an explicit human 'dismissed'; only the reply's human
// evidence (EventAddressedByReply) may.
var (
	// reactionDismissSources — a 👎 may correct a heuristic 'addressed' but not a
	// maintainer's manual 'resolved'.
	reactionDismissSources = []store.FindingState{store.FindingStatePosted, store.FindingStateDeferred, store.FindingStateAddressed}
	// replyDismissSources — a reply dismissal additionally recovers a mistaken
	// manual resolve (resolved → dismissed).
	replyDismissSources = []store.FindingState{store.FindingStatePosted, store.FindingStateDeferred, store.FindingStateAddressed, store.FindingStateResolved}
	// heuristicAddressSources — proximity 'addressed' must NOT leave 'dismissed'.
	heuristicAddressSources = []store.FindingState{store.FindingStatePosted, store.FindingStateDeferred}
	// replyAddressSources — human evidence may override a 'dismissed'.
	replyAddressSources = []store.FindingState{store.FindingStatePosted, store.FindingStateDeferred, store.FindingStateDismissed}
	// deferSources — only a still-open (posted) finding defers.
	deferSources = []store.FindingState{store.FindingStatePosted}
	// manualResolveSources — any non-resolved state may be manually closed.
	manualResolveSources = []store.FindingState{store.FindingStatePosted, store.FindingStateDeferred, store.FindingStateDismissed, store.FindingStateAddressed}
)

// eventPolicy is an event's output contract: its target ledger state, whether it
// resolves the GitHub thread (the security boundary — reactions + gauge are
// ledger-only), whether it is a DECISION (record first, resolve best-effort) or
// an ASSERTION (resolve first, assert state only on success), and the source
// states it may transition FROM.
type eventPolicy struct {
	state          store.FindingState
	resolvesThread bool
	decision       bool
	allowedFrom    []store.FindingState
}

var eventPolicies = map[LifecycleEvent]eventPolicy{
	// Decision events: a human/merge decision, true regardless of thread state —
	// write the ledger UNCONDITIONALLY, then best-effort resolve.
	EventReactionDismissed: {store.FindingStateDismissed, false, true, reactionDismissSources},
	EventDismissed:         {store.FindingStateDismissed, true, true, replyDismissSources},
	EventDeferred:          {store.FindingStateDeferred, false, true, deferSources},
	// Assertion events: state claims the thread is closed — resolve FIRST, assert
	// the state only on a successful resolve (or when there is no thread).
	EventAddressed:        {store.FindingStateAddressed, true, false, heuristicAddressSources},
	EventAddressedByReply: {store.FindingStateAddressed, true, false, replyAddressSources},
	EventAddressedAtMerge: {store.FindingStateAddressed, false, false, heuristicAddressSources},
	EventResolvedManually: {store.FindingStateResolved, true, false, manualResolveSources},
}

// findingLedger is the DB seam FindingLifecycle writes the ledger + reads thread
// identity through. Implemented by *store.Store; faked in tests.
type findingLedger interface {
	// UpdateFindingStateFrom moves a finding to `to` only from one of allowedFrom,
	// transition-guarded + idempotent in the store. Returns whether the row moved.
	UpdateFindingStateFrom(ctx context.Context, commentID uuid.UUID, to store.FindingState, allowedFrom []store.FindingState) (bool, error)
	// SetFindingResolvedSHA stamps the resolving push commit onto a finding
	// (stamp-once). Best-effort — additive to the ledger move, never a transition.
	SetFindingResolvedSHA(ctx context.Context, commentID uuid.UUID, sha string) error
	// GetThreadLinkForComment returns the finding's stored GitHub thread identity
	// (ThreadRegistry, #162): the authoritative node id for exactly this finding.
	GetThreadLinkForComment(ctx context.Context, commentID uuid.UUID) (*store.ThreadLink, error)
	// GetCommentByGithubID maps a thread's first-comment REST id back to its
	// tracked finding row, for the bulk-resolver entry point (TransitionThread).
	GetCommentByGithubID(ctx context.Context, githubCommentID int64) (*store.ReviewComment, error)
}

// threadResolver is the GitHub seam FindingLifecycle resolves threads through.
// Implemented by *ghpkg.Client; faked in tests.
type threadResolver interface {
	ResolveReviewThread(ctx context.Context, installationID int64, threadID string) error
	FindThreadForComment(ctx context.Context, installationID int64, owner, repo string, prNumber int, commentNodeID string) (string, error)
	ListReviewThreads(ctx context.Context, installationID int64, owner, repo string, prNumber int) ([]ghpkg.ReviewThread, error)
}

// FindingLifecycle owns the single Transition surface over the DB ledger and the
// GitHub thread. Construct once and share; it holds no per-call state.
type FindingLifecycle struct {
	ledger findingLedger
	gh     threadResolver
	logger *slog.Logger
}

// NewFindingLifecycle wires the module over the store ledger and GitHub client.
func NewFindingLifecycle(ledger findingLedger, gh threadResolver, logger *slog.Logger) *FindingLifecycle {
	return &FindingLifecycle{ledger: ledger, gh: gh, logger: logger}
}

// FindingTransition is one Transition request: the finding, the event, and the
// GitHub coordinates + optional thread hints needed to resolve its thread.
type FindingTransition struct {
	// FindingID is the review_comments PK — the finding's identity.
	FindingID uuid.UUID
	// Event is the lifecycle event to apply.
	Event LifecycleEvent

	// GitHub coordinates for thread resolution. Unused by ledger-only events
	// (EventDeferred), required by thread-resolving events.
	InstallationID int64
	Owner          string
	Repo           string
	PRNumber       int

	// ThreadNodeID, when set, is the GraphQL review-thread node id to resolve —
	// callers that already hold it (auto-resolve, @argus resolve) pass it so the
	// module resolves exactly that thread without any lookup.
	ThreadNodeID string
	// CommentNodeID is the GraphQL node id of the finding's review COMMENT, used
	// as a last-resort thread lookup (the reply path) for rows that predate the
	// ThreadRegistry and so have no stored link.
	CommentNodeID string

	// ResolvedSHA, when set, is the push commit that closed this finding. It is
	// stamped onto review_comments.resolved_sha (stamp-once) ONLY when this event
	// actually moves the ledger to addressed/resolved — the resolved-by-commit
	// breadcrumb (#167). Empty for events with no known SHA (@argus resolve, gauge
	// at-merge), which leave the breadcrumb absent.
	ResolvedSHA string
}

// TransitionResult reports what Transition did, for callers that count or
// surface it (auto-resolve counters, the @argus resolve reply).
type TransitionResult struct {
	// NewState is the event's target ledger state (attempted regardless of
	// whether the guard allowed the move).
	NewState store.FindingState
	// LedgerChanged is true when review_comments.state actually moved.
	LedgerChanged bool
	// ThreadResolved is true when a GitHub thread was resolved this call.
	ThreadResolved bool
	// ThreadErr is the error from a resolve attempt (nil when resolved, or when
	// there was no thread to resolve). Callers that surface a user-facing message
	// (the @argus resolve command) classify it.
	ThreadErr error
}

// Transition applies one lifecycle event to one finding over BOTH the ledger and
// the GitHub thread. It is the ONLY writer of review_comments.state and the ONLY
// finding-lifecycle caller of ResolveReviewThread.
//
// The ledger↔thread ordering splits by event CLASS so neither over-claims:
//
//   - DECISION events (dismissed, reaction-dismissed, deferred) record a human /
//     merge judgement that is true regardless of thread state, so they write the
//     ledger UNCONDITIONALLY, then best-effort resolve the thread. A 502 on the
//     resolve must NOT drop the recorded decision (comment_outcomes already has
//     it; the two would otherwise disagree with no retry).
//   - ASSERTION events (addressed, resolved) claim the thread is CLOSED, so they
//     resolve FIRST and assert the terminal state ONLY on a successful resolve
//     (or when there is no thread). A failed resolve leaves the prior retryable
//     state — the ledger never asserts closed while GitHub shows the thread open.
//
// Thread resolution is gated on the THREAD's open status, NOT the ledger state:
// the resolving callers (auto-resolve, @argus resolve) invoke Transition only for
// threads they have listed as unresolved, so a thread a human REOPENED gets
// re-closed even when its ledger state is already terminal (the ledger write is
// then an idempotent no-op). Per-event allowedFrom (see eventPolicies) keeps a
// heuristic 'addressed' from overwriting an explicit human 'dismissed'.
//
// Best-effort and non-fatal — a failed GitHub call must never break the webhook —
// so Transition returns an error only for an unknown event (a programming
// mistake); everything else is reported via the result and logged.
func (l *FindingLifecycle) Transition(ctx context.Context, req FindingTransition) (TransitionResult, error) {
	pol, ok := eventPolicies[req.Event]
	if !ok {
		return TransitionResult{}, fmt.Errorf("finding-lifecycle: unknown event %q", req.Event)
	}
	res := TransitionResult{NewState: pol.state}

	if pol.decision {
		// Record the decision first, then best-effort resolve.
		l.writeLedger(ctx, req, pol, &res)
		l.resolveThread(ctx, req, pol, &res)
		return res, nil
	}

	// Assertion: resolve the thread first; assert the state only on success (or
	// when there is nothing to resolve).
	if pol.resolvesThread {
		if nodeID := l.locateThread(ctx, req); nodeID == "" {
			l.logger.Debug("finding-lifecycle: no thread to resolve", "finding", req.FindingID, "event", req.Event)
		} else if rerr := l.gh.ResolveReviewThread(ctx, req.InstallationID, nodeID); rerr != nil {
			res.ThreadErr = rerr
			l.logger.Warn("finding-lifecycle: resolve thread", "error", rerr, "finding", req.FindingID, "thread", nodeID)
			return res, nil // under-claim: leave the prior state
		} else {
			res.ThreadResolved = true
		}
	}
	l.writeLedger(ctx, req, pol, &res)
	return res, nil
}

// writeLedger applies the event's ledger move (the ONLY writer of
// review_comments.state), restricted to the event's allowed source states.
func (l *FindingLifecycle) writeLedger(ctx context.Context, req FindingTransition, pol eventPolicy, res *TransitionResult) {
	changed, err := l.ledger.UpdateFindingStateFrom(ctx, req.FindingID, pol.state, pol.allowedFrom)
	if err != nil {
		l.logger.Warn("finding-lifecycle: ledger update", "error", err, "finding", req.FindingID, "event", req.Event)
		return
	}
	res.LedgerChanged = changed

	// Resolved-by-commit breadcrumb (#167): stamp the resolving SHA ONLY when this
	// event actually moved the finding to a resolved-ish terminal — so we never
	// misattribute a SHA to a finding a human dismissal kept at 'dismissed' (the
	// provenance guard returns changed=false there), and never to a decision event
	// (dismissed/deferred). Best-effort; stamp-once is enforced in the store.
	if changed && req.ResolvedSHA != "" &&
		(pol.state == store.FindingStateAddressed || pol.state == store.FindingStateResolved) {
		if err := l.ledger.SetFindingResolvedSHA(ctx, req.FindingID, req.ResolvedSHA); err != nil {
			l.logger.Warn("finding-lifecycle: stamp resolved sha", "error", err, "finding", req.FindingID)
		}
	}
}

// resolveThread best-effort resolves the finding's thread (no-op for ledger-only
// events). Used by the decision path, where a failure is recorded on the result
// but does not undo the already-written ledger decision.
func (l *FindingLifecycle) resolveThread(ctx context.Context, req FindingTransition, pol eventPolicy, res *TransitionResult) {
	if !pol.resolvesThread {
		return
	}
	nodeID := l.locateThread(ctx, req)
	if nodeID == "" {
		l.logger.Debug("finding-lifecycle: no thread to resolve", "finding", req.FindingID, "event", req.Event)
		return
	}
	if rerr := l.gh.ResolveReviewThread(ctx, req.InstallationID, nodeID); rerr != nil {
		res.ThreadErr = rerr
		l.logger.Warn("finding-lifecycle: resolve thread", "error", rerr, "finding", req.FindingID, "thread", nodeID)
		return
	}
	res.ThreadResolved = true
}

// ThreadTransition is a bulk-resolver request: resolve one review thread the
// caller has already listed as UNRESOLVED, and record the event on its tracked
// finding if the thread maps to one.
type ThreadTransition struct {
	Event LifecycleEvent
	// ThreadNodeID is the GraphQL node id of the (confirmed-open) thread.
	ThreadNodeID string
	// RestCommentID is the thread's first-comment REST id — maps to the finding.
	RestCommentID  int64
	InstallationID int64
	Owner          string
	Repo           string
	PRNumber       int

	// ResolvedSHA, when set, is the push commit that closed the thread — forwarded
	// to Transition to stamp the finding's resolved-by-commit breadcrumb (#167).
	ResolvedSHA string
}

// TransitionThread is the single entry point the bulk resolvers (auto-resolve,
// @argus resolve) use, so their "map thread→finding, route or direct-resolve"
// glue is written and tested ONCE here. The caller has already filtered to
// unresolved threads. A thread that maps to a tracked finding routes through
// Transition (ledger + thread); an untracked thread (pre-DB / unmapped) is
// resolved directly — it has no ledger row to move.
func (l *FindingLifecycle) TransitionThread(ctx context.Context, req ThreadTransition) (TransitionResult, error) {
	if finding, err := l.ledger.GetCommentByGithubID(ctx, req.RestCommentID); err == nil && finding != nil {
		return l.Transition(ctx, FindingTransition{
			FindingID:      finding.ID,
			Event:          req.Event,
			ThreadNodeID:   req.ThreadNodeID,
			InstallationID: req.InstallationID,
			Owner:          req.Owner,
			Repo:           req.Repo,
			PRNumber:       req.PRNumber,
			ResolvedSHA:    req.ResolvedSHA,
		})
	}

	// Untracked thread — resolve directly (no ledger).
	res := TransitionResult{}
	if pol, ok := eventPolicies[req.Event]; ok {
		res.NewState = pol.state
	}
	if req.ThreadNodeID == "" {
		return res, nil
	}
	if err := l.gh.ResolveReviewThread(ctx, req.InstallationID, req.ThreadNodeID); err != nil {
		res.ThreadErr = err
		l.logger.Warn("finding-lifecycle: direct resolve of untracked thread", "error", err, "thread", req.ThreadNodeID)
	} else {
		res.ThreadResolved = true
	}
	return res, nil
}

// locateThread finds the GraphQL node id of the finding's own review thread,
// preferring the most authoritative source and never a line-proximity guess:
//
//  1. a caller-supplied node id (auto-resolve / @argus resolve already hold it);
//  2. the finding's stored ThreadRegistry link — its OWN node id, so dismissing
//     finding B targets B's thread, never a same-line neighbour's;
//  3. an EXACT REST-comment-id join against the live thread list (rows hydrated
//     before the node id existed, but whose github_comment_id is known);
//  4. the reply path's comment node id → its thread (rows predating the registry).
//
// Returns "" when no thread can be identified — the ledger still moves; there is
// simply nothing to resolve.
func (l *FindingLifecycle) locateThread(ctx context.Context, req FindingTransition) string {
	if req.ThreadNodeID != "" {
		return req.ThreadNodeID
	}

	link, err := l.ledger.GetThreadLinkForComment(ctx, req.FindingID)
	if err != nil {
		link = nil // old row / no link — fall through to the node-id path
	}
	if link != nil && link.ThreadNodeID != nil && *link.ThreadNodeID != "" {
		return *link.ThreadNodeID
	}

	// Exact REST-id join fallback: the stored link knows the finding's REST
	// comment id even when the GraphQL node id was never hydrated.
	if link != nil && link.RestCommentID != nil && req.Owner != "" {
		if id := l.threadByRestID(ctx, req, *link.RestCommentID); id != "" {
			return id
		}
	}

	// Last resort (reply path): resolve the comment node id → its thread.
	if req.CommentNodeID != "" && req.Owner != "" {
		tid, ferr := l.gh.FindThreadForComment(ctx, req.InstallationID, req.Owner, req.Repo, req.PRNumber, req.CommentNodeID)
		if ferr != nil {
			l.logger.Warn("finding-lifecycle: find thread for comment", "error", ferr, "finding", req.FindingID)
			return ""
		}
		return tid
	}
	return ""
}

// threadByRestID lists the PR's review threads and returns the node id of the
// one whose first comment is the finding's REST comment id — an exact join, not
// a proximity match.
func (l *FindingLifecycle) threadByRestID(ctx context.Context, req FindingTransition, restID int64) string {
	threads, err := l.gh.ListReviewThreads(ctx, req.InstallationID, req.Owner, req.Repo, req.PRNumber)
	if err != nil {
		l.logger.Warn("finding-lifecycle: list threads", "error", err, "finding", req.FindingID)
		return ""
	}
	for _, t := range threads {
		if t.FirstCommentID == restID {
			return t.ID
		}
	}
	return ""
}
