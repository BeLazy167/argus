package graph

import (
	"log/slog"
	"path"
	"regexp"
	"sort"
	"strings"
)

// Cross-repo API edges, by string matching only.
//
// The motivating case in issue #221: an API repo declares GET /api/v1/jobs/{id}
// and a web repo calls fetch(`/api/v1/jobs/${id}`). The two sides share no AST
// reference and no symbol identity, so neither the parsers in builder*.go nor a
// SCIP-style symbol index can ever produce that edge — the two halves have
// nothing in common except the string.
//
// The issue proposed proposing those edges with embedding similarity and
// confirming them with an LLM. This file deliberately does neither. No
// industrial code-intelligence system (Sourcegraph/SCIP, Glean, Kythe,
// stack-graphs) puts embeddings in the reference-resolution path, and
// Sourcegraph deprecated the one product that did. The server half of this
// problem — recovering a route table as (method, normalised path) — is
// recoverable by static analysis alone, so that is what runs first. Embedding
// proposal + LLM confirmation remain the documented fallback for what string
// matching misses; see the seam comment on MatchAPIEndpoints.
//
// Matching by path pattern inside ONE installation is what makes this viable
// where OpenAPI-spec matching is not: Wittern et al. had to discard 4,177 of
// 4,926 observed requests because spec matching needs an absolute base URL to
// resolve a host. We never resolve a host — we compare paths among repos that
// already share a tenant boundary.

// Endpoint roles. A server endpoint DECLARES a route; a client endpoint CALLS
// one. Both are stored in the same shape because matching is symmetric.
const (
	RoleServer = "server"
	RoleClient = "client"
)

// AnyMethod is the method for a registration that names no verb (Go's
// mux.HandleFunc("/x", h), Flask's @app.route with no methods= list). It
// matches any client method, because the handler really does serve them all.
const AnyMethod = "ANY"

// paramSegment is the canonical form every path parameter normalises to, so
// that "/jobs/:id" (chi/express), "/jobs/{id}" (chi/FastAPI), "/jobs/<int:id>"
// (Flask), "/jobs/[id]" (Next.js) and "/jobs/${id}" (JS template literal) all
// compare equal. The value is deliberately not a legal identifier in any of
// those syntaxes, so a literal path segment can never collide with it.
const paramSegment = "{}"

// wildcardSegment is the canonical form for catch-all segments ("*",
// "[...slug]", "{*rest}"). Kept distinct from paramSegment because a catch-all
// spans many segments and a single param spans exactly one — collapsing them
// would make "/files/*" equal to "/files/{}" and link a client that calls one
// to a route that serves the other.
const wildcardSegment = "*"

// endOfPathAnchor is Go 1.22's net/http end-of-path anchor. It is NOT a
// parameter: "GET /jobs/{$}" serves /jobs and refuses every child path.
const endOfPathAnchor = "{$}"

// APIEndpoint is one declared route or one call site, already normalised.
//
// Handler is the symbol NAMED in a route registration (chi's
// r.Get("/x", s.healthz) names healthz), which usually lives in another file.
// Symbol is filled later by anchorEndpoints from the enclosing definition. The
// two are kept apart because Handler is a lookup key across the whole repo
// while Symbol is a fact about this file.
// RepoID and NodeID are the DB identity of the endpoint. The extractors leave
// them zero — a parser has no idea what a repo id is — and they are filled only
// when an endpoint is read back out of api_endpoints for matching.
type APIEndpoint struct {
	Role     string
	Method   string
	Path     string // normalised pattern
	RawPath  string // as written — provenance, and what a UI should show
	FilePath string
	Line     int
	Handler  string
	Symbol   string
	RepoID   int64
	NodeID   int64
}

// APIMatch is a confirmed client→server link.
type APIMatch struct {
	Client APIEndpoint
	Server APIEndpoint
}

// httpVerbs maps a method-shaped call or decorator name to its canonical HTTP
// method. Lowercase keys only; callers lowercase before lookup.
var httpVerbs = map[string]string{
	"get": "GET", "post": "POST", "put": "PUT", "patch": "PATCH",
	"delete": "DELETE", "head": "HEAD", "options": "OPTIONS",
}

// NormalizeAPIPath converts a written path into the canonical pattern used for
// matching. Returns "" when the input is not a usable relative path.
//
// Absolute URLs are REJECTED, not stripped to their path. A client that calls
// https://api.stripe.com/v1/charges names a host we do not own, and matching it
// by path alone against a local /v1/charges route is exactly the false link
// this whole approach has to avoid. Relative URLs are the common case inside an
// installation and carry no host claim at all.
func NormalizeAPIPath(raw string) string {
	p := trimQueryAndFragment(strings.TrimSpace(raw))
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") {
		return ""
	}
	parts := strings.Split(p, "/")
	out := make([]string, 0, len(parts))
	for _, seg := range parts {
		if seg == "" {
			continue // collapses "//" and drops the trailing slash
		}
		if seg == endOfPathAnchor {
			// Go 1.22's "{$}" means the pattern matches ONLY the prefix and no
			// child segment at all. Normalising it as an ordinary parameter made
			// "GET /jobs/{$}" claim to serve /jobs/<anything>, so a client of
			// /jobs/${id} linked to a handler that 404s every URL it supposedly
			// served. Dropping the segment gives the path the anchor means.
			continue
		}
		out = append(out, normalizeSegment(seg))
	}
	if len(out) == 0 {
		return "/"
	}
	return "/" + strings.Join(out, "/")
}

// trimQueryAndFragment cuts a path at its query string or fragment, ignoring
// any '?' or '#' inside a ${...} template hole.
//
// A naive IndexAny truncates `/installations/${api.active?.id}/features` at the
// optional-chaining '?' — producing "/installations/${api.active", which is a
// path no server declares, so the call site silently stops matching. Optional
// chaining inside an interpolated id is ordinary TypeScript, so this is the
// common case, not an edge case.
func trimQueryAndFragment(p string) string {
	depth := 0
	for i := 0; i < len(p); i++ {
		switch {
		case p[i] == '$' && i+1 < len(p) && p[i+1] == '{':
			depth++
			i++
		case p[i] == '}' && depth > 0:
			depth--
		case (p[i] == '?' || p[i] == '#') && depth == 0:
			return p[:i]
		}
	}
	return p
}

// segmentParamRe recognises a segment that is ENTIRELY one parameter, in any of
// the framework syntaxes we index. Anchored on both ends so a literal segment
// that merely contains a brace is not silently turned into a parameter.
var segmentParamRe = regexp.MustCompile(`^(?::[\w.-]+|\{[^}]*\}|<[^>]*>|\[[^\]]*\])$`)

// interpolationRe recognises a JS/TS template hole or a Go format verb anywhere
// in a segment. "job-${id}" and "job-%s" are one parameter's worth of segment,
// so the whole segment normalises to a parameter — the server side writes them
// as "{name}" and would otherwise never match.
var interpolationRe = regexp.MustCompile(`\$\{[^}]*\}|%[sdvq]`)

func normalizeSegment(seg string) string {
	if seg == "*" || strings.HasPrefix(seg, "...") {
		return wildcardSegment
	}
	// Next.js optional catch-all, checked BEFORE segmentParamRe rather than
	// inside it. segmentParamRe's `\[[^\]]*\]$` alternative cannot consume the
	// inner ']' of "[[...slug]]", so the branch that used to live inside the
	// match was unreachable: the segment fell through every rule and was
	// returned verbatim, and an app/docs/[[...slug]]/route.ts handler was stored
	// under a pattern no client call can ever normalise to.
	if strings.HasPrefix(seg, "[[") && strings.HasSuffix(seg, "]]") {
		return wildcardSegment
	}
	if segmentParamRe.MatchString(seg) {
		inner := seg[1 : len(seg)-1]
		// "[...slug]" (Next.js), "{*rest}" (chi/gin), "{id...}" (Go 1.22
		// multi-segment wildcard) and "<path:rest>" (Flask) all span more than
		// one segment, so they are catch-alls rather than parameters.
		if strings.HasPrefix(inner, "...") || strings.HasSuffix(inner, "...") ||
			strings.HasPrefix(inner, "*") || strings.HasPrefix(inner, "path:") {
			return wildcardSegment
		}
		return paramSegment
	}
	if interpolationRe.MatchString(seg) {
		return paramSegment
	}
	return seg
}

// isMatchablePath rejects patterns too generic to identify one API.
//
// Two conditions, and both are precision guards rather than taste:
//
//   - at least one LITERAL segment, so "/{}/{}"  cannot match everything;
//   - at least two segments, so "/health", "/metrics", "/" — the paths every
//     unrelated service in an installation independently defines — cannot link
//     a web repo to a service it has never called.
//
// The cost is that a genuinely shared single-segment endpoint is missed. That
// is the right trade: a missed edge is a smaller graph, a wrong edge silently
// claims an unrelated repo is affected.
func isMatchablePath(p string) bool {
	if p == "" || p == "/" {
		return false
	}
	segs := strings.Split(strings.TrimPrefix(p, "/"), "/")
	if len(segs) < 2 {
		return false
	}
	for _, s := range segs {
		if s != paramSegment && s != wildcardSegment {
			return true
		}
	}
	return false
}

// MatchAPIEndpoints links every client call to every server route that declares
// the same (method, normalised path) within the given endpoint set.
//
// The caller is responsible for the tenant boundary: pass endpoints from ONE
// installation. Repos inside an installation may link; across installations
// must never, and that predicate lives in the store query that feeds this.
//
// Same-repo pairs are dropped HERE, in the exported function, not in a caller.
// A client and a route in one repository are already connected by the parsed
// graph, and an inferred edge there would silently change what today's
// repo-scoped blast radius returns. Cross-repo edges are invisible to that
// query (it filters repo_id inside the recursion), so this slice cannot alter
// any existing answer. The rule lives on the function that documents it
// because this is the extension seam below: a second caller written against
// this contract must not be able to produce same-repo inferred edges by
// omitting a check it was told had already run.
//
// The result is bounded by maxInferredEdgesPerRun. Matching is a cross product
// — N call sites for one path against M declarations of it — so a generated
// client that repeats a call thousands of times would otherwise materialise
// N*M matches in memory before any caller could cap the WRITES. This runs in a
// background goroutine on a VM that has already OOM'd once (see indexFileSet).
//
// THE SEAM: everything unmatched after this returns is what embedding proposal
// plus LLM confirmation would attach to — take the leftover client endpoints,
// propose candidate servers by similarity, confirm, and append APIMatch values
// to this result. Nothing downstream needs to change, because a proposed match
// is persisted exactly like a string match. That path is NOT built; it is the
// documented fallback for what string matching misses, not the primary
// mechanism.
func MatchAPIEndpoints(endpoints []APIEndpoint) []APIMatch {
	type key struct{ method, path string }
	servers := make(map[key][]APIEndpoint)
	anyServers := make(map[string][]APIEndpoint)
	for _, e := range endpoints {
		if e.Role != RoleServer || !isMatchablePath(e.Path) {
			continue
		}
		if e.Method == AnyMethod {
			anyServers[e.Path] = append(anyServers[e.Path], e)
			continue
		}
		servers[key{e.Method, e.Path}] = append(servers[key{e.Method, e.Path}], e)
	}

	var out []APIMatch
	for _, c := range endpoints {
		if c.Role != RoleClient || !isMatchablePath(c.Path) {
			continue
		}
		candidates := append([]APIEndpoint{}, servers[key{c.Method, c.Path}]...)
		candidates = append(candidates, anyServers[c.Path]...)
		if len(candidates) == 0 {
			candidates = uniqueCompatibleServer(c, endpoints)
		}
		for _, s := range candidates {
			// Cross-repo only. This subsumes the self-match case as well: a
			// route and a call on the same line are in the same repository by
			// definition, so no (file, line) guard is needed — and the guard
			// that existed compared file paths without repo ids, which silently
			// discarded a legitimate match between two repos that share a file
			// path (mirrors, forks, monorepo splits).
			if s.RepoID == c.RepoID {
				continue
			}
			if len(out) >= maxInferredEdgesPerRun {
				slog.Warn("graph: api match cross product hit the cap, truncating",
					"cap", maxInferredEdgesPerRun, "method", c.Method, "path", c.Path)
				return out
			}
			out = append(out, APIMatch{Client: c, Server: s})
		}
	}
	return out
}

// uniqueCompatibleServer is the grounded fallback for an unmatched client.
// A concrete client segment may match a declared server parameter, but an edge
// is emitted only when exactly one cross-repository declaration fits. This
// recovers calls such as /jobs/42 -> /jobs/{} without guessing between services.
func uniqueCompatibleServer(client APIEndpoint, endpoints []APIEndpoint) []APIEndpoint {
	var match APIEndpoint
	found := false
	for _, server := range endpoints {
		if server.Role != RoleServer || server.RepoID == client.RepoID {
			continue
		}
		if server.Method != AnyMethod && server.Method != client.Method {
			continue
		}
		if !serverPatternMatchesClient(server.Path, client.Path) {
			continue
		}
		if found && (match.RepoID != server.RepoID || match.NodeID != server.NodeID) {
			return nil
		}
		match = server
		found = true
	}
	if !found {
		return nil
	}
	return []APIEndpoint{match}
}

func serverPatternMatchesClient(serverPath, clientPath string) bool {
	server := strings.Split(strings.TrimPrefix(serverPath, "/"), "/")
	client := strings.Split(strings.TrimPrefix(clientPath, "/"), "/")
	if len(server) == 0 || len(client) == 0 {
		return false
	}
	usedPattern := false
	for i, segment := range server {
		if segment == wildcardSegment {
			if i >= len(client) {
				return false
			}
			for _, remaining := range client[i:] {
				if remaining == paramSegment || remaining == wildcardSegment {
					return false
				}
			}
			return true
		}
		if i >= len(client) {
			return false
		}
		if segment == paramSegment {
			if client[i] == paramSegment || client[i] == wildcardSegment {
				return false
			}
			usedPattern = true
			continue
		}
		if segment != client[i] {
			return false
		}
	}
	return usedPattern && len(server) == len(client)
}

// ExtractAPIEndpoints recovers the route table and the client call sites of one
// source file. Dispatch is per language because the discriminator between "this
// declares a route" and "this calls one" is language-specific — see each
// extractor.
func ExtractAPIEndpoints(filePath, content string) []APIEndpoint {
	if len(content) > maxContentBytes {
		return nil
	}
	// Test files declare no route table and call no API. A fixture string
	// entered the installation-wide match set exactly like production code:
	// backend/internal/graph/apiedges_test.go alone contributed
	// `client DELETE /api/v1/rules/{}`, which matches the real route in
	// internal/api/server.go, so a Go test function became the client end of a
	// cross-repo "call" that exists only inside a string. In an installation
	// where a test suite stubs another service's URLs, every stub became a
	// claimed dependency. Filtered HERE rather than at the one call site, so no
	// future caller of the exported extractor has to remember.
	if isTestFile(filePath) {
		return nil
	}
	lang := langForFile(filePath)
	// Comments are blanked before anything reads the text. The extractors are
	// regex/line based and have no notion of a comment, so
	// `// r.Get("/api/v1/legacy/thing", h.Legacy)` was extracted as a real
	// route and the JSDoc example `getApi(ctx).get("/api/v1/repos")` in
	// web/src/lib/query-kit.ts as a real client call. A repo that only
	// DOCUMENTS or has commented out a call then gets an inferred cross-repo
	// edge asserting a dependency that does not exist, and a commented-out
	// route becomes a route that live clients in other repos link to.
	content = stripComments(lang, content)
	var eps []APIEndpoint
	switch lang {
	case "go":
		eps = extractGoEndpoints(filePath, content)
	case "typescript", "javascript":
		eps = extractJSEndpoints(filePath, content)
	case "python":
		eps = extractPythonEndpoints(filePath, content)
	default:
		return nil
	}
	out := eps[:0]
	for _, e := range eps {
		if isMatchablePath(e.Path) {
			out = append(out, e)
		}
	}
	return out
}

// testFileMarkers are the path fragments that mark a file as test code in the
// languages the endpoint extractors cover.
var testFileMarkers = []string{
	"_test.go", ".test.ts", ".test.tsx", ".test.js", ".test.jsx",
	".spec.ts", ".spec.tsx", ".spec.js", ".spec.jsx",
	"_test.py", "__tests__/", "__mocks__/", "/testdata/",
}

// isTestFile reports whether a path is test code, whose API strings are
// fixtures rather than a route table.
func isTestFile(filePath string) bool {
	p := strings.ToLower(filePath)
	for _, m := range testFileMarkers {
		if strings.Contains(p, m) {
			return true
		}
	}
	return strings.HasPrefix(path.Base(p), "test_") && strings.HasSuffix(p, ".py")
}

// --- shared lexing ---

// stripComments replaces every comment byte with a space, keeping newlines and
// every byte offset intact so reported line numbers and the brace-depth stack
// are unaffected.
//
// Two failures it prevents, both observed on this repo's own tree:
//
//   - a commented-out registration or call becomes a real route-table row, and
//     an inferred cross-repo edge then asserts a dependency on dead code;
//   - a comment carrying an unbalanced brace shifts braceDelta's depth and pops
//     the chi Route frame early, after which every remaining route in the file
//     is recorded under the wrong prefix and silently stops matching.
//
// String literals are skipped so a path containing "//" is not mistaken for a
// comment. The one shape this loses is a JS regex literal ending in an escaped
// slash followed by the closing slash (`/x\//`), which reads as a line comment
// — a narrower cost than treating every doc comment as code.
func stripComments(lang, content string) string {
	var slashComments, backtick, hashComments, tripleQuotes bool
	switch lang {
	case "go":
		slashComments, backtick = true, true
	case "typescript", "javascript":
		slashComments, backtick = true, true
	case "python":
		hashComments, tripleQuotes = true, true
	default:
		return content
	}

	b := []byte(content)
	blank := func(i int) {
		if b[i] != '\n' {
			b[i] = ' '
		}
	}
	blankUntilNewline := func(i int) int {
		for i < len(b) && b[i] != '\n' {
			b[i] = ' '
			i++
		}
		return i
	}
	blankUntil := func(i int, closer string) int {
		for i < len(b) {
			if i+len(closer) <= len(b) && string(b[i:i+len(closer)]) == closer {
				for k := 0; k < len(closer); k++ {
					blank(i + k)
				}
				return i + len(closer)
			}
			blank(i)
			i++
		}
		return i
	}

	for i := 0; i < len(b); {
		c := b[i]
		switch {
		case slashComments && c == '/' && i+1 < len(b) && b[i+1] == '/':
			i = blankUntilNewline(i)
		case slashComments && c == '/' && i+1 < len(b) && b[i+1] == '*':
			blank(i)
			blank(i + 1)
			i = blankUntil(i+2, "*/")
		case hashComments && c == '#':
			i = blankUntilNewline(i)
		case tripleQuotes && (c == '"' || c == '\'') && i+2 < len(b) && b[i+1] == c && b[i+2] == c:
			// A Python docstring is the doc-comment of the language, and a
			// route example inside one is documentation, not a route.
			q := string([]byte{c, c, c})
			blank(i)
			blank(i + 1)
			blank(i + 2)
			i = blankUntil(i+3, q)
		case c == '"' || c == '\'' || (backtick && c == '`'):
			i++
			for i < len(b) {
				if b[i] == '\\' && c != '`' {
					i += 2
					continue
				}
				if b[i] == c {
					i++
					break
				}
				if b[i] == '\n' && c != '`' {
					break // an unterminated single-line literal: resync
				}
				i++
			}
		default:
			i++
		}
	}
	return string(b)
}

// readStringLiteral reads the literal starting at the first non-space rune at
// or after i. Handles ", ', and JS backticks (including ${...} holes, whose
// braces are tracked so a nested backtick does not end the literal early).
// Returns the inner text and the index just past the closing quote.
func readStringLiteral(s string, i int) (string, int, bool) {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n') {
		i++
	}
	if i >= len(s) {
		return "", i, false
	}
	quote := s[i]
	if quote != '"' && quote != '\'' && quote != '`' {
		return "", i, false
	}
	var b strings.Builder
	depth := 0
	for j := i + 1; j < len(s); j++ {
		c := s[j]
		if c == '\\' {
			j++
			continue
		}
		if quote == '`' {
			if c == '$' && j+1 < len(s) && s[j+1] == '{' {
				depth++
			} else if c == '}' && depth > 0 {
				depth--
			}
		}
		if c == quote && depth == 0 {
			return b.String(), j + 1, true
		}
		if c == '\n' && quote != '`' {
			return "", i, false
		}
		b.WriteByte(c)
	}
	return "", i, false
}

// argAfterLiteral reports what follows the literal that ended at index i: the
// rune kind of the next argument, and whether one exists at all.
//
//	')' — the call ends here, so the literal was the only argument
//	'{' — an options object follows (fetch init, axios config)
//	'f' — a function or identifier follows (a handler)
//
// This is the discriminator for Go: chi's r.Get(path, handler) always passes a
// handler, an HTTP client's c.Get(url) never does.
func argAfterLiteral(s string, i int) byte {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n') {
		i++
	}
	if i >= len(s) {
		return ')'
	}
	if s[i] != ',' {
		return ')'
	}
	i++
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n') {
		i++
	}
	if i >= len(s) {
		return ')'
	}
	switch s[i] {
	case ')':
		return ')'
	case '{':
		return '{'
	default:
		return 'f'
	}
}

// identAfterLiteral returns the dotted identifier that follows the literal
// ending at i, reduced to its last segment. "s.healthz" → "healthz". Empty when
// the next argument is not a plain identifier (an inline closure, for example).
func identAfterLiteral(s string, i int) string {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == ',') {
		i++
	}
	start := i
	for i < len(s) && (isIdentByte(s[i]) || s[i] == '.') {
		i++
	}
	if i == start {
		return ""
	}
	name := s[start:i]
	if k := strings.LastIndexByte(name, '.'); k >= 0 {
		name = name[k+1:]
	}
	return name
}

func isIdentByte(c byte) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// verbCallRe matches `recv.Verb(` and `recv.verb<T>(`. The receiver may end in
// ')' so that getApi(ctx).get<T>(...) — the shape this codebase's own web
// client uses — is recognised. Group 1 is the receiver token, group 2 the verb.
var verbCallRe = regexp.MustCompile(`([A-Za-z_$][\w$]*|\))\s*\.\s*([A-Za-z]+)(?:<[^<>()]*>)?\s*\(`)

// fetchCallRe matches a bare fetch(...) call, including the wrappers this repo
// uses (fetchWithTrace).
var fetchCallRe = regexp.MustCompile(`\b(?:fetch|fetchWithTrace)\s*\(`)

// methodOptionRe finds the method named in a fetch init object.
var methodOptionRe = regexp.MustCompile(`\bmethod\s*:\s*["'` + "`" + `]([A-Za-z]+)`)

// --- Go ---

// goPrefixRe matches the chi nesting forms that push a path prefix:
// r.Route("/api/v1", func(r chi.Router) { ... }) and r.Mount("/x", sub).
var goPrefixRe = regexp.MustCompile(`\.\s*(Route|Mount)\s*\(`)

// goMethodCallRe matches r.Method("GET", "/x", h) and r.MethodFunc(...), where
// the verb is a string argument rather than the function name.
var goMethodCallRe = regexp.MustCompile(`\.\s*(Method|MethodFunc)\s*\(`)

// goHandleRe matches net/http's mux.Handle / mux.HandleFunc, whose pattern may
// carry a method since Go 1.22 ("POST /jobs/{id}").
var goHandleRe = regexp.MustCompile(`\.\s*(Handle|HandleFunc)\s*\(`)

// extractGoEndpoints reads a Go route table, following chi's Route/Mount
// nesting so a handler registered inside r.Route("/api/v1", ...) gets its full
// path. Without the prefix stack every route in this repo's own server.go would
// normalise to "/me" or "/repos" and match nothing.
//
// Server vs client is decided by argument count: a route registration always
// passes a handler after the path, an HTTP client call never does.
//
// The ARGUMENTS are read from the whole file, not from one line. gofmt wraps a
// long registration, and a line-local reader saw `r.Get("/repos/{repoID}/config",`
// end with nothing after the comma — which argAfterLiteral reported as "no
// second argument", i.e. a client call. That produced two wrong rows from one
// input: the real route vanished from the table so every legitimate client of
// it stopped matching, and a phantom client endpoint was recorded in the SERVER
// repo, ready to link this repo to whoever else declares that path. Only the
// chi prefix stack stays line-driven, because block nesting is a line fact.
func extractGoEndpoints(filePath, content string) []APIEndpoint {
	lines := strings.Split(content, "\n")

	// prefixByLine[i] is the chi Route/Mount prefix in force on line i.
	prefixByLine := make([]string, len(lines))
	lineStarts := make([]int, len(lines))
	type frame struct {
		prefix string
		depth  int
	}
	var stack []frame
	depth, off := 0, 0
	prefix := func() string {
		if len(stack) == 0 {
			return ""
		}
		return stack[len(stack)-1].prefix
	}
	for n, line := range lines {
		lineStarts[n] = off
		off += len(line) + 1

		if m := goPrefixRe.FindStringIndex(line); m != nil {
			if raw, _, ok := readStringLiteral(line, m[1]); ok {
				stack = append(stack, frame{prefix: joinRoutePath(prefix(), raw), depth: depth})
			}
		}
		prefixByLine[n] = prefix()

		depth += braceDelta(line)
		for len(stack) > 0 && depth <= stack[len(stack)-1].depth {
			stack = stack[:len(stack)-1]
		}
	}
	at := func(offset int) (int, string) {
		n := sort.SearchInts(lineStarts, offset+1) - 1
		if n < 0 {
			n = 0
		}
		return n + 1, prefixByLine[n]
	}

	var out []APIEndpoint

	for _, m := range goMethodCallRe.FindAllStringIndex(content, -1) {
		verb, next, ok := readStringLiteral(content, m[1])
		if !ok {
			continue
		}
		raw, after, ok := readStringLiteral(content, skipComma(content, next))
		if !ok {
			continue
		}
		lineNo, pfx := at(m[0])
		out = append(out, APIEndpoint{
			Role: RoleServer, Method: canonicalMethod(verb),
			Path: NormalizeAPIPath(joinRoutePath(pfx, raw)), RawPath: raw,
			FilePath: filePath, Line: lineNo, Handler: identAfterLiteral(content, after),
		})
	}

	for _, m := range goHandleRe.FindAllStringIndex(content, -1) {
		raw, after, ok := readStringLiteral(content, m[1])
		if !ok {
			continue
		}
		method, p := splitMuxPattern(raw)
		lineNo, pfx := at(m[0])
		out = append(out, APIEndpoint{
			Role: RoleServer, Method: method,
			Path: NormalizeAPIPath(joinRoutePath(pfx, p)), RawPath: raw,
			FilePath: filePath, Line: lineNo, Handler: identAfterLiteral(content, after),
		})
	}

	for _, m := range verbCallRe.FindAllStringSubmatchIndex(content, -1) {
		verb, ok := httpVerbs[strings.ToLower(content[m[4]:m[5]])]
		if !ok {
			continue
		}
		raw, after, ok := readStringLiteral(content, m[1])
		if !ok {
			continue
		}
		lineNo, pfx := at(m[0])
		if argAfterLiteral(content, after) == ')' {
			// One argument: an HTTP client call, not a registration.
			out = append(out, APIEndpoint{
				Role: RoleClient, Method: verb, Path: NormalizeAPIPath(raw), RawPath: raw,
				FilePath: filePath, Line: lineNo,
			})
			continue
		}
		out = append(out, APIEndpoint{
			Role: RoleServer, Method: verb,
			Path: NormalizeAPIPath(joinRoutePath(pfx, raw)), RawPath: raw,
			FilePath: filePath, Line: lineNo, Handler: identAfterLiteral(content, after),
		})
	}
	return out
}

// skipComma advances past one comma so a second literal argument can be read.
// Newlines count as skippable space: gofmt puts each argument of a wrapped
// r.Method("GET", "/x", h) on its own line.
func skipComma(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n') {
		i++
	}
	if i < len(s) && s[i] == ',' {
		i++
	}
	return i
}

// splitMuxPattern splits Go 1.22's "METHOD /path" ServeMux pattern. A pattern
// with no method serves every method, which is what AnyMethod means.
func splitMuxPattern(pat string) (string, string) {
	pat = strings.TrimSpace(pat)
	if i := strings.IndexByte(pat, ' '); i > 0 {
		verb := canonicalMethod(pat[:i])
		if verb != AnyMethod {
			return verb, strings.TrimSpace(pat[i+1:])
		}
	}
	return AnyMethod, pat
}

func canonicalMethod(v string) string {
	if m, ok := httpVerbs[strings.ToLower(strings.TrimSpace(v))]; ok {
		return m
	}
	return AnyMethod
}

// joinRoutePath concatenates a nesting prefix with a leaf path. chi writes
// r.Route("/api/v1", ...) + r.Get("/me") and means /api/v1/me; it also allows
// r.Get("/") inside a Route, which means the prefix itself.
func joinRoutePath(prefix, leaf string) string {
	switch {
	case prefix == "":
		return leaf
	case leaf == "" || leaf == "/":
		return prefix
	}
	return strings.TrimSuffix(prefix, "/") + "/" + strings.TrimPrefix(leaf, "/")
}

// stringLiteralRe blanks literals before brace counting, so a chi path such as
// "/repos/{repoID}" cannot be mistaken for a block delimiter.
var stringLiteralRe = regexp.MustCompile("\"(?:[^\"\\\\]|\\\\.)*\"|'(?:[^'\\\\]|\\\\.)*'|`[^`]*`")

func braceDelta(line string) int {
	code := stringLiteralRe.ReplaceAllString(line, `""`)
	return strings.Count(code, "{") - strings.Count(code, "}")
}

// --- TypeScript / JavaScript ---

// expressBindingRe finds the variables an Express app or router is bound to:
// const app = express(), const router = express.Router(), const r = Router().
var expressBindingRe = regexp.MustCompile(`(?m)\b(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*(?::[^=]+)?=\s*(?:express\s*\(|express\s*\.\s*Router\s*\(|Router\s*\()`)

// nextRouteFileRe recognises a Next.js App Router handler file. The URL is the
// directory path, so the route table is recovered from the FILE PATH rather
// than from any call — the most deterministic server signal there is.
var nextRouteFileRe = regexp.MustCompile(`(?:^|/)app/(.*/)?route\.(?:ts|tsx|js|jsx|mjs)$`)

// nextHandlerRe finds the exported HTTP handlers of such a file.
//
// Leading indentation is [ \t]* rather than \s*, which would also match the
// newlines of preceding blank lines and report the endpoint on the wrong line —
// and a line number that points above the definition breaks the anchoring in
// anchorEndpoints.
var nextHandlerRe = regexp.MustCompile(`(?m)^[ \t]*export\s+(?:async\s+)?(?:function\s+|const\s+)(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\b`)

// extractJSEndpoints reads TS/JS routes and calls.
//
// The discriminator here is NOT argument count. axios.post(url, body) and
// app.post(path, handler) are the same shape, so counting arguments would file
// every axios POST in the codebase as a declared route. Instead a `recv.verb(`
// call is a route only when recv is bound to an Express app or router IN THIS
// FILE — a fact the file itself states — and is otherwise a client call.
func extractJSEndpoints(filePath, content string) []APIEndpoint {
	var out []APIEndpoint

	if m := nextRouteFileRe.FindStringSubmatch(filePath); m != nil {
		p := nextRoutePath(m[1])
		for _, h := range nextHandlerRe.FindAllStringSubmatchIndex(content, -1) {
			verb := content[h[2]:h[3]]
			out = append(out, APIEndpoint{
				Role: RoleServer, Method: verb, Path: NormalizeAPIPath(p), RawPath: p,
				FilePath: filePath, Line: lineAt(content, h[0]), Handler: verb,
			})
		}
	}

	servers := make(map[string]bool)
	for _, m := range expressBindingRe.FindAllStringSubmatch(content, -1) {
		servers[m[1]] = true
	}

	for _, m := range verbCallRe.FindAllStringSubmatchIndex(content, -1) {
		verb, ok := httpVerbs[strings.ToLower(content[m[4]:m[5]])]
		if !ok {
			continue
		}
		raw, after, ok := readStringLiteral(content, m[1])
		if !ok {
			continue
		}
		recv := content[m[2]:m[3]]
		role := RoleClient
		handler := ""
		if servers[recv] {
			role = RoleServer
			handler = identAfterLiteral(content, after)
		}
		out = append(out, APIEndpoint{
			Role: role, Method: verb, Path: NormalizeAPIPath(raw), RawPath: raw,
			FilePath: filePath, Line: lineAt(content, m[0]), Handler: handler,
		})
	}

	for _, m := range fetchCallRe.FindAllStringIndex(content, -1) {
		raw, after, ok := readStringLiteral(content, m[1])
		if !ok {
			continue
		}
		method := "GET" // fetch defaults to GET when init names no method
		tail := content[after:min(after+400, len(content))]
		if mm := methodOptionRe.FindStringSubmatch(tail); mm != nil {
			method = canonicalMethod(mm[1])
		}
		out = append(out, APIEndpoint{
			Role: RoleClient, Method: method, Path: NormalizeAPIPath(raw), RawPath: raw,
			FilePath: filePath, Line: lineAt(content, m[0]),
		})
	}
	return out
}

// nextRoutePath turns the directory chain between app/ and route.ts into a URL.
// Route groups — "(dashboard)" — are organisational and contribute no segment.
func nextRoutePath(dir string) string {
	segs := strings.Split(strings.Trim(dir, "/"), "/")
	out := make([]string, 0, len(segs))
	for _, s := range segs {
		if s == "" || (strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")")) {
			continue
		}
		out = append(out, s)
	}
	return "/" + path.Join(out...)
}

// --- Python ---

// pyDecoratorRe matches FastAPI/Flask route decorators: @app.get("/x"),
// @router.post("/x"), @app.route("/x", methods=["POST"]).
// Indentation is [ \t]* for the same reason as nextHandlerRe: \s* swallows the
// newlines of blank lines above the decorator, and the reported line then sits
// above the decorator instead of on it.
var pyDecoratorRe = regexp.MustCompile(`(?m)^[ \t]*@[ \t]*[\w.]*\.[ \t]*(get|post|put|patch|delete|head|options|route|api_route)\s*\(`)

// pyMethodsKwargRe reads the methods= list of a Flask @route decorator.
var pyMethodsKwargRe = regexp.MustCompile(`methods\s*=\s*\[([^\]]*)\]`)

// extractPythonEndpoints treats decorators as routes and verb calls as client
// calls. Python web frameworks register routes with decorators, never with
// requests.get(...) — so unlike Go there is no argument-count ambiguity, and
// unlike JS no binding lookup is needed.
func extractPythonEndpoints(filePath, content string) []APIEndpoint {
	var out []APIEndpoint

	for _, m := range pyDecoratorRe.FindAllStringSubmatchIndex(content, -1) {
		name := content[m[2]:m[3]]
		raw, after, ok := readStringLiteral(content, m[1])
		if !ok {
			continue
		}
		line := lineAt(content, m[0])
		methods := []string{canonicalMethod(name)}
		if name == "route" || name == "api_route" {
			methods = pyDecoratorMethods(content, after)
		}
		for _, verb := range methods {
			out = append(out, APIEndpoint{
				Role: RoleServer, Method: verb, Path: NormalizeAPIPath(raw), RawPath: raw,
				FilePath: filePath, Line: line,
			})
		}
	}

	for _, m := range verbCallRe.FindAllStringSubmatchIndex(content, -1) {
		verb, ok := httpVerbs[strings.ToLower(content[m[4]:m[5]])]
		if !ok {
			continue
		}
		if strings.HasSuffix(strings.TrimRight(content[:m[0]], " \t"), "@") {
			continue // already recorded as a route by the decorator pass
		}
		raw, _, ok := readStringLiteral(content, m[1])
		if !ok {
			continue
		}
		out = append(out, APIEndpoint{
			Role: RoleClient, Method: verb, Path: NormalizeAPIPath(raw), RawPath: raw,
			FilePath: filePath, Line: lineAt(content, m[0]),
		})
	}
	return out
}

// pyDecoratorMethods reads the methods= kwarg of a @route decorator. Flask
// defaults to GET when the kwarg is absent, which is what the caller relies on
// — NOT AnyMethod, because a Flask route without methods= genuinely refuses a
// POST and an ANY route would claim to serve it.
func pyDecoratorMethods(content string, after int) []string {
	tail := content[after:min(after+300, len(content))]
	m := pyMethodsKwargRe.FindStringSubmatch(tail)
	if m == nil {
		return []string{"GET"}
	}
	var out []string
	for _, part := range strings.Split(m[1], ",") {
		v := canonicalMethod(strings.Trim(strings.TrimSpace(part), `"'`))
		if v != AnyMethod {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return []string{"GET"}
	}
	return out
}

// anchorEndpoints attaches each endpoint to the symbol that owns it, so the
// edge can be written between code_nodes rows.
//
// Two rules, in order:
//
//  1. the innermost symbol whose line range contains the endpoint;
//  2. failing that, the nearest symbol starting within decoratorLookahead lines
//     BELOW it — a Python decorator sits above the function it decorates, so it
//     is contained by nothing.
//
// Endpoints that anchor to neither keep an empty Symbol and are dropped by the
// caller rather than anchored to a synthetic node: a route we cannot place in
// the graph is better absent than attached to something it does not describe.
func anchorEndpoints(eps []APIEndpoint, symbols []Symbol) []APIEndpoint {
	const decoratorLookahead = 5
	out := make([]APIEndpoint, 0, len(eps))
	for _, e := range eps {
		best := ""
		bestSpan := 1 << 30
		for _, s := range symbols {
			if s.LineStart <= e.Line && e.Line <= s.LineEnd {
				if span := s.LineEnd - s.LineStart; span < bestSpan {
					bestSpan, best = span, s.Name
				}
			}
		}
		if best == "" {
			bestGap := decoratorLookahead + 1
			for _, s := range symbols {
				if gap := s.LineStart - e.Line; gap >= 0 && gap < bestGap {
					bestGap, best = gap, s.Name
				}
			}
		}
		e.Symbol = best
		out = append(out, e)
	}
	return out
}
