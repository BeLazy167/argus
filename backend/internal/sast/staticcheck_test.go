package sast

import (
	"bytes"
	"context"
	"log/slog"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestStaticcheckRunner_Integration(t *testing.T) {
	if _, err := exec.LookPath("staticcheck"); err != nil {
		t.Skip("staticcheck not installed")
	}

	runner := &StaticcheckRunner{}

	files := map[string]string{
		"bad.go": `package main

import "fmt"

func main() {
	fmt.Sprintf("%d", "string")
}
`,
	}

	findings, err := runner.Run(context.Background(), files)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) == 0 {
		t.Fatal("expected at least one finding")
	}

	found := false
	for _, f := range findings {
		if f.File == "bad.go" && f.Rule != "" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected finding in bad.go, got: %+v", findings)
	}
}

func TestStaticcheckRunner_EmptyOutputExitErrorIncludesStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses executable shell scripts")
	}
	binDir := t.TempDir()
	writeExecutable(t, filepath.Join(binDir, "staticcheck"), "#!/bin/sh\necho 'staticcheck crashed' >&2\nexit 2\n")
	t.Setenv("PATH", binDir)

	_, err := (&StaticcheckRunner{}).Run(context.Background(), map[string]string{"bad.go": "package main"})
	if err == nil || !strings.Contains(err.Error(), "staticcheck crashed") {
		t.Fatalf("expected stderr in empty-output error, got: %v", err)
	}
}

func TestStaticcheckRunner_DoesNotLogSourceExpressions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses executable shell scripts")
	}
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	const source = "PRIVATE_SOURCE_EXPRESSION_SECRET"
	binDir := t.TempDir()
	writeExecutable(t, filepath.Join(binDir, "staticcheck"), `#!/bin/sh
printf '%s\n' '{"code":"SA9999","message":"PRIVATE_SOURCE_EXPRESSION_SECRET","severity":"warning","location":{"file":"bad.go","line":1,"column":1}}'
exit 1
`)
	t.Setenv("PATH", binDir)

	findings, err := (&StaticcheckRunner{}).Run(context.Background(), map[string]string{"bad.go": "package main"})
	if err != nil || len(findings) != 1 {
		t.Fatalf("findings=%+v error=%v", findings, err)
	}
	if strings.Contains(logs.String(), source) {
		t.Fatalf("source expression leaked into logs: %s", logs.String())
	}
}

func TestStaticcheckRunner_NonzeroRequiresValidOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses executable shell scripts")
	}
	for _, stdout := range []string{"\n", "not-json\n", "{}\n"} {
		stdout := stdout
		t.Run(strings.TrimSpace(stdout), func(t *testing.T) {
			binDir := t.TempDir()
			script := "#!/bin/sh\nprintf '%s' " + shellQuote(stdout) + "\necho 'staticcheck fatal' >&2\nexit 2\n"
			writeExecutable(t, filepath.Join(binDir, "staticcheck"), script)
			t.Setenv("PATH", binDir)

			findings, err := (&StaticcheckRunner{}).Run(context.Background(), map[string]string{"bad.go": "package main"})
			if err == nil || !strings.Contains(err.Error(), "staticcheck fatal") {
				t.Fatalf("expected fatal error, got findings=%+v error=%v", findings, err)
			}
		})
	}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}
