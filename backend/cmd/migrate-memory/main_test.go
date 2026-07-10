package main

import (
	"context"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/store/db"
)

func strp(s string) *string { return &s }
func i64p(i int64) *int64   { return &i }
func ip(i int) *int         { return &i }

// mustMeta flattens a Metadata or fails — the expected-metadata builder for
// assertions.
func mustMeta(t *testing.T, m memory.Metadata) map[string]string {
	t.Helper()
	flat, err := m.ToMap()
	if err != nil {
		t.Fatalf("building expected metadata: %v", err)
	}
	return flat
}

// TestMapReviewComment asserts the review doc lands in {repo} with the
// FindingFingerprint customID over the stored body and type=review metadata.
func TestMapReviewComment(t *testing.T) {
	rid := uuid.New()
	row := db.ListReviewCommentsForBackfillRow{
		ID:       uuid.New(),
		FilePath: "pkg/api/handler.go",
		Severity: strp("critical"),
		Category: strp("security"),
		Body:     "SQL injection via string concat",
		PRNumber: 42,
		PRAuthor: "octocat",
		ReviewID: rid,
		FullName: "acme/webapp",
	}
	got, err := mapReviewComment(row)
	if err != nil {
		t.Fatalf("mapReviewComment: %v", err)
	}
	if got.Container != memory.RepoTagNew("webapp") {
		t.Errorf("container = %q, want %q", got.Container, memory.RepoTagNew("webapp"))
	}
	wantID := memory.FindingFingerprint("", "webapp", row.FilePath, "security", row.Body)
	if got.Doc.CustomID != wantID {
		t.Errorf("customID = %q, want %q", got.Doc.CustomID, wantID)
	}
	if got.Doc.Content != row.Body {
		t.Errorf("content = %q, want stored body %q", got.Doc.Content, row.Body)
	}
	want := mustMeta(t, memory.Metadata{
		Type: memory.TypeReview, FilePath: row.FilePath, Severity: "critical",
		Category: "security", PRNumber: 42, Extra: map[string]string{"review_id": rid.String()},
	})
	if !reflect.DeepEqual(got.Doc.Metadata, want) {
		t.Errorf("metadata = %v, want %v", got.Doc.Metadata, want)
	}
}

// TestMapPattern_CustomIDs covers each source→customID reconstruction plus the
// shared vs repo routing and confidence pin.
func TestMapPattern_CustomIDs(t *testing.T) {
	const conv = "use table-driven tests"
	tests := []struct {
		name     string
		row      db.ListPatternsForBackfillRow
		wantCID  string
		wantTag  string
		wantConf bool
	}{
		{
			name:    "scoring_confirmed repo",
			row:     db.ListPatternsForBackfillRow{ID: 1, RepoID: i64p(9), Content: "Confirmed pattern [bug]: x (file: a.go)", Source: "scoring_confirmed", FullName: strp("acme/webapp")},
			wantCID: memory.PatternCustomID("", "webapp", "confirmed", "Confirmed pattern [bug]: x (file: a.go)"),
			wantTag: memory.RepoTagNew("webapp"),
		},
		{
			name:    "auto_learn repo",
			row:     db.ListPatternsForBackfillRow{ID: 2, RepoID: i64p(9), Content: "always validate input", Source: "auto_learn", FullName: strp("acme/webapp")},
			wantCID: memory.PatternCustomID("", "webapp", "learned", "always validate input"),
			wantTag: memory.RepoTagNew("webapp"),
		},
		{
			name:     "auto_learn shared",
			row:      db.ListPatternsForBackfillRow{ID: 3, RepoID: nil, Content: "prefer composition", Source: "auto_learn"},
			wantCID:  memory.PatternCustomID("", "", "org_learned", "prefer composition"),
			wantTag:  memory.SharedTag,
			wantConf: true,
		},
		{
			name:    "convention hashes raw",
			row:     db.ListPatternsForBackfillRow{ID: 4, RepoID: i64p(9), Content: "Convention [style]: " + conv, Source: "convention", Category: strp("style"), FullName: strp("acme/webapp")},
			wantCID: memory.PatternCustomID("", "webapp", "convention", conv),
			wantTag: memory.RepoTagNew("webapp"),
		},
		{
			name:    "manual repo falls back to default derivation",
			row:     db.ListPatternsForBackfillRow{ID: 5, RepoID: i64p(9), Content: "hand written", Source: "manual", FullName: strp("acme/webapp")},
			wantCID: memory.PatternCustomID("", "webapp", "manual", "hand written"),
			wantTag: memory.RepoTagNew("webapp"),
		},
		{
			name:     "manual shared falls back to default derivation",
			row:      db.ListPatternsForBackfillRow{ID: 6, RepoID: nil, Content: "org level manual", Source: "manual"},
			wantCID:  memory.SharedPatternCustomID("manual", "org level manual"),
			wantTag:  memory.SharedTag,
			wantConf: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := mapPattern(tt.row)
			if err != nil {
				t.Fatalf("mapPattern: %v", err)
			}
			if got.Doc.CustomID != tt.wantCID {
				t.Errorf("customID = %q, want %q", got.Doc.CustomID, tt.wantCID)
			}
			if got.Container != tt.wantTag {
				t.Errorf("container = %q, want %q", got.Container, tt.wantTag)
			}
			if got.Doc.Metadata["type"] != string(memory.TypePattern) {
				t.Errorf("type = %q, want pattern", got.Doc.Metadata["type"])
			}
			if got.Doc.Metadata["source"] != tt.row.Source {
				t.Errorf("source = %q, want %q", got.Doc.Metadata["source"], tt.row.Source)
			}
			if _, ok := got.Doc.Metadata["confidence"]; ok != tt.wantConf {
				t.Errorf("confidence present = %v, want %v", ok, tt.wantConf)
			}
			if tt.wantConf && got.Doc.Metadata["confidence"] != "1.00" {
				t.Errorf("confidence = %q, want 1.00", got.Doc.Metadata["confidence"])
			}
		})
	}
}

// TestMapFeedback covers both polarities and the body-only content (reply text
// is never persisted to Postgres, so the backfill can't include it).
func TestMapFeedback(t *testing.T) {
	tests := []struct {
		name        string
		row         db.ListCommentOutcomesForBackfillRow
		wantPol     string
		wantContent string
	}{
		{
			name:        "confirmed",
			row:         db.ListCommentOutcomesForBackfillRow{ID: 1, Outcome: "confirmed", FilePath: "a.go", Category: strp("bug"), Body: "off by one", PRNumber: 7, FullName: "acme/webapp"},
			wantPol:     "positive",
			wantContent: "off by one",
		},
		{
			name:        "dismissed body-only",
			row:         db.ListCommentOutcomesForBackfillRow{ID: 2, Outcome: "dismissed", FilePath: "b.go", Category: strp("style"), Body: "prefer const", PRNumber: 8, FullName: "acme/webapp"},
			wantPol:     "negative",
			wantContent: "prefer const",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := mapFeedback(tt.row)
			if err != nil {
				t.Fatalf("mapFeedback: %v", err)
			}
			if got.Container != memory.RepoTagNew("webapp") {
				t.Errorf("container = %q", got.Container)
			}
			wantID := memory.FeedbackCustomID("", "webapp", tt.row.FilePath, *tt.row.Category, tt.row.Body, tt.row.Outcome)
			if got.Doc.CustomID != wantID {
				t.Errorf("customID = %q, want %q", got.Doc.CustomID, wantID)
			}
			if got.Doc.Content != tt.wantContent {
				t.Errorf("content = %q, want %q", got.Doc.Content, tt.wantContent)
			}
			if got.Doc.Metadata["polarity"] != tt.wantPol {
				t.Errorf("polarity = %q, want %q", got.Doc.Metadata["polarity"], tt.wantPol)
			}
			if got.Doc.Metadata["action"] != tt.row.Outcome {
				t.Errorf("action = %q, want %q", got.Doc.Metadata["action"], tt.row.Outcome)
			}
		})
	}
}

// TestMapFeedback_BadOutcome ensures an unexpected outcome is a permanent skip.
func TestMapFeedback_BadOutcome(t *testing.T) {
	_, err := mapFeedback(db.ListCommentOutcomesForBackfillRow{Outcome: "ignored", Category: strp("bug"), FullName: "a/b"})
	if err == nil {
		t.Fatal("expected error for outcome=ignored")
	}
}

func TestMapScenario(t *testing.T) {
	row := db.ListScenariosForBackfillRow{ID: 55, RepoID: i64p(9), Description: "cache stampede on cold start", Severity: strp("high"), Files: []string{"cache.go", "warm.go"}, FullName: "acme/webapp"}
	got, err := mapScenario(row)
	if err != nil {
		t.Fatalf("mapScenario: %v", err)
	}
	if got.Doc.CustomID != memory.ScenarioCustomID("webapp", 55) {
		t.Errorf("customID = %q, want %q", got.Doc.CustomID, memory.ScenarioCustomID("webapp", 55))
	}
	wantContent := "cache stampede on cold start\n\nRelated files: cache.go, warm.go"
	if got.Doc.Content != wantContent {
		t.Errorf("content = %q, want %q", got.Doc.Content, wantContent)
	}
	if got.Doc.Metadata["scenario_id"] != "55" || got.Doc.Metadata["severity"] != "high" {
		t.Errorf("metadata = %v", got.Doc.Metadata)
	}
}

func TestMapRule(t *testing.T) {
	row := db.ListRulesForBackfillRow{ID: 12, Category: "security", Content: "no eval()", Priority: 5}
	got, err := mapRule(row)
	if err != nil {
		t.Fatalf("mapRule: %v", err)
	}
	if got.Container != memory.SharedTag {
		t.Errorf("container = %q, want _shared", got.Container)
	}
	if got.Doc.CustomID != memory.RuleCustomID(12) {
		t.Errorf("customID = %q, want %q", got.Doc.CustomID, memory.RuleCustomID(12))
	}
	if got.Doc.Metadata["rule_id"] != "12" || got.Doc.Metadata["priority"] != "5" {
		t.Errorf("metadata = %v", got.Doc.Metadata)
	}
}

func TestMapReviewSummary(t *testing.T) {
	row := db.ListReviewSummariesForBackfillRow{ID: uuid.New(), PRNumber: 99, PRTitle: "Add cache", PRAuthor: "octocat", Summary: "Looks good overall.", Score: ip(8), FullName: "acme/webapp", Files: "cache.go, warm.go"}
	got, err := mapReviewSummary(row)
	if err != nil {
		t.Fatalf("mapReviewSummary: %v", err)
	}
	if got.Doc.CustomID != memory.PRSummaryCustomID("", "webapp", 99) {
		t.Errorf("customID = %q, want %q", got.Doc.CustomID, memory.PRSummaryCustomID("", "webapp", 99))
	}
	wantContent := "PR #99 \"Add cache\" by octocat\nScore: 8/10\nFiles: cache.go, warm.go\n\nLooks good overall."
	if got.Doc.Content != wantContent {
		t.Errorf("content = %q, want %q", got.Doc.Content, wantContent)
	}
	if got.Doc.Metadata["type"] != string(memory.TypePRSummary) || got.Doc.Metadata["source"] != "pr_summary" {
		t.Errorf("metadata = %v", got.Doc.Metadata)
	}
}

// TestMapper_BadFullName confirms a malformed full_name is a permanent per-row
// error the sweep will skip without tripping the breaker.
func TestMapper_BadFullName(t *testing.T) {
	if _, err := mapReviewComment(db.ListReviewCommentsForBackfillRow{FullName: "no-slash", Body: "x"}); err == nil {
		t.Fatal("expected error for malformed full_name")
	}
}

// TestFlushContainer_PlanMode is the --plan smoke test: no client call, no
// write-back, counts recorded as planned.
func TestFlushContainer_PlanMode(t *testing.T) {
	report := newRunReport()
	wbCalled := false
	s := &sweeper{
		ctx:       context.Background(),
		logger:    slog.New(slog.NewTextHandler(nopWriter{}, nil)),
		cfg:       runConfig{plan: true, batchSize: 100, maxRows: 1000},
		installID: 1,
		report:    report,
	}
	pb := &pendingBatch{
		docs: []memory.BatchDocument{{CustomID: "a"}, {CustomID: "b"}},
		wbs:  []writeBackFn{func(context.Context, string) error { wbCalled = true; return nil }, nil},
	}
	s.flushContainer("pattern", "webapp", pb)
	st := report.stat(1, "webapp", "pattern")
	if st.Read != 2 || st.Planned != 2 || st.Written != 0 || st.Errors != 0 {
		t.Errorf("plan stat = %+v, want Read=2 Planned=2 Written=0 Errors=0", *st)
	}
	if wbCalled {
		t.Error("write-back must not run in plan mode")
	}
	if s.consecutiveFailures != 0 {
		t.Errorf("breaker advanced in plan mode: %d", s.consecutiveFailures)
	}
}

// TestSweepType_PlanPaging drives the generic sweep in plan mode over an
// in-memory dataset to exercise keyset paging, per-container grouping, and the
// max-rows cap without any DB or network.
func TestSweepType_PlanPaging(t *testing.T) {
	type fakeRow struct {
		id        int
		container string
	}
	data := []fakeRow{
		{1, "webapp"}, {2, "webapp"}, {3, "api"}, {4, "webapp"}, {5, "api"},
	}
	fetch := func(cur int, limit int32) ([]fakeRow, error) {
		var out []fakeRow
		for _, r := range data {
			if r.id > cur {
				out = append(out, r)
				if int32(len(out)) == limit {
					break
				}
			}
		}
		return out, nil
	}
	report := newRunReport()
	s := &sweeper{
		ctx:       context.Background(),
		logger:    slog.New(slog.NewTextHandler(nopWriter{}, nil)),
		cfg:       runConfig{plan: true, batchSize: 2, maxRows: 1000},
		installID: 1,
		report:    report,
	}
	sweepType(s, "fake", 0, fetch,
		func(r fakeRow) int { return r.id },
		func(r fakeRow) (mappedDoc, writeBackFn, error) {
			return mappedDoc{Container: r.container, Doc: memory.BatchDocument{CustomID: "x"}}, nil, nil
		},
		nil)

	webapp := report.stat(1, "webapp", "fake")
	api := report.stat(1, "api", "fake")
	if webapp.Read != 3 {
		t.Errorf("webapp read = %d, want 3", webapp.Read)
	}
	if api.Read != 2 {
		t.Errorf("api read = %d, want 2", api.Read)
	}
	if webapp.Planned != 3 || api.Planned != 2 {
		t.Errorf("planned = webapp %d api %d, want 3/2", webapp.Planned, api.Planned)
	}
}

// TestSweepType_MaxRows caps the sweep at max-rows even when more rows remain.
func TestSweepType_MaxRows(t *testing.T) {
	fetch := func(cur int, limit int32) ([]int, error) {
		var out []int
		for i := cur + 1; i <= 100 && int32(len(out)) < limit; i++ {
			out = append(out, i)
		}
		return out, nil
	}
	report := newRunReport()
	s := &sweeper{
		ctx:       context.Background(),
		logger:    slog.New(slog.NewTextHandler(nopWriter{}, nil)),
		cfg:       runConfig{plan: true, batchSize: 10, maxRows: 25},
		installID: 1,
		report:    report,
	}
	sweepType(s, "fake", 0, fetch,
		func(r int) int { return r },
		func(r int) (mappedDoc, writeBackFn, error) {
			return mappedDoc{Container: "c", Doc: memory.BatchDocument{}}, nil, nil
		},
		nil)
	if got := report.stat(1, "c", "fake").Read; got != 25 {
		t.Errorf("read = %d, want max-rows 25", got)
	}
}

// TestResolveCutoff covers the required-flag rejection, RFC3339 parsing, and the
// --verify-legacy bypass.
func TestResolveCutoff(t *testing.T) {
	// Missing flag on a real backfill → error.
	if _, err := resolveCutoff("", false); err == nil {
		t.Error("expected error when --new-shape-since is missing")
	}
	// Unparseable → error.
	if _, err := resolveCutoff("2026-07-09", false); err == nil {
		t.Error("expected error for non-RFC3339 value")
	}
	// Valid RFC3339 → parsed.
	got, err := resolveCutoff("2026-07-09T00:00:00Z", false)
	if err != nil {
		t.Fatalf("resolveCutoff: %v", err)
	}
	if !got.Equal(time.Date(2026, 7, 9, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("cutoff = %v", got)
	}
	// --verify-legacy: missing flag is fine, returns zero time.
	z, err := resolveCutoff("", true)
	if err != nil || !z.IsZero() {
		t.Errorf("verify-legacy cutoff = (%v, %v), want (zero, nil)", z, err)
	}
}

// TestAfterCutoff checks the inclusive (>=) boundary.
func TestAfterCutoff(t *testing.T) {
	cut := time.Date(2026, 7, 9, 0, 0, 0, 0, time.UTC)
	if afterCutoff(cut.Add(-time.Second), cut) {
		t.Error("row before cutoff must NOT be skipped")
	}
	if !afterCutoff(cut, cut) {
		t.Error("row exactly at cutoff must be skipped (inclusive)")
	}
	if !afterCutoff(cut.Add(time.Second), cut) {
		t.Error("row after cutoff must be skipped")
	}
}

// TestSweepType_CutoffSkip drives the generic sweep with a created_at cutoff:
// rows at/after the boundary are counted as skipped (in report.skipped) and
// never routed to a container; rows before are read normally.
func TestSweepType_CutoffSkip(t *testing.T) {
	type fakeRow struct {
		id        int
		createdAt time.Time
	}
	cut := time.Date(2026, 7, 9, 0, 0, 0, 0, time.UTC)
	data := []fakeRow{
		{1, cut.Add(-48 * time.Hour)}, // before → keep
		{2, cut.Add(-time.Second)},    // before → keep
		{3, cut},                      // at → skip
		{4, cut.Add(time.Hour)},       // after → skip
		{5, cut.Add(-time.Hour)},      // before → keep
	}
	fetch := func(cur int, limit int32) ([]fakeRow, error) {
		var out []fakeRow
		for _, r := range data {
			if r.id > cur {
				out = append(out, r)
				if int32(len(out)) == limit {
					break
				}
			}
		}
		return out, nil
	}
	report := newRunReport()
	s := &sweeper{
		ctx:       context.Background(),
		logger:    slog.New(slog.NewTextHandler(nopWriter{}, nil)),
		cfg:       runConfig{plan: true, batchSize: 2, maxRows: 1000, cutoff: cut},
		installID: 1,
		report:    report,
	}
	sweepType(s, "review", 0, fetch,
		func(r fakeRow) int { return r.id },
		func(r fakeRow) (mappedDoc, writeBackFn, error) {
			return mappedDoc{Container: "webapp", Doc: memory.BatchDocument{}}, nil, nil
		},
		func(r fakeRow) bool { return afterCutoff(r.createdAt, cut) })

	if got := report.stat(1, "webapp", "review").Read; got != 3 {
		t.Errorf("read = %d, want 3 (pre-cutoff rows)", got)
	}
	if got := report.skipped[skipKey{install: 1, typ: "review"}]; got != 2 {
		t.Errorf("skipped = %d, want 2 (at+after cutoff)", got)
	}
}

// TestLegacyTagFormatters pins the private legacy container-name reconstruction
// that --verify-legacy counts and --delete-legacy deletes. These must reproduce
// the deprecated memory.RepoTag/OwnerTag output EXACTLY — the sanitized names
// are the real Supermemory containers, so a drift here would delete the wrong
// container (or miss the target). Cases mirror the deleted memory tag tests.
func TestLegacyTagFormatters(t *testing.T) {
	repoCases := []struct {
		owner, repo, kind, want string
	}{
		{"acme", "widget", "negative_patterns", "acme--widget--negative_patterns"},
		{"acme", "widget", "positive_patterns", "acme--widget--positive_patterns"},
		{"acme/org", "my.repo", "reviews", "acme-org--my-repo--reviews"},
		{"org:team", "repo~v2", "traces", "org-team--repo-v2--traces"},
	}
	for _, c := range repoCases {
		if got := legacyRepoTag(c.owner, c.repo, c.kind); got != c.want {
			t.Errorf("legacyRepoTag(%q,%q,%q) = %q, want %q", c.owner, c.repo, c.kind, got, c.want)
		}
	}

	ownerCases := []struct {
		owner, kind, want string
	}{
		{"acme", "patterns", "acme--patterns"},
		{"acme", "rules", "acme--rules"},
		{"org:team", "reviews", "org-team--reviews"},
	}
	for _, c := range ownerCases {
		if got := legacyOwnerTag(c.owner, c.kind); got != c.want {
			t.Errorf("legacyOwnerTag(%q,%q) = %q, want %q", c.owner, c.kind, got, c.want)
		}
	}
}

// TestDeleteModeLabel pins the machine-readable mode labels emitted in the
// delete-legacy summary blob.
func TestDeleteModeLabel(t *testing.T) {
	if got := deleteModeLabel(true); got != "execute" {
		t.Errorf("deleteModeLabel(true) = %q, want execute", got)
	}
	if got := deleteModeLabel(false); got != "dry_run" {
		t.Errorf("deleteModeLabel(false) = %q, want dry_run", got)
	}
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }
