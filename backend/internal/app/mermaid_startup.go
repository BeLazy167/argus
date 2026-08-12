package app

import (
	"log/slog"

	"github.com/BeLazy167/argus/backend/internal/config"
)

func logMermaidValidatorStatus(logger *slog.Logger, cfg *config.Config) {
	if cfg.MermaidValidatorEnabled() {
		logger.Info("mermaid validator enabled", "status", "enabled")
		return
	}
	logger.Warn(
		"mermaid validator disabled; PR diagrams will not be generated",
		"status", "disabled",
		"reason", "MERMAID_VALIDATOR_BASE_URL and MERMAID_VALIDATOR_SECRET are unset",
	)
}
