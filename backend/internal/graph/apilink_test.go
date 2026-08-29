package graph

import (
	"context"
	"errors"
	"testing"

	"github.com/BeLazy167/argus/backend/internal/store"
)

// fakeAPIEndpointStore stands in for the two store methods LinkAPIEndpoints
// uses. The rows it returns model what the installation join would have
// produced, so a test can state "these repos, these endpoints" and assert
// exactly which edges are written.
type fakeAPIEndpointStore struct {
	rows      []store.APIEndpointRow
	listErr   error
	truncated bool
	// edges is the fake's whole derived-edge table. Replaced, not appended,
	// because the real writer replaces the installation's set.
	edges    []inferredEdge
	replaces int
}

type inferredEdge struct {
	repoID   int64
	sourceID int64
	targetID int64
	kind     string
}

func (f *fakeAPIEndpointStore) ListAPIEndpointsForInstallationOf(context.Context, int64) ([]store.APIEndpointRow, bool, error) {
	return f.rows, f.truncated, f.listErr
}

func (f *fakeAPIEndpointStore) ReplaceInferredAPIEdges(_ context.Context, _ int64, edges []store.InferredEdgeRow) (int, error) {
	f.replaces++
	f.edges = nil
	for _, e := range edges {
		f.edges = append(f.edges, inferredEdge{repoID: e.RepoID, sourceID: e.SourceID, targetID: e.TargetID, kind: e.Kind})
	}
	return len(edges), nil
}

func srvRow(repoID, nodeID int64, method, path string) store.APIEndpointRow {
	return store.APIEndpointRow{
		RepoID: repoID, NodeID: nodeID, Role: RoleServer, Method: method,
		PathPattern: path, RawPath: path, FilePath: "server.go", Line: int(nodeID),
	}
}

func cliRow(repoID, nodeID int64, method, path string) store.APIEndpointRow {
	return store.APIEndpointRow{
		RepoID: repoID, NodeID: nodeID, Role: RoleClient, Method: method,
		PathPattern: path, RawPath: path, FilePath: "client.ts", Line: int(nodeID),
	}
}

func TestLinkAPIEndpoints(t *testing.T) {
	const apiRepo, webRepo int64 = 1, 2

	tests := []struct {
		name  string
		rows  []store.APIEndpointRow
		want  []inferredEdge
		count int
	}{
		{
			// The motivating case: the API repo declares it, the web repo calls
			// it, and the edge runs client → server so that walking edges
			// backwards from the handler surfaces the caller.
			name: "cross-repo match becomes one calls_api edge",
			rows: []store.APIEndpointRow{
				srvRow(apiRepo, 100, "GET", "/api/v1/jobs/{}"),
				cliRow(webRepo, 200, "GET", "/api/v1/jobs/{}"),
			},
			want:  []inferredEdge{{repoID: webRepo, sourceID: 200, targetID: 100, kind: EdgeCallsAPI}},
			count: 1,
		},
		{
			// A client and a route inside ONE repo are already connected by the
			// parsed graph, and an inferred edge there would land inside the
			// repo-scoped blast-radius query — silently changing an answer that
			// is currently made of parsed facts only.
			name: "same-repo match writes nothing",
			rows: []store.APIEndpointRow{
				srvRow(webRepo, 100, "GET", "/api/jobs/{}"),
				cliRow(webRepo, 200, "GET", "/api/jobs/{}"),
			},
			count: 0,
		},
		{
			name: "unrelated paths write nothing",
			rows: []store.APIEndpointRow{
				srvRow(apiRepo, 100, "GET", "/api/v1/jobs/{}"),
				cliRow(webRepo, 200, "GET", "/api/v1/users/{}"),
			},
			count: 0,
		},
		{
			name: "two callers of one route give two edges",
			rows: []store.APIEndpointRow{
				srvRow(apiRepo, 100, "POST", "/api/v1/jobs"),
				cliRow(webRepo, 200, "POST", "/api/v1/jobs"),
				cliRow(webRepo, 201, "POST", "/api/v1/jobs"),
			},
			count: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeAPIEndpointStore{rows: tt.rows}
			got, err := LinkAPIEndpoints(context.Background(), f, webRepo)
			if err != nil {
				t.Fatalf("LinkAPIEndpoints: %v", err)
			}
			if got != tt.count {
				t.Fatalf("wrote %d edges, want %d (%+v)", got, tt.count, f.edges)
			}
			if len(f.edges) != tt.count {
				t.Fatalf("store saw %d edges, want %d (%+v)", len(f.edges), tt.count, f.edges)
			}
			for i, want := range tt.want {
				if f.edges[i] != want {
					t.Fatalf("edge %d = %+v, want %+v", i, f.edges[i], want)
				}
			}
		})
	}
}

func TestLinkAPIEndpointsCapsRunawayMatches(t *testing.T) {
	// One route, more callers than the cap. Without the cap this writes every
	// pair, and a generated client would push an unbounded number of rows into
	// a table the whole graph reads.
	rows := []store.APIEndpointRow{srvRow(1, 1, "GET", "/api/v1/jobs")}
	for i := range maxInferredEdgesPerRun + 50 {
		rows = append(rows, cliRow(2, int64(1000+i), "GET", "/api/v1/jobs"))
	}
	f := &fakeAPIEndpointStore{rows: rows}
	got, err := LinkAPIEndpoints(context.Background(), f, 2)
	if err != nil {
		t.Fatalf("LinkAPIEndpoints: %v", err)
	}
	if got != maxInferredEdgesPerRun {
		t.Fatalf("wrote %d edges, want the cap %d", got, maxInferredEdgesPerRun)
	}
}

// A derived edge has no other way to die. The FK cascade from code_nodes only
// fires when a node disappears, and a route can be renamed while its handler
// survives — so an insert-only writer left a permanent claim that one function
// calls an API it does not call.
func TestLinkAPIEndpointsDropsEdgesWhoseEndpointIsGone(t *testing.T) {
	const apiRepo, webRepo int64 = 1, 2

	f := &fakeAPIEndpointStore{rows: []store.APIEndpointRow{
		srvRow(apiRepo, 100, "GET", "/api/v1/jobs/{}"),
		cliRow(webRepo, 200, "GET", "/api/v1/jobs/{}"),
	}}
	if _, err := LinkAPIEndpoints(context.Background(), f, webRepo); err != nil {
		t.Fatalf("first link: %v", err)
	}
	if len(f.edges) != 1 {
		t.Fatalf("first link wrote %d edges, want 1", len(f.edges))
	}

	// The route is renamed; both nodes still exist, so nothing cascades.
	f.rows = []store.APIEndpointRow{
		srvRow(apiRepo, 100, "GET", "/api/v1/tasks/{}"),
		cliRow(webRepo, 200, "GET", "/api/v1/jobs/{}"),
	}
	got, err := LinkAPIEndpoints(context.Background(), f, webRepo)
	if err != nil {
		t.Fatalf("second link: %v", err)
	}
	if got != 0 || len(f.edges) != 0 {
		t.Fatalf("stale edge survived the endpoint that justified it: %+v", f.edges)
	}
}

// A clipped endpoint window is biased against the freshest rows, because the
// rewrite path gives the just-indexed repo new ids and the query orders by id.
// Replacing the derived set from a partial view deletes links it cannot
// re-derive, so the pass must refuse rather than write a smaller answer.
func TestLinkAPIEndpointsRefusesTruncatedWindow(t *testing.T) {
	f := &fakeAPIEndpointStore{
		truncated: true,
		rows: []store.APIEndpointRow{
			srvRow(1, 100, "GET", "/api/v1/jobs/{}"),
			cliRow(2, 200, "GET", "/api/v1/jobs/{}"),
		},
	}
	got, err := LinkAPIEndpoints(context.Background(), f, 2)
	if err != nil {
		t.Fatalf("LinkAPIEndpoints: %v", err)
	}
	if got != 0 || f.replaces != 0 {
		t.Fatalf("rewrote the derived edge set from a truncated window (wrote %d, replaces %d)", got, f.replaces)
	}
}

func TestLinkAPIEndpointsPropagatesListError(t *testing.T) {
	// A failed read must not be reported as "no cross-repo edges exist" — the
	// caller logs the difference, and a silent zero is how a broken linker
	// looks exactly like a codebase with no cross-repo calls.
	want := errors.New("boom")
	f := &fakeAPIEndpointStore{listErr: want}
	if _, err := LinkAPIEndpoints(context.Background(), f, 1); !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}
