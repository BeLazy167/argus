// Command backfill-memory populates the Postgres memories table from the
// phase-0 archive (memory_export_archive) and repoints the pattern id mirrors.
//
// The archive is a complete, unparsed snapshot of every exported document,
// so it is the PRIMARY source rather than re-derivation from Postgres source
// rows. That matters for fidelity: it preserves the real customIds the
// deterministic id builders assume, the dismissal `reason` extras, the decayed
// `_shared` confidence values, and the synthesis/feedback classes that exist
// nowhere else. Re-derivation would rebuild ~90% of the corpus approximately
// and silently drop the rest.
//
// Writes go through PGIndexer.ImportDocs — the same path live writes use — so
// an imported row is indistinguishable from one the pipeline wrote.
//
// Safe to re-run: upserts key on (installation_id, custom_id).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/BeLazy167/argus/backend/internal/crypto"
	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/store"
)

// pageSize bounds one embed+upsert batch. The embedder batches internally; this
// bounds memory held at once on a 1GB machine where a review may run alongside.
const pageSize = 100

type runConfig struct {
	installation int64
	plan         bool
	repoint      bool
	reembed      bool
}

func main() {
	var cfg runConfig
	flag.Int64Var(&cfg.installation, "installation", 0, "restrict to one installation id (0 = every installation present in the archive)")
	flag.BoolVar(&cfg.plan, "plan", false, "dry-run: report what would be written, touch nothing")
	flag.BoolVar(&cfg.repoint, "repoint", false, "also rewrite patterns.memory_doc_id from archived server doc ids to the archived customIds")
	flag.BoolVar(&cfg.reembed, "reembed", false, "repair mode: embed live memories rows that have a NULL embedding, then exit (ignores the archive)")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)
	started := time.Now()
	logger.Info("memory backfill command started",
		"installation_id", cfg.installation, "plan", cfg.plan, "repoint", cfg.repoint, "reembed", cfg.reembed)

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		logger.Error("DATABASE_URL is required")
		os.Exit(1)
	}
	// Needed to resolve a per-installation BYOK embeddings key. Without it the
	// registry silently falls back to the platform key and stamps rows with the
	// WRONG embedding space — and embedding_space is an equality gate at read
	// time, not a label, so those rows would score 0 forever while reading fine.
	if err := crypto.InitFromEnv(); err != nil {
		logger.Error("ENCRYPTION_KEY is required to resolve embeddings keys", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		logger.Info("memory backfill shutdown requested", "reason", ctx.Err())
	}()

	connectionStarted := time.Now()
	logger.InfoContext(ctx, "memory backfill database initialization started")
	st, err := store.New(ctx, dsn)
	if err != nil {
		logger.ErrorContext(ctx, "memory backfill database initialization failed",
			"duration_ms", time.Since(connectionStarted).Milliseconds(), "error", err)
		os.Exit(1)
	}
	logger.InfoContext(ctx, "memory backfill database initialization completed",
		"duration_ms", time.Since(connectionStarted).Milliseconds())
	defer func() {
		logger.Info("memory backfill database close started")
		st.Close()
		logger.Info("memory backfill database close completed")
	}()

	if err := run(ctx, logger, st, cfg); err != nil {
		logger.Error("memory backfill command failed", "duration_ms", time.Since(started).Milliseconds(), "error", err)
		os.Exit(1)
	}
	logger.Info("memory backfill command completed", "duration_ms", time.Since(started).Milliseconds())
}

func run(ctx context.Context, logger *slog.Logger, st *store.Store, cfg runConfig) error {
	runStarted := time.Now()
	mode := "archive_import"
	if cfg.reembed {
		mode = "reembed"
	}
	logger.InfoContext(ctx, "memory backfill run started", "mode", mode,
		"installation_id", cfg.installation, "plan", cfg.plan, "repoint", cfg.repoint)
	logger.InfoContext(ctx, "memory backfill registries initialization started")
	embedRegistry := memory.NewEmbedderRegistry(st, memory.PlatformEmbeddings{
		APIKey:     os.Getenv("EMBEDDINGS_API_KEY"),
		BaseURL:    getenv("EMBEDDINGS_BASE_URL", "https://api.voyageai.com/v1"),
		Model:      getenv("EMBEDDINGS_MODEL", "voyage-4"),
		Dimensions: 1024,
	}, logger)
	memRegistry := memory.NewRegistry(logger).WithPostgresBackend(st.Pool, embedRegistry)
	logger.InfoContext(ctx, "memory backfill registries initialization completed")

	if cfg.reembed {
		return runReembed(ctx, logger, st, embedRegistry, memRegistry, cfg)
	}

	installs, err := archivedInstallations(ctx, st, cfg.installation)
	if err != nil {
		return fmt.Errorf("listing archived installations: %w", err)
	}
	logger.InfoContext(ctx, "archive backfill cycle started", "installations", len(installs), "plan", cfg.plan, "repoint", cfg.repoint)

	var totalImported, totalSkipped int
	for _, id := range installs {
		if ctx.Err() != nil {
			logger.InfoContext(context.WithoutCancel(ctx), "archive backfill cycle stopped", "reason", ctx.Err(),
				"imported", totalImported, "skipped_no_type", totalSkipped, "duration_ms", time.Since(runStarted).Milliseconds())
			return ctx.Err()
		}
		installationStarted := time.Now()
		logger.InfoContext(ctx, "archive installation backfill started", "installation_id", id)
		imported, skipped, err := backfillInstallation(ctx, logger, st, embedRegistry, memRegistry, id, cfg)
		totalImported += imported
		totalSkipped += skipped
		if err != nil {
			logger.ErrorContext(ctx, "archive installation backfill failed", "installation_id", id,
				"imported", imported, "skipped_no_type", skipped,
				"duration_ms", time.Since(installationStarted).Milliseconds(), "error", err)
			return fmt.Errorf("installation %d: %w", id, err)
		}
		if cfg.repoint && !cfg.plan {
			repointStarted := time.Now()
			logger.InfoContext(ctx, "pattern repoint started", "installation_id", id)
			repointed, orphaned, err := repointPatterns(ctx, st, id)
			if err != nil {
				logger.ErrorContext(ctx, "pattern repoint failed", "installation_id", id,
					"duration_ms", time.Since(repointStarted).Milliseconds(), "error", err)
				return fmt.Errorf("repointing installation %d: %w", id, err)
			}
			// Orphans are patterns whose archived doc no longer exists —
			// decay retired it, or its container was unreachable at export.
			// Their id is cleared rather than left dangling: a dangling id
			// makes deletion a silent no-op, while NULL marks the row as
			// unindexed so the next write re-indexes it properly.
			logger.InfoContext(ctx, "pattern repoint completed", "installation_id", id,
				"repointed", repointed, "orphaned_ids_cleared", orphaned,
				"duration_ms", time.Since(repointStarted).Milliseconds())
		}
		logger.InfoContext(ctx, "archive installation backfill completed", "installation_id", id,
			"imported", imported, "skipped_no_type", skipped,
			"duration_ms", time.Since(installationStarted).Milliseconds())
	}

	logger.InfoContext(ctx, "archive backfill cycle completed", "imported", totalImported,
		"skipped_no_type", totalSkipped, "duration_ms", time.Since(runStarted).Milliseconds())
	if totalImported == 0 && !cfg.plan {
		return fmt.Errorf("nothing was imported; refusing to report success on an empty corpus")
	}
	return nil
}

// runReembed repairs rows the write path left unembedded. It is independent of
// the archive: PGIndexer fails open, so any embeddings outage since the
// migration can have left NULL vectors behind, and those rows are reachable
// through the full-text leg only until something re-embeds them.
//
// Installations are resolved from the damage itself rather than from a
// registry, so a clean fleet is a no-op that reports zero instead of an error.
func runReembed(ctx context.Context, logger *slog.Logger, st *store.Store, embeds *memory.EmbedderRegistry, registry *memory.Registry, cfg runConfig) error {
	runStarted := time.Now()
	logger.InfoContext(ctx, "reembed discovery started", "installation_id", cfg.installation, "plan", cfg.plan)
	rows, err := st.Pool.Query(ctx, `
		SELECT DISTINCT installation_id
		FROM live_memories
		WHERE $1 = 0 OR installation_id = $1
		ORDER BY installation_id`, cfg.installation)
	if err != nil {
		logger.ErrorContext(ctx, "reembed discovery failed", "phase", "list_installations", "error", err)
		return fmt.Errorf("listing installations with live memory: %w", err)
	}
	var installationIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("scanning installation: %w", err)
		}
		installationIDs = append(installationIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		logger.ErrorContext(ctx, "reembed discovery failed", "phase", "read_installations", "error", err)
		return fmt.Errorf("reading installations: %w", err)
	}
	logger.InfoContext(ctx, "reembed installation discovery completed", "installation_count", len(installationIDs))

	type target struct {
		id      int64
		pending int64
	}
	var targets []target
	for _, id := range installationIDs {
		discoveryStarted := time.Now()
		logger.InfoContext(ctx, "reembed installation inspection started", "installation_id", id)
		embedder, _ := embeds.GetEmbedder(ctx, id)
		if embedder == nil {
			if cfg.plan {
				// Preserve dry-run behavior: unavailable providers are reported and
				// omitted because no current-space count can be computed.
				logger.Warn("reembed: no embedder resolved; skipping installation", "installation_id", id)
				continue
			}
			// A cached nil is not execution authority. PostgreSQL may have gained
			// a provider since it was cached; strict repair resolves under lock.
			targets = append(targets, target{id: id})
			continue
		}

		idx, ok := registry.GetIndexer(ctx, id).(*memory.PGIndexer)
		if !ok {
			logger.Warn("reembed: no indexer resolved; skipping installation", "installation_id", id)
			continue
		}
		pending, err := idx.CountReembedPending(ctx)
		if err != nil {
			if cfg.plan {
				return fmt.Errorf("counting installation %d reembed rows: %w", id, err)
			}
			// Counting is progress metadata, not write authority. The strict
			// repair below may resolve a newly configured provider even when this
			// process still has a cached stale reader.
			logger.Warn("reembed: could not count with cached reader; continuing with durable repair",
				"installation_id", id, "error", err)
		}
		if cfg.plan && pending == 0 {
			logger.InfoContext(ctx, "reembed installation inspection completed", "installation_id", id,
				"pending_rows", pending, "selected", false, "duration_ms", time.Since(discoveryStarted).Milliseconds())
			continue
		}
		// Execution still visits clean-looking installations. This count can
		// come from a process-cached reader, while ReembedCurrentSpace resolves
		// PostgreSQL's durable current space only after taking the tenant lock.
		targets = append(targets, target{id: id, pending: pending})
		logger.InfoContext(ctx, "reembed installation inspection completed", "installation_id", id,
			"pending_rows", pending, "selected", true, "duration_ms", time.Since(discoveryStarted).Milliseconds())
	}
	if len(targets) == 0 {
		logger.InfoContext(ctx, "reembed discovery completed", "target_count", 0,
			"outcome", "nothing_to_do", "duration_ms", time.Since(runStarted).Milliseconds())
		return nil
	}

	total := int64(0)
	for _, t := range targets {
		total += t.pending
	}
	logger.InfoContext(ctx, "reembed execution started", "installations", len(targets), "pending_rows", total, "plan", cfg.plan)
	if cfg.plan {
		for _, t := range targets {
			logger.InfoContext(ctx, "reembed installation planned", "installation_id", t.id, "pending_rows", t.pending)
		}
		logger.InfoContext(ctx, "reembed execution completed", "plan", true, "installations", len(targets),
			"pending_rows", total, "duration_ms", time.Since(runStarted).Milliseconds())
		return nil
	}

	repairedTotal := 0
	var failed []int64
	for _, t := range targets {
		if ctx.Err() != nil {
			logger.InfoContext(context.WithoutCancel(ctx), "reembed execution stopped", "reason", ctx.Err(),
				"repaired", repairedTotal, "failed_installations", len(failed), "duration_ms", time.Since(runStarted).Milliseconds())
			return ctx.Err()
		}
		attemptStarted := time.Now()
		logger.InfoContext(ctx, "reembed installation attempt started", "installation_id", t.id,
			"pending_rows", t.pending, "page_size", pageSize)
		repaired, err := registry.ReembedCurrentSpace(ctx, t.id, pageSize)
		repairedTotal += repaired
		if err != nil {
			logger.ErrorContext(ctx, "reembed installation attempt failed",
				"installation_id", t.id, "repaired_before_failure", repaired,
				"duration_ms", time.Since(attemptStarted).Milliseconds(), "error", err)
			failed = append(failed, t.id)
		} else {
			logger.InfoContext(ctx, "reembed installation attempt completed", "installation_id", t.id,
				"repaired", repaired, "duration_ms", time.Since(attemptStarted).Milliseconds())
		}
	}
	logger.InfoContext(ctx, "reembed execution completed", "repaired", repairedTotal,
		"failed_installations", len(failed), "duration_ms", time.Since(runStarted).Milliseconds())
	if len(failed) > 0 {
		return fmt.Errorf("%d of %d installation(s) failed to reembed (%v); %d rows repaired",
			len(failed), len(targets), failed, repairedTotal)
	}
	return nil
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func archivedInstallations(ctx context.Context, st *store.Store, one int64) ([]int64, error) {
	if one > 0 {
		// Verify the installation actually appears in the archive. Taking the
		// caller's word for it is destructive: --repoint on a mistyped or
		// never-exported id would clear that installation's pattern pointers
		// (the orphan clause nulls everything when memories holds nothing for
		// it) and only afterwards fail on the empty corpus.
		var n int
		if err := st.Pool.QueryRow(ctx,
			`SELECT count(*) FROM memory_export_archive WHERE installation_id = $1`, one).Scan(&n); err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, fmt.Errorf("installation %d has no rows in memory_export_archive; refusing to run (a --repoint here would clear its pattern pointers)", one)
		}
		return []int64{one}, nil
	}
	rows, err := st.Pool.Query(ctx,
		`SELECT DISTINCT installation_id FROM memory_export_archive ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// archivedDoc is the subset of an archived payload that maps onto a memories row.
type archivedDoc struct {
	Content  string            `json:"content"`
	Metadata map[string]string `json:"metadata"`
}

func backfillInstallation(ctx context.Context, logger *slog.Logger, st *store.Store, embeds *memory.EmbedderRegistry, registry *memory.Registry, installID int64, cfg runConfig) (int, int, error) {
	started := time.Now()
	logger.InfoContext(ctx, "archive installation import started", "installation_id", installID, "plan", cfg.plan)
	// The archive keys on the SERVER doc_id precisely because customIds can
	// collide across merge-corrupted documents. upsertDocs dedupes
	// last-write-wins on customId, so any collision silently drops a document
	// the archive deliberately preserved. Enumerate them up front: the import
	// still proceeds (the archive remains the durable record), but a run that
	// loses documents must never read as a clean one.
	collisions, err := customIDCollisions(ctx, st, installID)
	if err != nil {
		return 0, 0, fmt.Errorf("checking customId collisions: %w", err)
	}
	for _, c := range collisions {
		logger.Warn("archived customId collision; only the last document survives import",
			"installation_id", installID, "custom_id", c.customID, "doc_count", c.n,
			"recover_with", "SELECT doc_id, payload FROM memory_export_archive WHERE installation_id="+
				fmt.Sprint(installID)+" AND custom_id='"+c.customID+"'")
	}

	embedder, _ := embeds.GetEmbedder(ctx, installID)
	if embedder == nil && !cfg.plan {
		// FTS still works, but every similarity-floored read returns nothing —
		// which is indistinguishable from "no relevant memory" at every call
		// site. Refuse rather than build a corpus that reads as empty.
		return 0, 0, fmt.Errorf("no embedder resolved for installation %d; configure an embeddings provider before backfilling", installID)
	}
	idx, ok := registry.GetIndexer(ctx, installID).(*memory.PGIndexer)
	if !ok {
		return 0, 0, fmt.Errorf("memory registry is not configured for installation %d", installID)
	}

	var imported, skipped int
	var lastID int64
	batchNumber := 0
	for {
		if ctx.Err() != nil {
			return imported, skipped, ctx.Err()
		}
		batchNumber++
		batchStarted := time.Now()
		logger.InfoContext(ctx, "archive import batch started", "installation_id", installID,
			"batch_number", batchNumber, "after_archive_id", lastID, "page_size", pageSize)
		rows, err := st.Pool.Query(ctx, `
			SELECT id, container_tag, custom_id, payload
			FROM memory_export_archive
			WHERE installation_id = $1 AND id > $2
			ORDER BY id
			LIMIT $3`, installID, lastID, pageSize)
		if err != nil {
			return imported, skipped, err
		}
		var docs []memory.Doc
		n := 0
		for rows.Next() {
			var rowID int64
			var tag string
			var customID *string
			var payload []byte
			if err := rows.Scan(&rowID, &tag, &customID, &payload); err != nil {
				rows.Close()
				return imported, skipped, err
			}
			lastID = rowID
			n++
			var ad archivedDoc
			if err := json.Unmarshal(payload, &ad); err != nil {
				skipped++
				continue
			}
			// type is a filterable column on memories and every read filters on
			// it; a doc without one could never be retrieved, so importing it
			// would inflate the row count without adding recall.
			docType := ad.Metadata["type"]
			if docType == "" || customID == nil || *customID == "" {
				skipped++
				continue
			}
			docs = append(docs, memory.Doc{
				ContainerTag: tag,
				CustomID:     *customID,
				Type:         docType,
				Content:      ad.Content,
				Metadata:     ad.Metadata,
			})
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return imported, skipped, err
		}
		if n == 0 {
			logger.InfoContext(ctx, "archive import batch completed", "installation_id", installID,
				"batch_number", batchNumber, "archive_rows", 0, "imported", 0, "outcome", "end_of_archive",
				"duration_ms", time.Since(batchStarted).Milliseconds())
			break
		}
		if len(docs) > 0 && !cfg.plan {
			if err := idx.ImportDocs(ctx, docs); err != nil {
				return imported, skipped, fmt.Errorf("importing batch: %w", err)
			}
		}
		imported += len(docs)
		logger.InfoContext(ctx, "archive import batch completed", "installation_id", installID,
			"batch_number", batchNumber, "archive_rows", n, "imported", len(docs),
			"running_total", imported, "skipped_running_total", skipped, "plan", cfg.plan,
			"duration_ms", time.Since(batchStarted).Milliseconds())
	}
	logger.InfoContext(ctx, "archive installation import completed", "installation_id", installID,
		"imported", imported, "skipped_no_type", skipped, "duration_ms", time.Since(started).Milliseconds())
	return imported, skipped, nil
}

type customIDCollision struct {
	customID string
	n        int
}

// customIDCollisions lists archived customIds held by more than one document.
func customIDCollisions(ctx context.Context, st *store.Store, installID int64) ([]customIDCollision, error) {
	rows, err := st.Pool.Query(ctx, `
		SELECT custom_id, count(*) FROM memory_export_archive
		WHERE installation_id = $1 AND custom_id IS NOT NULL AND custom_id <> ''
		GROUP BY custom_id HAVING count(*) > 1
		ORDER BY custom_id`, installID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []customIDCollision
	for rows.Next() {
		var c customIDCollision
		if err := rows.Scan(&c.customID, &c.n); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// repointPatterns rewrites patterns.memory_doc_id from the archived server
// id it was written with to the archived customId, which is what PGIndexer
// matches on. Rows whose doc is absent from the archive have their
// id cleared instead.
//
// Both statements run in ONE transaction: a partial rewrite would leave the
// table half-addressable by each backend, and there is no way to tell the two
// id shapes apart afterwards.
func repointPatterns(ctx context.Context, st *store.Store, installID int64) (int, int, error) {
	tx, err := st.Pool.Begin(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	mapped, err := tx.Exec(ctx, `
		UPDATE patterns p
		SET memory_doc_id = a.custom_id, updated_at = NOW()
		FROM memory_export_archive a
		WHERE p.installation_id = $1
		  AND a.installation_id = p.installation_id
		  AND a.doc_id = p.memory_doc_id
		  AND a.custom_id IS NOT NULL AND a.custom_id <> ''`, installID)
	if err != nil {
		return 0, 0, fmt.Errorf("mapping ids: %w", err)
	}

	orphaned, err := tx.Exec(ctx, `
		UPDATE patterns p
		SET memory_doc_id = NULL, updated_at = NOW()
		WHERE p.installation_id = $1
		  AND p.memory_doc_id IS NOT NULL
		  AND NOT EXISTS (
		      SELECT 1 FROM live_memories m
		      WHERE m.installation_id = p.installation_id
		        AND m.custom_id = p.memory_doc_id)`, installID)
	if err != nil {
		return 0, 0, fmt.Errorf("clearing orphans: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, 0, err
	}
	return int(mapped.RowsAffected()), int(orphaned.RowsAffected()), nil
}
