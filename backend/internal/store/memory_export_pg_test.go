package store

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BeLazy167/argus/backend/internal/store/db"
)

func exportTestPool(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; PG-backed tests run where the CI harness provides a database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, ctx
}

func seedExportInstall(t *testing.T, ctx context.Context, pool *pgxpool.Pool) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(ctx, `
		INSERT INTO installations (installation_id, org_login)
		VALUES ((random() * 1000000000)::bigint, 'export-test')
		RETURNING id`).Scan(&id)
	if err != nil {
		t.Fatalf("seed installation: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM memory_export_archive WHERE installation_id = $1`, id)
		_, _ = pool.Exec(context.Background(), `DELETE FROM installations WHERE id = $1`, id)
	})
	return id
}

// TestArchiveExportedDocsIsIdempotent: the export is a one-shot snapshot that
// may be interrupted — a revoked key mid-sweep, a container that 500s, an
// operator hitting Ctrl-C. Re-running must resume rather than duplicate, and
// must refresh the payload so a re-export after a fix actually takes.
func TestArchiveExportedDocsIsIdempotent(t *testing.T) {
	pool, ctx := exportTestPool(t)
	st := &Store{Pool: pool, q: db.New(pool)}
	id := seedExportInstall(t, ctx, pool)

	docs := []ExportedDoc{
		{ContainerTag: "widget", DocID: "sm_1", CustomID: "widget--learned--a", Payload: json.RawMessage(`{"v":1}`)},
		{ContainerTag: "widget", DocID: "sm_2", CustomID: "", Payload: json.RawMessage(`{"v":1}`)},
	}
	archived, skipped, err := st.ArchiveExportedDocs(ctx, id, docs)
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if archived != 2 || skipped != 0 {
		t.Fatalf("first run archived=%d skipped=%d, want 2/0", archived, skipped)
	}

	// Re-run with a changed payload: same rows, refreshed content.
	docs[0].Payload = json.RawMessage(`{"v":2}`)
	if _, _, err := st.ArchiveExportedDocs(ctx, id, docs); err != nil {
		t.Fatalf("re-archive: %v", err)
	}

	total, err := st.CountArchivedDocs(ctx, id)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if total != 2 {
		t.Errorf("archive holds %d docs after two runs, want 2 — the export duplicates instead of resuming", total)
	}
	var payload string
	if err := pool.QueryRow(ctx,
		`SELECT payload::text FROM memory_export_archive WHERE installation_id = $1 AND doc_id = 'sm_1'`,
		id).Scan(&payload); err != nil {
		t.Fatalf("read payload: %v", err)
	}
	if payload != `{"v": 2}` {
		t.Errorf("payload = %s, want the refreshed {\"v\": 2}", payload)
	}
	// An empty customId must land NULL, not "", so the import step can tell
	// "no customId" from "customId is the empty string".
	var customID *string
	if err := pool.QueryRow(ctx,
		`SELECT custom_id FROM memory_export_archive WHERE installation_id = $1 AND doc_id = 'sm_2'`,
		id).Scan(&customID); err != nil {
		t.Fatalf("read custom_id: %v", err)
	}
	if customID != nil {
		t.Errorf("empty customId stored as %q, want NULL", *customID)
	}
}

// TestArchiveExportedDocsSkipsEmptyDocID is the data-loss guard. doc_id carries
// the unique constraint, so inserting several empty ones would collapse them
// onto a single row — silently discarding documents from the only copy of the
// SM-only classes. They are skipped and COUNTED instead, so the run can report
// what the archive does not hold.
func TestArchiveExportedDocsSkipsEmptyDocID(t *testing.T) {
	pool, ctx := exportTestPool(t)
	st := &Store{Pool: pool, q: db.New(pool)}
	id := seedExportInstall(t, ctx, pool)

	archived, skipped, err := st.ArchiveExportedDocs(ctx, id, []ExportedDoc{
		{ContainerTag: "widget", DocID: "", Payload: json.RawMessage(`{"a":1}`)},
		{ContainerTag: "widget", DocID: "", Payload: json.RawMessage(`{"b":2}`)},
		{ContainerTag: "widget", DocID: "sm_ok", Payload: json.RawMessage(`{"c":3}`)},
	})
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if skipped != 2 {
		t.Errorf("skipped = %d, want 2 — docs without a server id must be reported, not swallowed", skipped)
	}
	if archived != 1 {
		t.Errorf("archived = %d, want 1", archived)
	}
	total, err := st.CountArchivedDocs(ctx, id)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if total != 1 {
		t.Errorf("archive holds %d rows, want 1 (two empty ids must not collapse onto one row)", total)
	}
}

// TestArchiveExportedDocsScopesByInstallation: two installations can hold the
// same doc id only in principle, but the archive is read back per
// installation during import, so the scope must be part of the key rather than
// assumed unique globally.
func TestArchiveExportedDocsScopesByInstallation(t *testing.T) {
	pool, ctx := exportTestPool(t)
	st := &Store{Pool: pool, q: db.New(pool)}
	a := seedExportInstall(t, ctx, pool)
	b := seedExportInstall(t, ctx, pool)

	doc := []ExportedDoc{{ContainerTag: "widget", DocID: "sm_same", Payload: json.RawMessage(`{"x":1}`)}}
	if _, _, err := st.ArchiveExportedDocs(ctx, a, doc); err != nil {
		t.Fatalf("archive A: %v", err)
	}
	if _, _, err := st.ArchiveExportedDocs(ctx, b, doc); err != nil {
		t.Fatalf("archive B: %v", err)
	}
	for _, id := range []int64{a, b} {
		n, err := st.CountArchivedDocs(ctx, id)
		if err != nil {
			t.Fatalf("count %d: %v", id, err)
		}
		if n != 1 {
			t.Errorf("installation %d holds %d docs, want its own 1", id, n)
		}
	}
}
