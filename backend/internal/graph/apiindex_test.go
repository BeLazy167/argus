package graph

import (
	"context"
	"testing"
)

// TestPersistAPIEndpoints covers the step between extraction and storage: which
// code_nodes row an endpoint is attached to. Getting this wrong does not fail
// loudly — it produces an edge that points at the wrong function.
func TestPersistAPIEndpoints(t *testing.T) {
	const repoID int64 = 7

	tests := []struct {
		name       string
		endpoint   APIEndpoint
		keyToID    map[string]int64
		nameToIDs  map[string][]int64
		wantNodeID int64
		wantRows   int
	}{
		{
			// chi registers r.Get("/x", s.healthz) in server.go while healthz
			// lives in handlers_health.go. The handler is what a change to the
			// endpoint actually touches, so it wins over the router function.
			name:     "handler in another file beats the enclosing symbol",
			endpoint: APIEndpoint{Role: RoleServer, Method: "GET", Path: "/api/v1/me", Handler: "handleMe", Symbol: "routes", Line: 12},
			keyToID:  map[string]int64{nodeKey("server.go", "routes"): 500},
			// handleMe was parsed out of a different file in the same run.
			nameToIDs:  map[string][]int64{"handleMe": {900}, "routes": {500}},
			wantNodeID: 900,
			wantRows:   1,
		},
		{
			// A client call names no handler, so the function containing it is
			// the only honest anchor.
			name:       "client call anchors to its enclosing function",
			endpoint:   APIEndpoint{Role: RoleClient, Method: "GET", Path: "/api/v1/repos", Symbol: "useRepos", Line: 4},
			keyToID:    map[string]int64{nodeKey("server.go", "useRepos"): 610},
			nameToIDs:  map[string][]int64{"useRepos": {610}},
			wantNodeID: 610,
			wantRows:   1,
		},
		{
			// An inline arrow-function handler at module scope resolves to
			// nothing. Dropping it is deliberate: anchoring it to a synthesised
			// node would make the edge point at code that does not serve the
			// route.
			name:      "unanchorable endpoint is dropped, not guessed",
			endpoint:  APIEndpoint{Role: RoleServer, Method: "GET", Path: "/api/v1/me", Line: 3},
			keyToID:   map[string]int64{},
			nameToIDs: map[string][]int64{},
			wantRows:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeIndexerStore()
			persistAPIEndpoints(context.Background(), f, repoID,
				map[string][]APIEndpoint{"server.go": {tt.endpoint}}, tt.keyToID, tt.nameToIDs)

			rows, ok := f.endpointRows["server.go"]
			if !ok {
				t.Fatalf("ReplaceAPIEndpointsForFiles was never called for server.go")
			}
			if len(rows) != tt.wantRows {
				t.Fatalf("wrote %d rows, want %d (%+v)", len(rows), tt.wantRows, rows)
			}
			if tt.wantRows == 0 {
				return
			}
			if rows[0].NodeID != tt.wantNodeID {
				t.Fatalf("node_id = %d, want %d", rows[0].NodeID, tt.wantNodeID)
			}
			if rows[0].RepoID != repoID || rows[0].Role != tt.endpoint.Role || rows[0].PathPattern != tt.endpoint.Path {
				t.Fatalf("row = %+v, does not describe the endpoint", rows[0])
			}
		})
	}
}

// A file that no longer declares any endpoint must still be written — with an
// empty row set — because that empty write is what removes a route that was
// deleted from the source. Skipping the call leaves the old row behind and the
// linker keeps inferring an edge to an endpoint that no longer exists.
func TestPersistAPIEndpointsClearsFilesWithNoEndpoints(t *testing.T) {
	f := newFakeIndexerStore()
	persistAPIEndpoints(context.Background(), f, 7,
		map[string][]APIEndpoint{"gone.go": nil}, map[string]int64{}, map[string][]int64{})

	rows, ok := f.endpointRows["gone.go"]
	if !ok {
		t.Fatal("ReplaceAPIEndpointsForFiles was not called for a file with no endpoints")
	}
	if len(rows) != 0 {
		t.Fatalf("wrote %d rows for a file with no endpoints", len(rows))
	}
}

// A route registration names a handler that lives in a file this run never
// fetched, which is the ordinary case for an incremental index of a router
// file. Without the DB fallback the endpoint falls through to the enclosing
// router function, so every route in server.go anchors to `routes` on a PR
// index and to its own handler on a full index — the node a route carries then
// depends on which files the PR happened to touch.
func TestPersistAPIEndpointsResolvesHandlerFromTheDatabase(t *testing.T) {
	f := newFakeIndexerStore()
	f.nodeIDsByName = map[string]int64{"getRepo": 4242}

	persistAPIEndpoints(context.Background(), f, 7, map[string][]APIEndpoint{
		"server.go": {{Role: RoleServer, Method: "GET", Path: "/api/v1/repos/{}", Handler: "getRepo", Symbol: "routes", Line: 12}},
	},
		map[string]int64{nodeKey("server.go", "routes"): 500},
		map[string][]int64{"routes": {500}},
	)

	rows := f.endpointRows["server.go"]
	if len(rows) != 1 {
		t.Fatalf("wrote %d rows, want 1 (%+v)", len(rows), rows)
	}
	if rows[0].NodeID != 4242 {
		t.Fatalf("node_id = %d, want the handler node 4242 (anchored to the router function instead)", rows[0].NodeID)
	}
}

// The change gate. An index run that leaves the route table exactly as it was
// must not re-derive the whole installation's edge set: the link pass relists
// every endpoint in the installation, rematches it, and rewrites every row.
func TestPersistAPIEndpointsReportsNoChangeOnIdenticalRows(t *testing.T) {
	f := newFakeIndexerStore()
	eps := map[string][]APIEndpoint{
		"server.go": {{Role: RoleServer, Method: "GET", Path: "/api/v1/repos/{}", Symbol: "routes", Line: 12}},
	}
	keyToID := map[string]int64{nodeKey("server.go", "routes"): 500}
	nameToIDs := map[string][]int64{"routes": {500}}

	if changed := persistAPIEndpoints(context.Background(), f, 7, eps, keyToID, nameToIDs); !changed {
		t.Fatal("first write must report a change")
	}
	if changed := persistAPIEndpoints(context.Background(), f, 7, eps, keyToID, nameToIDs); changed {
		t.Fatal("re-writing identical endpoint rows reported a change")
	}
}
