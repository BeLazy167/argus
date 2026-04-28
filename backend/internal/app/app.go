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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Database
	db, err := store.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connecting to database: %w", err)
	}
	defer db.Close()

	// GitHub App
	ghApp := ghpkg.NewApp(cfg.GitHubAppID, cfg.GitHubPrivateKey)
	ghClient := ghpkg.NewClient(ghApp)

	// Encryption (optional — only required if BYOK keys are used)
	if cfg.EncryptionKey != "" {
		if err := crypto.Init(cfg.EncryptionKey); err != nil {
			return fmt.Errorf("initializing encryption: %w", err)
		}
	}

	// LLM (BYOK only — keys resolved from DB)
	registry := llm.NewRegistry()
	registry.SetResolver(db)

	// Pricing (DB-backed, cached 10min)
	pricingCache := store.NewPricingCache(db)
	llm.SetPricingLookup(func(model string) (float64, float64, bool) {
		return pricingCache.Lookup(ctx, model)
	})

	// Memory / RAG (per-org via registry)
	memRegistry := memory.NewRegistry(db, logger)

	// Pipeline
	eventBus := pipeline.NewEventBus()
	triageStage := pipeline.NewTriageStage(registry, db, memRegistry)
	reviewStage := pipeline.NewReviewStage(registry, db, ghClient, memRegistry, cfg.MaxConcurrentReviews)
	intentStage := pipeline.NewIntentExtractionStage(registry, db, ghClient, logger)
	scoringStage := pipeline.NewScoringStage(registry, db, memRegistry)
	orchestrator := pipeline.NewOrchestrator(db.Pool, db, ghClient, reviewStage, triageStage, intentStage, scoringStage, memRegistry, registry, eventBus, logger)
	replyAnalyzer := pipeline.NewReplyAnalyzer(registry, db, ghClient, memRegistry, logger)
	reactionAnalyzer := pipeline.NewReactionAnalyzer(db, ghClient, memRegistry, logger)

	// Mark stale reviews as failed before resuming incomplete pipelines
	if count, err := db.RecoverStaleReviews(ctx, 10*time.Minute); err != nil {
		logger.Warn("failed to recover stale reviews", "error", err)
	} else if count > 0 {
		logger.Info("recovered stale reviews", "count", count)
	}

	// Recover incomplete pipeline runs (async — don't block server startup)
	appCtx, appCancel := context.WithCancel(context.Background())
	defer appCancel()
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

	// JWT auth (Clerk or SuperTokens)
	if cfg.ClerkJWKSURL != "" {
		api.InitJWKS(cfg.ClerkJWKSURL, logger)
	}

	// API Server
	server := api.NewServer(db, ghApp, orchestrator, replyAnalyzer, reactionAnalyzer, registry, eventBus, cfg.GitHubWebhookSecret, cfg.CORSAllowOrigin, logger, memRegistry)
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
		// TODO(agent5): pendingCount = # in-flight reviews. No easy accessor
		// exposed on Server today, so we emit 0. Agent 5 can wire this from
		// the orchestrator's run registry when they add pipeline.panic_recovered
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
