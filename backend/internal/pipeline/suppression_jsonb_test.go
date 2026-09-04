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
	switch b := suppressionSeparator[0]; {
	case b == 0x00:
		t.Fatal("separator is NUL: jsonb rejects it with SQLSTATE 22P05 and the run becomes unrecoverable")
	case b >= 0x20:
		t.Fatalf("separator byte %#x is printable and can appear in a file path or review body", b)
	case b == 0x09 || b == 0x0a || b == 0x0d:
		// Caught by the gate on #287: the byte-class check alone admits tab, LF
		// and CR. Review bodies are multi-line markdown, so LF is the ordinary
		// case, and it collides exactly like a printable separator would:
		//   suppressionKey("a.go", 1, "b\n2\nc") == suppressionKey("a.go\n1\nb", 2, "c")
		t.Fatalf("separator byte %#x is whitespace that review bodies contain routinely", b)
	}

	// Assert the property directly against a body shaped like a real finding,
	// rather than trusting the byte class to imply it.
	body := "Guard the write.\n\n| file | line |\n| --- | --- |\n| a.go | 12 |\n\n\tindented\r\n"
	if strings.Contains(body, suppressionSeparator) {
		t.Fatalf("separator %q occurs in a representative review body, so keys are ambiguous", suppressionSeparator)
	}
}
