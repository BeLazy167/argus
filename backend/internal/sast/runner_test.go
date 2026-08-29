package sast

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"testing"
	"time"
)

type mockRunner struct {
	name     string
	langs    []string
	findings []Finding
	err      error
	delay    time.Duration
}

func (m *mockRunner) Name() string { return m.name }

func (m *mockRunner) CanRun(language string) bool {
	for _, l := range m.langs {
		if l == language {
			return true
		}
	}
	return false
}

func (m *mockRunner) Run(ctx context.Context, _ map[string]string) ([]Finding, error) {
	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return m.findings, m.err
}

func TestRunAll(t *testing.T) {
	tests := []struct {
		name     string
		runners  []Runner
		language string
		wantLen  int
	}{
		{
			name: "collects findings from multiple runners",
			runners: []Runner{
				&mockRunner{
					name:  "a",
					langs: []string{"go"},
					findings: []Finding{
						{File: "a.go", Line: 1, Rule: "R1", Message: "msg1", Severity: "error"},
					},
				},
				&mockRunner{
					name:  "b",
					langs: []string{"go"},
					findings: []Finding{
						{File: "b.go", Line: 2, Rule: "R2", Message: "msg2", Severity: "warning"},
					},
				},
			},
			language: "go",
			wantLen:  2,
		},
		{
			name: "filters by language",
			runners: []Runner{
				&mockRunner{name: "go-only", langs: []string{"go"}, findings: []Finding{{File: "a.go", Line: 1, Rule: "R1", Message: "m", Severity: "error"}}},
				&mockRunner{name: "ts-only", langs: []string{"typescript"}, findings: []Finding{{File: "b.ts", Line: 1, Rule: "R2", Message: "m", Severity: "error"}}},
			},
			language: "go",
			wantLen:  1,
		},
		{
			name: "no eligible runners",
			runners: []Runner{
				&mockRunner{name: "ts-only", langs: []string{"typescript"}},
			},
			language: "go",
			wantLen:  0,
		},
		{
			name: "erroring runner is skipped, others succeed",
			runners: []Runner{
				&mockRunner{name: "bad", langs: []string{"go"}, err: errors.New("boom")},
				&mockRunner{name: "good", langs: []string{"go"}, findings: []Finding{{File: "c.go", Line: 3, Rule: "R3", Message: "m", Severity: "info"}}},
			},
			language: "go",
			wantLen:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings, err := RunAll(context.Background(), tt.runners, tt.language, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(findings) != tt.wantLen {
				t.Errorf("got %d findings, want %d", len(findings), tt.wantLen)
			}
		})
	}
}

func TestRunAll_Timeout(t *testing.T) {
	slow := &mockRunner{
		name:  "slow",
		langs: []string{"go"},
		delay: 30 * time.Second,
		findings: []Finding{
			{File: "x.go", Line: 1, Rule: "R", Message: "never", Severity: "error"},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	findings, err := RunAll(ctx, []Runner{slow}, "go", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The slow runner should have been cancelled, returning no findings.
	if len(findings) != 0 {
		t.Errorf("expected 0 findings from timed-out runner, got %d", len(findings))
	}
}

func TestRunCommandDoesNotLogStdout(t *testing.T) {
	for _, tool := range []string{"eslint", "semgrep", "staticcheck"} {
		tool := tool
		t.Run(tool, func(t *testing.T) {
			var logs bytes.Buffer
			old := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(old) })

			const source = "PRIVATE_MATCHED_SOURCE_SECRET"
			cmd := exec.Command("sh", "-c", "printf %s \"$RUN_COMMAND_TEST_SOURCE\"")
			cmd.Env = append(cmd.Environ(), "RUN_COMMAND_TEST_SOURCE="+source)
			out, _, err := runCommand(context.Background(), tool, cmd)
			if err != nil {
				t.Fatalf("run command: %v", err)
			}
			if string(out) != source {
				t.Fatalf("stdout = %q, want %q", out, source)
			}
			if strings.Contains(logs.String(), source) {
				t.Fatalf("captured stdout leaked into logs: %s", logs.String())
			}
			if !strings.Contains(logs.String(), "stdout_bytes="+fmt.Sprint(len(source))) {
				t.Fatalf("stdout byte count missing from logs: %s", logs.String())
			}
		})
	}
}

func TestRunAllLogsFindingSummariesWithoutMessages(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	const source = "PRIVATE_FINDING_MESSAGE_SECRET"
	runner := &mockRunner{
		name:  "mock-sast",
		langs: []string{"go"},
		findings: []Finding{
			{File: "bad.go", Line: 1, Rule: "RULE-B", Message: source, Severity: "warning"},
			{File: "bad.go", Line: 2, Rule: "RULE-A", Message: "another private expression", Severity: "error"},
		},
	}

	findings, err := RunAll(context.Background(), []Runner{runner}, "go", nil)
	if err != nil || len(findings) != 2 {
		t.Fatalf("RunAll findings=%+v error=%v", findings, err)
	}
	got := logs.String()
	if strings.Contains(got, source) || strings.Contains(got, "another private expression") || strings.Contains(got, "bad.go") {
		t.Fatalf("finding source details leaked into logs: %s", got)
	}
	for _, want := range []string{"RULE-A", "RULE-B", "warning:1", "error:1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary %q missing from logs: %s", want, got)
		}
	}
}
