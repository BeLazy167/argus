package sast

import (
	"context"
	"os/exec"
	"testing"
)

func TestESLintRunner_Integration(t *testing.T) {
	if _, err := exec.LookPath("eslint"); err != nil {
		t.Skip("eslint not installed")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
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
