package pipeline

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// SuppressedKeys are map keys on PipelineRun, and persistState marshals the run
// into the jsonb pipeline_states.payload column. jsonb cannot represent a NUL:
// Postgres rejects the whole upsert with SQLSTATE 22P05. Because that payload is
// how a run resumes, such a run can never persist and never recover -- recovery
// re-reads it, fails identically, and backs off for 30 minutes, forever.
//
// Six production runs stranded exactly that way between 2026-08-21 and
// 2026-09-02, reporting:
//
//	failed to recover run ... error="persisting state: upserting pipeline_states:
//	ERROR: unsupported Unicode escape sequence (SQLSTATE 22P05)"
//
// Both assertions fail if suppressionSeparator goes back to NUL.
func TestSuppressionKeyIsRepresentableInJSONB(t *testing.T) {
	key := suppressionKey("src/lib/acquisition/submit.ts", 45, "cookies retry indefinitely")
	if strings.ContainsRune(key, 0) {
		t.Fatal("suppression key contains NUL: jsonb rejects it with SQLSTATE 22P05, leaving the run unrecoverable")
	}

	// The marshalled shape is what reaches Postgres, so assert on that too. Go
	// encodes a NUL inside a map key as a six-character escape, and it is that
	// escape, not the raw byte, which jsonb refuses.
	payload, err := json.Marshal(map[string]struct{}{key: {}})
	if err != nil {
		t.Fatalf("marshal suppressed keys: %v", err)
	}
	if bytes.Contains(payload, []byte("\\u0000")) {
		t.Fatalf("marshalled SuppressedKeys carries a NUL escape that jsonb rejects: %s", payload)
	}
}

// The separator only works as a delimiter if it cannot appear in the fields it
// joins, which is why NUL was chosen originally. Keep that property explicit so
// a future move to a printable separator has to confront it.
func TestSuppressionKeySeparatorStaysUnambiguous(t *testing.T) {
	key := suppressionKey("a.go", 12, "body")
	if got := strings.Count(key, suppressionSeparator); got != 2 {
		t.Fatalf("separator count = %d, want 2 (path/line/body must stay distinguishable)", got)
	}
	if strings.ContainsAny(suppressionSeparator, "abcdefghijklmnopqrstuvwxyz0123456789/.-_ ") {
		t.Fatalf("separator %q is a character review bodies and file paths can contain", suppressionSeparator)
	}
}
