// Command migrate-memory is a one-shot, idempotent backfill that re-derives
// every memory document from Postgres (the source of truth) into the NEW unified
// Supermemory shape — one `{repo}` container per repo (via memory.RepoTagNew)
// plus `_shared` — so the legacy `{owner}--{repo}--{kind}` containers can be
// deleted afterward.
//
// It is safe to re-run: every doc carries a deterministic customID identical to
// the pipeline's own writer, so a second pass upserts in place rather than
// duplicating. Batch responses return the created document ids, which are
// mirrored back into patterns.supermemory_id / scenarios.supermemory_id /
// decision_traces.supermemory_id (overwriting the legacy ids is the point of the
// migration), and pattern_stats rows are repointed from the legacy id to the new
// one.
//
// Doc types re-derived (see internal/store/sqlc/query/migrate.sql):
//
//	review_comments   → type=review     {repo}
//	patterns          → type=pattern    {repo} | _shared (NULL repo_id)
//	comment_outcomes  → type=feedback   {repo}
//	scenarios         → type=scenario   {repo}
//	decision_traces   → type=trace      {repo}
//	rules             → type=rule       _shared
//	reviews (summary) → type=pr_summary {repo}
//
// Flags:
//
//	--installation=ID    restrict to one installation (0 = all with a SM key)
//	--new-shape-since=T  RFC3339 created_at cutoff (the post-#66 deploy time),
//	                     REQUIRED unless --verify-legacy. Rows created at/after T
//	                     are skipped by the drift-prone sweeps (review, feedback,
//	                     pr_summary): the live pipeline already indexed them into
//	                     the new shape, and the batch API MERGES content under a
//	                     '---' separator when the same customId arrives with
//	                     different content, so re-submitting a not-byte-exact
//	                     re-derivation would corrupt the live doc. The byte-exact
//	                     sweeps (patterns/scenarios/traces/rules) ignore the cutoff
//	                     — their collisions are clean no-op upserts.
//	--plan               dry-run; logs intended writes, performs NO remote writes
//	                     (no batch add, and no DisableLLMFilter settings PATCH —
//	                     the client is built read-only), but still issues the SQL
//	                     reads to plan the sweep
//	--batch-size=N       rows per SQL page AND max docs per batch add (default 100,
//	                     clamped to [1,600] — the batch API caps at 600 docs/call)
//	--max-rows=N         safety cap on rows processed per table per installation
//	--verify-legacy      read-only: list the legacy containers per installation and
//	                     print each container's totalItems (count-verify gate); no
//	                     backfill is performed in this mode
//	--delete-legacy      destructive: BulkDelete the deprecated legacy containers
//	                     (the exact set --verify-legacy lists) per installation.
//	                     Mutually exclusive with --plan and --verify-legacy and
//	                     never runs a backfill. Dry-run by default — it prints what
//	                     it WOULD delete and exits 1 unless --yes-delete-legacy is
//	                     also passed.
//	--yes-delete-legacy  arm --delete-legacy to actually delete (no-op without
//	                     --delete-legacy)
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/BeLazy167/argus/backend/internal/crypto"
	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/store"
	"github.com/BeLazy167/argus/backend/internal/store/db"
)

// errPermanent marks a per-row failure that can never succeed on retry — a
// malformed repo full_name or an unroutable outcome. The sweeps log and skip
// these WITHOUT advancing the circuit breaker, so a cluster of poison rows can't
// wedge the batch for healthy rows behind them.
var errPermanent = errors.New("permanent per-row failure")

// maxConsecutiveFailures is the circuit breaker: after this many consecutive
// batch-add failures a type's sweep aborts, assuming a Supermemory outage rather
// than looping over every page burning the API quota.
const maxConsecutiveFailures = 5

// maxBatchDocs is Supermemory's hard cap on documents per /v3/documents/batch.
const maxBatchDocs = 600

// runConfig gathers CLI flags for pass-through to the sweep.
type runConfig struct {
	installation int64
	plan         bool
	batchSize    int32
	maxRows      int
	verifyLegacy bool
	// deleteLegacy runs the destructive legacy-container deletion mode; it is
	// mutually exclusive with plan/verifyLegacy and never backfills.
	deleteLegacy bool
	// yesDeleteLegacy arms deleteLegacy to actually delete. Without it,
	// deleteLegacy is a dry-run that prints what it would delete and exits 1.
	yesDeleteLegacy bool
	// cutoff is the --new-shape-since created_at boundary. Rows created at/after
	// it are skipped by the three drift-prone sweeps (review, feedback,
	// pr_summary) — the live pipeline already wrote those into the new shape and
	// the batch API MERGES content with a '---' separator when the same customId
	// arrives with different content, so re-submitting our not-byte-exact
	// re-derivation would corrupt the live doc. Not applied to the byte-exact
	// sweeps (patterns/scenarios/traces/rules), where a post-deploy collision is
	// a clean no-op upsert that heals a failed live write.
	cutoff time.Time
}

func main() {
	cfg := runConfig{}
	flag.Int64Var(&cfg.installation, "installation", 0, "restrict to one installation ID (0 = all with a Supermemory key)")
	flag.BoolVar(&cfg.plan, "plan", false, "dry-run: log intended writes; perform NO remote writes (no batch add / settings PATCH), but still issue SQL reads")
	var batchSize int
	flag.IntVar(&batchSize, "batch-size", 100, "rows per SQL page and max docs per batch add (clamped to [1,600])")
	flag.IntVar(&cfg.maxRows, "max-rows", 10000, "safety cap on rows processed per table per installation per run")
	flag.BoolVar(&cfg.verifyLegacy, "verify-legacy", false, "read-only: list legacy containers per installation and print each container's totalItems")
	flag.BoolVar(&cfg.deleteLegacy, "delete-legacy", false, "destructive: delete the deprecated legacy containers per installation. Dry-run (lists what it would delete, exits 1) unless --yes-delete-legacy is also set. Mutually exclusive with --plan and --verify-legacy")
	flag.BoolVar(&cfg.yesDeleteLegacy, "yes-delete-legacy", false, "arm --delete-legacy to actually delete (no-op without --delete-legacy)")
	var newShapeSince string
	flag.StringVar(&newShapeSince, "new-shape-since", "", "RFC3339 created_at cutoff (the post-#66 deploy time); rows at/after are skipped by the drift-prone sweeps (review/feedback/pr_summary) so the backfill can't merge-corrupt docs the live pipeline already wrote. REQUIRED unless --verify-legacy.")
	flag.Parse()
	if batchSize < 1 {
		batchSize = 1
	}
	if batchSize > maxBatchDocs {
		batchSize = maxBatchDocs
	}
	cfg.batchSize = int32(batchSize)

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// --delete-legacy is a standalone destructive mode: it must not share a run
	// with the read-only verify gate, a dry-run --plan backfill, or a real
	// backfill.
	if cfg.deleteLegacy && (cfg.plan || cfg.verifyLegacy) {
		logger.Error("--delete-legacy is mutually exclusive with --plan and --verify-legacy")
		os.Exit(1)
	}
	if cfg.yesDeleteLegacy && !cfg.deleteLegacy {
		logger.Error("--yes-delete-legacy requires --delete-legacy")
		os.Exit(1)
	}

	// --new-shape-since gates only the backfill sweeps; verify and delete modes
	// touch legacy containers directly and need no cutoff.
	cutoff, err := resolveCutoff(newShapeSince, cfg.verifyLegacy || cfg.deleteLegacy)
	if err != nil {
		logger.Error("invalid --new-shape-since", "value", newShapeSince, "error", err)
		os.Exit(1)
	}
	cfg.cutoff = cutoff

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		logger.Error("DATABASE_URL not set")
		os.Exit(1)
	}

	// The BYOK Supermemory keys in installations.supermemory_key_enc are
	// AES-encrypted; the app initializes the key at startup (app.go) and this
	// binary must too, or every per-installation decrypt fails.
	if err := crypto.InitFromEnv(); err != nil {
		logger.Error("initializing encryption key", "error", err)
		os.Exit(1)
	}

	// SIGINT/SIGTERM cleanly finishes the current batch before exit.
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	st, err := store.New(ctx, databaseURL)
	if err != nil {
		logger.Error("connecting to database", "error", err)
		os.Exit(1)
	}
	defer st.Close()

	registry := memory.NewRegistry(st, logger)

	runErr := run(ctx, logger, st, registry, cfg)
	if errors.Is(runErr, errDeleteDryRun) {
		// Dry-run safety gate: exit non-zero WITHOUT an error banner so the
		// operator sees it as "nothing deleted, rerun with --yes-delete-legacy",
		// not a failure.
		logger.Warn("delete-legacy dry-run complete; no deletions performed — rerun with --yes-delete-legacy to execute")
		os.Exit(1)
	}
	if runErr != nil {
		logger.Error("migrate-memory run failed", "error", runErr)
		os.Exit(1)
	}
	logger.Info("migrate-memory run complete")
}

// run resolves the installation set and dispatches to the backfill or the
// read-only legacy-count mode. Per-installation errors are logged, never fatal.
func run(ctx context.Context, logger *slog.Logger, st *store.Store, registry *memory.Registry, cfg runConfig) error {
	installs, err := resolveInstallations(ctx, st, cfg.installation)
	if err != nil {
		return fmt.Errorf("resolving installations: %w", err)
	}
	logger.Info("migrate-memory starting", "installations", len(installs), "plan", cfg.plan, "verify_legacy", cfg.verifyLegacy, "delete_legacy", cfg.deleteLegacy)

	if cfg.deleteLegacy {
		return runDeleteLegacy(ctx, logger, st, registry, installs, cfg)
	}

	if cfg.verifyLegacy {
		for _, id := range installs {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			verifyLegacyInstallation(ctx, logger, st, registry, id, cfg)
		}
		return nil
	}

	report := newRunReport()
	var processed, skippedNoKey, skippedKeyErr int
	for _, id := range installs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		switch backfillInstallation(ctx, logger, st, registry, id, cfg, report) {
		case statusProcessed:
			processed++
		case statusNoKey:
			skippedNoKey++
		case statusKeyError:
			skippedKeyErr++
		}
	}
	report.emit(logger, cfg.cutoff)
	logger.Info("migrate-memory run summary",
		"installations", len(installs),
		"processed", processed,
		"skipped_no_key", skippedNoKey,
		"skipped_key_error", skippedKeyErr,
		"plan", cfg.plan,
	)
	if skippedKeyErr > 0 {
		logger.Warn("installations skipped due to unavailable supermemory key (decrypt/load failure)", "count", skippedKeyErr)
	}
	return nil
}

// resolveCutoff validates the --new-shape-since flag. It is mandatory for a real
// backfill: without it the drift-prone sweeps (review/feedback/pr_summary) would
// re-submit not-byte-exact re-derivations of docs the live pipeline already
// indexed post-deploy, and the batch API merges differing content under the same
// customId — corrupting the live doc. --verify-legacy is read-only and needs no
// cutoff, so it returns the zero time and no error.
func resolveCutoff(newShapeSince string, verifyLegacy bool) (time.Time, error) {
	if verifyLegacy {
		return time.Time{}, nil
	}
	if newShapeSince == "" {
		return time.Time{}, fmt.Errorf("--new-shape-since is required (RFC3339); pass the post-#66 deploy timestamp so drift-prone sweeps skip docs the live pipeline already wrote")
	}
	t, err := time.Parse(time.RFC3339, newShapeSince)
	if err != nil {
		return time.Time{}, fmt.Errorf("--new-shape-since must be RFC3339: %w", err)
	}
	return t, nil
}

// installStatus classifies one installation's outcome so run() can distinguish a
// benign "no key configured" skip from a key-present-but-unusable skip.
type installStatus int

const (
	statusProcessed installStatus = iota
	statusNoKey
	statusKeyError
)

// resolveInstallations returns the installation IDs to sweep. 0 means "every
// installation with a Supermemory key".
func resolveInstallations(ctx context.Context, st *store.Store, one int64) ([]int64, error) {
	if one > 0 {
		return []int64{one}, nil
	}
	return st.Q.ListInstallationsWithSMKey(ctx)
}

// resolveClient returns a rate-limited Supermemory Client for the installation
// and classifies a nil result — mirroring reconcile-memory's resolveIndexer. In
// --plan mode it builds the client directly (GetClient) so it never triggers
// GetIndexer's one-time DisableLLMFilter settings PATCH; a dry-run must not
// mutate the customer's account. In a real run it goes through GetIndexer so the
// account is configured exactly as the live pipeline configures it, then uses
// the shared rate-limited client for the batch writes.
func resolveClient(ctx context.Context, logger *slog.Logger, st *store.Store, registry *memory.Registry, installID int64, plan bool) (*memory.Client, installStatus) {
	var client *memory.Client
	if plan {
		client = registry.GetClient(ctx, installID)
	} else if registry.GetIndexer(ctx, installID) != nil {
		// Route through GetIndexer first so the account is configured exactly as
		// the live pipeline configures it (one-time DisableLLMFilter PATCH), then
		// take the shared rate-limited client (cached, same instance) for writes.
		client = registry.GetClient(ctx, installID)
	}
	if client != nil {
		return client, statusProcessed
	}
	hasKey, err := st.Q.InstallationHasSMKey(ctx, installID)
	if err != nil {
		logger.Warn("checking supermemory key presence", "installation_id", installID, "error", err)
		return nil, statusKeyError
	}
	if hasKey {
		logger.Warn("supermemory key present but client unavailable (decrypt/load failure); skipping", "installation_id", installID)
		return nil, statusKeyError
	}
	logger.Debug("no supermemory key configured; skipping", "installation_id", installID)
	return nil, statusNoKey
}

// backfillInstallation runs every doc-type sweep for one installation, sharing a
// single rate-limited client and circuit breaker.
func backfillInstallation(ctx context.Context, logger *slog.Logger, st *store.Store, registry *memory.Registry, installID int64, cfg runConfig, report *runReport) installStatus {
	client, status := resolveClient(ctx, logger, st, registry, installID, cfg.plan)
	if client == nil {
		return status
	}
	start := time.Now()
	s := &sweeper{
		ctx:       ctx,
		logger:    logger.With("installation_id", installID),
		st:        st,
		client:    client,
		cfg:       cfg,
		installID: installID,
		report:    report,
	}

	// review_comments → type=review. Drift-prone: the pipeline hashes the raw
	// finding body (not persisted), so our customID isn't collision-identical;
	// skip rows the live pipeline already indexed (created_at >= cutoff).
	sweepType(s, "review", uuid.Nil,
		func(cur uuid.UUID, limit int32) ([]db.ListReviewCommentsForBackfillRow, error) {
			return st.Q.ListReviewCommentsForBackfill(ctx, db.ListReviewCommentsForBackfillParams{InstallationID: installID, ID: cur, Limit: limit})
		},
		func(r db.ListReviewCommentsForBackfillRow) uuid.UUID { return r.ID },
		func(r db.ListReviewCommentsForBackfillRow) (mappedDoc, writeBackFn, error) {
			md, err := mapReviewComment(r)
			return md, nil, err
		},
		func(r db.ListReviewCommentsForBackfillRow) bool { return afterCutoff(r.CreatedAt, cfg.cutoff) })

	// patterns → type=pattern (+ supermemory_id + pattern_stats remap write-back)
	sweepType(s, "pattern", 0,
		func(cur int, limit int32) ([]db.ListPatternsForBackfillRow, error) {
			return st.Q.ListPatternsForBackfill(ctx, db.ListPatternsForBackfillParams{InstallationID: installID, ID: cur, Limit: limit})
		},
		func(r db.ListPatternsForBackfillRow) int { return r.ID },
		func(r db.ListPatternsForBackfillRow) (mappedDoc, writeBackFn, error) {
			md, err := mapPattern(r)
			if err != nil {
				return mappedDoc{}, nil, err
			}
			id, oldSM := r.ID, r.OldSmID
			wb := func(ctx context.Context, newID string) error {
				if _, err := st.Q.UpdatePatternSupermemoryID(ctx, db.UpdatePatternSupermemoryIDParams{SupermemoryID: &newID, ID: id}); err != nil {
					return err
				}
				if oldSM != nil && *oldSM != "" && *oldSM != newID {
					if _, rerr := st.Q.RemapPatternStatsSupermemoryID(ctx, db.RemapPatternStatsSupermemoryIDParams{NewID: newID, OldID: *oldSM}); rerr != nil {
						s.logger.Warn("remap pattern_stats supermemory_id", "old", *oldSM, "new", newID, "error", rerr)
					}
				}
				return nil
			}
			return md, wb, nil
		},
		nil) // byte-exact: post-deploy collisions are clean no-op upserts, no cutoff

	// comment_outcomes → type=feedback. Drift-prone: feedback content in SM can
	// carry a reply suffix PG never persisted, so skip post-deploy rows.
	sweepType(s, "feedback", 0,
		func(cur int, limit int32) ([]db.ListCommentOutcomesForBackfillRow, error) {
			return st.Q.ListCommentOutcomesForBackfill(ctx, db.ListCommentOutcomesForBackfillParams{InstallationID: installID, ID: cur, Limit: limit})
		},
		func(r db.ListCommentOutcomesForBackfillRow) int { return r.ID },
		func(r db.ListCommentOutcomesForBackfillRow) (mappedDoc, writeBackFn, error) {
			md, err := mapFeedback(r)
			return md, nil, err
		},
		// created_at is COALESCE'd to epoch in SQL (never NULL at runtime); a nil
		// pointer is treated as pre-cutoff (included) defensively.
		func(r db.ListCommentOutcomesForBackfillRow) bool {
			return r.CreatedAt != nil && afterCutoff(*r.CreatedAt, cfg.cutoff)
		})

	// scenarios → type=scenario (+ supermemory_id write-back)
	sweepType(s, "scenario", int64(0),
		func(cur int64, limit int32) ([]db.ListScenariosForBackfillRow, error) {
			return st.Q.ListScenariosForBackfill(ctx, db.ListScenariosForBackfillParams{InstallationID: installID, ID: cur, Limit: limit})
		},
		func(r db.ListScenariosForBackfillRow) int64 { return r.ID },
		func(r db.ListScenariosForBackfillRow) (mappedDoc, writeBackFn, error) {
			md, err := mapScenario(r)
			if err != nil {
				return mappedDoc{}, nil, err
			}
			id := r.ID
			wb := func(ctx context.Context, newID string) error {
				return st.Q.UpdateScenarioSupermemoryID(ctx, db.UpdateScenarioSupermemoryIDParams{SupermemoryID: &newID, ID: id})
			}
			return md, wb, nil
		},
		nil) // byte-exact

	// decision_traces backfill retired: Supermemory trace writes were removed
	// (Postgres decision_traces is the source of truth), so mirroring traces into
	// Supermemory is pointless. The mapper + ListTracesForBackfill query are gone.
	s.logger.Info("skipping trace backfill", "reason", "supermemory trace writes retired")

	// rules → type=rule (_shared). No supermemory_id column on rules → no write-back.
	sweepType(s, "rule", int64(0),
		func(cur int64, limit int32) ([]db.ListRulesForBackfillRow, error) {
			return st.Q.ListRulesForBackfill(ctx, db.ListRulesForBackfillParams{InstallationID: &installID, ID: cur, Limit: limit})
		},
		func(r db.ListRulesForBackfillRow) int64 { return r.ID },
		func(r db.ListRulesForBackfillRow) (mappedDoc, writeBackFn, error) {
			md, err := mapRule(r)
			return md, nil, err
		},
		nil) // byte-exact

	// reviews (summary present) → type=pr_summary. No supermemory_id column → no
	// write-back. Drift-prone: the pipeline's summary content (score/title/files/
	// truncated summary) isn't reproducible byte-exact, so skip post-deploy rows.
	sweepType(s, "pr_summary", uuid.Nil,
		func(cur uuid.UUID, limit int32) ([]db.ListReviewSummariesForBackfillRow, error) {
			return st.Q.ListReviewSummariesForBackfill(ctx, db.ListReviewSummariesForBackfillParams{InstallationID: installID, ID: cur, Limit: limit})
		},
		func(r db.ListReviewSummariesForBackfillRow) uuid.UUID { return r.ID },
		func(r db.ListReviewSummariesForBackfillRow) (mappedDoc, writeBackFn, error) {
			md, err := mapReviewSummary(r)
			return md, nil, err
		},
		func(r db.ListReviewSummariesForBackfillRow) bool { return afterCutoff(r.CreatedAt, cfg.cutoff) })

	s.logger.Info("installation_backfill_complete", "mode", modeLabel(cfg), "duration_ms", time.Since(start).Milliseconds())
	return statusProcessed
}

// modeLabel returns a short run-mode label for logs.
func modeLabel(cfg runConfig) string {
	if cfg.plan {
		return "plan"
	}
	return "write"
}

// afterCutoff reports whether a row's created_at is at or after the cutoff — the
// drift-prone sweeps skip such rows because the live pipeline already indexed
// them into the new shape. Boundary is inclusive (>= cutoff) so a row created
// exactly at the deploy instant is left to the pipeline, not re-derived.
func afterCutoff(createdAt, cutoff time.Time) bool {
	return !createdAt.Before(cutoff)
}

// writeBackFn persists the batch-returned document id to Postgres for one row.
// Nil for doc types with no supermemory_id mirror column.
type writeBackFn func(ctx context.Context, newID string) error

// pendingBatch accumulates docs (and their per-doc write-backs) destined for one
// container within a single page, flushed together in one AddMemoryBatch call.
type pendingBatch struct {
	docs []memory.BatchDocument
	wbs  []writeBackFn
}

// sweeper carries the per-installation state shared across every doc-type sweep:
// context, rate-limited client, config, the circuit breaker, and the report.
type sweeper struct {
	ctx                 context.Context
	logger              *slog.Logger
	st                  *store.Store
	client              *memory.Client
	cfg                 runConfig
	installID           int64
	report              *runReport
	consecutiveFailures int
}

// sweepType keyset-pages one doc type, groups each page's rows by destination
// container, and flushes each container as a single batch add with per-doc
// write-back. Generic over the row type and its (comparable) id cursor so all
// seven types share one loop with no duplicated paging/breaker logic.
//
// skip is the optional created_at cutoff predicate. When non-nil and it returns
// true, the row is counted as skipped (advancing the cursor + processed budget)
// and never mapped or indexed — used by the drift-prone sweeps to leave
// post-deploy rows to the live pipeline. Pass nil for the byte-exact sweeps.
func sweepType[Row any, Cur comparable](
	s *sweeper,
	typeName string,
	zero Cur,
	fetch func(cur Cur, limit int32) ([]Row, error),
	cursorOf func(Row) Cur,
	mapper func(Row) (mappedDoc, writeBackFn, error),
	skip func(Row) bool,
) {
	cursor := zero
	processed := 0
	for processed < s.cfg.maxRows {
		if s.ctx.Err() != nil {
			return
		}
		rows, err := fetch(cursor, s.cfg.batchSize)
		if err != nil {
			s.logger.Error("backfill list failed", "type", typeName, "error", err)
			return
		}
		if len(rows) == 0 {
			return
		}

		groups := map[string]*pendingBatch{}
		var order []string
		for _, r := range rows {
			if processed >= s.cfg.maxRows {
				break
			}
			processed++
			cursor = cursorOf(r)
			if skip != nil && skip(r) {
				s.report.recordSkip(s.installID, typeName)
				continue
			}
			md, wb, merr := mapper(r)
			if merr != nil {
				s.recordMapError(typeName, merr)
				continue
			}
			pb := groups[md.Container]
			if pb == nil {
				pb = &pendingBatch{}
				groups[md.Container] = pb
				order = append(order, md.Container)
			}
			pb.docs = append(pb.docs, md.Doc)
			pb.wbs = append(pb.wbs, wb)
		}

		for _, container := range order {
			if s.consecutiveFailures >= maxConsecutiveFailures {
				s.logger.Error("backfill circuit breaker tripped; aborting type",
					"type", typeName, "consecutive_failures", s.consecutiveFailures)
				return
			}
			s.flushContainer(typeName, container, groups[container])
		}

		if len(rows) < int(s.cfg.batchSize) {
			return
		}
	}
}

// recordMapError logs a per-row mapping failure and counts it under the type's
// synthetic "<unmapped>" container. Permanent failures (bad full_name) are
// skipped quietly; neither trips the circuit breaker — mapping errors are data
// issues, not outages.
func (s *sweeper) recordMapError(typeName string, err error) {
	st := s.report.stat(s.installID, "<unmapped>", typeName)
	st.Read++
	st.Errors++
	if errors.Is(err, errPermanent) {
		s.logger.Warn("skipping unroutable row", "type", typeName, "error", err)
		return
	}
	s.logger.Warn("mapping row failed", "type", typeName, "error", err)
}

// flushContainer writes one container's accumulated docs in a single batch add,
// then mirrors each returned id back to Postgres. In --plan mode it logs the
// intended write and performs no remote call. A batch-call error advances the
// circuit breaker; a per-doc empty id (that document failed server-side) counts
// as an error but does not.
func (s *sweeper) flushContainer(typeName, container string, pb *pendingBatch) {
	if pb == nil || len(pb.docs) == 0 {
		return
	}
	st := s.report.stat(s.installID, container, typeName)
	st.Read += len(pb.docs)

	if s.cfg.plan {
		st.Planned += len(pb.docs)
		s.logger.Info("plan: would batch-index", "type", typeName, "container", container, "docs", len(pb.docs))
		return
	}

	resp, err := s.client.AddMemoryBatch(s.ctx, memory.BatchAddRequest{ContainerTag: container, Documents: pb.docs})
	if err != nil {
		s.consecutiveFailures++
		st.Errors += len(pb.docs)
		s.logger.Error("batch add failed", "type", typeName, "container", container, "docs", len(pb.docs), "error", err)
		return
	}
	s.consecutiveFailures = 0

	ids := resp.DocIDs()
	if len(ids) != len(pb.docs) {
		// The batch API returns one result per input document, in order; a
		// mismatch means some docs have no id to write back. Surface it — the
		// per-doc loop below counts the missing ones as errors.
		s.logger.Warn("batch response id count mismatch",
			"type", typeName, "container", container, "docs", len(pb.docs), "ids", len(ids))
	}
	for i, doc := range pb.docs {
		var id string
		if i < len(ids) {
			id = ids[i]
		}
		if id == "" {
			st.Errors++
			s.logger.Error("batch doc failed (empty id)", "type", typeName, "container", container, "custom_id", doc.CustomID)
			continue
		}
		st.Written++
		if wb := pb.wbs[i]; wb != nil {
			if werr := wb(s.ctx, id); werr != nil {
				s.logger.Warn("write-back failed", "type", typeName, "sm_id", id, "error", werr)
			}
		}
	}
}

// ---- verification report -------------------------------------------------

// statKey identifies a per-(installation, container, type) tally.
type statKey struct {
	install   int64
	container string
	typ       string
}

// typeStat is the count tuple the count-verify gate consumes: rows read
// (attempted in this container), docs planned (--plan only), docs written, and
// errors.
type typeStat struct {
	Read    int
	Planned int
	Written int
	Errors  int
}

// skipKey identifies a per-(installation, type) count of rows skipped by the
// created_at cutoff.
type skipKey struct {
	install int64
	typ     string
}

// runReport accumulates per-(installation, container, type) tallies plus
// per-(installation, type) cutoff skips across the whole run, then emits them
// machine-readably at the end.
type runReport struct {
	stats   map[statKey]*typeStat
	skipped map[skipKey]int
}

func newRunReport() *runReport {
	return &runReport{stats: make(map[statKey]*typeStat), skipped: make(map[skipKey]int)}
}

func (r *runReport) stat(install int64, container, typ string) *typeStat {
	k := statKey{install: install, container: container, typ: typ}
	st := r.stats[k]
	if st == nil {
		st = &typeStat{}
		r.stats[k] = st
	}
	return st
}

// recordSkip counts one row skipped by the --new-shape-since cutoff.
func (r *runReport) recordSkip(install int64, typ string) {
	r.skipped[skipKey{install: install, typ: typ}]++
}

// countRecord is the machine-readable per-(installation, container, type) line.
type countRecord struct {
	Installation int64  `json:"installation"`
	Container    string `json:"container"`
	Type         string `json:"type"`
	RowsRead     int    `json:"rows_read"`
	DocsPlanned  int    `json:"docs_planned"`
	DocsWritten  int    `json:"docs_written"`
	Errors       int    `json:"errors"`
}

// skipRecord is the machine-readable per-(installation, type) cutoff-skip line.
type skipRecord struct {
	Installation      int64  `json:"installation"`
	Type              string `json:"type"`
	RowsSkippedCutoff int    `json:"rows_skipped_post_cutoff"`
}

// reportBlob is the single JSON object the count-verify gate parses: the cutoff
// applied plus every per-container tally and per-type skip count.
type reportBlob struct {
	NewShapeSince string        `json:"new_shape_since,omitempty"`
	Records       []countRecord `json:"records"`
	Skipped       []skipRecord  `json:"skipped"`
}

// emit prints one structured JSON line per tally (machine-readable, keyed
// "backfill_count"), one per cutoff-skip ("backfill_skipped"), the consolidated
// "backfill_report" blob (cutoff + records + skips), and a run-total summary.
// Deterministic ordering so runs diff cleanly. cutoff is the zero time under
// --verify-legacy (never reached here) or a real backfill's --new-shape-since.
func (r *runReport) emit(logger *slog.Logger, cutoff time.Time) {
	records := make([]countRecord, 0, len(r.stats))
	for k, st := range r.stats {
		records = append(records, countRecord{
			Installation: k.install,
			Container:    k.container,
			Type:         k.typ,
			RowsRead:     st.Read,
			DocsPlanned:  st.Planned,
			DocsWritten:  st.Written,
			Errors:       st.Errors,
		})
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].Installation != records[j].Installation {
			return records[i].Installation < records[j].Installation
		}
		if records[i].Container != records[j].Container {
			return records[i].Container < records[j].Container
		}
		return records[i].Type < records[j].Type
	})

	skips := make([]skipRecord, 0, len(r.skipped))
	for k, n := range r.skipped {
		skips = append(skips, skipRecord{Installation: k.install, Type: k.typ, RowsSkippedCutoff: n})
	}
	sort.Slice(skips, func(i, j int) bool {
		if skips[i].Installation != skips[j].Installation {
			return skips[i].Installation < skips[j].Installation
		}
		return skips[i].Type < skips[j].Type
	})

	var totRead, totPlanned, totWritten, totErr, totSkipped int
	for _, rec := range records {
		totRead += rec.RowsRead
		totPlanned += rec.DocsPlanned
		totWritten += rec.DocsWritten
		totErr += rec.Errors
		logger.Info("backfill_count",
			"installation", rec.Installation,
			"container", rec.Container,
			"type", rec.Type,
			"rows_read", rec.RowsRead,
			"docs_planned", rec.DocsPlanned,
			"docs_written", rec.DocsWritten,
			"errors", rec.Errors,
		)
	}
	for _, sk := range skips {
		totSkipped += sk.RowsSkippedCutoff
		logger.Info("backfill_skipped",
			"installation", sk.Installation,
			"type", sk.Type,
			"rows_skipped_post_cutoff", sk.RowsSkippedCutoff,
		)
	}

	blob := reportBlob{Records: records, Skipped: skips}
	if !cutoff.IsZero() {
		blob.NewShapeSince = cutoff.UTC().Format(time.RFC3339)
	}
	// Emit the full report as a single JSON line for machine ingestion (the
	// count-verify gate parses this one line).
	if b, err := json.Marshal(blob); err == nil {
		logger.Info("backfill_report", "report", string(b))
	}
	logger.Info("backfill_totals",
		"new_shape_since", blob.NewShapeSince,
		"rows_read", totRead,
		"docs_planned", totPlanned,
		"docs_written", totWritten,
		"errors", totErr,
		"rows_skipped_post_cutoff", totSkipped,
	)
}

// ---- legacy count verification (read-only) -------------------------------

// legacyRepoKinds are the per-repo legacy container suffixes (legacyRepoTag).
var legacyRepoKinds = []string{"reviews", "patterns", "scenarios", "traces", "negative_patterns", "positive_patterns"}

// legacyOwnerKinds are the per-owner legacy container suffixes (legacyOwnerTag).
var legacyOwnerKinds = []string{"patterns", "rules", "reviews"}

// legacyTagSanitizer mirrors the memory package's tag sanitizer. Inlined here so
// this binary can reconstruct the deprecated {owner}--{repo}--{kind} /
// {owner}--{kind} container names after the exported memory.RepoTag/OwnerTag
// helpers are deleted — enumerating legacy containers is only this tool's job.
var legacyTagSanitizer = strings.NewReplacer(":", "-", "/", "-", "~", "-", ".", "-")

// legacyRepoTag reconstructs a deprecated per-repo legacy container tag
// (former memory.RepoTag).
func legacyRepoTag(owner, repo, kind string) string {
	return legacyTagSanitizer.Replace(owner) + "--" + legacyTagSanitizer.Replace(repo) + "--" + kind
}

// legacyOwnerTag reconstructs a deprecated per-owner legacy container tag
// (former memory.OwnerTag).
func legacyOwnerTag(owner, kind string) string {
	return legacyTagSanitizer.Replace(owner) + "--" + kind
}

// legacyContainerTags returns the deduped, sorted set of deprecated legacy
// container tags for one installation — the exact set --verify-legacy counts
// and --delete-legacy removes. Malformed full_names are logged and skipped.
func legacyContainerTags(ctx context.Context, ilog *slog.Logger, st *store.Store, installID int64) ([]string, error) {
	fullNames, err := st.Q.ListRepoFullNamesForInstallation(ctx, installID)
	if err != nil {
		return nil, err
	}
	tags := map[string]struct{}{}
	owners := map[string]struct{}{}
	for _, fn := range fullNames {
		owner, repo, err := splitFullName(fn)
		if err != nil {
			ilog.Warn("legacy: bad full_name; skipping", "full_name", fn, "error", err)
			continue
		}
		owners[owner] = struct{}{}
		for _, kind := range legacyRepoKinds {
			tags[legacyRepoTag(owner, repo, kind)] = struct{}{}
		}
	}
	for owner := range owners {
		for _, kind := range legacyOwnerKinds {
			tags[legacyOwnerTag(owner, kind)] = struct{}{}
		}
	}
	ordered := make([]string, 0, len(tags))
	for t := range tags {
		ordered = append(ordered, t)
	}
	sort.Strings(ordered)
	return ordered, nil
}

// verifyLegacyInstallation lists every legacy container for one installation and
// prints each container's totalItems. Strictly read-only — it only issues
// /v3/documents/list with limit=1 and reads pagination.totalItems.
func verifyLegacyInstallation(ctx context.Context, logger *slog.Logger, st *store.Store, registry *memory.Registry, installID int64, cfg runConfig) {
	_ = cfg
	client, status := resolveClient(ctx, logger, st, registry, installID, true) // read-only build
	if client == nil {
		logger.Debug("verify-legacy: no client", "installation_id", installID, "status", status)
		return
	}
	ilog := logger.With("installation_id", installID)

	ordered, err := legacyContainerTags(ctx, ilog, st, installID)
	if err != nil {
		ilog.Error("verify-legacy: listing repos", "error", err)
		return
	}

	var total int
	for _, tag := range ordered {
		if ctx.Err() != nil {
			return
		}
		count, err := legacyContainerCount(ctx, client, tag)
		if err != nil {
			ilog.Warn("verify-legacy: list container", "container", tag, "error", err)
			continue
		}
		total += count
		ilog.Info("legacy_container_count", "container", tag, "total_items", count)
	}
	ilog.Info("legacy_verify_summary", "containers", len(ordered), "total_items", total)
}

// legacyContainerCount returns totalItems for one container via a single
// list call (limit=1, includeContent=false). A nil pagination envelope falls
// back to the returned page length.
func legacyContainerCount(ctx context.Context, client *memory.Client, tag string) (int, error) {
	resp, err := client.ListDocuments(ctx, memory.ListRequest{
		Limit:         1,
		Page:          1,
		ContainerTags: []string{tag},
	})
	if err != nil {
		return 0, err
	}
	if resp == nil {
		return 0, nil
	}
	if resp.Pagination != nil {
		return resp.Pagination.TotalItems, nil
	}
	return len(resp.Memories), nil
}

// ---- legacy container deletion (destructive) -----------------------------

// errDeleteDryRun is returned by runDeleteLegacy when --delete-legacy runs
// without --yes-delete-legacy: nothing was deleted and the process must exit
// non-zero so the safety gate is visible in scripts/CI.
var errDeleteDryRun = errors.New("delete-legacy dry-run: no deletions performed")

// deleteRecord is the machine-readable per-(installation, container) result of a
// delete-legacy run: how many docs were deleted (execute) or would be deleted
// (dry-run), plus any per-container error.
type deleteRecord struct {
	Installation     int64  `json:"installation"`
	Container        string `json:"container"`
	DeletedCount     int    `json:"deleted_count,omitempty"`
	WouldDeleteItems int    `json:"would_delete_items,omitempty"`
	Error            string `json:"error,omitempty"`
}

// runDeleteLegacy deletes (or, without --yes-delete-legacy, previews) every
// legacy container for each installation, emits a machine-readable summary, and
// returns errDeleteDryRun when nothing was executed so main() exits 1.
func runDeleteLegacy(ctx context.Context, logger *slog.Logger, st *store.Store, registry *memory.Registry, installs []int64, cfg runConfig) error {
	execute := cfg.yesDeleteLegacy
	logger.Info("delete-legacy starting", "installations", len(installs), "execute", execute)

	var all []deleteRecord
	for _, id := range installs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		all = append(all, deleteLegacyInstallation(ctx, logger, st, registry, id, execute)...)
	}
	emitDeleteReport(logger, all, execute)
	if !execute {
		return errDeleteDryRun
	}
	return nil
}

// deleteLegacyInstallation removes every deprecated legacy container for one
// installation via BulkDelete, logging the API's deletedCount per container. In
// dry-run (execute=false) it lists each container's current item count and
// deletes nothing. The client is always built read-only (like verify): deletion
// goes through BulkDelete directly and must not trigger the settings PATCH that
// GetIndexer would perform. Returns the per-container records for the summary.
func deleteLegacyInstallation(ctx context.Context, logger *slog.Logger, st *store.Store, registry *memory.Registry, installID int64, execute bool) []deleteRecord {
	client, status := resolveClient(ctx, logger, st, registry, installID, true) // read-only build (no settings PATCH)
	if client == nil {
		logger.Debug("delete-legacy: no client", "installation_id", installID, "status", status)
		return nil
	}
	ilog := logger.With("installation_id", installID)

	ordered, err := legacyContainerTags(ctx, ilog, st, installID)
	if err != nil {
		ilog.Error("delete-legacy: listing repos", "error", err)
		return nil
	}

	records := make([]deleteRecord, 0, len(ordered))
	for _, tag := range ordered {
		if ctx.Err() != nil {
			return records
		}
		if !execute {
			count, cerr := legacyContainerCount(ctx, client, tag)
			if cerr != nil {
				ilog.Warn("delete-legacy dry-run: list container", "container", tag, "error", cerr)
				records = append(records, deleteRecord{Installation: installID, Container: tag, Error: cerr.Error()})
				continue
			}
			ilog.Info("legacy_container_would_delete", "container", tag, "total_items", count)
			records = append(records, deleteRecord{Installation: installID, Container: tag, WouldDeleteItems: count})
			continue
		}
		resp, derr := client.BulkDelete(ctx, memory.BulkDeleteRequest{ContainerTags: []string{tag}})
		if derr != nil {
			ilog.Error("delete-legacy: bulk delete", "container", tag, "error", derr)
			records = append(records, deleteRecord{Installation: installID, Container: tag, Error: derr.Error()})
			continue
		}
		ilog.Info("legacy_container_deleted", "container", tag, "deleted_count", resp.DeletedCount)
		records = append(records, deleteRecord{Installation: installID, Container: tag, DeletedCount: resp.DeletedCount})
	}
	return records
}

// emitDeleteReport prints one structured JSON line per container result, the
// consolidated report blob, and a run-total summary. Deterministic ordering so
// runs diff cleanly.
func emitDeleteReport(logger *slog.Logger, records []deleteRecord, execute bool) {
	sort.Slice(records, func(i, j int) bool {
		if records[i].Installation != records[j].Installation {
			return records[i].Installation < records[j].Installation
		}
		return records[i].Container < records[j].Container
	})

	var totalDeleted, totalWould, errCount int
	for _, r := range records {
		totalDeleted += r.DeletedCount
		totalWould += r.WouldDeleteItems
		if r.Error != "" {
			errCount++
		}
	}

	blob := struct {
		Mode    string         `json:"mode"`
		Records []deleteRecord `json:"records"`
	}{Mode: deleteModeLabel(execute), Records: records}
	if b, err := json.Marshal(blob); err == nil {
		logger.Info("legacy_delete_report", "report", string(b))
	}
	logger.Info("legacy_delete_totals",
		"mode", deleteModeLabel(execute),
		"containers", len(records),
		"deleted_count", totalDeleted,
		"would_delete_items", totalWould,
		"errors", errCount,
	)
}

// deleteModeLabel returns a short label for the delete run mode.
func deleteModeLabel(execute bool) string {
	if execute {
		return "execute"
	}
	return "dry_run"
}
