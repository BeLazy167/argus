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

	"github.com/BeLazy167/argus/internal/api"
	"github.com/BeLazy167/argus/internal/config"
	"github.com/BeLazy167/argus/internal/crypto"
	ghpkg "github.com/BeLazy167/argus/internal/github"
	"github.com/BeLazy167/argus/internal/llm"
	"github.com/BeLazy167/argus/internal/memory"
	"github.com/BeLazy167/argus/internal/pipeline"
	"github.com/BeLazy167/argus/internal/store"
)

// Run initializes all components and starts the server.
func Run() error {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

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

	// Memory / RAG (per-org via registry)
	memRegistry := memory.NewRegistry(db, logger)

	// Pipeline
	eventBus := pipeline.NewEventBus()
	triageStage := pipeline.NewTriageStage(registry, db, memRegistry)
	reviewStage := pipeline.NewReviewStage(registry, db, ghClient, memRegistry, cfg.MaxConcurrentReviews)
	scoringStage := pipeline.NewScoringStage(registry, db, memRegistry)
	orchestrator := pipeline.NewOrchestrator(db.Pool, db, ghClient, reviewStage, triageStage, scoringStage, memRegistry, registry, eventBus, logger, nil)
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
	orchestrator.SetTracker(server.EventTracker())

	httpServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      server,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

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
		logger.Info("received signal, shutting down", "signal", sig)
	case err := <-errCh:
		if err != http.ErrServerClosed {
			return fmt.Errorf("server error: %w", err)
		}
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	return httpServer.Shutdown(shutdownCtx)
}
