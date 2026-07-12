// Command reconcile-memory runs drift-repair + _shared decay against the
// Argus memory system. Designed to run as a Fly.io scheduled machine (daily
// cron at 3am UTC) — isolated from the main app so a slow or crashed sweep
// can't stall live review pipelines.
//
// Two jobs per run, in order:
//
//  1. Drift repair — find Postgres rows whose supermemory_id is NULL (write
//     to SM failed at creation time) and retry the index call. The new SM
//     doc ID is written back to the PG row. Empty rowset = no drift = no-op.
//
//  2. _shared decay (Bundle 5, added in a follow-up edit in this same PR) —
//     walk `_shared` container docs, decay confidence on dormant ones, delete
//     docs below the retirement floor. Skipped when the installation sets
//     org_settings.disable_shared_decay.
//
// With --full the run switches to re-push mode: instead of only the NULL-id
// drift rows, EVERY pattern/scenario/trace is re-indexed into the unified
// {repo}/_shared containers so Supermemory rebuilds the relationship graph
// (each container tag has its own graph; seeding a fresh container is what
// triggers Index → Build Relationships). Deterministic customIDs make the
// re-push idempotent. Decay is skipped in --full mode so a seed run never
// deletes anything; old per-kind containers are left untouched.
//
// Flags:
//
//	--installation=ID    restrict to one installation (0 = all)
//	--plan               dry-run; logs intended writes and performs NO remote
//	                     writes (no index/delete, and no DisableLLMFilter settings
//	                     PATCH — the indexer is built read-only), but still issues
//	                     reads (list/get) to plan the sweep
//	--full               re-push ALL rows (not just NULL supermemory_id); skips decay
//	--batch-size=N       rows per SQL page (default 100)
//	--max-rows=N         safety cap per table per run (default 10000)
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
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/BeLazy167/argus/backend/internal/crypto"
	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/store"
	"github.com/BeLazy167/argus/backend/internal/store/db"
)

// errPermanent marks a per-row failure that can never succeed on retry — the
// repo row is gone, its full_name is malformed, or the row is otherwise
// unroutable. The batch loops log and skip these WITHOUT counting them toward
// the consecutive-failure circuit breaker, so one poison row can't wedge the
// sweep for every healthy row queued behind it (created_at ASC parks the
// oldest, most-likely-permanent rows at the head of every page).
var errPermanent = errors.New("permanent per-row failure")

// handleRowErr applies the circuit-breaker policy to a per-row reindex failure.
// A permanent failure (errPermanent) is logged and skipped WITHOUT advancing
// the breaker — it will fail identically every run, so counting it would let a
// cluster of poison rows abort the whole phase. A transient failure (SM outage,
// DB blip) advances the breaker so a genuine outage still aborts. Returns the
// new consecutiveFailures value.
func handleRowErr(logger *slog.Logger, failMsg, idField string, id any, err error, consecutiveFailures int) int {
	if errors.Is(err, errPermanent) {
		logger.Warn("skipping unroutable row", "reason", failMsg, idField, id, "error", err)
		return consecutiveFailures
	}
	logger.Warn(failMsg, idField, id, "error", err)
	return consecutiveFailures + 1
}

// runConfig gathers CLI flags into one struct for pass-through to the phases.
type runConfig struct {
	installation int64
	plan         bool
	full         bool
	batchSize    int32
	maxRows      int
}

func main() {
	cfg := runConfig{}
	flag.Int64Var(&cfg.installation, "installation", 0, "restrict to one installation ID (0 = all)")
	flag.BoolVar(&cfg.plan, "plan", false, "dry-run: log intended writes; performs NO remote writes (no index/delete/settings PATCH), but still issues reads (list/get) to plan the sweep")
	flag.BoolVar(&cfg.full, "full", false, "re-push ALL memories (not just supermemory_id IS NULL) so Supermemory rebuilds the relationship graph; skips _shared decay")
	var batchSize int
	flag.IntVar(&batchSize, "batch-size", 100, "rows per SQL page")
	flag.IntVar(&cfg.maxRows, "max-rows", 10000, "safety cap on rows processed per table per installation per run")
	flag.Parse()
	cfg.batchSize = int32(batchSize)

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

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

	// Signal-handling context so SIGINT/SIGTERM cleanly finishes the current
	// batch before exit. Fly machine restarts send SIGTERM with a 30s grace.
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	st, err := store.New(ctx, databaseURL)
	if err != nil {
		logger.Error("connecting to database", "error", err)
		os.Exit(1)
	}
	defer st.Close()

	registry := memory.NewRegistry(st, logger)

	if err := run(ctx, logger, st, registry, cfg); err != nil {
		logger.Error("reconciler run failed", "error", err)
		os.Exit(1)
	}
	logger.Info("reconciler run complete")
}

// run performs one end-to-end reconciliation pass: drift repair then decay.
// Errors from one installation are logged but do not abort the run — other
// installations still get their sweep.
func run(ctx context.Context, logger *slog.Logger, st *store.Store, registry *memory.Registry, cfg runConfig) error {
	installs, err := resolveInstallations(ctx, st, cfg.installation)
	if err != nil {
		return fmt.Errorf("resolving installations: %w", err)
	}
	logger.Info("reconciler starting", "installations", len(installs), "plan", cfg.plan)

	var processed, skippedNoKey, skippedKeyErr int
	for _, id := range installs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		switch reconcileInstallation(ctx, logger, st, registry, id, cfg) {
		case statusProcessed:
			processed++
		case statusNoKey:
			skippedNoKey++
		case statusKeyError:
			skippedKeyErr++
		}
	}
	logger.Info("reconciler run summary",
		"installations", len(installs),
		"processed", processed,
		"skipped_no_key", skippedNoKey,
		"skipped_key_error", skippedKeyErr,
	)
	// A present-but-unusable key (decrypt/load failure) is an operational
	// problem — e.g. the encryption key was rotated without re-encrypting stored
	// values — that would otherwise accumulate drift silently while the job
	// still exits 0. Surface it at Warn with a count so it's alertable.
	if skippedKeyErr > 0 {
		logger.Warn("installations skipped due to unavailable supermemory key (decrypt/load failure)", "count", skippedKeyErr)
	}
	return nil
}

// installStatus classifies the outcome of one installation's sweep so run() can
// summarize and distinguish a benign "no key configured" skip from a
// key-present-but-unusable skip worth alerting on.
type installStatus int

const (
	statusProcessed installStatus = iota
	statusNoKey
	statusKeyError
)

// resolveIndexer returns an Indexer for the installation and classifies a nil
// result. In --plan mode it builds the indexer from GetClient + NewIndexer so
// it never triggers GetIndexer's one-time DisableLLMFilter PATCH: a dry-run
// must not mutate the customer's account-level Supermemory settings. A nil
// indexer with a configured key means the client couldn't be built
// (decrypt/load failure) — worth a Warn + count; a nil indexer with no key is
// the expected BYOK-optional case and stays at Debug.
func resolveIndexer(ctx context.Context, logger *slog.Logger, st *store.Store, registry *memory.Registry, installID int64, plan bool) (memory.Indexer, installStatus) {
	var indexer memory.Indexer
	if plan {
		if client := registry.GetClient(ctx, installID); client != nil {
			indexer = memory.NewIndexer(client, logger)
		}
	} else {
		indexer = registry.GetIndexer(ctx, installID)
	}
	if indexer != nil {
		return indexer, statusProcessed
	}
	hasKey, err := st.Q.InstallationHasSMKey(ctx, installID)
	if err != nil {
		logger.Warn("checking supermemory key presence", "installation_id", installID, "error", err)
		return nil, statusKeyError
	}
	if hasKey {
		logger.Warn("supermemory key present but indexer unavailable (decrypt/load failure); skipping", "installation_id", installID)
		return nil, statusKeyError
	}
	logger.Debug("no supermemory key configured; skipping", "installation_id", installID)
	return nil, statusNoKey
}

// resolveInstallations returns the list of installation IDs to sweep. A flag
// value of 0 (default) means "every installation with a Supermemory key".
func resolveInstallations(ctx context.Context, st *store.Store, one int64) ([]int64, error) {
	if one > 0 {
		return []int64{one}, nil
	}
	return st.Q.ListInstallationsWithSMKey(ctx)
}

// reconcileInstallation runs every phase for a single installation. The
// indexer is resolved once so every sub-phase shares the same rate-limited
// client; if the key is missing or invalid the indexer is nil and we skip.
func reconcileInstallation(ctx context.Context, logger *slog.Logger, st *store.Store, registry *memory.Registry, installID int64, cfg runConfig) installStatus {
	indexer, status := resolveIndexer(ctx, logger, st, registry, installID, cfg.plan)
	if indexer == nil {
		return status
	}
	start := time.Now()
	ilog := logger.With("installation_id", installID)

	repairPatterns, err := reconcilePatterns(ctx, ilog, st, indexer, installID, cfg)
	if err != nil {
		ilog.Warn("pattern reconcile error", "error", err)
	}
	repairScenarios, err := reconcileScenarios(ctx, ilog, st, indexer, installID, cfg)
	if err != nil {
		ilog.Warn("scenario reconcile error", "error", err)
	}
	// Decision-trace drift repair retired: Supermemory trace writes were removed
	// (Postgres decision_traces is the source of truth), so there is nothing to
	// reconcile into Supermemory.

	// Phase 2 — `_shared` decay + retirement. Skipped when the org opts out,
	// and skipped entirely in --full mode: a seed run must never delete docs.
	var decayed, retired int
	if !cfg.full {
		decayed, retired, err = decayShared(ctx, ilog, st, registry.GetClient(ctx, installID), installID, cfg)
		if err != nil {
			ilog.Warn("shared decay error", "error", err)
		}
	}

	ilog.Info("reconcile_summary",
		"mode", reconcileMode(cfg),
		"duration_ms", time.Since(start).Milliseconds(),
		"patterns_repaired", repairPatterns,
		"scenarios_repaired", repairScenarios,
		"shared_decayed", decayed,
		"shared_retired", retired,
	)
	return statusProcessed
}

// sharedDecayDisabled reads org_settings.disable_shared_decay from the
// installation's default_settings JSON. DB error returns an error so the
// caller can fail closed (skip decay on DB blip) rather than deleting
// `_shared` docs for an org that had opted out. JSON parse failure is
// treated as "not disabled" — the JSON is authored by the app's own settings
// writer, so corruption is a real bug we want to surface.
func sharedDecayDisabled(ctx context.Context, st *store.Store, installID int64) (bool, error) {
	var raw []byte
	if err := st.Pool.QueryRow(ctx,
		`SELECT COALESCE(default_settings, '{}') FROM installations WHERE id = $1`,
		installID,
	).Scan(&raw); err != nil {
		return false, fmt.Errorf("load settings for installation %d: %w", installID, err)
	}
	var parsed struct {
		DisableSharedDecay *bool `json:"disable_shared_decay,omitempty"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return false, fmt.Errorf("parse settings JSON for installation %d: %w", installID, err)
	}
	return parsed.DisableSharedDecay != nil && *parsed.DisableSharedDecay, nil
}

// maxConsecutiveFailures is the reconciler's circuit breaker. A batch phase
// stops after this many consecutive per-row failures so a persistent outage
// (Supermemory 401, DB connection lost, all-fail page) can't loop forever
// hitting the same error and burning the API quota + log volume.
const maxConsecutiveFailures = 5

// decayShared pages through the `_shared` container, writes decayed confidence
// back on dormant docs, and deletes docs that age past the retirement floor.
// Returns (decayed, retired) counts.
//
// Scope: only type=pattern docs are listed (BuildFiltersJSON). Rules and any
// future non-decaying _shared types carry no confidence — treating them as 1.0
// then aging them would wrongly retire stable org data — so they're excluded at
// the source rather than defended against per-doc.
//
// maxRows caps rows PROCESSED (scanned), not just deletions — each scanned doc
// costs one GetDocument, and the flag is documented as a per-run safety cap on
// work, so an installation with tens of thousands of shared docs can't funnel
// hundreds of thousands of GetDocument calls through the per-install limiter.
//
// Pagination: sort=updatedAt asc, always advancing page++. Deleting docs on a
// page shifts later docs earlier, so the few that slide into the just-scanned
// tail are skipped this run and caught next run — the previous "restart at
// page 1 on any deletion" made the scan O(N^2) and could loop. Forward progress
// is the invariant; complete coverage converges across nightly runs.
//
// All-fail detection: if every GetDocument attempted on a page errors (outage),
// surface an error instead of returning silently, which would be
// indistinguishable from "no work to do."
func decayShared(ctx context.Context, logger *slog.Logger, st *store.Store, client *memory.Client, installID int64, cfg runConfig) (int, int, error) {
	disabled, err := sharedDecayDisabled(ctx, st, installID)
	if err != nil {
		// Fail closed — an org that configured disable_shared_decay must not
		// see deletions just because we couldn't read their setting.
		logger.Warn("could not load decay setting, skipping decay this run", "error", err)
		return 0, 0, nil
	}
	if disabled {
		logger.Debug("shared decay disabled for installation")
		return 0, 0, nil
	}

	if client == nil {
		return 0, 0, nil
	}

	filtersJSON, err := memory.BuildFiltersJSON(&memory.SearchFilters{
		AND: []memory.FilterCondition{{Key: "type", Value: string(memory.TypePattern)}},
	})
	if err != nil {
		return 0, 0, fmt.Errorf("building decay type filter: %w", err)
	}

	var decayed, retired, processed, consecutiveFailures int
	page := 1
	for processed < cfg.maxRows {
		if ctx.Err() != nil {
			return decayed, retired, ctx.Err()
		}
		resp, err := client.ListDocuments(ctx, memory.ListRequest{
			Limit:         int(cfg.batchSize),
			Page:          page,
			ContainerTags: []string{memory.SharedTag},
			Filters:       filtersJSON,
			Sort:          "updatedAt",
			Order:         "asc",
		})
		if err != nil {
			return decayed, retired, fmt.Errorf("listing shared docs page %d: %w", page, err)
		}
		if resp == nil || len(resp.Memories) == 0 {
			return decayed, retired, nil
		}

		getFailures, attempted := 0, 0
		for _, doc := range resp.Memories {
			if processed >= cfg.maxRows {
				break
			}
			if ctx.Err() != nil {
				return decayed, retired, ctx.Err()
			}
			// Circuit breaker — if a write (delete or decay upsert) fails
			// maxConsecutiveFailures times in a row we assume the API is down
			// and bail rather than burning through every doc on every page.
			// GetDocument failures are tracked separately (all-fail detection)
			// and don't advance this counter.
			if consecutiveFailures >= maxConsecutiveFailures {
				return decayed, retired, fmt.Errorf("shared decay: %d consecutive write failures, aborting", consecutiveFailures)
			}
			processed++
			attempted++
			// ListDocuments returns a thin Document — we need the full doc for
			// metadata.confidence + UpdatedAt, so fetch the single record.
			full, err := client.GetDocument(ctx, doc.ID)
			if err != nil || full == nil {
				logger.Debug("get shared doc for decay", "doc_id", doc.ID, "error", err)
				getFailures++
				continue
			}
			newConfidence, action := computeDecay(full)
			switch action {
			case decayActionRetire:
				if cfg.plan {
					logger.Info("plan: retire shared doc", "doc_id", doc.ID, "new_confidence", newConfidence)
					retired++
					consecutiveFailures = 0
					continue
				}
				if err := client.DeleteMemory(ctx, doc.ID); err != nil {
					logger.Warn("retire shared doc", "doc_id", doc.ID, "error", err)
					consecutiveFailures++
					continue
				}
				retired++
				consecutiveFailures = 0
			case decayActionDecay:
				if cfg.plan {
					logger.Info("plan: decay shared doc confidence", "doc_id", doc.ID, "new_confidence", fmt.Sprintf("%.2f", newConfidence))
					decayed++
					consecutiveFailures = 0
					continue
				}
				if err := writeBackDecay(ctx, client, full, newConfidence); err != nil {
					logger.Warn("decay shared doc confidence", "doc_id", doc.ID, "error", err)
					consecutiveFailures++
					continue
				}
				decayed++
				consecutiveFailures = 0
			default: // decayActionNoop
				continue
			}
		}

		// Every GetDocument attempted on this page failed → likely an outage.
		if attempted > 0 && getFailures == attempted {
			return decayed, retired, fmt.Errorf("all %d GetDocument calls failed on page %d", getFailures, page)
		}

		if len(resp.Memories) < int(cfg.batchSize) {
			return decayed, retired, nil
		}
		page++
	}
	return decayed, retired, nil
}

// decayAnchorKey holds the RFC3339 timestamp the decay clock measures dormancy
// from. A decay write-back re-upserts the doc, which resets Supermemory's
// UpdatedAt to now; without this anchor the doc would look freshly active every
// run and never progress toward retirement. It's set on the first write-back to
// the doc's then-current UpdatedAt (its last genuine activity) and preserved on
// subsequent write-backs. A genuine pipeline re-learn rebuilds metadata from
// scratch and drops this key, correctly restarting the decay clock.
const decayAnchorKey = "decay_anchor"

// writeBackDecay re-upserts a dormant _shared doc with its decayed confidence so
// the retrieval floor (confidence >= 0.30) can fade it out of reviews before
// retirement, instead of the doc influencing reviews at full weight until abrupt
// deletion. It writes via the customId upsert path (AddMemory directly, NOT
// IndexOwnerPattern — that resets confidence to 1.00) so the same doc is updated
// in place.
//
// The API-returned customId is preferred; reconstruction from (source, content)
// — the same inputs the write paths hashed — is the fallback for docs the API
// returns without one. Reconstruction is exact for every known _shared pattern
// writer; residual risk only if Supermemory returned non-verbatim content
// (normalizeBody absorbs whitespace differences).
func writeBackDecay(ctx context.Context, client *memory.Client, full *memory.Document, newConfidence float64) error {
	customID := full.CustomID
	if customID == "" {
		customID = sharedDocCustomID(full)
	}
	md := make(map[string]string, len(full.Metadata)+2)
	for k, v := range full.Metadata {
		md[k] = v
	}
	md["confidence"] = fmt.Sprintf("%.2f", newConfidence)
	if md[decayAnchorKey] == "" {
		md[decayAnchorKey] = full.UpdatedAt
	}
	_, err := client.AddMemory(ctx, memory.AddRequest{
		Content:       full.Content,
		CustomID:      customID,
		ContainerTags: []string{memory.SharedTag},
		Metadata:      md,
	})
	return err
}

// sharedDocCustomID reconstructs the deterministic customId of a type=pattern
// doc in the `_shared` container from its (source, content), so a decay
// write-back upserts the SAME doc rather than creating a duplicate. Mirrors the
// shared-pattern write paths: org-learned patterns (source=auto_learn) use the
// pipeline's org_learned customId; every other shared writer (reply_feedback,
// dashboard, remember_command, …) used IndexOwnerPattern with an empty customId,
// which derives SharedPatternCustomID(source, content).
func sharedDocCustomID(doc *memory.Document) string {
	source := doc.Metadata["source"]
	if source == "auto_learn" {
		return memory.PatternCustomID("", "", "org_learned", doc.Content)
	}
	if source == "" {
		source = "pattern" // IndexOwnerPattern's default when metadata.source is empty
	}
	return memory.SharedPatternCustomID(source, doc.Content)
}

// decayAction enumerates what decayShared decides per doc.
type decayAction int

const (
	decayActionNoop   decayAction = iota
	decayActionDecay              // past grace, still above the floor → write decayed confidence back
	decayActionRetire             // computed confidence at/below the retirement floor → delete
)

// String renders the decayAction for structured logging.
func (a decayAction) String() string {
	switch a {
	case decayActionRetire:
		return "retire"
	case decayActionDecay:
		return "decay"
	default:
		return "noop"
	}
}

// computeDecay reads confidence + dormancy age from a Document and decides
// whether to leave it, write a decayed confidence back, or retire it.
//
// Rules (Bundle 5 spec):
//   - age < SharedGraceDays → noop (fresh doc protected)
//   - 1.0 - (weeks past grace × SharedDecayPerWeek) ≤ SharedRetirementFloor → retire
//   - else if the 2-dp confidence changed vs what's stored → decay (write-back)
//   - else → noop
//
// The decay CURVE is always computed from the write-time base of 1.0
// (IndexOwnerPattern pins every _shared write to confidence=1.00), NOT from the
// stored value — decaying from the last-written (already-decayed) confidence
// would compound and over-retire. The stored confidence is used only for change
// detection so a run doesn't re-upsert a doc whose 2-dp value hasn't moved.
//
// Age is measured from decay_anchor when present (see decayAnchorKey) and
// otherwise from UpdatedAt, so nightly write-backs — which bump UpdatedAt — don't
// reset the clock. Legacy docs with no confidence field age from base 1.0.
// Malformed confidence or an unparseable timestamp stays conservative: noop.
//
// Returns the computed confidence for logging.
func computeDecay(doc *memory.Document) (float64, decayAction) {
	if doc == nil {
		return 0, decayActionNoop
	}
	storedConf := 1.0
	if confStr, ok := doc.Metadata["confidence"]; ok {
		parsed, err := strconv.ParseFloat(confStr, 64)
		if err != nil || parsed <= 0 {
			return 0, decayActionNoop // malformed, don't risk retirement
		}
		storedConf = parsed
	}
	ageBasis := doc.UpdatedAt
	if anchor := doc.Metadata[decayAnchorKey]; anchor != "" {
		ageBasis = anchor
	}
	anchoredAt, err := time.Parse(time.RFC3339, ageBasis)
	if err != nil {
		return 0, decayActionNoop
	}
	ageDays := time.Since(anchoredAt).Hours() / 24
	if ageDays < float64(memory.SharedGraceDays) {
		return storedConf, decayActionNoop
	}
	weeksPastGrace := (ageDays - float64(memory.SharedGraceDays)) / 7
	newConf := 1.0 - (weeksPastGrace * memory.SharedDecayPerWeek)
	if newConf <= memory.SharedRetirementFloor {
		return newConf, decayActionRetire
	}
	if fmt.Sprintf("%.2f", newConf) == fmt.Sprintf("%.2f", storedConf) {
		return newConf, decayActionNoop
	}
	return newConf, decayActionDecay
}

// reconcilePatterns pages through patterns rows lacking supermemory_id, calls
// IndexRepoPattern / IndexOwnerPattern (upsert via customID, so retries are
// idempotent), and writes the returned SM ID back to the row. Stops on
// context cancel, empty page, max-rows cap, OR maxConsecutiveFailures — the
// last is a circuit breaker that prevents an outage from looping over the
// same pending rows forever.
//
// Routing: rows with non-nil repo_id go through IndexRepoPattern into the
// {repo} container. Rows with nil repo_id are org-wide / shared patterns
// (auto-learned or reply-feedback) and go through IndexOwnerPattern into
// the _shared container. Previously these rows failed repoNameFor and
// stayed pending forever.
func reconcilePatterns(ctx context.Context, logger *slog.Logger, st *store.Store, indexer memory.Indexer, installID int64, cfg runConfig) (int, error) {
	// --full: single bounded pass over every pattern (the result set doesn't
	// shrink as rows are processed, so the pending sweep's loop-requery would
	// spin forever — see ListAllPatternsForRepush).
	if cfg.full {
		rows, err := st.Q.ListAllPatternsForRepush(ctx, db.ListAllPatternsForRepushParams{
			InstallationID: installID,
			Limit:          int32(cfg.maxRows),
		})
		if err != nil {
			return 0, fmt.Errorf("listing all patterns: %w", err)
		}
		var total, consecutiveFailures int
		for _, row := range rows {
			if ctx.Err() != nil {
				return total, ctx.Err()
			}
			if consecutiveFailures >= maxConsecutiveFailures {
				return total, fmt.Errorf("pattern repush: %d consecutive failures, aborting", consecutiveFailures)
			}
			if err := reindexPattern(ctx, logger, st, indexer, cfg, row.ID, row.RepoID, row.Content, row.Source, row.Category, row.PRNumber); err != nil {
				consecutiveFailures = handleRowErr(logger, "repush pattern failed", "pattern_id", row.ID, err, consecutiveFailures)
				continue
			}
			total++
			consecutiveFailures = 0
		}
		return total, nil
	}

	var total, consecutiveFailures int
	for total < cfg.maxRows {
		if ctx.Err() != nil {
			return total, ctx.Err()
		}
		rows, err := st.Q.ListPatternsPendingSM(ctx, db.ListPatternsPendingSMParams{
			InstallationID: installID,
			Limit:          cfg.batchSize,
		})
		if err != nil {
			return total, fmt.Errorf("listing patterns: %w", err)
		}
		if len(rows) == 0 {
			return total, nil
		}
		for _, row := range rows {
			if consecutiveFailures >= maxConsecutiveFailures {
				return total, fmt.Errorf("pattern reconcile: %d consecutive failures, aborting", consecutiveFailures)
			}
			if err := reindexPattern(ctx, logger, st, indexer, cfg, row.ID, row.RepoID, row.Content, row.Source, row.Category, row.PRNumber); err != nil {
				consecutiveFailures = handleRowErr(logger, "reindex pattern failed", "pattern_id", row.ID, err, consecutiveFailures)
				continue
			}
			total++
			consecutiveFailures = 0
		}
		// Plan mode never writes supermemory_id back, so the next query
		// returns the same pending rows forever. Cap at one batch and
		// surface the true pending count.
		if cfg.plan {
			return total, nil
		}
		if len(rows) < int(cfg.batchSize) {
			return total, nil
		}
	}
	return total, nil
}

// reindexPattern indexes one pattern row into the new container (repo-scoped
// into {repo}, org-wide into _shared) and writes the resulting Supermemory id
// back to Postgres. Returns nil on success or in --plan mode; a non-nil error
// means the row failed and the caller should count it toward the circuit
// breaker. Shared by the drift sweep and the --full re-push.
func reindexPattern(ctx context.Context, logger *slog.Logger, st *store.Store, indexer memory.Indexer, cfg runConfig, id int, repoID *int64, content, source string, category *string, prNumber *int) error {
	pattern := memory.PatternMemory{Content: content, Source: source}
	if category != nil {
		pattern.Category = *category
	}
	if prNumber != nil {
		pattern.PRNumber = *prNumber
	}
	if cfg.plan {
		logger.Info("plan: would reindex pattern", "id", id, "source", source, "scope", patternScopeName(repoID))
		return nil
	}
	var smID string
	if repoID == nil {
		// Shared / org-wide pattern. Reconstruct the pipeline's customID so a
		// --full re-push upserts the existing _shared doc instead of duplicating
		// it under a reconciler-specific ID.
		pattern.CustomID = pipelinePatternCustomID("", source, content, category, true)
		resp, err := indexer.IndexSharedPattern(ctx, pattern)
		if err != nil {
			return fmt.Errorf("index owner pattern: %w", err)
		}
		if resp != nil {
			smID = resp.ID
		}
	} else {
		repoName, err := repoNameFor(ctx, st, repoID)
		if err != nil {
			return fmt.Errorf("resolve repo: %w", err)
		}
		pattern.CustomID = pipelinePatternCustomID(repoName, source, content, category, false)
		resp, err := indexer.IndexPattern(ctx, repoName, pattern)
		if err != nil {
			return fmt.Errorf("index repo pattern: %w", err)
		}
		if resp != nil {
			smID = resp.ID
		}
	}
	if smID == "" {
		return fmt.Errorf("indexer returned empty supermemory id")
	}
	rows, err := st.Q.UpdatePatternSupermemoryID(ctx, db.UpdatePatternSupermemoryIDParams{
		SupermemoryID: &smID,
		ID:            id,
	})
	if err != nil {
		return fmt.Errorf("write-back pattern SM id: %w", err)
	}
	if rows == 0 {
		// The PG row vanished between the pending/repush snapshot and now (e.g.
		// an admin deleted the pattern from the dashboard mid-run). The UI delete
		// path only removes the SM doc when patterns.supermemory_id was non-NULL,
		// so the doc we just created would be an undeletable orphan surfacing in
		// specialist retrieval forever. Best-effort delete it by server id.
		logger.Warn("pattern row vanished mid-run; deleting orphaned SM doc", "pattern_id", id, "sm_id", smID)
		if delErr := indexer.DeleteDocument(ctx, smID); delErr != nil {
			logger.Warn("delete orphaned pattern SM doc", "pattern_id", id, "sm_id", smID, "error", delErr)
		}
	}
	return nil
}

// pipelinePatternCustomID delegates to memory.PipelinePatternCustomID — the
// single source of truth for reconstructing the deterministic customID the
// pipeline assigned when it first indexed a pattern, shared with
// cmd/migrate-memory so both tools upsert the SAME Supermemory doc instead of
// duplicating it.
func pipelinePatternCustomID(repoName, source, content string, category *string, shared bool) string {
	return memory.PipelinePatternCustomID(repoName, source, content, category, shared)
}

// rawConvention delegates to memory.RawConvention (single source of truth).
func rawConvention(content string, category *string) string {
	return memory.RawConvention(content, category)
}

// reconcileMode returns a short label describing the run mode for summary logs:
// plan (dry-run) takes precedence, then full (re-push), else the default drift
// sweep.
func reconcileMode(cfg runConfig) string {
	switch {
	case cfg.plan:
		return "plan"
	case cfg.full:
		return "full"
	default:
		return "drift"
	}
}

// patternScopeName returns a log-friendly label for a pattern's scope based on
// its (nullable) repo_id. Used by --plan output so operators see where a
// pattern would route without calling the real indexer.
func patternScopeName(repoID *int64) string {
	if repoID == nil {
		return "shared"
	}
	return "repo"
}

// reconcileScenarios is the scenario-table mirror of reconcilePatterns with
// the same circuit-breaker semantics. scenarios.repo_id is nullable (015_
// scenarios) and issue-webhook scenarios for un-synced repos carry NULL — the
// pending/repush queries filter those out (repo_id IS NOT NULL) since they can
// never route to a {repo} container; any that still slip through fail as
// errPermanent and are skipped without tripping the breaker.
func reconcileScenarios(ctx context.Context, logger *slog.Logger, st *store.Store, indexer memory.Indexer, installID int64, cfg runConfig) (int, error) {
	if cfg.full {
		rows, err := st.Q.ListAllScenariosForRepush(ctx, db.ListAllScenariosForRepushParams{
			InstallationID: installID,
			Limit:          int32(cfg.maxRows),
		})
		if err != nil {
			return 0, fmt.Errorf("listing all scenarios: %w", err)
		}
		var total, consecutiveFailures int
		for _, row := range rows {
			if ctx.Err() != nil {
				return total, ctx.Err()
			}
			if consecutiveFailures >= maxConsecutiveFailures {
				return total, fmt.Errorf("scenario repush: %d consecutive failures, aborting", consecutiveFailures)
			}
			if err := reindexScenario(ctx, logger, st, indexer, cfg, row.ID, row.RepoID, row.Description, row.Severity, row.Files); err != nil {
				consecutiveFailures = handleRowErr(logger, "repush scenario failed", "scenario_id", row.ID, err, consecutiveFailures)
				continue
			}
			total++
			consecutiveFailures = 0
		}
		return total, nil
	}

	var total, consecutiveFailures int
	for total < cfg.maxRows {
		if ctx.Err() != nil {
			return total, ctx.Err()
		}
		rows, err := st.Q.ListScenariosPendingSM(ctx, db.ListScenariosPendingSMParams{
			InstallationID: installID,
			Limit:          cfg.batchSize,
		})
		if err != nil {
			return total, fmt.Errorf("listing scenarios: %w", err)
		}
		if len(rows) == 0 {
			return total, nil
		}
		for _, row := range rows {
			if consecutiveFailures >= maxConsecutiveFailures {
				return total, fmt.Errorf("scenario reconcile: %d consecutive failures, aborting", consecutiveFailures)
			}
			if err := reindexScenario(ctx, logger, st, indexer, cfg, row.ID, row.RepoID, row.Description, row.Severity, row.Files); err != nil {
				consecutiveFailures = handleRowErr(logger, "reindex scenario failed", "scenario_id", row.ID, err, consecutiveFailures)
				continue
			}
			total++
			consecutiveFailures = 0
		}
		// Plan mode: see the note on reconcilePatterns — same loop hazard.
		if cfg.plan {
			return total, nil
		}
		if len(rows) < int(cfg.batchSize) {
			return total, nil
		}
	}
	return total, nil
}

// reindexScenario indexes one scenario into the {repo} container and records
// the deterministic customID as the sync marker. Returns nil on success or in
// --plan mode. Shared by the drift sweep and the --full re-push.
func reindexScenario(ctx context.Context, logger *slog.Logger, st *store.Store, indexer memory.Indexer, cfg runConfig, id int64, repoID *int64, description string, severity *string, files []string) error {
	if cfg.plan {
		logger.Info("plan: would reindex scenario", "id", id, "severity", nullStr(severity))
		return nil
	}
	repoName, err := repoNameFor(ctx, st, repoID)
	if err != nil {
		return fmt.Errorf("resolve repo: %w", err)
	}
	if err := indexer.IndexScenario(ctx, "", repoName, id, description, nullStr(severity), files); err != nil {
		return fmt.Errorf("index scenario: %w", err)
	}
	// IndexScenario's customID is deterministic from (repo, scenarioID) — we
	// reconstruct it here via memory.ScenarioCustomID (the single source, same
	// repoIDSegment collision-hash the real write uses) and record it as
	// supermemory_id. This is a sync-complete marker, not the actual SM document
	// ID from the server response. Acceptable: the column stores what the next
	// SearchScenariosWithIDs call would target, which is exactly the customID
	// space. A future iteration may expose the server ID via IndexScenario's
	// return; tracked as follow-up.
	customID := memory.ScenarioCustomID(repoName, id)
	if err := st.Q.UpdateScenarioSupermemoryID(ctx, db.UpdateScenarioSupermemoryIDParams{
		SupermemoryID: &customID,
		ID:            id,
	}); err != nil {
		return fmt.Errorf("write-back scenario SM id: %w", err)
	}
	return nil
}

// repoNameFor resolves the pipeline's `repo` token from a repo's DB id so the
// reconciler builds container tags / customIDs identical to what the live
// pipeline wrote. It selects full_name via sqlc (GetRepoFullName) — the repos
// table has NO `name` column (only full_name, 001_initial), and the previous
// raw `SELECT name` failed every repo-scoped row with SQLSTATE 42703, wedging
// the circuit breaker so no drift was ever repaired.
//
// It then splits owner/repo with EXACTLY pipeline.splitRepoFullName's semantics
// (strings.SplitN(full_name, "/", 2), require two parts, return parts[1]) —
// RepoTagNew(repo) and every PatternCustomID/ScenarioCustomID derive from that
// token, so any other derivation (full_name, split_part edge cases) writes to a
// different container and duplicates docs.
//
// A missing repo row or malformed full_name is a permanent per-row failure
// (errPermanent) — the caller skips it without tripping the circuit breaker. A
// transient DB error is returned bare so the breaker still trips on an outage.
func repoNameFor(ctx context.Context, st *store.Store, repoID *int64) (string, error) {
	if repoID == nil {
		return "", fmt.Errorf("nil repo id (row is not repo-scoped): %w", errPermanent)
	}
	fullName, err := st.Q.GetRepoFullName(ctx, *repoID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("repo id %d not found: %w", *repoID, errPermanent)
		}
		return "", fmt.Errorf("querying repo %d: %w", *repoID, err)
	}
	parts := strings.SplitN(fullName, "/", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid repo full_name %q for repo id %d: %w", fullName, *repoID, errPermanent)
	}
	return parts[1], nil
}

// nullStr returns the string value of a *string or "" if nil. Keeps call
// sites tidy around the nullable columns sqlc returns.
func nullStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
