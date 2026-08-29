package sast

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestESLintRunner_Integration(t *testing.T) {
	if _, err := exec.LookPath("eslint"); err != nil {
		t.Skip("eslint not installed")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	if !globalESLintParserAvailable(t) {
		t.Skip("@typescript-eslint/parser not installed globally")
	}

	runner := &ESLintRunner{}

	files := map[string]string{
		"bad.js": `async function foo() { return 1; }
foo();
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
		if f.File == "bad.js" && f.Rule == "require-await" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected require-await finding in bad.js, got: %+v", findings)
	}
}

func TestESLintRunner_IntegrationTypeScript(t *testing.T) {
	if _, err := exec.LookPath("eslint"); err != nil {
		t.Skip("eslint not installed")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	if !globalESLintParserAvailable(t) {
		t.Skip("@typescript-eslint/parser not installed globally")
	}

	findings, err := (&ESLintRunner{}).Run(context.Background(), map[string]string{
		"bad.ts": "async function foo(): Promise<number> { return 1; }\nfoo();\n",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, finding := range findings {
		if finding.File == "bad.ts" && finding.Rule == "require-await" {
			return
		}
	}
	t.Fatalf("expected require-await finding in bad.ts, got: %+v", findings)
}

func TestESLintRunner_IntegrationAdditionalExtensions(t *testing.T) {
	if _, err := exec.LookPath("eslint"); err != nil {
		t.Skip("eslint not installed")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	if !globalESLintParserAvailable(t) {
		t.Skip("@typescript-eslint/parser not installed globally")
	}

	files := map[string]string{
		"component.tsx": "async function Component(): Promise<JSX.Element> { return <div />; }\n",
		"module.mjs":    "async function foo() { return 1; }\nfoo();\n",
	}
	findings, err := (&ESLintRunner{}).Run(context.Background(), files)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, name := range []string{"component.tsx", "module.mjs"} {
		found := false
		for _, finding := range findings {
			if finding.File == name && finding.Rule == "require-await" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected require-await finding in %s, got: %+v", name, findings)
		}
	}
}

func TestESLintRunner_UppercaseExtensionIsSkipped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses executable shell scripts")
	}

	binDir := t.TempDir()
	writeExecutable(t, filepath.Join(binDir, "node"), "#!/bin/sh\nexit 0\n")
	writeExecutable(t, filepath.Join(binDir, "eslint"), "#!/bin/sh\necho 'eslint should not run' >&2\nexit 2\n")
	t.Setenv("PATH", binDir)

	findings, err := (&ESLintRunner{}).Run(context.Background(), map[string]string{
		"bad.TS": "async function foo() { return 1; }",
	})
	if err != nil {
		t.Fatalf("uppercase unsupported extension must be skipped cleanly: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected no findings for skipped uppercase extension, got: %+v", findings)
	}
}

func TestESLintRunner_ParsesFindingsOnExitOne(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses executable shell scripts")
	}

	binDir := t.TempDir()
	writeExecutable(t, filepath.Join(binDir, "node"), "#!/bin/sh\nexit 0\n")
	writeExecutable(t, filepath.Join(binDir, "npm"), "#!/bin/sh\nprintf '/fake/global/node_modules\n'\n")
	writeExecutable(t, filepath.Join(binDir, "eslint"), `#!/bin/sh
for arg do
  if [ "$arg" = "--no-eslintrc" ]; then
    echo "Invalid option '--eslintrc'" >&2
    exit 2
  fi
  target="$arg"
done
printf '[{"filePath":"%s","messages":[{"ruleId":"require-await","message":"Async function has no await expression.","line":1,"column":1,"severity":1}]}]' "$target"
exit 1
`)
	t.Setenv("PATH", binDir)

	findings, err := (&ESLintRunner{}).Run(context.Background(), map[string]string{"bad.js": "async function foo() { return 1; }"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1: %+v", len(findings), findings)
	}
	if findings[0].File != "bad.js" || findings[0].Rule != "require-await" || findings[0].Severity != "warning" {
		t.Fatalf("unexpected finding: %+v", findings[0])
	}
}

func TestESLintRunner_MixedFilesOnlyScansSupportedTargets(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses executable shell scripts")
	}

	binDir := t.TempDir()
	writeExecutable(t, filepath.Join(binDir, "node"), "#!/bin/sh\nexit 0\n")
	writeExecutable(t, filepath.Join(binDir, "npm"), "#!/bin/sh\nprintf '/fake/global/node_modules\n'\n")
	writeExecutable(t, filepath.Join(binDir, "eslint"), `#!/bin/sh
for arg do
  case "$arg" in
    *.md) echo 'unsupported Markdown target was passed' >&2; exit 2 ;;
    *.ts) target="$arg" ;;
  esac
done
printf '[{"filePath":"%s","messages":[{"ruleId":"require-await","message":"Async function has no await expression.","line":1,"column":1,"severity":1}]}]' "$target"
exit 0
`)
	t.Setenv("PATH", binDir)

	findings, err := (&ESLintRunner{}).Run(context.Background(), map[string]string{
		"bad.ts":    "async function foo() { return 1; }",
		"README.md": "# Documentation",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 1 || findings[0].File != "bad.ts" || findings[0].Rule != "require-await" {
		t.Fatalf("unexpected findings: %+v", findings)
	}
}

func TestESLintRunner_IgnoredFileDiagnosticIsError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses executable shell scripts")
	}

	binDir := t.TempDir()
	writeExecutable(t, filepath.Join(binDir, "node"), "#!/bin/sh\nexit 0\n")
	writeExecutable(t, filepath.Join(binDir, "npm"), "#!/bin/sh\nprintf '/fake/global/node_modules\n'\n")
	writeExecutable(t, filepath.Join(binDir, "eslint"), `#!/bin/sh
for target do :; done
printf '[{"filePath":"%s","messages":[{"ruleId":null,"message":"File ignored because no matching configuration was supplied","line":0,"column":0,"severity":1}]}]' "$target"
exit 0
`)
	t.Setenv("PATH", binDir)

	findings, err := (&ESLintRunner{}).Run(context.Background(), map[string]string{"bad.ts": "const x: number = 1;"})
	if err == nil {
		t.Fatalf("expected ignored-file error, got findings: %+v", findings)
	}
	if !strings.Contains(err.Error(), "File ignored") || !strings.Contains(err.Error(), "bad.ts") {
		t.Fatalf("expected error to identify ignored file, got: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("ignored diagnostic must not become a finding: %+v", findings)
	}
}

func TestESLintRunner_ContextCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses executable shell scripts")
	}

	binDir := t.TempDir()
	writeExecutable(t, filepath.Join(binDir, "node"), "#!/bin/sh\nexit 0\n")
	writeExecutable(t, filepath.Join(binDir, "npm"), "#!/bin/sh\nprintf '/fake/global/node_modules\n'\n")
	writeExecutable(t, filepath.Join(binDir, "eslint"), "#!/bin/sh\nwhile :; do :; done\n")
	t.Setenv("PATH", binDir)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, err := (&ESLintRunner{}).Run(ctx, map[string]string{"bad.js": "const x = 1;"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline error, got: %v", err)
	}
}

func TestESLintRunner_FatalUsageErrorIncludesStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses executable shell scripts")
	}

	binDir := t.TempDir()
	writeExecutable(t, filepath.Join(binDir, "node"), "#!/bin/sh\nexit 0\n")
	writeExecutable(t, filepath.Join(binDir, "npm"), "#!/bin/sh\nprintf '/fake/global/node_modules\n'\n")
	writeExecutable(t, filepath.Join(binDir, "eslint"), "#!/bin/sh\necho \"Invalid option '--eslintrc'\" >&2\nexit 2\n")
	t.Setenv("PATH", binDir)

	_, err := (&ESLintRunner{}).Run(context.Background(), map[string]string{"bad.js": "const x = 1;"})
	if err == nil {
		t.Fatal("expected fatal eslint error")
	}
	if !strings.Contains(err.Error(), "Invalid option '--eslintrc'") {
		t.Fatalf("expected error to include eslint stderr, got: %v", err)
	}
}

func globalESLintParserAvailable(t *testing.T) bool {
	t.Helper()
	root, err := exec.Command("npm", "root", "-g").Output()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(strings.TrimSpace(string(root)), "@typescript-eslint", "parser"))
	return err == nil
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write executable: %v", err)
	}
}
