package pipeline

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/memory/memorytest"
)

// The shape fix is three wiring changes, one per side of the comparison, and
// every one of them is a single expression that compiles fine when deleted.
// TestFindingTextRoundTripsThroughPostedBody proves the FUNCTIONS agree; the
// tests below prove the CALL SITES use them, which is the part that was
// missing — the whole fix could be reverted with the suite green.

// captureQueries records every memory query the enricher issues, so a test can
// assert on the exact text the dismissal leg searched with.
type queryCapture struct {
	mu sync.Mutex
	q  []memory.MemoryQuery
}

func (c *queryCapture) search(q memory.MemoryQuery) ([]memory.PatternMatch, error) {
	c.mu.Lock()
	c.q = append(c.q, q)
	c.mu.Unlock()
	return nil, nil
}

// dismissalQueries returns the queries issued against dismissed feedback.
func (c *queryCapture) dismissalQueries() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, q := range c.q {
		if q.Type == memory.TypeFeedback {
			out = append(out, q.Query)
		}
	}
	return out
}

// READ SIDE. The enricher must query dismissals with commentTitle(c), not
// c.Body. The two differ on exactly the findings that matter: `what` is one
// sentence while `Body` carries the impact prose and, for 6 of 200 live
// findings, a whole "Context:\ndiff --git …" blob. Querying with the body pairs
// a diff-dominated embedding against one-sentence dismissal documents — one
// such body scored 0.9455 against an UNRELATED finding on the same file.
//
// Revert `dismissalQuery := commentTitle(*c)` to `c.Body` and this fails.
func TestEnricherQueriesDismissalsWithTheStatement(t *testing.T) {
	qc := &queryCapture{}
	c := FileComment{
		Severity: SeverityWarning, Category: CategoryBug, Line: 10,
		What: "The retry loop never resets the counter",
		Body: "The retry loop never resets the counter\nContext:\n" +
			"diff --git a/internal/worker/retry.go b/internal/worker/retry.go\n" +
			"@@ -18,7 +18,7 @@\n-\tattempts = 0\n",
	}
	e := newTestEnricher(&memorytest.Fake{SearchFn: qc.search}, &fakeEnrichStore{})

	enrichComments(e, []FileComment{c})

	got := qc.dismissalQueries()
	if len(got) != 1 {
		t.Fatalf("dismissal searches = %d, want 1: %q", len(got), got)
	}
	if want := commentTitle(c); got[0] != want {
		t.Errorf("the dismissal query is not the finding statement\n  got:  %q\n  want: %q", got[0], want)
	}
	if strings.Contains(got[0], "diff --git") {
		t.Errorf("the dismissal query embedded the diff blob: %q", got[0])
	}
}

// A finding whose `what` is empty falls back to the body, and the query must
// still be the normalised statement rather than the raw multi-line body — the
// legacy shape the whole path exists to stop comparing against.
func TestEnricherDismissalQueryIsNormalizedWithoutWhat(t *testing.T) {
	qc := &queryCapture{}
	c := FileComment{
		Severity: SeverityWarning, Category: CategoryBug, Line: 4,
		Body: "The guard is inverted\n\nEvery request is admitted.",
	}
	e := newTestEnricher(&memorytest.Fake{SearchFn: qc.search}, &fakeEnrichStore{})

	enrichComments(e, []FileComment{c})

	got := qc.dismissalQueries()
	if len(got) != 1 || got[0] != "The guard is inverted" {
		t.Errorf("dismissal queries = %q, want [\"The guard is inverted\"]", got)
	}
}

// WRITE SIDE, orchestrator. Praise is learned straight off a live FileComment,
// so its statement comes from commentTitle. A multi-line body stored raw would
// sit in the same container as the one-line documents every other writer
// produces.
//
// Revert `OriginalBody: commentTitle(c)` to `c.Body` and this fails.
func TestLearnPositivePatternsStoresTheStatement(t *testing.T) {
	fake := &memorytest.Fake{}
	c := FileComment{
		Severity: SeverityPraise, Category: CategoryBug, Line: 15,
		Body: "Good edge-case handling\nContext:\n" +
			"diff --git a/handler.go b/handler.go\n@@ -1,3 +1,7 @@\n+\tif len(items) == 0 {\n",
	}
	run := &PipelineRun{
		LearnPatterns: true,
		Indexer:       fake,
		FileReviews:   []FileReview{{Path: "handler.go", Comments: []FileComment{c}}},
	}
	o := &Orchestrator{logger: slog.Default()}

	if got := o.learnPositivePatterns(t.Context(), run, "acme", "widget"); got != 1 {
		t.Fatalf("indexed = %d, want 1", got)
	}
	if len(fake.Feedback) != 1 {
		t.Fatalf("feedback signals = %d, want 1", len(fake.Feedback))
	}
	if got, want := fake.Feedback[0].OriginalBody, commentTitle(c); got != want {
		t.Errorf("stored body is not the finding statement\n  got:  %q\n  want: %q", got, want)
	}
}

// WRITE SIDE, reaction and reply. Those two handlers build their FeedbackMemory
// from a review_comments row — the RENDERED GitHub body — and reach it through
// *store.Store and *github.Client, which no unit test can drive. What can be
// checked without a database is the property that actually broke: every
// FeedbackMemory in this package must set OriginalBody from a normaliser, never
// from a raw body.
//
// This is deliberately a source-level check. The defect being pinned is not
// "the function is wrong" (the round-trip test covers that) but "a call site
// stopped calling it", and a call site can stop calling it in three places
// today and in a fourth tomorrow. Reverting reactions.go or reply.go to
// `comment.Body` fails here, and so does a new writer that never got the memo.
func TestFeedbackMemorySitesStoreTheStatement(t *testing.T) {
	// The functions that turn arbitrary finding text into the one shape every
	// feedback document is stored in. Keep in sync with findingStatement's
	// callers; adding a third normaliser means adding it here.
	normalizers := map[string]bool{
		"FindingTextFromPostedBody": true, // a body read back out of Postgres
		"commentTitle":              true, // a live FileComment
	}

	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading package dir: %v", err)
	}

	sites := 0
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok || !isFeedbackMemoryLit(lit) {
				return true
			}
			sites++
			for _, elt := range lit.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok || key.Name != "OriginalBody" {
					continue
				}
				if fn := calleeName(kv.Value); !normalizers[fn] {
					t.Errorf("%s: memory.FeedbackMemory sets OriginalBody to `%s`, which is not one of %v.\n"+
						"A raw body is the RENDERED GitHub comment; dismissalSearch queries with the finding statement, "+
						"and cosine between those two shapes is not a similarity between the findings.",
						fset.Position(kv.Pos()), types.ExprString(kv.Value), sortedKeys(normalizers))
				}
			}
			return true
		})
	}

	// A guard that silently matches nothing is worse than no guard: it reports
	// success after a rename moves every site out of its reach.
	if sites < 3 {
		t.Errorf("found %d memory.FeedbackMemory literals, want at least the 3 known writers "+
			"(orchestrator praise, reaction, reply) — did they move out of this package?", sites)
	}
}

// isFeedbackMemoryLit reports whether the literal constructs a
// memory.FeedbackMemory.
func isFeedbackMemoryLit(lit *ast.CompositeLit) bool {
	sel, ok := lit.Type.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "FeedbackMemory" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "memory"
}

// sortedKeys returns the map's keys in a stable order, so the failure message
// does not shuffle between runs.
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// calleeName returns the function name of a call expression, or "" for any
// expression that is not a plain call — a bare identifier or field selector
// (the raw-body shape) reports "" and fails the check.
func calleeName(expr ast.Expr) string {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return ""
	}
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		return fn.Sel.Name
	}
	return ""
}
