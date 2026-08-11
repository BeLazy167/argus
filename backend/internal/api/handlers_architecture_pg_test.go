package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BeLazy167/argus/backend/internal/store"
)

func architectureTestPool(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, ctx
}

func seedArchitectureRepo(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (installationID, repoID int64) {
	t.Helper()
	if err := pool.QueryRow(ctx, `
		INSERT INTO installations (installation_id, org_login)
		VALUES ((random()*1000000000)::bigint, 'architecture-response-test') RETURNING id`).Scan(&installationID); err != nil {
		t.Fatalf("seed installation: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO repos (installation_id, github_id, full_name)
		VALUES ($1, (random()*1000000000)::bigint, $2) RETURNING id`, installationID,
		"architecture/response-"+strconv.FormatInt(time.Now().UnixNano(), 10)).Scan(&repoID); err != nil {
		t.Fatalf("seed repo: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM repos WHERE id = $1`, repoID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM installations WHERE id = $1`, installationID)
	})
	return installationID, repoID
}

func requestArchitecture(t *testing.T, ctx context.Context, pool *pgxpool.Pool, installationID, repoID int64) archResponse {
	t.Helper()
	server := &Server{
		store:  store.NewWithDB(pool),
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("repoID", strconv.FormatInt(repoID, 10))
	ctx = context.WithValue(ctx, chi.RouteCtxKey, routeCtx)
	ctx = context.WithValue(ctx, installationIDsKey, []int64{installationID})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/repos/1/architecture", nil).WithContext(ctx)
	recorder := httptest.NewRecorder()
	server.getArchitecture(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response archResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return response
}

func TestArchitectureResponseAlwaysIncludesGraphSnapshot(t *testing.T) {
	pool, ctx := architectureTestPool(t)
	installationID, repoID := seedArchitectureRepo(t, ctx, pool)

	response := requestArchitecture(t, ctx, pool, installationID, repoID)
	if response.Snapshot.GenerationID != 0 || response.Snapshot.Status != "" || response.Snapshot.Complete {
		t.Fatalf("empty graph snapshot = %+v, want explicit zero snapshot", response.Snapshot)
	}
	if response.Files == nil || response.Edges == nil {
		t.Fatalf("empty topology must remain arrays: files=%v edges=%v", response.Files, response.Edges)
	}
}

func TestArchitectureResponseShowsNewestBuildingSnapshotWithPublishedTopology(t *testing.T) {
	pool, ctx := architectureTestPool(t)
	installationID, repoID := seedArchitectureRepo(t, ctx, pool)
	if _, err := pool.Exec(ctx, `
		INSERT INTO code_nodes (repo_id, kind, name, file_path, line_start, line_end, language)
		VALUES ($1, 'file', 'published.go', 'published.go', 1, 8, 'go')`, repoID); err != nil {
		t.Fatalf("seed published topology: %v", err)
	}
	var generationID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO graph_index_generations
		  (repo_id, commit_sha, status, expected_files, visited_files, failed_files)
		VALUES ($1, 'new-commit', 'building', 7, 3, 1) RETURNING id`, repoID).Scan(&generationID); err != nil {
		t.Fatalf("seed building generation: %v", err)
	}

	response := requestArchitecture(t, ctx, pool, installationID, repoID)
	if response.Snapshot.GenerationID != generationID || response.Snapshot.Status != "building" ||
		response.Snapshot.ExpectedFiles != 7 || response.Snapshot.VisitedFiles != 3 ||
		response.Snapshot.FailedFiles != 1 || response.Snapshot.Complete {
		t.Fatalf("building snapshot = %+v", response.Snapshot)
	}
	if len(response.Files) != 1 || response.Files[0].Path != "published.go" {
		t.Fatalf("last published topology hidden while building: %+v", response.Files)
	}
}
