package memory

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestDocument_UnmarshalJSON_RetainsRaw pins the guarantee the phase-0 export
// archive depends on: the typed Document is a LOSSY view of Supermemory's
// response, so the original bytes must survive decoding. Without Raw, the
// archive would persist a re-marshal and silently drop precisely the SM-only
// fields it exists to preserve.
func TestDocument_UnmarshalJSON_RetainsRaw(t *testing.T) {
	t.Parallel()

	// Fields we do not model, including a non-string metadata value that the
	// typed map[string]string cannot represent at all.
	const wire = `{
		"id": "doc_1",
		"customId": "cid_1",
		"content": "hello",
		"summary": "a field we do not model",
		"containerTags": ["repo_x", "_shared"],
		"confidence": 0.8125,
		"reason": {"kind": "dismissal", "note": "SM-only extra"}
	}`

	var d Document
	if err := json.Unmarshal([]byte(wire), &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if d.ID != "doc_1" || d.CustomID != "cid_1" || d.Content != "hello" {
		t.Errorf("typed fields wrong: %+v", d)
	}
	if len(d.Raw) == 0 {
		t.Fatal("Raw is empty — the archive would fall back to a lossy re-marshal")
	}

	// Every unmodelled field must still be reachable through Raw.
	var got map[string]any
	if err := json.Unmarshal(d.Raw, &got); err != nil {
		t.Fatalf("Raw is not valid JSON: %v", err)
	}
	for _, key := range []string{"summary", "containerTags", "confidence", "reason"} {
		if _, ok := got[key]; !ok {
			t.Errorf("Raw lost unmodelled field %q", key)
		}
	}

	// And a re-marshal of the typed struct must NOT contain them — this is what
	// makes the assertion above meaningful rather than vacuous.
	remarshalled, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(remarshalled), "SM-only extra") {
		t.Error("re-marshal unexpectedly preserved an unmodelled field; " +
			"this test can no longer detect the loss it guards against")
	}
}
