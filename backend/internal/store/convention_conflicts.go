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
 WHERE c.installation_id=$1 AND c.repo_id=$2 AND c.introducing_pr=$3 AND c.state='open' AND c.delivered_at IS NULL ORDER BY c.id`, installationID, repoID, prNumber)
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
	state := "resolved_old"
	if keepNew {
		state = "resolved_new"
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var conflictID int64
	if err := tx.QueryRow(ctx, `SELECT id FROM convention_conflicts WHERE installation_id=$1 AND artifact_comment_id=$2 AND state='open' FOR UPDATE`, installationID, artifactCommentID).Scan(&conflictID); err != nil {
		return fmt.Errorf("open convention conflict not found: %w", err)
	}
	tag, err := tx.Exec(ctx, `WITH chosen AS (
  UPDATE convention_conflicts c SET state=$3,updated_at=now()
  WHERE c.installation_id=$1 AND c.artifact_comment_id=$2 AND c.state='open'
  RETURNING c.memory_low_id,c.memory_high_id,c.introducing_memory_id
 )
 UPDATE memories old SET invalidated_at=now(),superseded_by=new.id,updated_at=now()
 FROM chosen,memories new
 WHERE old.id=CASE WHEN $3='resolved_new' THEN (CASE WHEN chosen.memory_low_id=chosen.introducing_memory_id THEN chosen.memory_high_id ELSE chosen.memory_low_id END) ELSE chosen.introducing_memory_id END
 AND new.id=CASE WHEN $3='resolved_new' THEN chosen.introducing_memory_id ELSE (CASE WHEN chosen.memory_low_id=chosen.introducing_memory_id THEN chosen.memory_high_id ELSE chosen.memory_low_id END) END`, installationID, artifactCommentID, state)
	if err != nil {
		return fmt.Errorf("resolve convention conflict: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("open convention conflict not found")
	}
	return tx.Commit(ctx)
}
