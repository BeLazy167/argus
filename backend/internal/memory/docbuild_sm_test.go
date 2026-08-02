package memory

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// TestSMTransportCarriesDocFields is the Supermemory-leg parity test the gate
// demanded: the SM adapters (addRequestFor, the batch conversion) must carry
// every Doc field to the wire. A broken mapping — dropped metadata, wrong
// container tag — passes build/vet and every other test, silently corrupting
// SM writes and the PR-6 shadow comparison with them.
func TestSMTransportCarriesDocFields(t *testing.T) {
	type captured struct {
		path string
		body []byte
	}
	var got []captured
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, r.ContentLength)
		if _, err := r.Body.Read(body); err != nil && err.Error() != "EOF" {
			t.Errorf("read body: %v", err)
		}
		got = append(got, captured{path: r.URL.Path, body: body})
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v3/documents/batch" {
			_, _ = w.Write([]byte(`{"results":[{"id":"b1"}],"success":1,"failed":0}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"d1","status":"ok"}`))
	}))
	defer srv.Close()

	idx := NewIndexer(NewClient("test-key", WithBaseURL(srv.URL)), slog.New(slog.DiscardHandler))
	ctx := t.Context()

	rule := RuleMemory{RuleID: 42, Category: "style", Content: "no dynamic imports", Priority: 2}
	if err := idx.IndexRule(ctx, "acme", rule); err != nil {
		t.Fatalf("IndexRule: %v", err)
	}
	pat := PatternMemory{Content: "C", Source: "synthesis", FilePath: "a.go"}
	if _, err := idx.IndexPattern(ctx, "api", pat); err != nil {
		t.Fatalf("IndexPattern: %v", err)
	}
	comments := []ReviewMemory{{ReviewID: "r1", FilePath: "a.go", Body: "nil deref", Severity: "warning", Category: "bug_risk", PRNumber: 7}}
	if err := idx.IndexReviewCommentsBatch(ctx, "acme", "api", comments); err != nil {
		t.Fatalf("IndexReviewCommentsBatch: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("captured %d requests, want 3", len(got))
	}

	// Single-write leg: wire AddRequest == builder Doc, field for field.
	assertAdd := func(t *testing.T, body []byte, want Doc) {
		t.Helper()
		var req AddRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if req.Content != want.Content || req.CustomID != want.CustomID {
			t.Errorf("content/customId drift: got (%q,%q) want (%q,%q)", req.Content, req.CustomID, want.Content, want.CustomID)
		}
		if !reflect.DeepEqual(req.ContainerTags, []string{want.ContainerTag}) {
			t.Errorf("containerTags = %v, want [%s]", req.ContainerTags, want.ContainerTag)
		}
		if !reflect.DeepEqual(req.Metadata, want.Metadata) {
			t.Errorf("metadata drift: got %v want %v", req.Metadata, want.Metadata)
		}
	}
	ruleDoc, err := buildRuleDoc(rule)
	if err != nil {
		t.Fatal(err)
	}
	assertAdd(t, got[0].body, ruleDoc)
	patDoc, err := buildPatternDoc("api", pat)
	if err != nil {
		t.Fatal(err)
	}
	assertAdd(t, got[1].body, patDoc)

	// Batch leg: container tag from the builder,every document carries all fields.
	var batch BatchAddRequest
	if err := json.Unmarshal(got[2].body, &batch); err != nil {
		t.Fatalf("unmarshal batch: %v", err)
	}
	shaped, skipped := buildReviewDocs("acme", "api", comments, slog.New(slog.DiscardHandler))
	if skipped != 0 || len(shaped) != 1 {
		t.Fatalf("shaped=%d skipped=%d", len(shaped), skipped)
	}
	if batch.ContainerTag != shaped[0].ContainerTag {
		t.Errorf("batch containerTag = %q, want %q", batch.ContainerTag, shaped[0].ContainerTag)
	}
	wantBatch := []BatchDocument{{Content: shaped[0].Content, CustomID: shaped[0].CustomID, Metadata: shaped[0].Metadata}}
	if !reflect.DeepEqual(batch.Documents, wantBatch) {
		t.Errorf("batch documents drift: got %+v want %+v", batch.Documents, wantBatch)
	}
}
