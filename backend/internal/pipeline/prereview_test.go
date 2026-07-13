// Package pipeline — prereview_test.go exercises each pre-review enricher
// (attachSAST / attachArchContext / attachLinks / attachIntent) against
// in-memory fakes, with no live GitHub client, store, or LLM. The enrichers
// take narrow consumer-declared dependency interfaces precisely so this is
// possible.
package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/store/db"
	"github.com/BeLazy167/argus/backend/pkg/diff"
)

// discardLogger lives in lifecycle_test.go (same package).

func prereviewRun(files []diff.FileDiff) *PipelineRun {
	return &PipelineRun{
		DBRepoID:         9,
		DBInstallationID: 7,
		PREvent: ghpkg.PREvent{
			RepoFullName:   "acme/widgets",
			PRNumber:       42,
			InstallationID: 123,
			HeadSHA:        "deadbeef",
		},
		Diff: &diff.PatchSet{Files: files},
	}
}

// --- attachSAST ---

type fakeFileFetcher struct {
	contents map[string]string
	err      error
	calls    []string
}

func (f *fakeFileFetcher) GetFileContent(ctx context.Context, installationID int64, owner, repo, path, ref string) (string, error) {
	f.calls = append(f.calls, path)
	if f.err != nil {
		return "", f.err
	}
	return f.contents[path], nil
}

func TestAttachSAST_SkipsOverFileCap(t *testing.T) {
	files := make([]diff.FileDiff, prereviewSASTFileCap+1)
	for i := range files {
		files[i] = diff.FileDiff{NewName: "f.go"}
	}
	run := prereviewRun(files)
	f := &fakeFileFetcher{}
	attachSAST(context.Background(), run, f, discardLogger())
	if len(f.calls) != 0 {
		t.Errorf("fetcher called %d times over the file cap; want 0", len(f.calls))
	}
	if run.SastFindings != nil {
		t.Error("SastFindings set over the file cap; want nil")
	}
}

func TestAttachSAST_SkipsUnknownLanguage(t *testing.T) {
	run := prereviewRun([]diff.FileDiff{{NewName: "notes.txt"}})
	f := &fakeFileFetcher{}
	attachSAST(context.Background(), run, f, discardLogger())
	if len(f.calls) != 0 {
		t.Errorf("fetcher called for an unknown-language diff; want 0, got %v", f.calls)
	}
}

func TestAttachSAST_FetchesMissingFullContent(t *testing.T) {
	run := prereviewRun([]diff.FileDiff{{NewName: "main.go"}}) // no FullContent
	// Returns "" so files stays empty and SAST short-circuits before running any
	// real tool — this test asserts only the fetch orchestration.
	f := &fakeFileFetcher{contents: map[string]string{}}
	attachSAST(context.Background(), run, f, discardLogger())
	if len(f.calls) != 1 || f.calls[0] != "main.go" {
		t.Errorf("expected a single fetch for main.go, got %v", f.calls)
	}
}

func TestAttachSAST_UsesFullContentWithoutFetch(t *testing.T) {
	run := prereviewRun([]diff.FileDiff{{NewName: "main.go", FullContent: "package main"}})
	f := &fakeFileFetcher{}
	attachSAST(context.Background(), run, f, discardLogger())
	if len(f.calls) != 0 {
		t.Errorf("fetcher called though FullContent was present; want 0, got %v", f.calls)
	}
}

func TestAttachSAST_NilDiffNoPanic(t *testing.T) {
	run := &PipelineRun{PREvent: ghpkg.PREvent{RepoFullName: "acme/widgets"}}
	attachSAST(context.Background(), run, &fakeFileFetcher{}, discardLogger())
	if run.SastFindings != nil {
		t.Error("SastFindings set with a nil diff")
	}
}

// --- attachArchContext ---

type fakeArchReader struct {
	edges   []db.ListArchFileEdgesRow
	density []db.ListArchBugDensityRow
	edgeErr error
	densErr error
}

func (f fakeArchReader) ListArchFileEdges(ctx context.Context, repoID int64) ([]db.ListArchFileEdgesRow, error) {
	return f.edges, f.edgeErr
}

func (f fakeArchReader) ListArchBugDensity(ctx context.Context, repoID int64) ([]db.ListArchBugDensityRow, error) {
	return f.density, f.densErr
}

func fanInEdges(target string, n int) []db.ListArchFileEdgesRow {
	edges := make([]db.ListArchFileEdgesRow, n)
	for i := range edges {
		edges[i] = db.ListArchFileEdgesRow{SourcePath: "src.go", TargetPath: target}
	}
	return edges
}

func TestAttachArchContext_MarksChokePointAndHotspot(t *testing.T) {
	run := prereviewRun([]diff.FileDiff{{NewName: "hub.go"}, {NewName: "quiet.go"}})
	dep := fakeArchReader{
		edges:   fanInEdges("hub.go", ArchChokePointFanIn),
		density: []db.ListArchBugDensityRow{{FilePath: "hub.go", Bugs: ArchHotspotBugCount}},
	}
	attachArchContext(context.Background(), run, dep, discardLogger())

	entry, ok := run.ArchContext["hub.go"]
	if !ok {
		t.Fatal("hub.go missing from ArchContext; want a choke-point/hotspot entry")
	}
	if entry.FanIn != ArchChokePointFanIn || entry.BugCount != ArchHotspotBugCount {
		t.Errorf("hub.go = %+v; want FanIn=%d BugCount=%d", entry, ArchChokePointFanIn, ArchHotspotBugCount)
	}
	if _, ok := run.ArchContext["quiet.go"]; ok {
		t.Error("quiet.go (below thresholds) should not be in ArchContext")
	}
}

func TestAttachArchContext_BothQueriesFail(t *testing.T) {
	run := prereviewRun([]diff.FileDiff{{NewName: "a.go"}})
	dep := fakeArchReader{edgeErr: errors.New("boom"), densErr: errors.New("boom")}
	attachArchContext(context.Background(), run, dep, discardLogger())
	if run.ArchContext != nil {
		t.Errorf("ArchContext set though both queries failed: %+v", run.ArchContext)
	}
}

func TestAttachArchContext_PartialFailureStillEnriches(t *testing.T) {
	run := prereviewRun([]diff.FileDiff{{NewName: "hub.go"}})
	dep := fakeArchReader{edges: fanInEdges("hub.go", ArchChokePointFanIn), densErr: errors.New("density down")}
	attachArchContext(context.Background(), run, dep, discardLogger())
	if _, ok := run.ArchContext["hub.go"]; !ok {
		t.Error("hub.go should be a choke point even when the density query fails")
	}
}

// --- attachLinks ---

type fakeLinkDeps struct {
	closing     []ghpkg.ClosingIssueRef
	closingErr  error
	flagsJSON   json.RawMessage
	flagsErr    error
	flagsCalled bool
	flagsCtxErr error
}

func (f *fakeLinkDeps) GetClosingIssues(ctx context.Context, installationID int64, owner, repo string, prNumber int) ([]ghpkg.ClosingIssueRef, error) {
	return f.closing, f.closingErr
}

func (f *fakeLinkDeps) GetInstallationFeatureFlags(ctx context.Context, installationID int64) (json.RawMessage, error) {
	f.flagsCalled = true
	f.flagsCtxErr = ctx.Err()
	return f.flagsJSON, f.flagsErr
}

func TestAttachLinks_MergesPrimaryAndFallback(t *testing.T) {
	run := prereviewRun(nil)
	run.PREvent.PRBody = "Closes #7"
	dep := &fakeLinkDeps{
		closing:   []ghpkg.ClosingIssueRef{{Owner: "acme", Repo: "widgets", Number: 3, URL: "u", Title: "t", Body: "b"}},
		flagsJSON: json.RawMessage(`{}`),
	}
	attachLinks(context.Background(), run, dep, discardLogger())

	var found3, found7 bool
	for _, l := range run.LinkedIssues {
		switch l.Number {
		case 3:
			found3 = true
			if !l.Accessible {
				t.Error("primary issue #3 (from closingIssuesReferences) should be marked Accessible")
			}
		case 7:
			found7 = true
		}
	}
	if !found3 {
		t.Error("primary issue #3 missing from LinkedIssues")
	}
	if !found7 {
		t.Error("fallback issue #7 (from PR body) missing from LinkedIssues")
	}
}

// TestAttachLinks_LoadsFeatureFlagsOnLiveContext guards the extraction's fix of
// a use-after-cancel: the old inline island cancelled the link context before
// reading feature flags, so the flag load always fell back to defaults. The
// deferred cancel keeps the context live, so configured flags actually apply.
func TestAttachLinks_LoadsFeatureFlagsOnLiveContext(t *testing.T) {
	run := prereviewRun(nil)
	dep := &fakeLinkDeps{flagsJSON: json.RawMessage(`{"issue_acceptance": false}`)}
	attachLinks(context.Background(), run, dep, discardLogger())

	if !dep.flagsCalled {
		t.Fatal("feature flags never loaded")
	}
	if dep.flagsCtxErr != nil {
		t.Errorf("feature flags loaded on a cancelled context (%v); the link budget must not be spent first", dep.flagsCtxErr)
	}
	if run.FeatureFlags.IssueAcceptance {
		t.Error("IssueAcceptance = true (the default); want false from the loaded flags — proves flags actually applied")
	}
}

func TestAttachLinks_NonFatalOnClosingIssuesError(t *testing.T) {
	run := prereviewRun(nil)
	run.PREvent.PRBody = "Fixes #9"
	dep := &fakeLinkDeps{closingErr: errors.New("graphql down"), flagsJSON: json.RawMessage(`{}`)}
	attachLinks(context.Background(), run, dep, discardLogger())

	// GraphQL failed, but the regex fallback still finds #9.
	if len(run.LinkedIssues) != 1 || run.LinkedIssues[0].Number != 9 {
		t.Errorf("LinkedIssues = %+v; want the regex fallback #9 after the GraphQL error", run.LinkedIssues)
	}
}

// --- attachIntent ---

type fakeIntent struct {
	called bool
	err    error
	source IntentSource
}

func (f *fakeIntent) Execute(ctx context.Context, run *PipelineRun) error {
	f.called = true
	run.PRIntent = &PRIntent{Source: f.source}
	return f.err
}

func TestAttachIntent_RunsStage(t *testing.T) {
	run := prereviewRun(nil)
	f := &fakeIntent{source: IntentSourceAuthor}
	attachIntent(context.Background(), run, f, discardLogger())
	if !f.called {
		t.Fatal("intent stage not executed")
	}
	if run.PRIntent == nil || run.PRIntent.Source != IntentSourceAuthor {
		t.Errorf("PRIntent = %+v; want Source=%q", run.PRIntent, IntentSourceAuthor)
	}
}

func TestAttachIntent_NilStageSkips(t *testing.T) {
	run := prereviewRun(nil)
	attachIntent(context.Background(), run, intentExecutorFor(nil), discardLogger())
	if run.PRIntent != nil {
		t.Error("PRIntent should stay nil when the stage is unwired")
	}
}

func TestAttachIntent_SwallowsUnexpectedError(t *testing.T) {
	run := prereviewRun(nil)
	f := &fakeIntent{err: errors.New("contract drift"), source: IntentSourceEmpty}
	attachIntent(context.Background(), run, f, discardLogger()) // must not panic or propagate
	if !f.called {
		t.Fatal("intent stage not executed")
	}
}

func TestIntentExecutorFor_DefusesTypedNil(t *testing.T) {
	if got := intentExecutorFor(nil); got != nil {
		t.Error("intentExecutorFor(nil) must return a nil interface, not a typed nil")
	}
	if intentExecutorFor(&IntentExtractionStage{}) == nil {
		t.Error("intentExecutorFor(non-nil) must return a usable executor")
	}
}
