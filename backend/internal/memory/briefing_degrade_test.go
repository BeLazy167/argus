package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

// legStubTransport fails /v4/search requests whose JSON body matches failWhen
// and answers everything else with one high-score pattern result. It stubs the
// wire so the per-leg degradation in assembleBriefing/specialistBlock is
// exercised through the real Client → runSearch path, DB-less.
type legStubTransport struct {
	failWhen func(body string) bool
}

func (t *legStubTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body := ""
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		body = string(b)
	}
	if strings.Contains(req.URL.Path, "/v4/search") && t.failWhen != nil && t.failWhen(body) {
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Body:       io.NopCloser(bytes.NewBufferString(`{"error":"boom"}`)),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
		}, nil
	}
	resp := SearchResponse{Results: []SearchResult{{
		ID: "doc1", Similarity: 0.9, Memory: "pattern: check WHERE clauses",
		Metadata: json.RawMessage(`{"type":"pattern"}`),
	}}}
	out, _ := json.Marshal(resp)
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBuffer(out)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}, nil
}

func degradeTestIndexer(failWhen func(string) bool) *indexerImpl {
	c := &Client{apiKey: "test", client: &http.Client{Transport: &legStubTransport{failWhen: failWhen}}}
	return &indexerImpl{client: c, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

// An OPTIONAL leg failure (rules side-search) must not blank the briefing:
// the core block renders, the failed section is omitted, no error returned.
func TestAssembleBriefingOptionalLegFailureKeepsCore(t *testing.T) {
	idx := degradeTestIndexer(func(body string) bool {
		return strings.Contains(body, `"value":"rule"`)
	})
	b, err := idx.assembleBriefing(context.Background(), BriefingQuery{
		Repo: "acme/widgets", FilePath: "a.go", Query: "invoice rounding",
	})
	if err != nil {
		t.Fatalf("optional-leg failure must not error the briefing: %v", err)
	}
	if len(b.Rules) != 0 {
		t.Errorf("failed rules leg must render empty, got %v", b.Rules)
	}
	if len(b.Patterns) == 0 && len(b.PastReviews) == 0 && b.Synthesis == "" {
		t.Error("successful legs must survive an optional-leg failure")
	}
}

// When EVERY leg fails there is nothing usable — the briefing errors.
func TestAssembleBriefingAllLegsFailErrors(t *testing.T) {
	idx := degradeTestIndexer(func(string) bool { return true })
	_, err := idx.assembleBriefing(context.Background(), BriefingQuery{
		Repo: "acme/widgets", FilePath: "a.go", Query: "q",
	})
	if err == nil {
		t.Fatal("all-legs failure must return an error")
	}
}

// specialistBlock keeps surviving legs when one of its three legs fails.
func TestSpecialistBlockPartialLegFailureKeepsRest(t *testing.T) {
	idx := degradeTestIndexer(func(body string) bool {
		return strings.Contains(body, `"value":"synthesis"`)
	})
	block, err := idx.specialistBlock(context.Background(), "acme/widgets", "a.go", "q", NewThresholds())
	if err != nil {
		t.Fatalf("partial specialist-leg failure must not error: %v", err)
	}
	if len(block.Repo) == 0 && len(block.Shared) == 0 {
		t.Error("surviving specialist legs must be kept")
	}
}
