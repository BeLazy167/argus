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
	// FindingStateAddressed — the flagged code was fixed (set by the
	// addressed-detector, PR4; the state exists now so the ledger is complete).
	FindingStateAddressed FindingState = "addressed"
	// FindingStateDismissed — developer rejected the finding (reply analysis
	// or 👎 reaction).
	FindingStateDismissed FindingState = "dismissed"
	// FindingStateDeferred — acknowledged but not fixed at merge time.
	FindingStateDeferred FindingState = "deferred"
	// FindingStateSuppressed — never posted; dropped by the suppression pass.
	// Set at insert time, never transitioned into or out of.
	FindingStateSuppressed FindingState = "suppressed"
)

// findingStateTransitions maps each state to the states it may move to.
// suppressed and addressed are terminal; a dismissal can still be revised to
// addressed when the developer fixes the code after arguing.
var findingStateTransitions = map[FindingState][]FindingState{
	FindingStatePosted:    {FindingStateAddressed, FindingStateDismissed, FindingStateDeferred},
	FindingStateDeferred:  {FindingStateAddressed, FindingStateDismissed},
	FindingStateDismissed: {FindingStateAddressed},
}

// CanTransitionFindingState reports whether the ledger allows moving a finding
// from `from` to `to`. Self-transitions are allowed (idempotent replays —
// webhooks redeliver).
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

// findingStateSources returns every state from which `to` is reachable,
// including `to` itself (idempotent replays).
func findingStateSources(to FindingState) []string {
	sources := []string{string(to)}
	for from, targets := range findingStateTransitions {
		for _, t := range targets {
			if t == to {
				sources = append(sources, string(from))
			}
		}
	}
	return sources
}

// UpdateFindingState moves a finding to `to`, enforcing the ledger's transition
// rules in the WHERE clause so a stale writer can never regress a state (e.g.
// a replayed dismissal webhook cannot un-address a fixed finding). Returns
// updated=false when the row is missing or the transition is illegal — both
// are non-fatal for callers.
func (s *Store) UpdateFindingState(ctx context.Context, commentID uuid.UUID, to FindingState) (updated bool, err error) {
	sources := findingStateSources(to)
	if len(sources) == 1 && to != FindingState(sources[0]) {
		return false, fmt.Errorf("finding state %q has no legal inbound transition", to)
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
