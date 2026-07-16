package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// FindingState is the follow-up-ledger lifecycle state of a posted finding
// (migration 049, review_comments.state).
type FindingState string

const (
	// FindingStatePosted — shipped to the PR, no outcome yet (column default).
	FindingStatePosted FindingState = "posted"
	// FindingStateAddressed — the flagged code was fixed. PROVISIONAL: today it is
	// set heuristically (auto-resolve / gauge proximity) until #166's real judge,
	// so it is NOT terminal — an explicit human action (dismissal, manual resolve)
	// must still win over a heuristic addressed.
	FindingStateAddressed FindingState = "addressed"
	// FindingStateDismissed — developer rejected the finding (reply analysis
	// or 👎 reaction).
	FindingStateDismissed FindingState = "dismissed"
	// FindingStateDeferred — acknowledged but not fixed at merge time.
	FindingStateDeferred FindingState = "deferred"
	// FindingStateResolved — a maintainer closed the thread via `@argus resolve`
	// (migration 055): an explicit operator "these are handled" signal, distinct
	// from an addressed fix or a dismissal so it never poisons the gauge's
	// address/dismiss rates. Recoverable: an explicit human reply-dismissal can
	// supersede a mistaken resolve (resolved → dismissed) so it is not an
	// irreversible trap; nothing else leaves it.
	FindingStateResolved FindingState = "resolved"
	// FindingStateSuppressed — never posted; dropped by the suppression pass.
	// Set at insert time, never transitioned into or out of.
	FindingStateSuppressed FindingState = "suppressed"
)

// findingStateTransitions is the STRUCTURAL legality DAG — the physically legal
// state moves, independent of which event triggers them. suppressed is
// insert-only. addressed is heuristic/provisional (see FindingStateAddressed),
// so a human dismissal or manual resolve overrides it; a dismissal can be revised
// to addressed when the developer fixes the code after arguing; any open state
// can be manually resolved; and a mistaken resolve is recoverable by an explicit
// human dismissal (resolved → dismissed).
//
// Per-EVENT restrictions (which event may use which of these legal edges — e.g.
// a HEURISTIC 'addressed' may not leave 'dismissed', only a reply's human
// evidence may) live in FindingLifecycle's eventPolicies.allowedFrom and are
// enforced by UpdateFindingStateFrom. This map is the superset those subsets are
// validated against.
var findingStateTransitions = map[FindingState][]FindingState{
	FindingStatePosted:    {FindingStateAddressed, FindingStateDismissed, FindingStateDeferred, FindingStateResolved},
	FindingStateDeferred:  {FindingStateAddressed, FindingStateDismissed, FindingStateResolved},
	FindingStateDismissed: {FindingStateAddressed, FindingStateResolved},
	FindingStateAddressed: {FindingStateDismissed, FindingStateResolved},
	FindingStateResolved:  {FindingStateDismissed},
}

// CanTransitionFindingState reports whether the ledger STRUCTURALLY allows moving
// a finding from `from` to `to`. Self-transitions are allowed (idempotent replays
// — webhooks redeliver). Per-event source restrictions are stricter (see
// eventPolicies.allowedFrom).
func CanTransitionFindingState(from, to FindingState) bool {
	if from == to {
		return true
	}
	for _, t := range findingStateTransitions[from] {
		if t == to {
			return true
		}
	}
	return false
}

// UpdateFindingStateFrom moves a finding to `to`, but ONLY from one of
// allowedFrom, enforced atomically in the WHERE clause. FindingLifecycle passes
// the per-event allowed source set so a HEURISTIC event (auto-resolve / gauge
// 'addressed') cannot leave an explicit human 'dismissed', while a reply's
// human-evidence 'addressed' can. Idempotent + no-regress: `state <> to` plus an
// allowedFrom that never contains `to` means a replay or a disallowed source is a
// silent no-op (updated=false), never an error. A missing row is likewise a
// non-fatal no-op.
func (s *Store) UpdateFindingStateFrom(ctx context.Context, commentID uuid.UUID, to FindingState, allowedFrom []FindingState) (updated bool, err error) {
	if len(allowedFrom) == 0 {
		return false, nil
	}
	sources := make([]string, len(allowedFrom))
	for i, st := range allowedFrom {
		sources[i] = string(st)
	}
	tag, err := s.Pool.Exec(ctx, `
		UPDATE review_comments SET state = $2
		WHERE id = $1 AND state = ANY($3::text[]) AND state <> $2
	`, commentID, string(to), sources)
	if err != nil {
		return false, fmt.Errorf("updating finding state: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// GetCommentChangeClass returns the ReviewContract change class of the review
// that produced the given comment ("" when the review predates contracts).
// Feedback indexers stamp it on dismissal memories so retrieval can ignore
// prototype-era dismissals during production review.
func (s *Store) GetCommentChangeClass(ctx context.Context, commentID uuid.UUID) (string, error) {
	var class string
	err := s.Pool.QueryRow(ctx, `
		SELECT COALESCE(rv.review_contract->>'change_class', '')
		FROM review_comments rc
		JOIN reviews rv ON rc.review_id = rv.id
		WHERE rc.id = $1
	`, commentID).Scan(&class)
	if err != nil {
		return "", fmt.Errorf("getting comment change class: %w", err)
	}
	return class, nil
}
