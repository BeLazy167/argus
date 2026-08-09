package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ExportedDoc is one archived Supermemory document, ready to insert.
type ExportedDoc struct {
	ContainerTag string
	DocID        string
	CustomID     string
	Payload      json.RawMessage
}

// ArchiveExportedDocs upserts a page of exported documents for one
// installation. Idempotent on (installation_id, doc_id): re-running the export
// refreshes payloads rather than duplicating them, so an interrupted sweep can
// simply be re-run.
//
// Docs with an empty DocID are SKIPPED and counted, never inserted: the unique
// constraint would collapse every one of them onto a single row, and a
// silently-truncated archive is worse than a short one — this table's only job
// is to still hold the SM-only classes when the containers are gone. Returns
// (archived, skipped).
func (s *Store) ArchiveExportedDocs(ctx context.Context, installationID int64, docs []ExportedDoc) (int, int, error) {
	if len(docs) == 0 {
		return 0, 0, nil
	}
	batch := &pgx.Batch{}
	skipped := 0
	for _, d := range docs {
		if d.DocID == "" {
			skipped++
			continue
		}
		batch.Queue(`
			INSERT INTO memory_export_archive
				(installation_id, container_tag, doc_id, custom_id, payload)
			VALUES ($1, $2, $3, NULLIF($4, ''), $5)
			ON CONFLICT (installation_id, doc_id) DO UPDATE
			SET payload = EXCLUDED.payload,
			    container_tag = EXCLUDED.container_tag,
			    custom_id = EXCLUDED.custom_id,
			    exported_at = now()`,
			installationID, d.ContainerTag, d.DocID, d.CustomID, d.Payload)
	}
	queued := batch.Len()
	if queued == 0 {
		return 0, skipped, nil
	}
	br := s.Pool.SendBatch(ctx, batch)
	for i := 0; i < queued; i++ {
		if _, err := br.Exec(); err != nil {
			_ = br.Close()
			return 0, skipped, fmt.Errorf("archiving exported doc %d/%d: %w", i+1, queued, err)
		}
	}
	// Close reports failures the Exec loop cannot see. Discarding it would let
	// a page with an indeterminate final write be counted as archived, and a
	// snapshot that overstates what it holds is the one failure mode this table
	// must not have.
	if err := br.Close(); err != nil {
		return 0, skipped, fmt.Errorf("closing archive batch of %d docs (final write indeterminate): %w", queued, err)
	}
	return queued, skipped, nil
}

// CountArchivedDocs reports how many documents an installation has archived,
// so an export run can report progress and a re-run can be verified.
func (s *Store) CountArchivedDocs(ctx context.Context, installationID int64) (int64, error) {
	var n int64
	err := s.Pool.QueryRow(ctx,
		`SELECT count(*) FROM memory_export_archive WHERE installation_id = $1`,
		installationID).Scan(&n)
	return n, err
}
