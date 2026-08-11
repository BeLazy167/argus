package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BeLazy167/argus/backend/internal/store/db"
)

// The tenant boundary of the cross-repo API matcher is a SQL join, and the
// separation between an inferred edge and a parsed one is a column default.
// Neither can be verified anywhere but against a real Postgres. Gated on
// TEST_DATABASE_URL — deliberately NOT the app's DATABASE_URL — so `go test`
// on a machine with live credentials exported can never touch that database.
func apiEndpointTestPool(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; PG-backed tests run where the CI harness provides a database")
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

func apiSeedRepo(t *testing.T, ctx context.Context, pool *pgxpool.Pool, installID int64, name string) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(ctx, `
		INSERT INTO repos (installation_id, github_id, full_name)
		VALUES ($1, (random() * 1000000000)::bigint, $2)
		RETURNING id`, installID, name).Scan(&id)
	if err != nil {
		t.Fatalf("seed repo: %v", err)
	}
	return id
}

func apiSeedNode(t *testing.T, ctx context.Context, pool *pgxpool.Pool, repoID int64, name, filePath string) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(ctx, `
		INSERT INTO code_nodes (repo_id, kind, name, file_path, line_start, line_end, language)
		VALUES ($1, 'function', $2, $3, 1, 5, 'go')
		RETURNING id`, repoID, name, filePath).Scan(&id)
	if err != nil {
		t.Fatalf("seed node: %v", err)
	}
	return id
}

// TestListAPIEndpointsForInstallationOf_TenantBoundary is the one that matters
// most. Repos inside an installation may link; repos in different installations
// must never — the same boundary the pattern_stats UNIQUE fix closed. Two
// installations here declare the SAME path, which is the situation that makes a
// missing predicate produce a cross-tenant edge rather than an empty result.
func TestListAPIEndpointsForInstallationOf_TenantBoundary(t *testing.T) {
	pool, ctx := apiEndpointTestPool(t)
	st := &Store{Pool: pool, q: db.New(pool)}

	installA := seedInstallation(t, ctx, pool, "{}")
	apiRepo := apiSeedRepo(t, ctx, pool, installA, "acme/api")
	webRepo := apiSeedRepo(t, ctx, pool, installA, "acme/web")

	installB := seedInstallation(t, ctx, pool, "{}")
	otherRepo := apiSeedRepo(t, ctx, pool, installB, "rival/api")

	apiNode := apiSeedNode(t, ctx, pool, apiRepo, "getJob", "handlers.go")
	webNode := apiSeedNode(t, ctx, pool, webRepo, "useJob", "job.ts")
	otherNode := apiSeedNode(t, ctx, pool, otherRepo, "getJob", "handlers.go")

	mustReplace := func(repoID, nodeID int64, role, file string) {
		t.Helper()
		_, err := st.ReplaceAPIEndpointsForFiles(ctx, repoID, []string{file}, []APIEndpointRow{{
			RepoID: repoID, NodeID: nodeID, Role: role, Method: "GET",
			PathPattern: "/api/v1/jobs/{}", RawPath: "/api/v1/jobs/{id}", FilePath: file, Line: 3,
		}})
		if err != nil {
			t.Fatalf("replace endpoints: %v", err)
		}
	}
	mustReplace(apiRepo, apiNode, "server", "handlers.go")
	mustReplace(webRepo, webNode, "client", "job.ts")
	mustReplace(otherRepo, otherNode, "server", "handlers.go")

	rows, _, err := st.ListAPIEndpointsForInstallationOf(ctx, webRepo)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	seen := map[int64]bool{}
	for _, r := range rows {
		seen[r.NodeID] = true
	}
	if !seen[apiNode] || !seen[webNode] {
		t.Fatalf("sibling repos in the same installation must be listed, got %+v", rows)
	}
	if seen[otherNode] {
		t.Fatalf("another installation's endpoint leaked into the match set: %+v", rows)
	}
}

// A route deleted from a file has to leave the table. An upsert-only path would
// keep inferring an edge to an endpoint that no longer exists.
//
// The second half is the change gate: rewriting an identical set must report
// `false`, because that `false` is what stops every index run re-deriving the
// whole installation's edge set.
func TestReplaceAPIEndpointsForFiles_RemovesStaleRows(t *testing.T) {
	pool, ctx := apiEndpointTestPool(t)
	st := &Store{Pool: pool, q: db.New(pool)}

	install := seedInstallation(t, ctx, pool, "{}")
	repo := apiSeedRepo(t, ctx, pool, install, "acme/api")
	node := apiSeedNode(t, ctx, pool, repo, "getJob", "routes.go")

	row := func(path string, line int) APIEndpointRow {
		return APIEndpointRow{
			RepoID: repo, NodeID: node, Role: "server", Method: "GET",
			PathPattern: path, RawPath: path, FilePath: "routes.go", Line: line,
		}
	}
	files := []string{"routes.go"}
	if changed, err := st.ReplaceAPIEndpointsForFiles(ctx, repo, files, []APIEndpointRow{
		row("/api/v1/jobs", 3), row("/api/v1/jobs/{}", 4),
	}); err != nil || !changed {
		t.Fatalf("first write: changed=%v err=%v", changed, err)
	}
	if changed, err := st.ReplaceAPIEndpointsForFiles(ctx, repo, files, []APIEndpointRow{
		row("/api/v1/jobs", 3),
	}); err != nil || !changed {
		t.Fatalf("second write: changed=%v err=%v", changed, err)
	}

	rows, _, err := st.ListAPIEndpointsForInstallationOf(ctx, repo)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 || rows[0].PathPattern != "/api/v1/jobs" {
		t.Fatalf("stale endpoint survived the rewrite: %+v", rows)
	}

	changed, err := st.ReplaceAPIEndpointsForFiles(ctx, repo, files, []APIEndpointRow{row("/api/v1/jobs", 3)})
	if err != nil {
		t.Fatalf("third write: %v", err)
	}
	if changed {
		t.Fatal("rewriting an identical endpoint set reported a change")
	}
}

// The derived edge set is REPLACED, not accumulated. Nothing else deletes a
// calls_api row: the FK cascade from code_nodes only fires when a node
// disappears, and a route can be renamed while its handler survives. An
// insert-only writer left a permanent claim that one function calls an API it
// does not call.
func TestReplaceInferredAPIEdges_DropsEdgesNoLongerDerived(t *testing.T) {
	pool, ctx := apiEndpointTestPool(t)
	st := &Store{Pool: pool, q: db.New(pool)}

	install := seedInstallation(t, ctx, pool, "{}")
	apiRepo := apiSeedRepo(t, ctx, pool, install, "acme/api")
	webRepo := apiSeedRepo(t, ctx, pool, install, "acme/web")
	apiNode := apiSeedNode(t, ctx, pool, apiRepo, "getJob", "handlers.go")
	webNode := apiSeedNode(t, ctx, pool, webRepo, "useJob", "job.ts")

	if _, err := st.ReplaceInferredAPIEdges(ctx, webRepo, []InferredEdgeRow{
		{RepoID: webRepo, SourceID: webNode, TargetID: apiNode, Kind: "calls_api"},
	}); err != nil {
		t.Fatalf("first replace: %v", err)
	}

	countEdges := func() int {
		var n int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM code_edges WHERE repo_id = $1 AND kind = 'calls_api'`, webRepo).Scan(&n); err != nil {
			t.Fatalf("count edges: %v", err)
		}
		return n
	}
	if countEdges() != 1 {
		t.Fatalf("first replace wrote %d edges, want 1", countEdges())
	}

	// The route was renamed; both nodes still exist, so nothing cascades.
	if _, err := st.ReplaceInferredAPIEdges(ctx, webRepo, nil); err != nil {
		t.Fatalf("second replace: %v", err)
	}
	if n := countEdges(); n != 0 {
		t.Fatalf("stale inferred edge survived the endpoint that justified it: %d rows", n)
	}
}

// The parsed graph must be untouched by the derived rewrite. A DELETE that is
// not restricted to inferred calls_api rows would take the parsed edges of
// every repo in the installation with it.
func TestReplaceInferredAPIEdges_LeavesParsedEdgesAlone(t *testing.T) {
	pool, ctx := apiEndpointTestPool(t)
	st := &Store{Pool: pool, q: db.New(pool)}

	install := seedInstallation(t, ctx, pool, "{}")
	webRepo := apiSeedRepo(t, ctx, pool, install, "acme/web")
	webNode := apiSeedNode(t, ctx, pool, webRepo, "useJob", "job.ts")
	webCaller := apiSeedNode(t, ctx, pool, webRepo, "callerOfUseJob", "page.ts")

	if err := st.UpsertCodeEdge(ctx, webRepo, webCaller, webNode, "calls"); err != nil {
		t.Fatalf("upsert parsed edge: %v", err)
	}
	if _, err := st.ReplaceInferredAPIEdges(ctx, webRepo, nil); err != nil {
		t.Fatalf("replace: %v", err)
	}

	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM code_edges WHERE repo_id = $1 AND kind = 'calls'`, webRepo).Scan(&n); err != nil {
		t.Fatalf("count parsed edges: %v", err)
	}
	if n != 1 {
		t.Fatalf("the derived rewrite deleted a parsed edge: %d rows remain", n)
	}
}

// The DELETE in ReplaceInferredAPIEdges is scoped to the repos of ONE
// installation, and that scoping had no test: relaxing the predicate to delete
// every inferred calls_api row in the table left the whole store suite green.
// The blast is cross-tenant and total — any installation's index run would wipe
// every OTHER installation's derived edges, which then stay missing until each
// of those installations happens to re-index. It is the same tenant boundary
// ListAPIEndpointsForInstallationOf defends on the read side, and the write side
// is the half that destroys data rather than merely leaking it.
func TestReplaceInferredAPIEdges_StopsAtTheInstallationBoundary(t *testing.T) {
	pool, ctx := apiEndpointTestPool(t)
	st := &Store{Pool: pool, q: db.New(pool)}

	// Two installations, each with a derived cross-repo edge of its own.
	seed := func(name string) (repo int64, src int64, dst int64) {
		install := seedInstallation(t, ctx, pool, "{}")
		apiRepo := apiSeedRepo(t, ctx, pool, install, name+"/api")
		webRepo := apiSeedRepo(t, ctx, pool, install, name+"/web")
		apiNode := apiSeedNode(t, ctx, pool, apiRepo, "getJob", "handlers.go")
		webNode := apiSeedNode(t, ctx, pool, webRepo, "useJob", "job.ts")
		if _, err := st.ReplaceInferredAPIEdges(ctx, webRepo, []InferredEdgeRow{
			{RepoID: webRepo, SourceID: webNode, TargetID: apiNode, Kind: "calls_api"},
		}); err != nil {
			t.Fatalf("seed %s inferred edge: %v", name, err)
		}
		return webRepo, webNode, apiNode
	}
	mineRepo, _, _ := seed("acme")
	theirsRepo, theirsSrc, theirsDst := seed("rival")

	// A re-derivation in OUR installation that yields nothing.
	if _, err := st.ReplaceInferredAPIEdges(ctx, mineRepo, nil); err != nil {
		t.Fatalf("replace: %v", err)
	}

	var n int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM code_edges
		WHERE repo_id = $1 AND source_id = $2 AND target_id = $3 AND kind = 'calls_api'`,
		theirsRepo, theirsSrc, theirsDst).Scan(&n); err != nil {
		t.Fatalf("count other installation's edges: %v", err)
	}
	if n != 1 {
		t.Fatalf("another installation's derived edge was deleted by our re-derivation: %d rows remain, want 1", n)
	}
}

// LookupCodeNodeIDsByName is the fallback an incremental index depends on: the
// handler a route names lives in a file the run never fetched.
//
// SEED ORDER IS THE TEST. The query is `SELECT DISTINCT ON (name) ... ORDER BY
// name, id`, so it returns the LOWEST id bearing the name. Seeding the wanted
// node first gave it the lowest id, and it then won whether or not
// `WHERE repo_id = $1` was there at all — the assertion named the scoping
// predicate while being unable to observe it. The foreign repo's node is
// therefore seeded FIRST and holds the lower id: only the repo_id predicate
// keeps it from being the answer. Relaxing that predicate to
// `(repo_id = $1 OR true)` now fails with `getRepo = <foreign id>`.
func TestLookupCodeNodeIDsByName(t *testing.T) {
	pool, ctx := apiEndpointTestPool(t)
	st := &Store{Pool: pool, q: db.New(pool)}

	install := seedInstallation(t, ctx, pool, "{}")
	repo := apiSeedRepo(t, ctx, pool, install, "acme/api")
	otherRepo := apiSeedRepo(t, ctx, pool, install, "acme/other")
	foreign := apiSeedNode(t, ctx, pool, otherRepo, "getRepo", "handlers_repos.go")
	want := apiSeedNode(t, ctx, pool, repo, "getRepo", "handlers_repos.go")
	if foreign >= want {
		t.Fatalf("fixture is inert: the foreign node (%d) must hold a LOWER id than the wanted one (%d), or DISTINCT ON returns the wanted node regardless of repo scoping", foreign, want)
	}

	ids, err := st.LookupCodeNodeIDsByName(ctx, repo, []string{"getRepo", "neverDefined"})
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if ids["getRepo"] != want {
		t.Fatalf("getRepo = %d, want %d (a node from another repo, or none)", ids["getRepo"], want)
	}
	if _, ok := ids["neverDefined"]; ok {
		t.Fatalf("an undefined name resolved to %d", ids["neverDefined"])
	}
}

// TestInferredEdgeProvenance covers the provenance decision: an inferred edge
// sits in the same table as a parsed one, so a consumer has to be able to tell
// them apart. Two signals, and the boolean is the one that survives new kinds
// being added.
func TestInferredEdgeProvenance(t *testing.T) {
	pool, ctx := apiEndpointTestPool(t)
	st := &Store{Pool: pool, q: db.New(pool)}

	install := seedInstallation(t, ctx, pool, "{}")
	apiRepo := apiSeedRepo(t, ctx, pool, install, "acme/api")
	webRepo := apiSeedRepo(t, ctx, pool, install, "acme/web")
	apiNode := apiSeedNode(t, ctx, pool, apiRepo, "getJob", "handlers.go")
	webNode := apiSeedNode(t, ctx, pool, webRepo, "useJob", "job.ts")
	webCaller := apiSeedNode(t, ctx, pool, webRepo, "callerOfUseJob", "page.ts")

	if _, err := st.ReplaceInferredAPIEdges(ctx, webRepo, []InferredEdgeRow{
		{RepoID: webRepo, SourceID: webNode, TargetID: apiNode, Kind: "calls_api"},
	}); err != nil {
		t.Fatalf("write inferred edge: %v", err)
	}
	// A parsed edge written through the ordinary path, for contrast.
	if err := st.UpsertCodeEdge(ctx, webRepo, webCaller, webNode, "calls"); err != nil {
		t.Fatalf("upsert parsed edge: %v", err)
	}

	var kind string
	var inferred bool
	err := pool.QueryRow(ctx, `
		SELECT kind, inferred FROM code_edges
		WHERE repo_id = $1 AND source_id = $2 AND target_id = $3`,
		webRepo, webNode, apiNode).Scan(&kind, &inferred)
	if err != nil {
		t.Fatalf("read inferred edge: %v", err)
	}
	if kind != "calls_api" || !inferred {
		t.Fatalf("kind = %q, inferred = %v; want calls_api / true", kind, inferred)
	}

	// The parsed edge must be distinguishable by the boolean alone, without
	// enumerating kinds.
	if err := pool.QueryRow(ctx, `
		SELECT inferred FROM code_edges
		WHERE repo_id = $1 AND source_id = $2 AND target_id = $3`,
		webRepo, webCaller, webNode).Scan(&inferred); err != nil {
		t.Fatalf("read parsed edge: %v", err)
	}
	if inferred {
		t.Fatal("a parsed edge was marked inferred")
	}
}

// TestRepoScopedQueriesIgnoreInferredEdges is the sibling half of this change.
//
// A derived cross-repo edge carries the CLIENT's repo_id while its target node
// lives in ANOTHER repository, so every query that filters code_edges by
// repo_id and joins both node ends would pick it up: the graph payload would
// carry an edge to a node id its own node list does not contain, and every
// architecture metric — fan-in, choke points, coupling — would count a foreign
// file as part of this repository. Each of these reads must exclude it, and one
// predicate does it for all of them.
func TestRepoScopedQueriesIgnoreInferredEdges(t *testing.T) {
	pool, ctx := apiEndpointTestPool(t)
	st := &Store{Pool: pool, q: db.New(pool)}

	install := seedInstallation(t, ctx, pool, "{}")
	apiRepo := apiSeedRepo(t, ctx, pool, install, "acme/api")
	webRepo := apiSeedRepo(t, ctx, pool, install, "acme/web")
	apiNode := apiSeedNode(t, ctx, pool, apiRepo, "getJob", "backend/handlers.go")
	webNode := apiSeedNode(t, ctx, pool, webRepo, "useJob", "web/job.ts")

	if _, err := st.ReplaceInferredAPIEdges(ctx, webRepo, []InferredEdgeRow{
		{RepoID: webRepo, SourceID: webNode, TargetID: apiNode, Kind: "calls_api"},
	}); err != nil {
		t.Fatalf("write inferred edge: %v", err)
	}

	edges, err := st.ListGraphEdges(ctx, webRepo)
	if err != nil {
		t.Fatalf("list graph edges: %v", err)
	}
	if len(edges) != 0 {
		t.Fatalf("inferred edge reached the graph payload with a target outside the repo: %+v", edges)
	}

	archEdges, err := st.ListArchFileEdges(ctx, webRepo)
	if err != nil {
		t.Fatalf("list arch file edges: %v", err)
	}
	if len(archEdges) != 0 {
		t.Fatalf("inferred edge reached the architecture metrics: %+v", archEdges)
	}

	choke, err := st.GetTopChokePoints(ctx, webRepo, 10)
	if err != nil {
		t.Fatalf("choke points: %v", err)
	}
	if len(choke) != 0 {
		t.Fatalf("inferred edge produced a choke point in another repo's file: %+v", choke)
	}

	fanIn, err := st.q.GetFileFanIn(ctx, db.GetFileFanInParams{RepoID: webRepo, FilePath: "backend/handlers.go"})
	if err != nil {
		t.Fatalf("file fan-in: %v", err)
	}
	if fanIn != 0 {
		t.Fatalf("inferred edge inflated fan-in to %d", fanIn)
	}
}

// The claim this slice rests on: a cross-repo inferred edge cannot change what
// today's blast radius returns, because that query filters repo_id inside the
// recursion. If it ever could, an unrelated repository would start appearing in
// review context as though a parser had found it.
func TestCrossRepoInferredEdgeStaysOutOfBlastRadius(t *testing.T) {
	pool, ctx := apiEndpointTestPool(t)
	st := &Store{Pool: pool, q: db.New(pool)}

	install := seedInstallation(t, ctx, pool, "{}")
	apiRepo := apiSeedRepo(t, ctx, pool, install, "acme/api")
	webRepo := apiSeedRepo(t, ctx, pool, install, "acme/web")
	apiNode := apiSeedNode(t, ctx, pool, apiRepo, "getJob", "handlers.go")
	webNode := apiSeedNode(t, ctx, pool, webRepo, "useJob", "job.ts")

	if _, err := st.ReplaceInferredAPIEdges(ctx, webRepo, []InferredEdgeRow{
		{RepoID: webRepo, SourceID: webNode, TargetID: apiNode, Kind: "calls_api"},
	}); err != nil {
		t.Fatalf("write inferred edge: %v", err)
	}

	nodes, err := st.GetBlastRadius(ctx, install, apiRepo, []string{"handlers.go"}, 2)
	if err != nil {
		t.Fatalf("blast radius: %v", err)
	}
	for _, n := range nodes {
		if n.ID == webNode {
			t.Fatalf("cross-repo inferred edge leaked into repo-scoped blast radius: %+v", nodes)
		}
	}
}
