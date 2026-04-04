package sast

import (
	"context"
	"os/exec"
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
