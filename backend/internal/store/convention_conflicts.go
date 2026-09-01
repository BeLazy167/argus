package store

import (
	"context"
	"fmt"
	"time"
)

type PendingConventionConflict struct {
	ID                     int64
	NewContent, OldContent string
	LeftPR                 int
	IntroducingPR          int
}

func (s *Store) PendingConventionConflicts(ctx context.Context, installationID, repoID int64, prNumber int) ([]PendingConventionConflict, error) {
	rows, err := s.Pool.Query(ctx, `SELECT c.id,new.content,old.content,COALESCE((old.metadata->>'pr_number')::int,0),c.introducing_pr
 FROM convention_conflicts c
 JOIN memories new ON new.id=c.introducing_memory_id
 JOIN memories old ON old.id=CASE WHEN c.memory_low_id=c.introducing_memory_id THEN c.memory_high_id ELSE c.memory_low_id END
 WHERE c.installation_id=$1 AND c.repo_id=$2 AND c.introducing_pr <= $3 AND c.state='open' AND c.delivered_at IS NULL ORDER BY c.id`, installationID, repoID, prNumber)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PendingConventionConflict
	for rows.Next() {
		var v PendingConventionConflict
		if err := rows.Scan(&v.ID, &v.NewContent, &v.OldContent, &v.LeftPR, &v.IntroducingPR); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) DeliverConventionConflict(ctx context.Context, id int64, post func(context.Context) (string, int64, error)) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var delivered *time.Time
	if err := tx.QueryRow(ctx, `SELECT delivered_at FROM convention_conflicts WHERE id=$1 FOR UPDATE`, id).Scan(&delivered); err != nil {
		return err
	}
	if delivered != nil {
		return nil
	}
	nodeID, commentID, err := post(ctx)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE convention_conflicts SET artifact_node_id=$2,artifact_comment_id=$3,delivered_at=now(),claimed_at=NULL,updated_at=now() WHERE id=$1`, id, nodeID, commentID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ResolveConventionConflict(ctx context.Context, installationID int64, artifactCommentID int64, keepNew bool) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var conflictID, low, high, intro int64
	if err := tx.QueryRow(ctx, `SELECT id,memory_low_id,memory_high_id,introducing_memory_id FROM convention_conflicts WHERE installation_id=$1 AND artifact_comment_id=$2 AND state='open' FOR UPDATE`, installationID, artifactCommentID).Scan(&conflictID, &low, &high, &intro); err != nil {
		return fmt.Errorf("open convention conflict not found: %w", err)
	}
	earlier := low
	if earlier == intro {
		earlier = high
	}
	state := "resolved_old"
	winner, loser := earlier, intro
	if keepNew {
		state = "resolved_new"
		winner, loser = intro, earlier
	}
	// Restore the winner only when this candidate was its supersession target.
	if _, err := tx.Exec(ctx, `UPDATE memories SET invalidated_at=NULL,superseded_by=NULL,updated_at=now() WHERE id=$1 AND (superseded_by IS NULL OR superseded_by=$2)`, winner, intro); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE memories SET invalidated_at=now(),superseded_by=$2,updated_at=now() WHERE id=$1`, loser, winner); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE convention_conflicts SET state=$2,updated_at=now() WHERE id=$1`, conflictID, state); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
