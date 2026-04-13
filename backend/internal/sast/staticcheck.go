package sast

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// staticcheckOutput is the JSON structure emitted by `staticcheck -f json`.
type staticcheckOutput struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	Severity string `json:"severity"`
	Location struct {
		File   string `json:"file"`
		Line   int    `json:"line"`
		Column int    `json:"column"`
	} `json:"location"`
}

// StaticcheckRunner runs staticcheck on Go source files.
type StaticcheckRunner struct{}

func (s *StaticcheckRunner) Name() string { return "staticcheck" }

func (s *StaticcheckRunner) CanRun(language string) bool {
	return strings.EqualFold(language, "go")
}

// Run writes the provided Go files to a temp directory and invokes staticcheck.
// Returns empty findings (not an error) if the staticcheck binary is not installed.
func (s *StaticcheckRunner) Run(ctx context.Context, files map[string]string) ([]Finding, error) {
	if _, err := exec.LookPath("staticcheck"); err != nil {
		return nil, nil
	}

	dir, err := os.MkdirTemp("", "sast-staticcheck-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	// Write source files.
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

	// Write minimal go.mod.
	gomod := "module tmpcheck\n\ngo 1.24\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, "staticcheck", "-f", "json", "./...")
	cmd.Dir = dir

	out, runErr := cmd.Output()
	// staticcheck exits non-zero when findings exist — that's expected.
	if runErr != nil {
		if _, ok := runErr.(*exec.ExitError); !ok {
			return nil, runErr
		}
	}

	var findings []Finding
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		var sc staticcheckOutput
		if err := json.Unmarshal([]byte(line), &sc); err != nil {
			continue
		}
		// Make path relative to temp dir so callers see the original file name.
		rel, _ := filepath.Rel(dir, sc.Location.File)
		sev := sc.Severity
		if sev == "" {
			sev = "warning"
		}
		findings = append(findings, Finding{
			File:     rel,
			Line:     sc.Location.Line,
			Column:   sc.Location.Column,
			Rule:     sc.Code,
			Message:  sc.Message,
			Severity: sev,
		})
	}
	return findings, nil
}
