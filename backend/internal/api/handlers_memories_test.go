package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BeLazy167/argus/backend/internal/store"
)

type fakeMemoryLister struct {
	repo   *store.Repo
	result store.MemoryListResult
	filter store.MemoryListFilter
	err    error
}

func (f *fakeMemoryLister) GetRepoScoped(_ context.Context, _ int64, _ []int64) (*store.Repo, error) {
	return f.repo, f.err
}
func (f *fakeMemoryLister) ListMemories(_ context.Context, filter store.MemoryListFilter) (store.MemoryListResult, error) {
	f.filter = filter
	return f.result, f.err
}

func TestListMemoriesRequiresAuthorizedInstallation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		query  string
		ids    []int64
		status int
	}{
		{"missing", "", []int64{7}, http.StatusBadRequest},
		{"malformed", "?installation_id=nope", []int64{7}, http.StatusBadRequest},
		{"not authorized", "?installation_id=8", []int64{7}, http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := &Server{memoryLister: &fakeMemoryLister{}, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
			req := httptest.NewRequest(http.MethodGet, "/api/v1/memories"+tt.query, nil)
			req = req.WithContext(context.WithValue(req.Context(), installationIDsKey, tt.ids))
			rec := httptest.NewRecorder()
			s.listMemories(rec, req)
			if rec.Code != tt.status {
				t.Fatalf("status=%d want=%d body=%s", rec.Code, tt.status, rec.Body.String())
			}
		})
	}
}

func TestListMemoriesBuildsTenantSafeFilterAndEnvelope(t *testing.T) {
	t.Parallel()
	fake := &fakeMemoryLister{
		repo:   &store.Repo{ID: 9, InstallationID: 7, FullName: "acme/sdk.js"},
		result: store.MemoryListResult{Memories: []store.MemoryAPI{{ID: 12, InstallationID: 7, ContainerTag: "sdk-js-abcdef", Type: "pattern", Content: "retry writes"}}, Total: 1},
	}
	s := &Server{memoryLister: fake, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/memories?installation_id=7&repo_id=9&scope=repo&type=pattern&q=retry&limit=10&offset=2", nil)
	req = req.WithContext(context.WithValue(req.Context(), installationIDsKey, []int64{7, 8}))
	rec := httptest.NewRecorder()
	s.listMemories(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if fake.filter.InstallationID != 7 || len(fake.filter.ContainerTags) != 1 || fake.filter.Type != "pattern" || fake.filter.Query != "retry" || fake.filter.Limit != 10 || fake.filter.Offset != 2 {
		t.Fatalf("unexpected filter: %+v", fake.filter)
	}
	var body struct {
		Memories []map[string]any `json:"memories"`
		Total    int              `json:"total"`
		Limit    int              `json:"limit"`
		Offset   int              `json:"offset"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Total != 1 || body.Limit != 10 || body.Offset != 2 || len(body.Memories) != 1 {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
	if _, leaked := body.Memories[0]["embedding"]; leaked {
		t.Fatal("embedding leaked")
	}
	if _, leaked := body.Memories[0]["deleted_at"]; leaked {
		t.Fatal("tombstone leaked")
	}
}

func TestParseMemoryListParamsRejectsInvalidBoundsAndScope(t *testing.T) {
	t.Parallel()
	tests := []string{
		"?installation_id=7&limit=0",
		"?installation_id=7&limit=101",
		"?installation_id=7&offset=-1",
		"?installation_id=7&scope=other",
		"?installation_id=7&scope=repo",
		"?installation_id=7&scope=shared&repo_id=9",
	}
	for _, query := range tests {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/memories"+query, nil)
		if _, err := parseMemoryListParams(req); err == nil {
			t.Errorf("%s: expected error", query)
		}
	}
}

func TestParseMemoryListParamsUsesDashboardPageDefault(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/memories?installation_id=7", nil)
	params, err := parseMemoryListParams(req)
	if err != nil {
		t.Fatal(err)
	}
	if params.limit != 25 || params.offset != 0 || params.scope != "all" {
		t.Fatalf("defaults = limit %d offset %d scope %q", params.limit, params.offset, params.scope)
	}
}
