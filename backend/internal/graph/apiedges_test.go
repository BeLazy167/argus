package graph

import (
	"testing"
)

func TestNormalizeAPIPath(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"chi brace param", "/jobs/{id}", "/jobs/{}"},
		{"chi typed param", "/jobs/{id:[0-9]+}", "/jobs/{}"},
		{"express colon param", "/jobs/:id", "/jobs/{}"},
		{"flask converter param", "/jobs/<int:id>", "/jobs/{}"},
		{"nextjs bracket param", "/jobs/[id]", "/jobs/{}"},
		{"template literal hole", "/jobs/${jobId}", "/jobs/{}"},
		{"partially interpolated segment", "/jobs/job-${id}", "/jobs/{}"},
		{"go format verb", "/jobs/%s", "/jobs/{}"},
		{"nextjs catch-all", "/files/[...path]", "/files/*"},
		// Go 1.22's end-of-path anchor: "GET /jobs/{$}" serves /jobs and
		// REFUSES every child path. Treating it as a parameter made the route
		// claim to serve /jobs/<anything>, so a client of /jobs/${id} linked to
		// a handler that 404s every URL it supposedly serves.
		{"servemux end-of-path anchor", "/api/v1/jobs/{$}", "/api/v1/jobs"},
		// Go 1.22's multi-segment wildcard spans many segments, so collapsing
		// it to a single parameter contradicts why wildcardSegment exists.
		{"servemux multi-segment wildcard", "/files/{rest...}", "/files/*"},
		// segmentParamRe cannot consume the inner ']' of "[[...slug]]", so the
		// branch that used to handle this sat inside a match that never fired
		// and the segment was returned verbatim — a pattern no client call can
		// ever normalise to, so the route silently linked nothing.
		{"nextjs optional catch-all", "/docs/[[...slug]]", "/docs/*"},
		{"chi wildcard", "/files/*", "/files/*"},
		{"flask path converter", "/files/<path:rest>", "/files/*"},
		{"query string dropped", "/jobs?state=open", "/jobs"},
		{"fragment dropped", "/jobs#top", "/jobs"},
		{"trailing slash dropped", "/jobs/{id}/", "/jobs/{}"},
		{"duplicate slashes collapsed", "/api//v1///jobs", "/api/v1/jobs"},
		{"root stays root", "/", "/"},

		// The '?' of optional chaining sits INSIDE a template hole. Cutting the
		// path there produced "/installations/${api.active", which no server
		// declares, so every call site written this way stopped matching.
		{"optional chaining inside hole", "/installations/${api.active?.id}/features", "/installations/{}/features"},

		// Absolute URLs name a host we do not own. Matching them by path alone
		// is what links a Stripe client to a local route of the same shape.
		{"absolute https rejected", "https://api.stripe.com/v1/charges", ""},
		{"absolute http rejected", "http://localhost:8080/api/v1/jobs", ""},
		{"protocol relative rejected", "//cdn.example.com/api/v1/jobs", ""},
		{"non-path rejected", "jobs/{id}", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeAPIPath(tt.raw); got != tt.want {
				t.Fatalf("NormalizeAPIPath(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestIsMatchablePath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/api/v1/jobs", true},
		{"/jobs/{}", true},
		{"/", false},
		{"", false},
		// Single-segment paths are what every unrelated service in an
		// installation independently defines. Linking on them would join a web
		// repo to a service it has never called.
		{"/healthz", false},
		{"/metrics", false},
		// All-parameter paths match everything of that arity.
		{"/{}/{}", false},
		{"/*/{}", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := isMatchablePath(tt.path); got != tt.want {
				t.Fatalf("isMatchablePath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

// findEndpoint returns the endpoint with the given role/method/path, so the
// assertions below do not depend on extraction order.
func findEndpoint(eps []APIEndpoint, role, method, path string) *APIEndpoint {
	for i := range eps {
		if eps[i].Role == role && eps[i].Method == method && eps[i].Path == path {
			return &eps[i]
		}
	}
	return nil
}

func TestExtractGoEndpoints(t *testing.T) {
	// Mirrors the shape of this repo's own api/server.go: chi routes nested
	// inside r.Route, a Go 1.22 ServeMux pattern, an explicit r.Method call,
	// and a one-argument HTTP client call.
	src := `package api

func routes() {
	r := chi.NewRouter()
	r.Get("/healthz", s.healthz)
	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/repos/{repoID}", s.getRepo)
		r.Route("/installations", func(r chi.Router) {
			r.Put("/{installationID}/features", s.setFeatureFlags)
		})
		r.Method("DELETE", "/rules/{ruleID}", s.deleteRule)
	})
	r.Get("/repos/{repoID}/config", s.getModelConfigs)
	mux.HandleFunc("POST /api/v1/webhooks/github", s.handleWebhook)
	resp, _ := client.Get("/api/v1/repos/42")
	_ = resp
}
`
	eps := ExtractAPIEndpoints("backend/internal/api/server.go", src)

	tests := []struct {
		name    string
		role    string
		method  string
		path    string
		handler string
	}{
		// Without the Route prefix stack this is "/repos/{}" and matches nothing.
		{"nested route carries prefix", RoleServer, "GET", "/api/v1/repos/{}", "getRepo"},
		{"doubly nested route", RoleServer, "PUT", "/api/v1/installations/{}/features", "setFeatureFlags"},
		{"r.Method verb argument", RoleServer, "DELETE", "/api/v1/rules/{}", "deleteRule"},
		// Written after the r.Route block closes, so the prefix must be popped.
		{"prefix popped after block", RoleServer, "GET", "/repos/{}/config", "getModelConfigs"},
		{"servemux method pattern", RoleServer, "POST", "/api/v1/webhooks/github", "handleWebhook"},
		// One argument means an HTTP client, not a registration. The concrete
		// "42" stays literal — only parameter syntax normalises.
		{"single-arg call is a client", RoleClient, "GET", "/api/v1/repos/42", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findEndpoint(eps, tt.role, tt.method, tt.path)
			if got == nil {
				t.Fatalf("no %s %s %s in %+v", tt.role, tt.method, tt.path, eps)
			}
			if tt.handler != "" && got.Handler != tt.handler {
				t.Fatalf("handler = %q, want %q", got.Handler, tt.handler)
			}
		})
	}

	// Single-segment paths never survive extraction, so /healthz cannot link
	// anything even though it parses fine.
	if got := findEndpoint(eps, RoleServer, "GET", "/healthz"); got != nil {
		t.Fatalf("single-segment /healthz should be dropped, got %+v", got)
	}
}

// gofmt wraps a long registration. A line-local reader saw the line end after
// the comma and read that as "no second argument", i.e. an HTTP client call —
// two wrong rows from one input: the real route vanished from the table, and a
// phantom client endpoint was recorded in the SERVER repo, ready to link this
// repo to whoever else declares that path.
func TestExtractGoEndpointsWrappedRegistration(t *testing.T) {
	src := `package api

func routes() {
	r.Route("/api/v1", func(r chi.Router) {
		r.Get(
			"/repos/{repoID}/config",
			s.getModelConfigs,
		)
		r.Method(
			"DELETE",
			"/rules/{ruleID}",
			s.deleteRule,
		)
	})
}
`
	eps := ExtractAPIEndpoints("backend/internal/api/server.go", src)

	got := findEndpoint(eps, RoleServer, "GET", "/api/v1/repos/{}/config")
	if got == nil {
		t.Fatalf("wrapped chi registration lost its route and its prefix: %+v", eps)
	}
	if got.Handler != "getModelConfigs" {
		t.Fatalf("handler = %q, want getModelConfigs", got.Handler)
	}
	if e := findEndpoint(eps, RoleServer, "DELETE", "/api/v1/rules/{}"); e == nil {
		t.Fatalf("wrapped r.Method registration was lost: %+v", eps)
	}
	// The phantom half: a registration must never be filed as a call.
	if e := findEndpoint(eps, RoleClient, "GET", "/repos/{}/config"); e != nil {
		t.Fatalf("registration was recorded as a client call: %+v", e)
	}
}

// A commented-out registration or call is not a route table and not a
// dependency. Extracting them gave a repo that merely DOCUMENTS an API an
// inferred cross-repo edge to code it never calls, and pointed blast radius at
// dead routes. The live instance was the JSDoc example in
// web/src/lib/query-kit.ts, which matched a real backend route.
func TestExtractAPIEndpointsIgnoresComments(t *testing.T) {
	t.Run("go", func(t *testing.T) {
		src := `package api

func routes() {
	// r.Get("/api/v1/legacy/thing", h.Legacy)
	/* r.Get("/api/v1/blocked/thing", h.Blocked) */
	r.Get("/api/v1/live/thing", h.Live)
}
`
		eps := ExtractAPIEndpoints("internal/api/server.go", src)
		if e := findEndpoint(eps, RoleServer, "GET", "/api/v1/live/thing"); e == nil {
			t.Fatalf("the real route was lost: %+v", eps)
		}
		for _, p := range []string{"/api/v1/legacy/thing", "/api/v1/blocked/thing"} {
			if e := findEndpoint(eps, RoleServer, "GET", p); e != nil {
				t.Fatalf("commented-out registration became a route: %+v", e)
			}
		}
	})

	t.Run("typescript jsdoc", func(t *testing.T) {
		src := "/**\n" +
			" * Example:\n" +
			" *   fetcher: (_vars, ctx) => getApi(ctx).get(\"/api/v1/repos\")\n" +
			" */\n" +
			"export const useRepos = () => api.get(\"/api/v1/installations\");\n"
		eps := ExtractAPIEndpoints("web/src/lib/query-kit.ts", src)
		if e := findEndpoint(eps, RoleClient, "GET", "/api/v1/installations"); e == nil {
			t.Fatalf("the real call was lost: %+v", eps)
		}
		if e := findEndpoint(eps, RoleClient, "GET", "/api/v1/repos"); e != nil {
			t.Fatalf("a doc-comment example became a client call: %+v", e)
		}
	})

	t.Run("python", func(t *testing.T) {
		src := "# @app.get(\"/api/v1/legacy/jobs\")\n" +
			"@app.get(\"/api/v1/live/jobs\")\n" +
			"def live_jobs():\n" +
			"    return {}\n"
		eps := ExtractAPIEndpoints("service/routes.py", src)
		if e := findEndpoint(eps, RoleServer, "GET", "/api/v1/live/jobs"); e == nil {
			t.Fatalf("the real decorator route was lost: %+v", eps)
		}
		if e := findEndpoint(eps, RoleServer, "GET", "/api/v1/legacy/jobs"); e != nil {
			t.Fatalf("a commented-out decorator became a route: %+v", e)
		}
	})
}

// braceDelta blanks string literals before counting, but not comments. A single
// comment carrying an unbalanced brace popped the chi Route frame early, and
// every remaining route in the file was then recorded under the wrong prefix
// and silently stopped matching.
func TestExtractGoEndpointsCommentBraceDoesNotPopThePrefix(t *testing.T) {
	src := `package api

func routes() {
	r.Route("/api/v1", func(r chi.Router) {
		// closing } in prose, not code
		r.Get("/repos/{repoID}", s.getRepo)
	})
}
`
	eps := ExtractAPIEndpoints("internal/api/server.go", src)
	if e := findEndpoint(eps, RoleServer, "GET", "/api/v1/repos/{}"); e == nil {
		t.Fatalf("a brace inside a comment popped the Route prefix: %+v", eps)
	}
}

func TestExtractJSEndpoints(t *testing.T) {
	t.Run("express binding makes a route", func(t *testing.T) {
		src := `import express from "express";
const app = express();
const router = express.Router();
app.get("/api/v1/jobs/:id", getJob);
router.post("/api/v1/jobs", createJob);
axios.post("/api/v1/other/thing", payload);
`
		eps := ExtractAPIEndpoints("server/index.ts", src)
		if e := findEndpoint(eps, RoleServer, "GET", "/api/v1/jobs/{}"); e == nil {
			t.Fatalf("express app.get should be a route: %+v", eps)
		}
		if e := findEndpoint(eps, RoleServer, "POST", "/api/v1/jobs"); e == nil {
			t.Fatalf("express router.post should be a route: %+v", eps)
		}
		// axios is not bound to an express app in this file, so an
		// argument-count rule would have filed this POST as a declared route.
		if e := findEndpoint(eps, RoleClient, "POST", "/api/v1/other/thing"); e == nil {
			t.Fatalf("axios.post should stay a client call: %+v", eps)
		}
	})

	t.Run("no express binding means every verb call is a client", func(t *testing.T) {
		// The shape this repo's own web client uses: a generic-typed method on
		// the value returned by a call.
		src := "export const q = (ctx) => getApi(ctx).get<Repo[]>(`/api/v1/repos/${repoId}/config`);\n" +
			"const del = () => api.delete(`/api/v1/rules/${ruleId}`);\n"
		eps := ExtractAPIEndpoints("web/src/lib/queries/repos.ts", src)
		if e := findEndpoint(eps, RoleClient, "GET", "/api/v1/repos/{}/config"); e == nil {
			t.Fatalf("generic-typed get should be a client call: %+v", eps)
		}
		if e := findEndpoint(eps, RoleClient, "DELETE", "/api/v1/rules/{}"); e == nil {
			t.Fatalf("api.delete should be a client call: %+v", eps)
		}
		if len(eps) != 2 {
			t.Fatalf("expected exactly 2 endpoints, got %+v", eps)
		}
	})

	t.Run("fetch reads its method from the init object", func(t *testing.T) {
		src := "await fetch(`/api/v1/repos/${id}/reviews`, { method: \"POST\", body });\n" +
			"await fetchWithTrace(\"/api/v1/me/installations\");\n"
		eps := ExtractAPIEndpoints("web/src/lib/x.ts", src)
		if e := findEndpoint(eps, RoleClient, "POST", "/api/v1/repos/{}/reviews"); e == nil {
			t.Fatalf("fetch with method:POST: %+v", eps)
		}
		// fetch defaults to GET when the init object names no method.
		if e := findEndpoint(eps, RoleClient, "GET", "/api/v1/me/installations"); e == nil {
			t.Fatalf("bare fetch should default to GET: %+v", eps)
		}
	})

	t.Run("nextjs route file recovers the path from the file path", func(t *testing.T) {
		src := `import { NextResponse } from "next/server";

export async function GET(req: Request) { return Response.json({}); }
export const POST = async (req: Request) => Response.json({});
`
		eps := ExtractAPIEndpoints("web/src/app/(marketing)/api/openrouter/models/route.ts", src)
		// "(marketing)" is a route group: organisational, contributes no segment.
		e := findEndpoint(eps, RoleServer, "GET", "/api/openrouter/models")
		if e == nil {
			t.Fatalf("exported GET handler: %+v", eps)
		}
		// Same blank-line trap as the Python decorator: a leading \s* reports
		// the export one line early.
		if e.Line != 3 {
			t.Fatalf("handler line = %d, want 3", e.Line)
		}
		if e := findEndpoint(eps, RoleServer, "POST", "/api/openrouter/models"); e == nil {
			t.Fatalf("exported POST handler: %+v", eps)
		}
	})
}

func TestExtractPythonEndpoints(t *testing.T) {
	src := `from fastapi import APIRouter
router = APIRouter()

@router.get("/api/v1/jobs/{job_id}")
async def get_job(job_id: int):
    return {}

@app.route("/api/v1/legacy/jobs", methods=["POST", "PUT"])
def legacy_jobs():
    return {}

@app.route("/api/v1/plain/jobs")
def plain_jobs():
    return {}

def call_it():
    return requests.get("/api/v1/jobs/7")
`
	eps := ExtractAPIEndpoints("service/routes.py", src)

	e := findEndpoint(eps, RoleServer, "GET", "/api/v1/jobs/{}")
	if e == nil {
		t.Fatalf("fastapi decorator route: %+v", eps)
	}
	// The decorator is on line 4, after a blank line. A leading \s* in the
	// pattern swallows the blank line's newline and reports line 3 — a line
	// ABOVE the decorator, which then anchors to the wrong function.
	if e.Line != 4 {
		t.Fatalf("decorator line = %d, want 4", e.Line)
	}
	for _, m := range []string{"POST", "PUT"} {
		if e := findEndpoint(eps, RoleServer, m, "/api/v1/legacy/jobs"); e == nil {
			t.Fatalf("flask methods=%s route: %+v", m, eps)
		}
	}
	// Flask defaults to GET, and it really does refuse a POST — so ANY would
	// claim a method the route does not serve.
	if e := findEndpoint(eps, RoleServer, "GET", "/api/v1/plain/jobs"); e == nil {
		t.Fatalf("flask route without methods= should default to GET: %+v", eps)
	}
	if e := findEndpoint(eps, RoleServer, AnyMethod, "/api/v1/plain/jobs"); e != nil {
		t.Fatalf("flask route without methods= must not be ANY: %+v", e)
	}
	// Python frameworks register with decorators, so a verb call is a client.
	if e := findEndpoint(eps, RoleClient, "GET", "/api/v1/jobs/7"); e == nil {
		t.Fatalf("requests.get should be a client call: %+v", eps)
	}
	// The decorator pass already recorded @router.get; the client pass must not
	// record it a second time as a call to itself.
	if e := findEndpoint(eps, RoleClient, "GET", "/api/v1/jobs/{}"); e != nil {
		t.Fatalf("decorator must not also be read as a client call: %+v", e)
	}
}

func TestMatchAPIEndpoints(t *testing.T) {
	// Distinct repo ids, because MatchAPIEndpoints links CROSS-repo pairs only.
	const apiRepo, webRepo int64 = 1, 2
	server := func(method, path string) APIEndpoint {
		return APIEndpoint{Role: RoleServer, Method: method, Path: path, FilePath: "server.go", Line: 10, RepoID: apiRepo}
	}
	client := func(method, path string) APIEndpoint {
		return APIEndpoint{Role: RoleClient, Method: method, Path: path, FilePath: "client.ts", Line: 20, RepoID: webRepo}
	}

	tests := []struct {
		name      string
		endpoints []APIEndpoint
		want      int
	}{
		{
			name:      "same method and path links",
			endpoints: []APIEndpoint{server("GET", "/api/v1/jobs/{}"), client("GET", "/api/v1/jobs/{}")},
			want:      1,
		},
		{
			// The whole point of normalising: the two sides never write the
			// parameter the same way.
			name: "different param syntax still links",
			endpoints: []APIEndpoint{
				{Role: RoleServer, Method: "GET", Path: NormalizeAPIPath("/api/v1/jobs/{jobID}"), FilePath: "s.go", Line: 1, RepoID: apiRepo},
				{Role: RoleClient, Method: "GET", Path: NormalizeAPIPath("/api/v1/jobs/${jobId}"), FilePath: "c.ts", Line: 1, RepoID: webRepo},
			},
			want: 1,
		},
		{
			// Two repos in one installation can share a file path (a mirror, a
			// fork, a monorepo split). The old self-match guard compared file
			// path and line without repo id, so a route and a call that landed
			// on the same line of the same-named file lost a real match.
			name: "same file path in two repos still links",
			endpoints: []APIEndpoint{
				{Role: RoleServer, Method: "GET", Path: "/api/v1/jobs/{}", FilePath: "src/api.ts", Line: 9, RepoID: apiRepo},
				{Role: RoleClient, Method: "GET", Path: "/api/v1/jobs/{}", FilePath: "src/api.ts", Line: 9, RepoID: webRepo},
			},
			want: 1,
		},
		{
			// The safety property this function DOCUMENTS, asserted on this
			// function. It used to live only in LinkAPIEndpoints, so a second
			// caller written against the doc — the embedding seam below it —
			// would have produced same-repo inferred edges, which land inside
			// the repo-scoped blast radius and change answers currently made of
			// parsed facts only.
			name: "same-repo pair is dropped here, not by the caller",
			endpoints: []APIEndpoint{
				{Role: RoleServer, Method: "GET", Path: "/api/v1/jobs/{}", FilePath: "s.go", Line: 1, RepoID: apiRepo},
				{Role: RoleClient, Method: "GET", Path: "/api/v1/jobs/{}", FilePath: "c.go", Line: 2, RepoID: apiRepo},
			},
			want: 0,
		},
		{
			name:      "ANY route serves every method",
			endpoints: []APIEndpoint{server(AnyMethod, "/api/v1/jobs"), client("POST", "/api/v1/jobs")},
			want:      1,
		},

		// --- negatives: similar-looking but genuinely unrelated ---
		{
			name:      "same path different method does not link",
			endpoints: []APIEndpoint{server("GET", "/api/v1/jobs/{}"), client("DELETE", "/api/v1/jobs/{}")},
			want:      0,
		},
		{
			name:      "one extra segment does not link",
			endpoints: []APIEndpoint{server("GET", "/api/v1/jobs/{}"), client("GET", "/api/v1/jobs/{}/logs")},
			want:      0,
		},
		{
			name:      "same shape different resource does not link",
			endpoints: []APIEndpoint{server("GET", "/api/v1/jobs/{}"), client("GET", "/api/v1/users/{}")},
			want:      0,
		},
		{
			name:      "a literal segment does not match a parameter",
			endpoints: []APIEndpoint{server("GET", "/api/v1/jobs/active"), client("GET", "/api/v1/jobs/{}")},
			want:      0,
		},
		{
			name:      "a wildcard does not match a single parameter",
			endpoints: []APIEndpoint{server("GET", "/files/*"), client("GET", "/files/{}")},
			want:      0,
		},
		{
			// Every service defines /healthz. Two that do are not related.
			name:      "generic single-segment paths never link",
			endpoints: []APIEndpoint{server("GET", "/healthz"), client("GET", "/healthz")},
			want:      0,
		},
		{
			// The client URL names stripe.com; the route is ours. Rejecting
			// absolute URLs at normalisation is what stops this.
			name: "absolute client URL never links to a local route",
			endpoints: []APIEndpoint{
				server("POST", "/v1/charges"),
				{Role: RoleClient, Method: "POST", Path: NormalizeAPIPath("https://api.stripe.com/v1/charges"), FilePath: "billing.ts", Line: 4, RepoID: webRepo},
			},
			want: 0,
		},
		{
			name:      "two clients do not link to each other",
			endpoints: []APIEndpoint{client("GET", "/api/v1/jobs"), client("GET", "/api/v1/jobs")},
			want:      0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MatchAPIEndpoints(tt.endpoints)
			if len(got) != tt.want {
				t.Fatalf("MatchAPIEndpoints = %d matches, want %d (%+v)", len(got), tt.want, got)
			}
		})
	}
}

func TestAnchorEndpoints(t *testing.T) {
	goSymbols := []Symbol{
		{Name: "routes", FilePath: "s.go", LineStart: 1, LineEnd: 40},
		{Name: "registerV1", FilePath: "s.go", LineStart: 10, LineEnd: 20},
	}
	// A decorated Python function: the decorator on line 7 is inside nothing.
	pySymbols := []Symbol{{Name: "get_job", FilePath: "s.py", LineStart: 8, LineEnd: 12}}

	tests := []struct {
		name    string
		symbols []Symbol
		line    int
		want    string
	}{
		// The innermost enclosing definition, not the outermost.
		{"innermost enclosing symbol wins", goSymbols, 15, "registerV1"},
		{"outer symbol when nothing tighter", goSymbols, 35, "routes"},
		// A Python decorator sits ABOVE the function it decorates, so it is
		// enclosed by nothing and has to look down.
		{"decorator anchors to the function below", pySymbols, 7, "get_job"},
		// Six lines above the only symbol is past the lookahead window.
		{"nothing within reach stays empty", pySymbols, 1, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := anchorEndpoints([]APIEndpoint{{Line: tt.line}}, tt.symbols)
			if got[0].Symbol != tt.want {
				t.Fatalf("Symbol = %q, want %q", got[0].Symbol, tt.want)
			}
		})
	}
}

// Test fixtures are not a route table. Indexing them put strings like
// "DELETE /api/v1/rules/{}" — written inside backend/internal/graph's own
// apiedges_test.go — into the installation-wide match set, where they matched
// the real route in internal/api/server.go and produced a cross-repo "call"
// whose client node is a Go test function.
func TestIsTestFile(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"backend/internal/graph/apiedges_test.go", true},
		{"web/src/lib/queries/repos.test.ts", true},
		{"web/src/lib/queries/repos.spec.tsx", true},
		{"web/src/__tests__/routes.ts", true},
		{"service/tests/test_routes.py", true},
		{"service/routes_test.py", true},
		{"backend/internal/api/testdata/fixture.go", true},
		{"backend/internal/api/server.go", false},
		{"web/src/lib/queries/repos.ts", false},
		{"service/routes.py", false},
		// "latest.go" ends in "test.go" but is not a test file.
		{"internal/pipeline/latest.go", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := isTestFile(tt.path); got != tt.want {
				t.Fatalf("isTestFile(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

// The filter has to sit on the exported extractor, not on its one caller: this
// is the exact shape that put `client DELETE /api/v1/rules/{}` from this
// package's own test file into the installation-wide match set.
func TestExtractAPIEndpointsSkipsTestFiles(t *testing.T) {
	src := `package graph

func TestX(t *testing.T) {
	src := "..."
	_ = api.Delete("/api/v1/rules/{ruleID}")
	r.Get("/api/v1/repos/{repoID}", s.getRepo)
}
`
	if eps := ExtractAPIEndpoints("backend/internal/graph/apiedges_test.go", src); len(eps) != 0 {
		t.Fatalf("test fixtures entered the route table: %+v", eps)
	}
	// The same body in production code is still extracted.
	if eps := ExtractAPIEndpoints("backend/internal/graph/apiedges.go", src); len(eps) == 0 {
		t.Fatal("production code stopped being extracted")
	}
}

func TestMatchAPIEndpoints_GroundedSegmentFallback(t *testing.T) {
	client := APIEndpoint{Role: RoleClient, Method: "GET", Path: "/api/v1/jobs/42", RepoID: 2, NodeID: 20}
	server := APIEndpoint{Role: RoleServer, Method: "GET", Path: "/api/v1/jobs/{}", RepoID: 1, NodeID: 10}

	matches := MatchAPIEndpoints([]APIEndpoint{client, server})
	if len(matches) != 1 || matches[0].Client.NodeID != client.NodeID || matches[0].Server.NodeID != server.NodeID {
		t.Fatalf("unique path-pattern fallback = %+v, want client %d -> server %d", matches, client.NodeID, server.NodeID)
	}
}

func TestMatchAPIEndpoints_GroundedSegmentFallbackRejectsAmbiguity(t *testing.T) {
	client := APIEndpoint{Role: RoleClient, Method: "GET", Path: "/api/v1/jobs/42", RepoID: 3, NodeID: 30}
	servers := []APIEndpoint{
		{Role: RoleServer, Method: "GET", Path: "/api/v1/jobs/{}", RepoID: 1, NodeID: 10},
		{Role: RoleServer, Method: "GET", Path: "/api/v1/jobs/{}", RepoID: 2, NodeID: 20},
	}

	if matches := MatchAPIEndpoints(append(servers, client)); len(matches) != 0 {
		t.Fatalf("ambiguous fallback invented an edge: %+v", matches)
	}
}
