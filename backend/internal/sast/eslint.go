package sast

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// eslintFileResult is the top-level JSON structure from `eslint --format json`.
type eslintFileResult struct {
	FilePath string          `json:"filePath"`
	Messages []eslintMessage `json:"messages"`
}

type eslintMessage struct {
	RuleID   string `json:"ruleId"`
	Message  string `json:"message"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	Severity int    `json:"severity"` // 1=warning, 2=error
}

// ESLintRunner runs eslint on TypeScript/JavaScript source files.
type ESLintRunner struct{}

func (e *ESLintRunner) Name() string { return "eslint" }

func (e *ESLintRunner) CanRun(language string) bool {
	l := strings.ToLower(language)
	return l == "typescript" || l == "javascript"
}

// Run writes the provided files to a temp directory and invokes eslint.
// Returns empty findings (not an error) if eslint or node is not installed.
func (e *ESLintRunner) Run(ctx context.Context, files map[string]string) ([]Finding, error) {
	if _, err := exec.LookPath("node"); err != nil {
		return nil, nil
	}
	if _, err := exec.LookPath("eslint"); err != nil {
		return nil, nil
	}

	dir, err := os.MkdirTemp("", "sast-eslint-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	var targets []string
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
		targets = append(targets, fp)
	}

	if len(targets) == 0 {
		return nil, nil
	}

	args := []string{
		"--format", "json",
		"--no-eslintrc",
		"--no-config-lookup",
		"--rule", `{"require-await":"warn","no-async-promise-executor":"error"}`,
	}
	args = append(args, targets...)

	cmd := exec.CommandContext(ctx, "eslint", args...)
	cmd.Dir = dir

	out, runErr := cmd.Output()
	// eslint exits non-zero when findings exist.
	if runErr != nil {
		if _, ok := runErr.(*exec.ExitError); !ok {
			return nil, runErr
		}
	}

	var results []eslintFileResult
	if err := json.Unmarshal(out, &results); err != nil {
		return nil, fmt.Errorf("parsing eslint output: %w", err)
	}

	var findings []Finding
	for _, r := range results {
		rel, _ := filepath.Rel(dir, r.FilePath)
		for _, m := range r.Messages {
			sev := "warning"
			if m.Severity == 2 {
				sev = "error"
			}
			rule := m.RuleID
			if rule == "" {
				rule = "unknown"
			}
			findings = append(findings, Finding{
				File:     rel,
				Line:     m.Line,
				Column:   m.Column,
				Rule:     rule,
				Message:  m.Message,
				Severity: sev,
			})
		}
	}
	return findings, nil
}
