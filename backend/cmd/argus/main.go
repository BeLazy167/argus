package main

import (
	"log/slog"
	"os"
	"time"

	"github.com/BeLazy167/argus/backend/internal/app"
)

func main() {
	started := time.Now()
	slog.Info("argus command started")
	if err := app.Run(); err != nil {
		slog.Error("argus command failed", "duration_ms", time.Since(started).Milliseconds(), "error", err)
		os.Exit(1)
	}
	slog.Info("argus command completed", "duration_ms", time.Since(started).Milliseconds())
}
