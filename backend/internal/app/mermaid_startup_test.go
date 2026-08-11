package app

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/BeLazy167/argus/backend/internal/config"
)

func TestLogMermaidValidatorStatusWarnsWhenIntentionallyDisabled(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))

	logMermaidValidatorStatus(logger, &config.Config{})

	got := logs.String()
	for _, want := range []string{
		`"level":"WARN"`,
		`"msg":"mermaid validator disabled; PR diagrams will not be generated"`,
		`"status":"disabled"`,
		"MERMAID_VALIDATOR_BASE_URL and MERMAID_VALIDATOR_SECRET are unset",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("startup log %q missing %q", got, want)
		}
	}
}

func TestLogMermaidValidatorStatusDoesNotLogCredentialsOrOrigin(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	cfg := &config.Config{
		MermaidValidatorBaseURL: "https://private-validator.example.test",
		MermaidValidatorSecret:  "super-secret",
	}

	logMermaidValidatorStatus(logger, cfg)

	got := logs.String()
	if !strings.Contains(got, `"msg":"mermaid validator enabled"`) ||
		!strings.Contains(got, `"status":"enabled"`) {
		t.Fatalf("startup log = %q, want enabled status", got)
	}
	for _, sensitive := range []string{cfg.MermaidValidatorBaseURL, cfg.MermaidValidatorSecret} {
		if strings.Contains(got, sensitive) {
			t.Fatalf("startup log exposed validator configuration: %q", got)
		}
	}
}
