package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/posthog/posthog-go"

	"github.com/BeLazy167/argus/backend/internal/api"
	"github.com/BeLazy167/argus/backend/internal/config"
	"github.com/BeLazy167/argus/backend/internal/crypto"
	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/llm"
	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/obs"
	"github.com/BeLazy167/argus/backend/internal/pipeline"
	"github.com/BeLazy167/argus/backend/internal/store"
)

// Run initializes all components and starts the server.
func Run() error {
	// Base handler stays JSON-on-stdout for Fly log shipping. We wrap it with
	// obs.Handler when POSTHOG_API_KEY is set so every structured slog call
	// that declares an `event=` attr also lands in PostHog. Missing key =
	// kill-switch: text logs continue, PostHog forwarding is a no-op.
	var baseHandler slog.Handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	var phClient posthog.Client
	var phHandler *obs.Handler
	if apiKey := os.Getenv("POSTHOG_API_KEY"); apiKey != "" {
		client, err := posthog.NewWithConfig(apiKey, posthog.Config{
			Endpoint:  "https://us.i.posthog.com",
			BatchSize: 100,
			Interval:  10 * time.Second,
		})
		if err != nil {
			slog.New(baseHandler).Error("posthog init failed", "error", err)
		} else {
			phClient = client
			phHandler = obs.NewPostHogHandler(baseHandler, client)
			baseHandler = phHandler
		}
	}
	logger := slog.New(baseHandler)
	// Route package-level `slog.*` calls (chat.go llm.call.* and 70+ other
	// sites across internal/) through the PostHog-wrapped handler. Without
	// this, slog.Default() stays on Go's stdlib text handler and those
	// structured events never reach the forwarder.
	slog.SetDefault(logger)
	// Ordering: phHandler.Close() must run BEFORE phClient.Close() so the
	// drain goroutine finishes enqueuing into posthog-go before we ask
	// posthog-go to flush its wire queue. defer runs LIFO, so declare the
	// client close first (outer) then the handler close (inner).
	if phClient != nil {
		defer func() {
			if err := phClient.Close(); err != nil {
				logger.Warn("posthog client close", "error", err)
			}
		}()
	}
	if phHandler != nil {
		defer func() {
			if err := phHandler.Close(); err != nil {
				logger.Warn("posthog handler close", "error", err)
			}
		}()
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	logMermaidValidatorStatus(logger, cfg)

	ctx, cancel := context.WithCancel(context.Background())

	// Database
	db, err := store.New(ctx, cfg.DatabaseURL)
	if err != nil {
		cancel()
		return fmt.Errorf("connecting to database: %w", err)
	}
	// The durable event bus holds a pool connection in WaitForNotification.
	// Defers run LIFO: cancel its root context before Close waits for every pool
	// connection to be released. Later appCtx/server defers still run first.
	defer db.Close()
	defer cancel()

	// GitHub App
	ghApp := ghpkg.NewApp(cfg.GitHubAppID, cfg.GitHubPrivateKey)
	ghClient := ghpkg.NewClient(ghApp, cfg.GitHubAppSlug)

	// Encryption (optional — only required if BYOK keys are used)
	if cfg.EncryptionKey != "" {
		if err := crypto.Init(cfg.EncryptionKey); err != nil {
			return fmt.Errorf("initializing encryption: %w", err)
		}
	}

	// LLM (BYOK only — keys resolved from DB)
	registry := llm.NewRegistry()
	registry.SetResolver(db)
	registry.SetReferer(cfg.DashboardBaseURL)

	// Pricing (DB-backed, cached 10min)
	pricingCache := store.NewPricingCache(db)
	llm.SetPricingLookup(func(model string) (float64, float64, bool) {
		return pricingCache.Lookup(ctx, model)
	})

	// Memory / RAG (per-org via registry). Memory lives in Postgres; wiring it
	// here — rather than leaving the constructors referenced only by tests —
	// is what makes GetIndexer return anything at all.
	embedRegistry := memory.NewEmbedderRegistry(db, memory.PlatformEmbeddings{
		APIKey:     cfg.EmbeddingsAPIKey,
		BaseURL:    cfg.EmbeddingsBaseURL,
		Model:      cfg.EmbeddingsModel,
		Dimensions: cfg.EmbeddingsDimensions,
	}, logger)
	memRegistry := memory.NewRegistry(logger).
		WithPostgresBackend(db.Pool, embedRegistry)

	// Pipeline
	eventBus := pipeline.NewDurableEventBus(ctx, db.Pool, logger)
	triageStage := pipeline.NewTriageStage(registry, db)
	reviewStage := pipeline.NewReviewStage(registry, db, ghClient, memRegistry, cfg.MaxConcurrentReviews)
	intentStage := pipeline.NewIntentExtractionStage(registry, db, ghClient, logger)
	scoringStage := pipeline.NewScoringStage(registry, db)
	orchestrator := pipeline.NewOrchestrator(db.Pool, db, ghClient, reviewStage, triageStage, intentStage, scoringStage, memRegistry, registry, eventBus, logger, cfg)
	replyAnalyzer := pipeline.NewReplyAnalyzer(registry, db, ghClient, memRegistry, logger)
	reactionAnalyzer := pipeline.NewReactionAnalyzer(db, ghClient, memRegistry, logger)
	orchestrator.SetRecoveryReactionReconciler(reactionAnalyzer.SweepPRReactions)

	// Mark stale reviews as failed before resuming incomplete pipelines
	if count, err := db.RecoverStaleReviews(ctx, 10*time.Minute); err != nil {
		logger.Warn("failed to recover stale reviews", "error", err)
	} else if count > 0 {
		logger.Info("recovered stale reviews", "count", count)
	}

	// Reconcile the shipped personas into rows.
	//
	// The compiled-in switch stays the source of truth; these rows are its
	// projection, and they are what makes the eight built-ins visible and
	// retunable in the dashboard. Without this a fresh deployment lists zero
	// personas while reviews keep using all eight.
	//
	// Non-fatal. Resolution falls back to the compiled-in overlay on any miss,
	// so a failed seed costs the dashboard listing, not a single review.
	if err := db.SeedBuiltinPersonas(ctx, builtinPersonaRows()); err != nil {
		logger.Warn("seeding built-in personas", "error", err)
	}

	// Recover incomplete pipeline runs (async — don't block server startup)
	appCtx, appCancel := context.WithCancel(context.Background())

	// Migration 074 deliberately stamps existing vectors with an unknown
	// embedding space. Check immediately on startup, retry transient failures
	// with bounded backoff, and periodically converge the whole fleet so a
	// failed rotation trigger cannot strand a tenant. Per-installation advisory
	// locks and Registry capacity limits keep every replica safe to run this.
	reembedDone := make(chan struct{})
	go func() {
		defer close(reembedDone)
		runMemoryReembedConvergence(appCtx, logger, defaultReembedConvergenceOptions(), memRegistry.ReembedAllCurrentSpaces)
	}()
	defer func() {
		appCancel()
		<-reembedDone
	}()

	// Durable projection of relational patterns/rules into the memory store.
	// The worker is safe to run on every replica: claims use SKIP LOCKED and
	// deterministic memory IDs make replay after a stale claim idempotent.
	mirrorWorker := memory.NewMirrorWorker(db, func(ctx context.Context, installationID int64) memory.MirrorIndexer {
		idx := memRegistry.GetIndexer(ctx, installationID)
		mirrorIndexer, _ := idx.(memory.MirrorIndexer)
		return mirrorIndexer
	}, logger)
	go mirrorWorker.Run(appCtx, time.Second)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("RecoverIncomplete panic", "recover", r)
			}
		}()
		if err := orchestrator.RecoverIncomplete(appCtx); err != nil {
			logger.Error("recovering incomplete pipelines", "error", err)
		}
	}()

	// Pattern decay goroutine — runs daily, cleans stale low-quality patterns
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				installations, err := db.ListInstallations(ctx)
				if err != nil {
					logger.Error("pattern decay: list installations", "error", err)
					cancel()
					continue
				}
				for _, inst := range installations {
					deleted, err := db.DecayStalePatterns(ctx, inst.ID, 90*24*time.Hour, 0.3)
					if err != nil {
						logger.Error("pattern decay", "installation", inst.ID, "error", err)
					} else if deleted > 0 {
						logger.Info("pattern decay", "installation", inst.ID, "deleted", deleted)
					}
				}
				cancel()
			case <-appCtx.Done():
				return
			}
		}
	}()

	// Code-graph full-index backfill — walks whole repos, one per hour.
	//
	// Before authoritative generations, the graph accumulated as disconnected
	// pull-request-shaped islands and blast radius returned fragments. This
	// backfill publishes complete default-branch snapshots.
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("graph index backfill panic", "recover", r)
			}
		}()
		runGraphIndexBackfill(appCtx, db, ghClient, logger)
	}()

	// JWT auth (Clerk or SuperTokens)
	if cfg.ClerkJWKSURL != "" {
		api.InitJWKS(cfg.ClerkJWKSURL, logger)
	}

	// API Server
	server := api.NewServer(db, ghApp, orchestrator, replyAnalyzer, reactionAnalyzer, registry, eventBus, cfg, logger, memRegistry)
	defer server.Close()

	httpServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      server,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Memory profiler — periodic RSS/heap samples + threshold-triggered
	// gzipped pprof heap dump. Tied to appCtx so it shuts down with the
	// pattern-decay goroutine.
	StartMemoryProfiler(appCtx, logger)

	// Graceful shutdown
	errCh := make(chan error, 1)
	go func() {
		logger.Info("server starting", "port", cfg.Port)
		errCh <- httpServer.ListenAndServe()
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		// TODO: pendingCount = # in-flight reviews. No easy accessor
		// exposed on Server today, so we emit 0. Wire this from
		// the orchestrator's run registry when adding pipeline.panic_recovered
		// & sweeper.recovered_orphan in the same pass.
		logger.Info("shutdown signal received",
			slog.String("event", "fly.shutdown_signal_received"),
			slog.String("signal", sig.String()),
			slog.Int("pending_reviews", 0),
		)
	case err := <-errCh:
		if err != http.ErrServerClosed {
			return fmt.Errorf("server error: %w", err)
		}
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	return httpServer.Shutdown(shutdownCtx)
}

// builtinPersonaRows adapts the pipeline's compiled-in personas to the store's
// row shape.
//
// The adaptation lives here, in the composition root, rather than in either
// package: store must not import pipeline (pipeline already imports store, so
// that direction is a cycle), and pipeline must not learn the row type just to
// describe its own prompts.
func builtinPersonaRows() []store.Persona {
	builtins := pipeline.BuiltinPersonas()
	rows := make([]store.Persona, 0, len(builtins))
	for _, b := range builtins {
		rows = append(rows, store.Persona{
			Slug:           string(b.Slug),
			Name:           b.Name,
			PromptOverlay:  b.PromptOverlay,
			SpecialistHint: b.SpecialistHint,
			IsBuiltin:      true,
		})
	}
	return rows
}
