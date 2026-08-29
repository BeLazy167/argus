package sast

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// eslintFileResult is the top-level JSON structure from `eslint --format json`.
type eslintFileResult struct {
	FilePath string          `json:"filePath"`
	Messages []eslintMessage `json:"messages"`
}

type eslintMessage struct {
	RuleID   *string `json:"ruleId"`
	Message  string  `json:"message"`
	Line     int     `json:"line"`
	Column   int     `json:"column"`
	Severity int     `json:"severity"` // 1=warning, 2=error
}

const eslintFlatConfig = `import { createRequire } from "node:module";
const require = createRequire(import.meta.url);
const tsParser = require("@typescript-eslint/parser");
const rules = {
  "require-await": "warn",
  "no-async-promise-executor": "error",
};
export default [
  { files: ["**/*.{js,jsx,mjs,cjs}"], languageOptions: { parserOptions: { ecmaFeatures: { jsx: true } } }, rules },
  { files: ["**/*.{ts,tsx}"], languageOptions: { parser: tsParser, parserOptions: { ecmaFeatures: { jsx: true } } }, rules },
];
`

// ESLintRunner runs eslint on TypeScript/JavaScript source files.
type ESLintRunner struct{}

func (e *ESLintRunner) Name() string { return "eslint" }

func (e *ESLintRunner) CanRun(language string) bool {
	l := strings.ToLower(language)
	return l == "typescript" || l == "javascript"
}

// Run writes the provided files to a temp directory and invokes eslint.
// Returns empty findings (not an error) if eslint or node is not installed.
func (e *ESLintRunner) Run(ctx context.Context, files map[string]string) (findings []Finding, err error) {
	started := time.Now()
	defer func() {
		level := slog.LevelInfo
		if err != nil {
			level = slog.LevelError
		}
		slog.Log(ctx, level, "SAST tool run completed", "tool", "eslint", "file_count", len(files), "finding_count", len(findings), "duration_ms", time.Since(started).Milliseconds(), "error", err)
	}()
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
	// ESLint compares config and target paths literally when enforcing its base path.
	// Resolve symlinks (notably macOS /var -> /private/var) so both share one prefix.
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, err
	}

	var targets []string
	for name, content := range files {
		if !eslintSupportedFile(name) {
			continue
		}
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

	configPath := filepath.Join(dir, "eslint.config.mjs")
	if err := os.WriteFile(configPath, []byte(eslintFlatConfig), 0o644); err != nil {
		return nil, err
	}

	nodePath, err := globalNodeModulesPath(ctx)
	if err != nil {
		return nil, err
	}

	args := []string{
		"--format", "json",
		"--config", configPath,
		"--no-config-lookup",
	}
	args = append(args, targets...)

	cmd := exec.CommandContext(ctx, "eslint", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "NODE_PATH="+nodePath)

	out, stderr, runErr := runCommand(ctx, "eslint", cmd)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, fmt.Errorf("eslint interrupted: %w", ctxErr)
	}
	var exitErr *exec.ExitError
	if runErr != nil && !errors.As(runErr, &exitErr) {
		return nil, runErr
	}

	var results []eslintFileResult
	if err := json.Unmarshal(out, &results); err != nil {
		if exitErr != nil {
			return nil, fmt.Errorf("eslint failed (exit %d): %s: %w", exitErr.ExitCode(), strings.TrimSpace(string(stderr)), err)
		}
		return nil, fmt.Errorf("parsing eslint output: %w", err)
	}
	// ESLint exits 1 when findings exist; other non-zero codes are fatal errors.
	if exitErr != nil && exitErr.ExitCode() != 1 {
		return nil, fmt.Errorf("eslint failed (exit %d): %s", exitErr.ExitCode(), strings.TrimSpace(string(stderr)))
	}

	ignored := make(map[string]string)
	for _, r := range results {
		rel, _ := filepath.Rel(dir, r.FilePath)
		for _, m := range r.Messages {
			if m.RuleID == nil && strings.Contains(strings.ToLower(m.Message), "file ignored") {
				ignored[rel] = m.Message
				continue
			}
			sev := "warning"
			if m.Severity == 2 {
				sev = "error"
			}
			rule := "unknown"
			if m.RuleID != nil && *m.RuleID != "" {
				rule = *m.RuleID
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
	if len(ignored) > 0 {
		ignoredFiles := make([]string, 0, len(ignored))
		for file, message := range ignored {
			ignoredFiles = append(ignoredFiles, fmt.Sprintf("%s: %s", file, message))
		}
		sort.Strings(ignoredFiles)
		slog.WarnContext(ctx, "ESLint ignored configured files", "files", ignoredFiles, "ignored_count", len(ignoredFiles), "target_count", len(targets))
		if len(ignoredFiles) == len(targets) {
			return nil, fmt.Errorf("eslint ignored all configured targets: %s", strings.Join(ignoredFiles, "; "))
		}
	}
	return findings, nil
}

func eslintSupportedFile(name string) bool {
	switch filepath.Ext(name) {
	case ".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx":
		return true
	default:
		return false
	}
}

func globalNodeModulesPath(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "npm", "root", "-g")
	out, stderr, err := runCommand(ctx, "npm", cmd)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", fmt.Errorf("finding global node modules interrupted: %w", ctxErr)
	}
	if err != nil {
		return "", fmt.Errorf("finding global node modules: %s: %w", strings.TrimSpace(string(stderr)), err)
	}
	path := strings.TrimSpace(string(out))
	if path == "" {
		return "", errors.New("finding global node modules: npm returned an empty path")
	}
	return path, nil
}
