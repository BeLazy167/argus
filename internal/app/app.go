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

	"github.com/acmeorg/argus/internal/api"
	"github.com/acmeorg/argus/internal/config"
	ghpkg "github.com/acmeorg/argus/internal/github"
	"github.com/acmeorg/argus/internal/llm"
	"github.com/acmeorg/argus/internal/memory"
	"github.com/acmeorg/argus/internal/pipeline"
	"github.com/acmeorg/argus/internal/rules"
	"github.com/acmeorg/argus/internal/store"
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

	// LLM (single OpenAI-compatible provider)
	registry := llm.NewRegistry()
	if cfg.LLMAPIKey != "" {
		registry.RegisterProvider("default", llm.NewChatProvider("default", cfg.LLMAPIKey, cfg.LLMBaseURL))
	}
	registry.SetDefault(llm.StageReview, llm.ModelConfig{
		Provider:    "default",
		Model:       cfg.DefaultReviewModel,
		MaxTokens:   4096,
		Temperature: 0.2,
	})
	registry.SetDefault(llm.StageTriage, llm.ModelConfig{
		Provider:    "default",
		Model:       cfg.DefaultTriageModel,
		MaxTokens:   2048,
		Temperature: 0.1,
	})

	// Memory / RAG
	var indexer *memory.Indexer
	if cfg.SupermemoryAPIKey != "" {
		memClient := memory.NewClient(cfg.SupermemoryAPIKey)
		indexer = memory.NewIndexer(memClient, logger)
	}

	// Rules engine
	rulesEngine := rules.NewEngine(db.Pool, ghClient, logger)

	// Pipeline
	triageStage := pipeline.NewTriageStage(registry, db)
	reviewStage := pipeline.NewReviewStage(registry, db, cfg.MaxConcurrentReviews)
	var contextStage *pipeline.ContextStage
	if indexer != nil {
		contextStage = pipeline.NewContextStage(indexer, rulesEngine, logger)
	}
	orchestrator := pipeline.NewOrchestrator(db.Pool, db, ghClient, reviewStage, triageStage, contextStage, indexer, logger)

	// Recover incomplete pipeline runs
	if err := orchestrator.RecoverIncomplete(ctx); err != nil {
		logger.Error("recovering incomplete pipelines", "error", err)
	}

	// JWT auth (Clerk or SuperTokens)
	if cfg.ClerkJWKSURL != "" {
		api.InitJWKS(cfg.ClerkJWKSURL, logger)
	}

	// API Server
	server := api.NewServer(db, ghApp, orchestrator, cfg.GitHubWebhookSecret, cfg.CORSAllowOrigin, logger)

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
