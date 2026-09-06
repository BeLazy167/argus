package app

import (
	"context"
	"fmt"
	"log"
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
	processStarted := time.Now()
	// Base handler stays JSON-on-stdout for Fly log shipping. We wrap it with
	// obs.Handler when POSTHOG_API_KEY is set so every structured slog call
	// that declares an `event=` attr also lands in PostHog. Missing key =
	// kill-switch: text logs continue, PostHog forwarding is a no-op.
	var baseHandler slog.Handler = obs.NewContextHandler(
		slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}),
	)
	var phClient posthog.Client
	var phHandler *obs.Handler
	if apiKey := os.Getenv("POSTHOG_API_KEY"); apiKey != "" {
		client, err := posthog.NewWithConfig(apiKey, posthog.Config{
			Endpoint:  "https://us.i.posthog.com",
			BatchSize: 100,
			Interval:  10 * time.Second,
			Verbose:   true,
			Transport: obs.NewLoggingRoundTripper("posthog", http.DefaultTransport),
			Logger:    obs.NewPrintfLogger(slog.New(baseHandler)),
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
	logger.Info("backend process startup started")
	defer func() {
		logger.Info("backend process stopped", "duration_ms", time.Since(processStarted).Milliseconds())
	}()
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
			logger.Info("posthog forwarding totals",
				"sent", phHandler.Sent(),
				"dropped_buffer", phHandler.DroppedBuffer(),
				"dropped_breaker", phHandler.DroppedBreaker(),
				"dropped_enqueue", phHandler.DroppedEnqueue(),
				"dropped_unattributed", phHandler.DroppedUnattributed(),
				"breaker_open", phHandler.BreakerOpen(),
			)
		}()
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	logger.Info("backend configuration loaded",
		"environment", cfg.Env,
		"port", cfg.Port,
		"github_app_id", cfg.GitHubAppID,
		"github_app_slug", cfg.GitHubAppSlug,
		"self_hosted", cfg.SelfHosted,
		"max_concurrent_reviews", cfg.MaxConcurrentReviews,
		"embeddings_model", cfg.EmbeddingsModel,
		"embeddings_dimensions", cfg.EmbeddingsDimensions,
		"embeddings_key_configured", cfg.EmbeddingsAPIKey != "",
		"encryption_configured", cfg.EncryptionKey != "",
		"authentication_configured", cfg.ClerkJWKSURL != "",
		"posthog_configured", phClient != nil,
	)
	logMermaidValidatorStatus(logger, cfg)

	ctx, cancel := context.WithCancel(context.Background())

	// Database
	databaseStarted := time.Now()
	logger.InfoContext(ctx, "database initialization started")
	db, err := store.New(ctx, cfg.DatabaseURL)
	if err != nil {
		cancel()
		logger.ErrorContext(ctx, "database initialization failed", "duration_ms", time.Since(databaseStarted).Milliseconds(), "error", err)
		return fmt.Errorf("connecting to database: %w", err)
	}
	logger.InfoContext(ctx, "database initialization completed", "duration_ms", time.Since(databaseStarted).Milliseconds(),
		"max_connections", db.Pool.Config().MaxConns, "min_connections", db.Pool.Config().MinConns)
	// The durable event bus holds a pool connection in WaitForNotification.
	// Defers run LIFO: cancel its root context before Close waits for every pool
	// connection to be released. Later appCtx/server defers still run first.
	defer db.Close()
	defer cancel()

	// GitHub App
	logger.InfoContext(ctx, "github clients initialization started")
	ghApp := ghpkg.NewApp(cfg.GitHubAppID, cfg.GitHubPrivateKey)
	ghClient := ghpkg.NewClient(ghApp, cfg.GitHubAppSlug)
	logger.InfoContext(ctx, "github clients initialization completed", "app_id", cfg.GitHubAppID, "app_slug", cfg.GitHubAppSlug)

	// Encryption (optional — only required if BYOK keys are used)
	if cfg.EncryptionKey != "" {
		logger.InfoContext(ctx, "encryption initialization started")
		if err := crypto.Init(cfg.EncryptionKey); err != nil {
			logger.ErrorContext(ctx, "encryption initialization failed", "error", err)
			return fmt.Errorf("initializing encryption: %w", err)
		}
		logger.InfoContext(ctx, "encryption initialization completed")
	} else {
		logger.InfoContext(ctx, "encryption initialization skipped", "reason", "encryption key not configured")
	}

	// LLM (BYOK only — keys resolved from DB)
	logger.InfoContext(ctx, "llm registry initialization started")
	registry := llm.NewRegistry()
	registry.SetResolver(db)
	registry.SetReferer(cfg.DashboardBaseURL)
	logger.InfoContext(ctx, "llm registry initialization completed")

	// Pricing (DB-backed, cached 10min)
	logger.InfoContext(ctx, "pricing cache initialization started")
	pricingCache := store.NewPricingCache(db)
	llm.SetPricingLookup(func(model string) (float64, float64, bool) {
		return pricingCache.Lookup(ctx, model)
	})
	logger.InfoContext(ctx, "pricing cache initialization completed", "cache_ttl", 10*time.Minute)

	// Memory / RAG (per-org via registry). Memory lives in Postgres; wiring it
	// here — rather than leaving the constructors referenced only by tests —
	// is what makes GetIndexer return anything at all.
	logger.InfoContext(ctx, "memory registry initialization started")
	embedRegistry := memory.NewEmbedderRegistry(db, memory.PlatformEmbeddings{
		APIKey:     cfg.EmbeddingsAPIKey,
		BaseURL:    cfg.EmbeddingsBaseURL,
		Model:      cfg.EmbeddingsModel,
		Dimensions: cfg.EmbeddingsDimensions,
	}, logger)
	memRegistry := memory.NewRegistry(logger).
		WithPostgresBackend(db.Pool, embedRegistry)
	// Latch the vector-type probe from a boot context. Left to the first search,
	// a short-deadline request could latch it wrong for the whole process.
	memRegistry.WarmVectorProbe(ctx)
	logger.InfoContext(ctx, "memory registry initialization completed",
		"platform_provider_configured", cfg.EmbeddingsAPIKey != "", "model", cfg.EmbeddingsModel,
		"dimensions", cfg.EmbeddingsDimensions)

	// Pipeline
	logger.InfoContext(ctx, "pipeline components initialization started")
	eventBus := pipeline.NewDurableEventBus(ctx, db.Pool, logger)
	triageStage := pipeline.NewTriageStage(registry, db)
	reviewStage := pipeline.NewReviewStage(registry, db, ghClient, memRegistry, cfg.MaxConcurrentReviews)
	intentStage := pipeline.NewIntentExtractionStage(registry, db, ghClient, logger)
	scoringStage := pipeline.NewScoringStage(registry, db)
	orchestrator := pipeline.NewOrchestrator(db.Pool, db, ghClient, reviewStage, triageStage, intentStage, scoringStage, memRegistry, registry, eventBus, logger, cfg)
	replyAnalyzer := pipeline.NewReplyAnalyzer(registry, db, ghClient, memRegistry, logger)
	reactionAnalyzer := pipeline.NewReactionAnalyzer(db, ghClient, memRegistry, logger)
	orchestrator.SetRecoveryReactionReconciler(reactionAnalyzer.SweepPRReactions)
	logger.InfoContext(ctx, "pipeline components initialization completed", "max_concurrent_reviews", cfg.MaxConcurrentReviews)

	// Mark stale reviews as failed before resuming incomplete pipelines.
	staleRecoveryStarted := time.Now()
	logger.InfoContext(ctx, "stale review recovery started", "stale_after", 10*time.Minute)
	if count, err := db.RecoverStaleReviews(ctx, 10*time.Minute); err != nil {
		logger.WarnContext(ctx, "stale review recovery failed", "duration_ms", time.Since(staleRecoveryStarted).Milliseconds(), "error", err)
	} else {
		logger.InfoContext(ctx, "stale review recovery completed", "count", count,
			"duration_ms", time.Since(staleRecoveryStarted).Milliseconds())
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
	personaSeedStarted := time.Now()
	logger.InfoContext(ctx, "built-in persona reconciliation started")
	if err := db.SeedBuiltinPersonas(ctx, builtinPersonaRows()); err != nil {
		logger.WarnContext(ctx, "built-in persona reconciliation failed", "duration_ms", time.Since(personaSeedStarted).Milliseconds(), "error", err)
	} else {
		logger.InfoContext(ctx, "built-in persona reconciliation completed", "persona_count", len(builtinPersonaRows()),
			"duration_ms", time.Since(personaSeedStarted).Milliseconds())
	}

	// Recover incomplete pipeline runs (async — don't block server startup)
	appCtx, appCancel := context.WithCancel(context.Background())
	logger.InfoContext(appCtx, "background workers initialization started")

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
		logger.Info("background workers shutdown started")
		appCancel()
		<-reembedDone
		logger.Info("background workers shutdown completed")
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
	logger.InfoContext(appCtx, "memory mirror worker launched", "poll_interval", time.Second)

	go runPipelineRecoverySweeper(appCtx, logger, pipelineRecoveryFirstSweepDelay, pipelineRecoverySweepInterval, orchestrator.RecoverIncomplete)

	// Pattern decay goroutine — runs daily, cleans stale low-quality patterns
	go func() {
		workerStarted := time.Now()
		logger.InfoContext(appCtx, "pattern decay worker started", "interval", 24*time.Hour)
		defer func() {
			logger.InfoContext(context.WithoutCancel(appCtx), "pattern decay worker stopped",
				"duration_ms", time.Since(workerStarted).Milliseconds(), "reason", appCtx.Err())
		}()
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				operationID := obs.NewLogID()
				cycleStarted := time.Now()
				logger.InfoContext(ctx, "pattern decay cycle started", "operation_id", operationID)
				installations, err := db.ListInstallations(ctx)
				if err != nil {
					logger.ErrorContext(ctx, "pattern decay cycle failed", "operation_id", operationID,
						"phase", "list_installations", "duration_ms", time.Since(cycleStarted).Milliseconds(), "error", err)
					cancel()
					continue
				}
				totalDeleted := 0
				failures := 0
				for _, inst := range installations {
					deleted, err := db.DecayStalePatterns(ctx, inst.ID, 90*24*time.Hour, 0.3)
					if err != nil {
						failures++
						logger.ErrorContext(ctx, "pattern decay", "operation_id", operationID, "installation", inst.ID, "error", err)
					} else {
						totalDeleted += deleted
						logger.InfoContext(ctx, "pattern decay installation completed", "operation_id", operationID,
							"installation", inst.ID, "deleted", deleted)
					}
				}
				logger.InfoContext(ctx, "pattern decay cycle completed", "operation_id", operationID,
					"installation_count", len(installations), "deleted", totalDeleted,
					"failure_count", failures, "duration_ms", time.Since(cycleStarted).Milliseconds())
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
		workerStarted := time.Now()
		logger.InfoContext(appCtx, "graph index backfill worker launched")
		defer func() {
			if r := recover(); r != nil {
				logger.ErrorContext(context.WithoutCancel(appCtx), "graph index backfill worker panic", "recover", r)
			}
			logger.InfoContext(context.WithoutCancel(appCtx), "graph index backfill worker stopped",
				"duration_ms", time.Since(workerStarted).Milliseconds(), "reason", appCtx.Err())
		}()
		runGraphIndexBackfill(appCtx, db, ghClient, logger)
	}()
	logger.InfoContext(appCtx, "background workers initialization completed")

	// JWT auth (Clerk or SuperTokens)
	if cfg.ClerkJWKSURL != "" {
		logger.InfoContext(ctx, "JWT authentication initialization started")
		api.InitJWKS(cfg.ClerkJWKSURL, logger)
		logger.InfoContext(ctx, "JWT authentication initialization completed")
	} else {
		logger.WarnContext(ctx, "JWT authentication initialization skipped", "reason", "Clerk JWKS URL not configured")
	}

	// API Server
	logger.InfoContext(ctx, "API server initialization started")
	server := api.NewServer(db, ghApp, orchestrator, replyAnalyzer, reactionAnalyzer, registry, eventBus, cfg, logger, memRegistry)
	defer func() {
		logger.Info("API server resources close started")
		server.Close()
		logger.Info("API server resources close completed")
	}()

	httpServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      server,
		ErrorLog:     log.New(obs.NewLogWriter(logger, slog.LevelError, "HTTP server error"), "", 0),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	logger.InfoContext(ctx, "API server initialization completed", "address", httpServer.Addr,
		"read_timeout", httpServer.ReadTimeout, "write_timeout", httpServer.WriteTimeout, "idle_timeout", httpServer.IdleTimeout)

	// Memory profiler — periodic RSS/heap samples + threshold-triggered
	// gzipped pprof heap dump. Tied to appCtx so it shuts down with the
	// pattern-decay goroutine.
	StartMemoryProfiler(appCtx, logger)

	// Graceful shutdown
	errCh := make(chan error, 1)
	go func() {
		started := time.Now()
		logger.Info("HTTP server listen started", "port", cfg.Port, "address", httpServer.Addr)
		err := httpServer.ListenAndServe()
		logger.Info("HTTP server listen stopped", "duration_ms", time.Since(started).Milliseconds(), "error", err)
		errCh <- err
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
			logger.Error("HTTP server stopped unexpectedly", "error", err)
			return fmt.Errorf("server error: %w", err)
		}
		logger.Info("HTTP server was already closed")
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	shutdownStarted := time.Now()
	logger.InfoContext(shutdownCtx, "graceful HTTP shutdown started", "timeout", 10*time.Second)
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.ErrorContext(shutdownCtx, "graceful HTTP shutdown failed",
			"duration_ms", time.Since(shutdownStarted).Milliseconds(), "error", err)
		return err
	}
	logger.Info("graceful HTTP shutdown completed", "duration_ms", time.Since(shutdownStarted).Milliseconds())
	return nil
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
