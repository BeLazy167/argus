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
// joins, which is why NUL was chosen originally.
//
// Asserted as a character-class property, not a blacklist. An earlier version
// of this test scanned `strings.ContainsAny(sep, "abc...0123/.-_ ")`, which let
// through every uppercase letter and every punctuation mark outside that short
// list. Swapping the separator to "|" or "A" kept it green even though both
// appear constantly in review bodies -- "|" in every markdown table -- and
// either makes suppressionKey ambiguous: with "|",
//
//	suppressionKey("a", 1, "b|2|c") == suppressionKey("a|1|b", 2, "c") == "a|1|b|2|c"
//
// so one dismissal suppresses an unrelated live finding and pattern-learning
// silently skips it.
//
// A single C0 control byte other than NUL is the property that actually holds:
// no file path or LLM-authored review body carries one, and jsonb represents it.
func TestSuppressionKeySeparatorStaysUnambiguous(t *testing.T) {
	key := suppressionKey("a.go", 12, "body")
	if got := strings.Count(key, suppressionSeparator); got != 2 {
		t.Fatalf("separator count = %d, want 2 (path/line/body must stay distinguishable)", got)
	}
	if len(suppressionSeparator) != 1 {
		t.Fatalf("separator %q is %d bytes, want a single control byte", suppressionSeparator, len(suppressionSeparator))
	}
	if b := suppressionSeparator[0]; b == 0x00 || b >= 0x20 {
		t.Fatalf("separator byte %#x must be a C0 control other than NUL: NUL breaks jsonb (SQLSTATE 22P05), and anything >= 0x20 is printable and can appear in a path or review body", b)
	}
}
