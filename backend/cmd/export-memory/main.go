// Command export-memory takes the phase-0 safety snapshot for the
// memory-in-Postgres migration (docs/memory-pg/02-migration-plan.md §2.2).
//
// Re-derivation rebuilds ~90% of the corpus from Postgres, but four classes
// live ONLY in Supermemory — synthesis file memories, reply_feedback learnings,
// dismissal `reason` extras, and `_shared` decayed confidence values. This
// binary pages every container of every installation that still has a BYOK key
// and archives the raw documents into memory_export_archive, unparsed. The
// import step decides later what to do with them.
//
// Read-only against Supermemory: it lists, it never writes or deletes. Safe to
// re-run — archiving is idempotent on (installation_id, doc_id), so an
// interrupted sweep resumes by simply running again.
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

	"github.com/BeLazy167/argus/backend/internal/crypto"
	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/store"
)

// listPageSize is Supermemory's practical page ceiling for /v3/documents/list
// with includeContent — larger pages time out on installs with long synthesis
// docs. A 50k-doc install exports in minutes at this size.
const listPageSize = 200

// maxPagesPerContainer bounds a single container's sweep. A container that
// keeps returning full pages past this is either enormous or paginating wrong;
// either way the run should say so rather than loop forever.
const maxPagesPerContainer = 500

type runConfig struct {
	installation int64
	plan         bool
}

func main() {
	var cfg runConfig
	flag.Int64Var(&cfg.installation, "installation", 0, "restrict to one installation ID (0 = all with a Supermemory key)")
	flag.BoolVar(&cfg.plan, "plan", false, "dry-run: page the containers and report counts, write nothing to Postgres")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		logger.Error("DATABASE_URL is required")
		os.Exit(1)
	}

	// Without this every installation's Supermemory key fails to decrypt, the
	// registry hands back a nil client, and the sweep reports each install as
	// an ordinary "no key" skip — exiting 0 with archived=0 and
	// failed_installations=0. A snapshot tool that reports success having
	// copied nothing is the worst outcome available, so refuse to start.
	if err := crypto.InitFromEnv(); err != nil {
		logger.Error("ENCRYPTION_KEY is required to decrypt per-installation Supermemory keys", "error", err)
		os.Exit(1)
	}

	// Signal-aware: a snapshot interrupted mid-container is fine (idempotent
	// re-run resumes), but it must stop promptly rather than finish the sweep.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.New(ctx, dsn)
	if err != nil {
		logger.Error("connecting to postgres", "error", err)
		os.Exit(1)
	}
	defer st.Close()

	if err := run(ctx, logger, st, cfg); err != nil {
		logger.Error("export failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger, st *store.Store, cfg runConfig) error {
	registry := memory.NewRegistry(st, logger)

	installs, err := resolveInstallations(ctx, st, cfg.installation)
	if err != nil {
		return fmt.Errorf("resolving installations: %w", err)
	}
	logger.Info("export starting", "installations", len(installs), "plan", cfg.plan)

	var archived, skipped, failed, skippedNoClient int
	for _, id := range installs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		a, s, noClient, err := exportInstallation(ctx, logger, st, registry, id, cfg)
		archived += a
		skipped += s
		if noClient {
			skippedNoClient++
		}
		if err != nil {
			// One installation's failure must not abandon the rest: a revoked
			// key or a container that 500s is expected at this scale, and the
			// snapshot's value is per-install.
			failed++
			logger.Warn("installation export failed", "installation_id", id, "error", err)
		}
	}

	logger.Info("export complete",
		"installations", len(installs),
		"archived", archived,
		"skipped_no_doc_id", skipped,
		"skipped_no_client", skippedNoClient,
		"failed_installations", failed,
	)
	if skippedNoClient == len(installs) && len(installs) > 0 {
		// Every install unreachable is a configuration fault, not an empty
		// corpus. Returning nil here would exit 0 on a snapshot of nothing.
		return fmt.Errorf("no installation yielded a supermemory client (%d/%d); nothing was archived", skippedNoClient, len(installs))
	}
	if skipped > 0 {
		// Loud: these are documents the archive does NOT hold, and the archive
		// is the only copy of the SM-only classes once containers are deleted.
		logger.Warn("documents skipped for an empty server id; they are NOT in the archive", "count", skipped)
	}
	if failed > 0 {
		return fmt.Errorf("%d installation(s) failed to export; the snapshot is incomplete", failed)
	}
	return nil
}

func resolveInstallations(ctx context.Context, st *store.Store, one int64) ([]int64, error) {
	if one > 0 {
		return []int64{one}, nil
	}
	return st.Q.ListInstallationsWithSMKey(ctx)
}

// exportInstallation archives every container this installation owns. Returns
// (archived, skipped, noClient). noClient reports that the installation was
// passed over for want of a usable Supermemory client, which the caller
// aggregates — a sweep where every install is skipped that way is a
// misconfiguration, not an empty corpus.
func exportInstallation(ctx context.Context, logger *slog.Logger, st *store.Store, registry *memory.Registry, installID int64, cfg runConfig) (archivedN, skippedN int, noClient bool, err error) {
	// GetClient, never GetIndexer: GetIndexer issues the one-time
	// DisableLLMFilter settings PATCH, and a read-only snapshot must not mutate
	// the customer's Supermemory account.
	client := registry.GetClient(ctx, installID)
	// A cancelled context also yields a nil client. Distinguish the two before
	// treating nil as "no key": reporting an interrupted run as a clean skip
	// would exit 0 on a partial snapshot, and this archive is the last copy of
	// the SM-only classes.
	if err := ctx.Err(); err != nil {
		return 0, 0, false, err
	}
	if client == nil {
		// No key, or a decrypt failure. Per the migration plan these installs
		// are re-derive-only and their SM-only classes are accepted losses —
		// but the caller counts them, because a run where EVERY install lands
		// here is indistinguishable from a clean empty sweep otherwise.
		logger.Info("no supermemory client; skipping installation", "installation_id", installID)
		return 0, 0, true, nil
	}

	tags, err := containerTags(ctx, st, installID)
	if err != nil {
		return 0, 0, false, fmt.Errorf("resolving container tags: %w", err)
	}

	var archived, skipped int
	for _, tag := range tags {
		a, s, err := exportContainer(ctx, logger, st, client, installID, tag, cfg)
		archived += a
		skipped += s
		if err != nil {
			return archived, skipped, false, fmt.Errorf("container %q: %w", tag, err)
		}
	}

	if !cfg.plan {
		total, err := st.CountArchivedDocs(ctx, installID)
		if err != nil {
			logger.Warn("counting archived docs", "installation_id", installID, "error", err)
		} else {
			logger.Info("installation archived", "installation_id", installID,
				"containers", len(tags), "archived_this_run", archived, "archive_total", total)
		}
	}
	return archived, skipped, false, nil
}

// containerTags lists every container an installation writes to: one per repo
// plus the cross-repo `_shared`, built from the same RepoTagNew the writers
// use. Mirrors legacyContainerTags in cmd/migrate-memory.
//
// KNOWN GAP, and the reason this is worth stating rather than assuming: the
// tags are derived from the repos table, because Supermemory exposes no way to
// enumerate an account's containers. A container whose repo row no longer
// exists is therefore invisible to this export and its SM-only documents are
// unrecoverable once the account is cleared. Nothing in the codebase hard-
// deletes repos today, so the gap is currently theoretical — but it is a
// property of the repos table, not of this function, and if that ever changes
// the snapshot silently narrows.
func containerTags(ctx context.Context, st *store.Store, installID int64) ([]string, error) {
	fullNames, err := st.Q.ListRepoFullNamesForInstallation(ctx, installID)
	if err != nil {
		return nil, err
	}
	tags := make([]string, 0, len(fullNames)+1)
	seen := map[string]bool{}
	for _, fn := range fullNames {
		tag := memory.RepoTagNew(shortName(fn))
		if tag == "" || seen[tag] {
			continue
		}
		seen[tag] = true
		tags = append(tags, tag)
	}
	// Always last, and always present: `_shared` holds the org-promoted
	// patterns, rules and reply_feedback learnings — two of the four SM-only
	// classes — so it must be exported even for an installation with no repo
	// rows at all.
	tags = append(tags, memory.SharedTag)
	return tags, nil
}

// shortName reduces "owner/repo" to "repo" — the form the container tags are
// built from.
func shortName(fullName string) string {
	for i := len(fullName) - 1; i >= 0; i-- {
		if fullName[i] == '/' {
			return fullName[i+1:]
		}
	}
	return fullName
}

// exportContainer pages one container to exhaustion. Returns (archived, skipped).
func exportContainer(ctx context.Context, logger *slog.Logger, st *store.Store, client *memory.Client, installID int64, tag string, cfg runConfig) (int, int, error) {
	var archived, skipped int
	for page := 1; page <= maxPagesPerContainer; page++ {
		if ctx.Err() != nil {
			return archived, skipped, ctx.Err()
		}
		resp, err := client.ListDocuments(ctx, memory.ListRequest{
			Limit:          listPageSize,
			Page:           page,
			ContainerTags:  []string{tag},
			IncludeContent: true,
		})
		if err != nil {
			return archived, skipped, fmt.Errorf("listing page %d: %w", page, err)
		}
		if len(resp.Memories) == 0 {
			return archived, skipped, nil
		}

		docs := make([]store.ExportedDoc, 0, len(resp.Memories))
		for _, m := range resp.Memories {
			// Prefer the bytes Supermemory actually sent. Re-marshalling the
			// typed Document would silently drop every field we don't model —
			// and this archive's whole purpose is to hold what re-derivation
			// cannot reconstruct. Fall back to a re-marshal only if Raw is
			// somehow absent, which would mean the doc was constructed rather
			// than decoded.
			payload := m.Raw
			if len(payload) == 0 {
				marshalled, err := json.Marshal(m)
				if err != nil {
					return archived, skipped, fmt.Errorf("marshalling doc %q: %w", m.ID, err)
				}
				logger.Warn("doc had no raw payload; archiving a lossy re-marshal",
					"installation_id", installID, "doc_id", m.ID, "container_tag", tag)
				payload = marshalled
			}
			docs = append(docs, store.ExportedDoc{
				ContainerTag: tag,
				DocID:        m.ID,
				CustomID:     m.CustomID,
				Payload:      payload,
			})
		}

		if cfg.plan {
			for _, d := range docs {
				if d.DocID == "" {
					skipped++
				} else {
					archived++
				}
			}
		} else {
			a, s, err := st.ArchiveExportedDocs(ctx, installID, docs)
			if err != nil {
				return archived, skipped, err
			}
			archived += a
			skipped += s
		}

		// Short page = last page. Trusting len < limit rather than the
		// pagination envelope keeps this correct if totalItems is stale or
		// absent, which it is on some containers.
		if len(resp.Memories) < listPageSize {
			return archived, skipped, nil
		}
		if page == maxPagesPerContainer {
			return archived, skipped, fmt.Errorf(
				"container still returning full pages at the %d-page cap; export is INCOMPLETE for this container", maxPagesPerContainer)
		}
	}
	return archived, skipped, nil
}
