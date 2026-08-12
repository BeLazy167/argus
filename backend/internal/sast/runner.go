package sast

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os/exec"
	"sync"
	"time"

	"github.com/BeLazy167/argus/backend/internal/obs"
	"golang.org/x/sync/errgroup"
)

// Finding represents a single issue found by a SAST tool.
type Finding struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Column   int    `json:"column,omitempty"`
	Rule     string `json:"rule"`
	Message  string `json:"message"`
	Severity string `json:"severity"` // "error", "warning", "info"
}

// Runner is the interface that SAST tool integrations must implement.
type Runner interface {
	Name() string
	CanRun(language string) bool
	Run(ctx context.Context, files map[string]string) ([]Finding, error)
}

// DefaultRunners returns all available SAST runners.
func DefaultRunners() []Runner {
	runners := []Runner{
		&StaticcheckRunner{},
		&ESLintRunner{},
		&SemgrepRunner{},
	}
	slog.Info("SAST runners registered", "runner_count", len(runners))
	return runners
}

// runCommand is the single subprocess boundary for SAST integrations. It logs
// the exact invocation plus complete stdout/stderr in Fly-safe chunks.
func runCommand(ctx context.Context, tool string, cmd *exec.Cmd) ([]byte, error) {
	operationID := obs.NewLogID()
	started := time.Now()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	slog.InfoContext(ctx, "SAST subprocess started",
		"operation_id", operationID, "tool", tool, "command", cmd.Args, "working_directory", cmd.Dir)
	err := cmd.Run()
	obs.LogPayload(ctx, slog.Default(), "SAST subprocess stdout", operationID, "stdout", "text/plain", stdout.Bytes())
	obs.LogPayload(ctx, slog.Default(), "SAST subprocess stderr", operationID, "stderr", "text/plain", stderr.Bytes())
	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	slog.InfoContext(ctx, "SAST subprocess completed",
		"operation_id", operationID, "tool", tool, "exit_code", exitCode,
		"duration_ms", time.Since(started).Milliseconds(), "stdout_bytes", stdout.Len(),
		"stderr_bytes", stderr.Len(), "error", err)
	return stdout.Bytes(), err
}

// RunAll executes all eligible runners in parallel, collecting their findings.
// Each runner gets a 15-second timeout. Runners that error are skipped;
// findings from successful runners are still returned.
func RunAll(ctx context.Context, runners []Runner, language string, files map[string]string) (all []Finding, err error) {
	started := time.Now()
	defer func() {
		level := slog.LevelInfo
		if err != nil {
			level = slog.LevelError
		}
		slog.Log(ctx, level, "SAST run-all completed", "language", language, "runner_count", len(runners), "file_count", len(files), "finding_count", len(all), "duration_ms", time.Since(started).Milliseconds(), "error", err)
	}()
	var eligible []Runner
	for _, r := range runners {
		if r.CanRun(language) {
			eligible = append(eligible, r)
		}
	}
	if len(eligible) == 0 {
		slog.InfoContext(ctx, "SAST run-all skipped", "language", language, "reason", "no_eligible_runners", "file_count", len(files))
		return nil, nil
	}
	slog.InfoContext(ctx, "SAST runners selected", "language", language, "eligible_count", len(eligible), "runner_count", len(runners))

	var mu sync.Mutex

	g, gctx := errgroup.WithContext(ctx)
	for _, r := range eligible {
		g.Go(func() error {
			operationID := obs.NewLogID()
			started := time.Now()
			rctx, cancel := context.WithTimeout(gctx, 15*time.Second)
			defer cancel()
			slog.InfoContext(rctx, "SAST runner started", "operation_id", operationID,
				"tool", r.Name(), "language", language, "file_count", len(files))

			findings, err := r.Run(rctx, files)
			if err != nil {
				slog.ErrorContext(rctx, "SAST runner failed", "operation_id", operationID,
					"tool", r.Name(), "language", language,
					"duration_ms", time.Since(started).Milliseconds(), "error", err)
				// Graceful degradation: skip failed runners.
				return nil
			}
			if payload, marshalErr := json.Marshal(findings); marshalErr == nil {
				obs.LogPayload(rctx, slog.Default(), "SAST findings", operationID, "result", "application/json", payload)
			} else {
				slog.ErrorContext(rctx, "SAST findings serialization failed", "operation_id", operationID, "tool", r.Name(), "error", marshalErr)
			}
			slog.InfoContext(rctx, "SAST runner completed", "operation_id", operationID,
				"tool", r.Name(), "language", language, "finding_count", len(findings),
				"duration_ms", time.Since(started).Milliseconds())
			mu.Lock()
			all = append(all, findings...)
			mu.Unlock()
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return all, err
	}
	return all, nil
}
