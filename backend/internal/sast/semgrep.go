package sast

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// semgrepOutput is the top-level JSON structure from `semgrep scan --json`.
type semgrepOutput struct {
	Results []semgrepResult `json:"results"`
}

type semgrepResult struct {
	CheckID string `json:"check_id"`
	Path    string `json:"path"`
	Start   struct {
		Line int `json:"line"`
		Col  int `json:"col"`
	} `json:"start"`
	Extra struct {
		Message  string `json:"message"`
		Severity string `json:"severity"` // ERROR, WARNING, INFO
	} `json:"extra"`
}

// SemgrepRunner runs semgrep on source files across 30+ languages.
type SemgrepRunner struct{}

func (s *SemgrepRunner) Name() string { return "semgrep" }

func (s *SemgrepRunner) CanRun(language string) bool {
	supported := map[string]bool{
		"go": true, "python": true, "java": true, "javascript": true,
		"typescript": true, "ruby": true, "rust": true, "c": true,
		"cpp": true, "csharp": true, "kotlin": true, "swift": true,
		"scala": true, "php": true, "dart": true,
	}
	return supported[language]
}

// Run writes files to a temp directory and invokes semgrep with auto-detected rules.
// Returns empty findings (not an error) if semgrep is not installed.
func (s *SemgrepRunner) Run(ctx context.Context, files map[string]string) ([]Finding, error) {
	if _, err := exec.LookPath("semgrep"); err != nil {
		return nil, nil
	}

	dir, err := os.MkdirTemp("", "sast-semgrep-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	for name, content := range files {
		fp := filepath.Join(dir, name)
		if !strings.HasPrefix(filepath.Clean(fp), filepath.Clean(dir)+string(os.PathSeparator)) {
			continue // skip path traversal attempts
		}
		if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(fp, []byte(content), 0o644); err != nil {
			return nil, err
		}
	}

	cmd := exec.CommandContext(ctx, "semgrep", "scan", "--config", "auto", "--json", "--quiet", dir)

	out, runErr := cmd.Output()
	if runErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) {
			return nil, runErr
		}
		// Semgrep exit 1 = findings exist (expected), other codes = real failure.
		if exitErr.ExitCode() == 1 && len(out) > 0 {
			// fall through to parse findings
		} else if exitErr.ExitCode() == 1 {
			return nil, fmt.Errorf("semgrep failed with no output: %w", runErr)
		} else {
			return nil, fmt.Errorf("semgrep error (exit %d): %w", exitErr.ExitCode(), runErr)
		}
	}

	if len(out) == 0 {
		return nil, nil
	}

	var parsed semgrepOutput
	if err := json.Unmarshal(out, &parsed); err != nil {
		return nil, err
	}

	var findings []Finding
	for _, r := range parsed.Results {
		rel, _ := filepath.Rel(dir, r.Path)
		findings = append(findings, Finding{
			File:     rel,
			Line:     r.Start.Line,
			Column:   r.Start.Col,
			Rule:     r.CheckID,
			Message:  r.Extra.Message,
			Severity: mapSemgrepSeverity(r.Extra.Severity),
		})
	}
	return findings, nil
}

func mapSemgrepSeverity(s string) string {
	switch strings.ToUpper(s) {
	case "ERROR":
		return "error"
	case "WARNING":
		return "warning"
	case "INFO":
		return "info"
	default:
		return "warning"
	}
}
