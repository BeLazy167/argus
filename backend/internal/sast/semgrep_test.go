package sast

import (
	"context"
	"encoding/json"
	"os/exec"
	"testing"
)

func TestSemgrepRunner_CanRun(t *testing.T) {
	runner := &SemgrepRunner{}
	tests := []struct {
		lang string
		want bool
	}{
		{"go", true},
		{"python", true},
		{"java", true},
		{"javascript", true},
		{"typescript", true},
		{"ruby", true},
		{"rust", true},
		{"c", true},
		{"cpp", true},
		{"csharp", true},
		{"kotlin", true},
		{"swift", true},
		{"scala", true},
		{"php", true},
		{"dart", true},
		{"haskell", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.lang, func(t *testing.T) {
			if got := runner.CanRun(tt.lang); got != tt.want {
				t.Errorf("CanRun(%q) = %v, want %v", tt.lang, got, tt.want)
			}
		})
	}
}

func TestSemgrepRunner_ParseOutput(t *testing.T) {
	tests := []struct {
		name     string
		json     string
		wantLen  int
		wantRule string
		wantSev  string
	}{
		{
			name: "single finding",
			json: `{
				"results": [{
					"check_id": "python.lang.security.audit.dangerous-system-call",
					"path": "app.py",
					"start": {"line": 10, "col": 5},
					"end": {"line": 10, "col": 30},
					"extra": {
						"message": "Detected dangerous system call",
						"severity": "WARNING"
					}
				}]
			}`,
			wantLen:  1,
			wantRule: "python.lang.security.audit.dangerous-system-call",
			wantSev:  "warning",
		},
		{
			name: "multiple findings",
			json: `{
				"results": [
					{
						"check_id": "rule1",
						"path": "a.py",
						"start": {"line": 1, "col": 1},
						"extra": {"message": "m1", "severity": "ERROR"}
					},
					{
						"check_id": "rule2",
						"path": "b.py",
						"start": {"line": 2, "col": 3},
						"extra": {"message": "m2", "severity": "INFO"}
					}
				]
			}`,
			wantLen:  2,
			wantRule: "rule1",
			wantSev:  "error",
		},
		{
			name:    "empty results",
			json:    `{"results": []}`,
			wantLen: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var parsed semgrepOutput
			if err := json.Unmarshal([]byte(tt.json), &parsed); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}

			var findings []Finding
			for _, r := range parsed.Results {
				findings = append(findings, Finding{
					File:     r.Path,
					Line:     r.Start.Line,
					Column:   r.Start.Col,
					Rule:     r.CheckID,
					Message:  r.Extra.Message,
					Severity: mapSemgrepSeverity(r.Extra.Severity),
				})
			}

			if len(findings) != tt.wantLen {
				t.Fatalf("got %d findings, want %d", len(findings), tt.wantLen)
			}
			if tt.wantLen > 0 {
				if findings[0].Rule != tt.wantRule {
					t.Errorf("rule = %q, want %q", findings[0].Rule, tt.wantRule)
				}
				if findings[0].Severity != tt.wantSev {
					t.Errorf("severity = %q, want %q", findings[0].Severity, tt.wantSev)
				}
			}
		})
	}
}

func TestMapSemgrepSeverity(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"ERROR", "error"},
		{"WARNING", "warning"},
		{"INFO", "info"},
		{"error", "error"},
		{"warning", "warning"},
		{"info", "info"},
		{"", "warning"},
		{"UNKNOWN", "warning"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := mapSemgrepSeverity(tt.input); got != tt.want {
				t.Errorf("mapSemgrepSeverity(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSemgrepRunner_GracefulDegradation(t *testing.T) {
	// If semgrep is not installed, Run should return nil, nil
	if _, err := exec.LookPath("semgrep"); err == nil {
		t.Skip("semgrep is installed, cannot test graceful degradation")
	}
	runner := &SemgrepRunner{}
	findings, err := runner.Run(context.Background(), map[string]string{"test.py": "x = 1"})
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if findings != nil {
		t.Fatalf("expected nil findings, got: %v", findings)
	}
}

func TestSemgrepRunner_IntegrationPython(t *testing.T) {
	if _, err := exec.LookPath("semgrep"); err != nil {
		t.Skip("semgrep not installed")
	}

	runner := &SemgrepRunner{}
	// Intentionally vulnerable code for SAST testing — uses os.system with user input
	files := map[string]string{
		"bad.py": "import os\nuser_input = input(\"cmd: \")\nos.system(user_input)\n",
	}

	findings, err := runner.Run(context.Background(), files)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) == 0 {
		t.Fatal("expected at least one finding for os.system(user_input)")
	}

	found := false
	for _, f := range findings {
		if f.File == "bad.py" && f.Rule != "" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected finding in bad.py, got: %+v", findings)
	}
}

func TestSemgrepRunner_IntegrationJava(t *testing.T) {
	if _, err := exec.LookPath("semgrep"); err != nil {
		t.Skip("semgrep not installed")
	}

	runner := &SemgrepRunner{}
	files := map[string]string{
		"Bad.java": "import java.sql.*;\npublic class Bad {\n    public void query(Connection conn, String userInput) throws Exception {\n        Statement stmt = conn.createStatement();\n        stmt.execute(\"SELECT * FROM users WHERE id = '\" + userInput + \"'\");\n    }\n}\n",
	}

	findings, err := runner.Run(context.Background(), files)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) == 0 {
		t.Fatal("expected at least one finding for SQL concatenation")
	}
}
