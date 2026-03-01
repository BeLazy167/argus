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

	// Memory / RAG
	var memClient *memory.Client
	var indexer *memory.Indexer
	if cfg.SupermemoryAPIKey != "" {
		memClient = memory.NewClient(cfg.SupermemoryAPIKey)
		indexer = memory.NewIndexer(memClient, logger)
	}

	// Pipeline
	triageStage := pipeline.NewTriageStage(registry, db)
	reviewStage := pipeline.NewReviewStage(registry, db, ghClient, memClient, cfg.MaxConcurrentReviews)
	scoringStage := pipeline.NewScoringStage(registry, db, memClient)
	orchestrator := pipeline.NewOrchestrator(db.Pool, db, ghClient, reviewStage, triageStage, scoringStage, indexer, registry, logger)
	replyAnalyzer := pipeline.NewReplyAnalyzer(registry, db, ghClient, indexer, logger)

	// Recover incomplete pipeline runs
	if err := orchestrator.RecoverIncomplete(ctx); err != nil {
		logger.Error("recovering incomplete pipelines", "error", err)
	}

	// JWT auth (Clerk or SuperTokens)
	if cfg.ClerkJWKSURL != "" {
		api.InitJWKS(cfg.ClerkJWKSURL, logger)
	}

	// API Server
	server := api.NewServer(db, ghApp, orchestrator, replyAnalyzer, indexer, registry, cfg.GitHubWebhookSecret, cfg.CORSAllowOrigin, logger)

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
