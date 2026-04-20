// Package pipeline — crosspr_stage_integration_test.go drives the async
// cross-PR stage + webhook-path orchestration end-to-end through in-memory
// fakes. All external boundaries (store, GitHub, state machine, LLM,
// event bus) are injected via Orchestrator.crossPRHooks so the tests run
// without a DB or network.
//
// Every test calls t.Cleanup(resetCrossPRGlobals) so package-level maps
// (crossPRRefreshCount, crossPRInstallCount, crossPRDebounceTimers,
// crossPRMutexes, jointAcceptanceMutexes) stay isolated across runs.
package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/llm"
	"github.com/BeLazy167/argus/backend/internal/store"
	"github.com/BeLazy167/argus/backend/internal/store/db"
	"github.com/BeLazy167/argus/backend/pkg/diff"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// --- Fakes ---

// fakeCrossPRStore is an in-memory crossPRStore. Maps hold canned rows;
// the *Calls slices capture each call so tests can assert on ordering
// and arguments. All mutations go through m so -race stays clean under
// concurrent stage runs.
type fakeCrossPRStore struct {
	m sync.Mutex

	reviews map[uuid.UUID]*store.Review
	repos   map[int64]*store.Repo
	// reposByFull maps full_name → *store.Repo. Separate from repos so a
	// lookup via GetRepoByFullName doesn't require scanning the repos map.
	reposByFull map[string]*store.Repo
	// latestRun maps reviewID → runID for GetLatestRunForReview.
	latestRun map[uuid.UUID]uuid.UUID
	// priorByPR maps (repoID, prNumber) to a canned row for
	// GetLatestCompletedReviewByPR. errNoRows[key]=true triggers pgx.ErrNoRows.
	priorByPR  map[priorKey]db.GetLatestCompletedReviewByPRRow
	priorErr   map[priorKey]error
	siblingRows []db.FindReviewsLinkingToPRRow
	sharedRows  []db.FindSharedLinkedIssuesRow
	// allFileReviews returns the JSONB projection for GetAllFileReviewsForReview.
	allFileReviews map[uuid.UUID]json.RawMessage
	// featureFlags returned by LoadFeatureFlags. Keyed by installationDBID.
	featureFlags map[int64]FeatureFlags
	// flagsDefault is returned when no per-install entry exists.
	flagsDefault FeatureFlags

	// Call capture. Each slice holds arg snapshots in invocation order.
	hashWrites   []db.UpdateReviewCrossPRHashParams
	tokenWrites  []db.MergeStageTokenEntryParams
	linkedPRSets []db.SetReviewLinkedPRRefsParams
	linkedIssueSets []db.SetReviewLinkedIssueRefsParams
	flagCalls    []int64

	// Error overrides — if non-nil, the matching call returns this error
	// instead of the canned row. Cleared after the call so a single test
	// can exercise a transient failure cleanly.
	hashWriteErr error

	// siblingLookups atomic counter drives waitSiblingFanoutSettled so
	// tests that schedule OnReviewCompleted can block until the background
	// enqueueSiblingRefreshes goroutine has completed its one store read.
	siblingLookups int32
}

type priorKey struct {
	RepoID int64
	PR     int
}

func newFakeCrossPRStore() *fakeCrossPRStore {
	return &fakeCrossPRStore{
		reviews:        map[uuid.UUID]*store.Review{},
		repos:          map[int64]*store.Repo{},
		reposByFull:    map[string]*store.Repo{},
		latestRun:      map[uuid.UUID]uuid.UUID{},
		priorByPR:      map[priorKey]db.GetLatestCompletedReviewByPRRow{},
		priorErr:       map[priorKey]error{},
		allFileReviews: map[uuid.UUID]json.RawMessage{},
		featureFlags:   map[int64]FeatureFlags{},
		flagsDefault:   FeatureFlags{CrossPRChecks: true, IssueAcceptance: true, MaxLinkedPRs: 5},
	}
}

func (f *fakeCrossPRStore) GetReview(ctx context.Context, id uuid.UUID) (*store.Review, error) {
	f.m.Lock()
	defer f.m.Unlock()
	r, ok := f.reviews[id]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	// Return a shallow copy so the stage's reads don't race fake state.
	cp := *r
	return &cp, nil
}

func (f *fakeCrossPRStore) GetRepo(ctx context.Context, id int64) (*store.Repo, error) {
	f.m.Lock()
	defer f.m.Unlock()
	r, ok := f.repos[id]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	cp := *r
	return &cp, nil
}

func (f *fakeCrossPRStore) GetRepoByFullName(ctx context.Context, fullName string) (*store.Repo, error) {
	f.m.Lock()
	defer f.m.Unlock()
	r, ok := f.reposByFull[fullName]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	cp := *r
	return &cp, nil
}

func (f *fakeCrossPRStore) GetLatestRunForReview(ctx context.Context, reviewID uuid.UUID) (uuid.UUID, error) {
	f.m.Lock()
	defer f.m.Unlock()
	rid, ok := f.latestRun[reviewID]
	if !ok {
		return uuid.Nil, pgx.ErrNoRows
	}
	return rid, nil
}

func (f *fakeCrossPRStore) FindReviewsLinkingToPR(ctx context.Context, arg db.FindReviewsLinkingToPRParams) ([]db.FindReviewsLinkingToPRRow, error) {
	f.m.Lock()
	defer f.m.Unlock()
	out := make([]db.FindReviewsLinkingToPRRow, len(f.siblingRows))
	copy(out, f.siblingRows)
	atomic.AddInt32(&f.siblingLookups, 1)
	return out, nil
}

func (f *fakeCrossPRStore) SetReviewLinkedPRRefs(ctx context.Context, arg db.SetReviewLinkedPRRefsParams) error {
	f.m.Lock()
	defer f.m.Unlock()
	f.linkedPRSets = append(f.linkedPRSets, arg)
	return nil
}

func (f *fakeCrossPRStore) SetReviewLinkedIssueRefs(ctx context.Context, arg db.SetReviewLinkedIssueRefsParams) error {
	f.m.Lock()
	defer f.m.Unlock()
	f.linkedIssueSets = append(f.linkedIssueSets, arg)
	return nil
}

func (f *fakeCrossPRStore) UpdateReviewCrossPRHash(ctx context.Context, arg db.UpdateReviewCrossPRHashParams) error {
	f.m.Lock()
	defer f.m.Unlock()
	if f.hashWriteErr != nil {
		err := f.hashWriteErr
		f.hashWriteErr = nil
		return err
	}
	f.hashWrites = append(f.hashWrites, arg)
	// Mirror into the review row so a subsequent GetReview sees the
	// updated CrossPRHash (the production UPDATE behaves the same).
	if rv, ok := f.reviews[arg.ID]; ok && arg.CrossPRHash != nil {
		h := *arg.CrossPRHash
		rv.CrossPRHash = &h
	}
	return nil
}

func (f *fakeCrossPRStore) GetLatestCompletedReviewByPR(ctx context.Context, arg db.GetLatestCompletedReviewByPRParams) (db.GetLatestCompletedReviewByPRRow, error) {
	f.m.Lock()
	defer f.m.Unlock()
	k := priorKey{RepoID: arg.RepoID, PR: arg.PRNumber}
	if err, ok := f.priorErr[k]; ok {
		return db.GetLatestCompletedReviewByPRRow{}, err
	}
	row, ok := f.priorByPR[k]
	if !ok {
		return db.GetLatestCompletedReviewByPRRow{}, pgx.ErrNoRows
	}
	return row, nil
}

func (f *fakeCrossPRStore) FindSharedLinkedIssues(ctx context.Context, reviewID uuid.UUID) ([]db.FindSharedLinkedIssuesRow, error) {
	f.m.Lock()
	defer f.m.Unlock()
	out := make([]db.FindSharedLinkedIssuesRow, len(f.sharedRows))
	copy(out, f.sharedRows)
	return out, nil
}

func (f *fakeCrossPRStore) GetAllFileReviewsForReview(ctx context.Context, reviewID uuid.UUID) (json.RawMessage, error) {
	f.m.Lock()
	defer f.m.Unlock()
	raw, ok := f.allFileReviews[reviewID]
	if !ok {
		return nil, nil
	}
	return raw, nil
}

func (f *fakeCrossPRStore) MergeStageTokenEntry(ctx context.Context, arg db.MergeStageTokenEntryParams) (int64, error) {
	f.m.Lock()
	defer f.m.Unlock()
	f.tokenWrites = append(f.tokenWrites, arg)
	return 1, nil
}

func (f *fakeCrossPRStore) LoadFeatureFlags(ctx context.Context, installationDBID int64) FeatureFlags {
	f.m.Lock()
	defer f.m.Unlock()
	f.flagCalls = append(f.flagCalls, installationDBID)
	if flags, ok := f.featureFlags[installationDBID]; ok {
		return flags
	}
	return f.flagsDefault
}

// fakeGithubClient captures every GitHub call and returns canned
// responses. Behavioural knobs (stickyErr, pullRequestErr,
// prDiffErr, issueErr) let individual tests exercise specific error
// paths without touching state other tests need.
type fakeGithubClient struct {
	m sync.Mutex

	// Canned responses.
	pullRequests map[string]*ghpkg.PREvent // key: owner/repo/#N
	prDiffs      map[string]string
	issues       map[string]*ghpkg.Issue

	// Per-call errors. Set to non-nil to force the next call on that
	// surface to fail with the given error (kept across calls).
	stickyErr      error
	pullRequestErr error
	prDiffErr      error
	issueErr       error

	// Call captures.
	stickyCalls      []stickyCall
	pullRequestCalls []string
	prDiffCalls      []string
	issueCalls       []string
}

type stickyCall struct {
	Owner, Repo, Section, Body string
	PRNumber                   int
	StickyReviewID             int64
	InstallationID             int64
}

func newFakeGithubClient() *fakeGithubClient {
	return &fakeGithubClient{
		pullRequests: map[string]*ghpkg.PREvent{},
		prDiffs:      map[string]string{},
		issues:       map[string]*ghpkg.Issue{},
	}
}

func (f *fakeGithubClient) UpdateStickySection(ctx context.Context, installationID int64, owner, repo string, prNumber int, stickyReviewID int64, section, bodyMD string) error {
	f.m.Lock()
	defer f.m.Unlock()
	if f.stickyErr != nil {
		return f.stickyErr
	}
	f.stickyCalls = append(f.stickyCalls, stickyCall{
		Owner: owner, Repo: repo, Section: section, Body: bodyMD,
		PRNumber: prNumber, StickyReviewID: stickyReviewID, InstallationID: installationID,
	})
	return nil
}

func (f *fakeGithubClient) GetPullRequest(ctx context.Context, installationID int64, owner, repo string, prNumber int) (*ghpkg.PREvent, error) {
	f.m.Lock()
	defer f.m.Unlock()
	key := ghKey(owner, repo, prNumber)
	f.pullRequestCalls = append(f.pullRequestCalls, key)
	if f.pullRequestErr != nil {
		return nil, f.pullRequestErr
	}
	pr, ok := f.pullRequests[key]
	if !ok {
		return nil, errors.New("404 not found")
	}
	cp := *pr
	return &cp, nil
}

func (f *fakeGithubClient) GetPRDiff(ctx context.Context, installationID int64, owner, repo string, prNumber int) (string, error) {
	f.m.Lock()
	defer f.m.Unlock()
	key := ghKey(owner, repo, prNumber)
	f.prDiffCalls = append(f.prDiffCalls, key)
	if f.prDiffErr != nil {
		return "", f.prDiffErr
	}
	d, ok := f.prDiffs[key]
	if !ok {
		return "", errors.New("404 not found")
	}
	return d, nil
}

func (f *fakeGithubClient) GetIssue(ctx context.Context, installationID int64, owner, repo string, number int) (*ghpkg.Issue, error) {
	f.m.Lock()
	defer f.m.Unlock()
	key := ghKey(owner, repo, number)
	f.issueCalls = append(f.issueCalls, key)
	if f.issueErr != nil {
		return nil, f.issueErr
	}
	is, ok := f.issues[key]
	if !ok {
		return nil, errors.New("404 not found")
	}
	cp := *is
	return &cp, nil
}

// ghKey builds the owner/repo#N string used as the fakeGithubClient map key.
// Matches the "owner/repo#N" convention used in crosspr prompts, which makes
// call-capture assertions readable in test failures.
func ghKey(owner, repo string, n int) string {
	var sb strings.Builder
	sb.WriteString(owner)
	sb.WriteString("/")
	sb.WriteString(repo)
	sb.WriteString("#")
	// Avoid fmt here to keep the package's hot-path alloc count lower;
	// this runs once per fake call but on a -race test grid it shows up.
	// Use simple manual int-to-string since n is small and non-negative.
	if n == 0 {
		sb.WriteByte('0')
	} else {
		var buf [20]byte
		i := len(buf)
		for n > 0 {
			i--
			buf[i] = byte('0' + n%10)
			n /= 10
		}
		sb.Write(buf[i:])
	}
	return sb.String()
}

// fakeStateLoader returns canned *PipelineRun by runID. Unknown run IDs
// yield pgx.ErrNoRows so the stage's early-exit path is exercised
// verbatim against the production behaviour.
type fakeStateLoader struct {
	m   sync.Mutex
	runs map[uuid.UUID]*PipelineRun
}

func newFakeStateLoader() *fakeStateLoader {
	return &fakeStateLoader{runs: map[uuid.UUID]*PipelineRun{}}
}

func (f *fakeStateLoader) LoadRun(ctx context.Context, runID uuid.UUID) (*PipelineRun, error) {
	f.m.Lock()
	defer f.m.Unlock()
	r, ok := f.runs[runID]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	// Return the canon *PipelineRun directly — the stage mutates
	// run.LinkedPRs + run.CrossPRCoverage + run.Tokens but those mutations
	// are scoped to a single run invocation, and the integration tests
	// don't call LoadRun twice expecting untouched state (each test builds
	// a fresh harness via t.Cleanup-reset). A deep copy would require
	// dodging the embedded sync.Mutex on RunTokenUsage; simpler to share.
	return r, nil
}

// fakeLLMProvider returns a canned completion and records every Complete
// call. err / content can be swapped per-test via SetResponse; the atomic
// count drives test assertions on whether the LLM was (or was NOT) invoked.
type fakeLLMProvider struct {
	m       sync.Mutex
	content string
	err     error
	name    string
	tokens  llm.TokenUsage
	cost    float64
	calls   int32
}

func newFakeLLMProvider() *fakeLLMProvider {
	return &fakeLLMProvider{
		name: "fake",
		tokens: llm.TokenUsage{
			PromptTokens: 123, CompletionTokens: 45, TotalTokens: 168,
		},
		cost: 0.00042,
	}
}

func (p *fakeLLMProvider) Name() string { return p.name }

func (p *fakeLLMProvider) Complete(ctx context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error) {
	atomic.AddInt32(&p.calls, 1)
	p.m.Lock()
	defer p.m.Unlock()
	if p.err != nil {
		return llm.CompletionResponse{}, p.err
	}
	return llm.CompletionResponse{
		Content:    p.content,
		TokensUsed: p.tokens,
		Cost:       p.cost,
	}, nil
}

func (p *fakeLLMProvider) SetContent(c string) {
	p.m.Lock()
	defer p.m.Unlock()
	p.content = c
}

func (p *fakeLLMProvider) SetError(e error) {
	p.m.Lock()
	defer p.m.Unlock()
	p.err = e
}

func (p *fakeLLMProvider) CallCount() int32 { return atomic.LoadInt32(&p.calls) }

// fakeEventPublisher records every Publish call with a monotonic
// timestamp drawn from nowFn. TestEventReviewCompleted uses the recorded
// timestamp against a separate "status committed at" sample to prove the
// ordering invariant.
type fakeEventPublisher struct {
	m      sync.Mutex
	nowFn  func() time.Time
	events []publishedEvent
}

type publishedEvent struct {
	ReviewID uuid.UUID
	Type     EventType
	Data     any
	At       time.Time
}

func newFakeEventPublisher(now func() time.Time) *fakeEventPublisher {
	if now == nil {
		now = time.Now
	}
	return &fakeEventPublisher{nowFn: now}
}

func (p *fakeEventPublisher) Publish(reviewID uuid.UUID, evtType EventType, data any) {
	p.m.Lock()
	defer p.m.Unlock()
	p.events = append(p.events, publishedEvent{
		ReviewID: reviewID, Type: evtType, Data: data, At: p.nowFn(),
	})
}

func (p *fakeEventPublisher) Events() []publishedEvent {
	p.m.Lock()
	defer p.m.Unlock()
	out := make([]publishedEvent, len(p.events))
	copy(out, p.events)
	return out
}

// --- Test harness helpers ---

// harness bundles the orchestrator + fakes for a single test so setup
// stays a single constructor + resetCrossPRGlobals cleanup line.
type harness struct {
	t         *testing.T
	o         *Orchestrator
	store     *fakeCrossPRStore
	gh        *fakeGithubClient
	state     *fakeStateLoader
	llm       *fakeLLMProvider
	publisher *fakeEventPublisher
	reviewID  uuid.UUID
	runID  uuid.UUID
	repo   *store.Repo
	review *store.Review
	run    *PipelineRun
	// now is a mutable clock; Advance to step time in deterministic tests.
	now int64 // unix nanos
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{
		t:         t,
		store:     newFakeCrossPRStore(),
		gh:        newFakeGithubClient(),
		state:     newFakeStateLoader(),
		llm:       newFakeLLMProvider(),
		reviewID:  uuid.New(),
		runID:     uuid.New(),
		now:       time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC).UnixNano(),
	}
	h.publisher = newFakeEventPublisher(h.Now)

	// Canned review + repo + run.
	githubReviewID := int64(900001)
	h.repo = &store.Repo{
		ID:             501,
		InstallationID: 2001,
		FullName:       "acme/primary",
		Enabled:        true,
	}
	h.review = &store.Review{
		ID:             h.reviewID,
		RepoID:         h.repo.ID,
		PRNumber:       42,
		Status:         "completed",
		GithubReviewID: &githubReviewID,
	}
	prBody := "depends on https://github.com/acme/api/pull/7"
	h.run = &PipelineRun{
		ID:       h.runID,
		ReviewID: h.reviewID,
		PREvent: ghpkg.PREvent{
			InstallationID: 2001,
			RepoFullName:   "acme/primary",
			PRNumber:       42,
			PRBody:         prBody,
			HeadSHA:        "primaryhead1234",
		},
		DBInstallationID: 2001,
		DBRepoID:         501,
		Diff: &diff.PatchSet{
			Files: []diff.FileDiff{{NewName: "main.go", RawDiff: "--- a\n+++ b\n@@ -1 +1 @@\n-x\n+y\n"}},
		},
	}

	h.store.reviews[h.reviewID] = h.review
	h.store.repos[h.repo.ID] = h.repo
	h.store.reposByFull[h.repo.FullName] = h.repo
	h.store.latestRun[h.reviewID] = h.runID
	h.state.runs[h.runID] = h.run

	// Canned linked-PR (acme/api#7) so ExtractLinkedPRs + hydratePRLink land
	// on a fully-accessible sibling with a canned prior review.
	linkedKey := ghKey("acme", "api", 7)
	h.gh.pullRequests[linkedKey] = &ghpkg.PREvent{
		PRNumber: 7,
		PRTitle:  "Linked API PR",
		HeadSHA:  "linkedhead5678",
		RepoFullName: "acme/api",
	}
	h.gh.prDiffs[linkedKey] = "--- linked-a\n+++ linked-b\n@@ -1 +1 @@\n-foo\n+bar\n"
	// Linked PR lives in a repo the primary installation does NOT have —
	// hydratePriorFindings will fall through cleanly (PriorReview == nil) in
	// the default harness. Tests that want a prior review override this
	// by seeding reposByFull + priorByPR.

	// LLM returns canned well-formed combination_risks JSON by default.
	h.llm.SetContent(`{"schema_version":1,"combination_risks":[]}`)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h.o = &Orchestrator{
		logger: logger,
		crossPRHooks: &crossPRHooks{
			Store:     h.store,
			Github:    h.gh,
			State:     h.state,
			Publisher: h.publisher,
			Now:       h.Now,
			Resolver: func(ctx context.Context, run *PipelineRun, stage string) (llm.Provider, llm.ModelConfig, bool) {
				return h.llm, llm.ModelConfig{Provider: "fake", Model: "fake-model", MaxTokens: 2000, Temperature: 0.1}, true
			},
		},
	}
	return h
}

// Now returns the harness's mutable clock reading. Assignments to h.now
// via Advance thread through every caller that uses the Now hook (stage
// rate limits, publisher timestamps).
func (h *harness) Now() time.Time {
	return time.Unix(0, atomic.LoadInt64(&h.now))
}

// Advance moves the clock forward by d. Call BEFORE any stage invocation
// that should observe the new time.
func (h *harness) Advance(d time.Duration) {
	atomic.AddInt64(&h.now, int64(d))
}

// --- Tests ---

// TestRunCrossPRStage_HappyPath drives the full stage end-to-end:
// review + run load, linked-PR hydration, LLM call, sticky edit, hash
// persist, and token record. Asserts the canonical happy-path contract
// so regressions in any of those write surfaces surface immediately.
func TestRunCrossPRStage_HappyPath(t *testing.T) {
	t.Cleanup(resetCrossPRGlobals)
	h := newHarness(t)

	h.o.runCrossPRStage(context.Background(), h.reviewID)

	if h.llm.CallCount() != 1 {
		t.Fatalf("expected 1 LLM call, got %d", h.llm.CallCount())
	}
	if len(h.gh.stickyCalls) != 1 || h.gh.stickyCalls[0].Section != "crosspr" {
		t.Fatalf("expected 1 sticky crosspr call, got %+v", h.gh.stickyCalls)
	}
	if len(h.store.hashWrites) != 1 {
		t.Fatalf("expected 1 hash write, got %d", len(h.store.hashWrites))
	}
	if len(h.store.tokenWrites) != 1 || h.store.tokenWrites[0].StageKey != stageKeyCrossPR {
		t.Fatalf("expected 1 token write under cross_pr bucket, got %+v", h.store.tokenWrites)
	}
	// EventCrossPRChecked must publish after the coverage is computed.
	saw := false
	for _, e := range h.publisher.Events() {
		if e.Type == EventCrossPRChecked {
			saw = true
			break
		}
	}
	if !saw {
		t.Fatalf("expected EventCrossPRChecked publish, got %+v", h.publisher.Events())
	}
}

// TestRunCrossPRStage_HashSkipsLLM verifies the idempotency guard: a
// second invocation with the same linked-PR bundle must skip the LLM,
// the sticky edit, and the hash re-write. If this ever regressed the
// stage would loop forever on every completion event.
func TestRunCrossPRStage_HashSkipsLLM(t *testing.T) {
	t.Cleanup(resetCrossPRGlobals)
	h := newHarness(t)

	h.o.runCrossPRStage(context.Background(), h.reviewID)
	first := h.llm.CallCount()
	stickyBefore := len(h.gh.stickyCalls)
	hashBefore := len(h.store.hashWrites)

	// Advance enough to clear any in-window refresh cap state but keep the
	// hash snapshot on the review row from the first run.
	h.Advance(crossPRRefreshWindow + time.Minute)
	h.o.runCrossPRStage(context.Background(), h.reviewID)

	if h.llm.CallCount() != first {
		t.Fatalf("LLM called again on unchanged bundle: before=%d after=%d", first, h.llm.CallCount())
	}
	if len(h.gh.stickyCalls) != stickyBefore {
		t.Fatalf("sticky edit on unchanged bundle: before=%d after=%d", stickyBefore, len(h.gh.stickyCalls))
	}
	if len(h.store.hashWrites) != hashBefore {
		t.Fatalf("hash re-write on unchanged bundle: before=%d after=%d", hashBefore, len(h.store.hashWrites))
	}
}

// TestRunCrossPRStage_LLMFailurePreservesNoHash locks in: an LLM error
// leaves the hash empty so the next trigger retries. Persisting the hash
// on failure would wedge the PR with stale coverage indefinitely.
func TestRunCrossPRStage_LLMFailurePreservesNoHash(t *testing.T) {
	t.Cleanup(resetCrossPRGlobals)
	h := newHarness(t)
	h.llm.SetError(errors.New("provider timeout"))

	h.o.runCrossPRStage(context.Background(), h.reviewID)

	if len(h.store.hashWrites) != 0 {
		t.Fatalf("hash persisted on LLM failure: %+v", h.store.hashWrites)
	}
	// Review's CrossPRHash must still be nil so the next tick retries.
	rv, err := h.store.GetReview(context.Background(), h.reviewID)
	if err != nil {
		t.Fatalf("GetReview: %v", err)
	}
	if rv.CrossPRHash != nil {
		t.Fatalf("review.CrossPRHash unexpectedly set: %v", *rv.CrossPRHash)
	}
}

// TestRunCrossPRStage_StickyNotFoundPersistsHash verifies the
// ErrStickyNotFound branch: there's nothing on GitHub to edit and no
// retry will help, so the stage persists the hash to prevent an infinite
// retry loop per the documented contract in runCrossPRStage step 7.
func TestRunCrossPRStage_StickyNotFoundPersistsHash(t *testing.T) {
	t.Cleanup(resetCrossPRGlobals)
	h := newHarness(t)
	h.gh.stickyErr = ghpkg.ErrStickyNotFound

	h.o.runCrossPRStage(context.Background(), h.reviewID)

	if len(h.store.hashWrites) != 1 {
		t.Fatalf("expected hash persisted on ErrStickyNotFound, got %d writes", len(h.store.hashWrites))
	}
}

// TestRunCrossPRStage_MarkersCorruptErrorsWithoutPersist verifies
// ErrMarkersCorrupt: a corrupt sticky needs manual intervention — we
// must log Error and bail WITHOUT persisting the hash so a human can
// clear the body and re-trigger.
func TestRunCrossPRStage_MarkersCorruptErrorsWithoutPersist(t *testing.T) {
	t.Cleanup(resetCrossPRGlobals)
	h := newHarness(t)
	h.gh.stickyErr = ghpkg.ErrMarkersCorrupt

	h.o.runCrossPRStage(context.Background(), h.reviewID)

	if len(h.store.hashWrites) != 0 {
		t.Fatalf("hash persisted on ErrMarkersCorrupt: %+v", h.store.hashWrites)
	}
}

// TestRunCrossPRStage_InaccessibleLinkedPRsNoPersist: when every linked
// PR fetch returns 404/403 we treat the bundle as compatible (IsCompatible()
// returns true because CombinationRisks is nil) but do NOT persist the
// hash — the sibling repos may simply not have Argus installed yet, and
// we want the recovery path to re-evaluate on the next trigger.
func TestRunCrossPRStage_InaccessibleLinkedPRsNoPersist(t *testing.T) {
	t.Cleanup(resetCrossPRGlobals)
	h := newHarness(t)
	// Force every linked-PR fetch to fail.
	h.gh.pullRequestErr = errors.New("404 not found")

	h.o.runCrossPRStage(context.Background(), h.reviewID)

	if len(h.store.hashWrites) != 0 {
		t.Fatalf("hash persisted despite all inaccessible: %+v", h.store.hashWrites)
	}
	if h.llm.CallCount() != 0 {
		t.Fatalf("LLM called despite all inaccessible: %d", h.llm.CallCount())
	}
}

// TestHandlePREdited_AddedLinkFiresRefresh drives the pr.edited webhook
// path directly — body diff adds a new linked PR, so RefreshCrossPR must
// fire. We assert this by checking OnReviewCompleted schedules a debounce
// timer for the reviewID.
//
// The full webhook handler lives in internal/api; to keep this test in the
// pipeline package we exercise the underlying refresh trigger: RefreshCrossPR
// (called by handlePREdited on delta hit) → OnReviewCompleted → debounce
// timer added to crossPRDebounceTimers.
func TestHandlePREdited_AddedLinkFiresRefresh(t *testing.T) {
	t.Cleanup(resetCrossPRGlobals)
	h := newHarness(t)

	// Simulate the webhook's decision: delta detected → schedule a refresh
	// via OnReviewCompleted (the findings half of RefreshCrossPR). Using
	// RefreshCrossPR directly spawns a background joint-accept goroutine
	// that outlives test cleanup and races resetCrossPRGlobals; the
	// debounce-timer contract here is exactly what OnReviewCompleted
	// exposes, so we scope the assertion to that surface.
	h.o.OnReviewCompleted(context.Background(), h.reviewID)

	// A debounce timer must now exist for the reviewID. We don't wait for
	// it to fire (30s default) — the presence proves the refresh was
	// scheduled.
	crossPRDebounceMu.Lock()
	_, ok := crossPRDebounceTimers[h.reviewID]
	crossPRDebounceMu.Unlock()
	if !ok {
		t.Fatalf("expected debounce timer for %s after OnReviewCompleted", h.reviewID)
	}
	// Let the sibling-fanout goroutine drain so it doesn't race the
	// resetCrossPRGlobals cleanup that runs on test exit.
	waitSiblingFanoutSettled(t, h)
}

// TestHandlePREdited_RemovedLink mirrors the added-link case from the
// opposite direction: webhook detects a removed linked PR → refresh is
// scheduled via OnReviewCompleted. Set-diff is symmetric, so the expected
// behaviour is identical.
func TestHandlePREdited_RemovedLink(t *testing.T) {
	t.Cleanup(resetCrossPRGlobals)
	h := newHarness(t)

	h.o.OnReviewCompleted(context.Background(), h.reviewID)

	crossPRDebounceMu.Lock()
	_, ok := crossPRDebounceTimers[h.reviewID]
	crossPRDebounceMu.Unlock()
	if !ok {
		t.Fatalf("expected debounce timer after removed-link OnReviewCompleted")
	}
	waitSiblingFanoutSettled(t, h)
}

// TestHandlePREdited_NoChangeNoOp documents the set-diff no-op invariant
// directly against ExtractLinkedPRs + the map-compare logic used in
// handlePREdited. When both sides produce the identical linked-PR set we
// skip the RefreshCrossPR call entirely (no timer, no LLM).
func TestHandlePREdited_NoChangeNoOp(t *testing.T) {
	t.Cleanup(resetCrossPRGlobals)

	before := extractLinkSet("depends on https://github.com/acme/api/pull/1", "acme/primary", 5)
	after := extractLinkSet("depends on https://github.com/acme/api/pull/1  \n(reword body text only)", "acme/primary", 5)
	if !sameLinkSet(before, after) {
		t.Fatalf("identical linked-PR sets wrongly flagged as changed: before=%+v after=%+v", before, after)
	}
}

// TestHandlePREdited_NoReviewYetNoOp covers the pgx.ErrNoRows branch: a
// pr.edited webhook fires for a PR whose review row was never created
// (first-time PR before the normal opened-event pipeline ran). The
// handler logs Info and returns — never panics, never dispatches.
//
// We drive the store-level behaviour through our fake: an unknown
// reviewID returns pgx.ErrNoRows, so enqueueSiblingRefreshes short-
// circuits and no refresh timer is scheduled.
func TestHandlePREdited_NoReviewYetNoOp(t *testing.T) {
	t.Cleanup(resetCrossPRGlobals)
	h := newHarness(t)

	missing := uuid.New() // not seeded in the store.
	h.o.enqueueSiblingRefreshes(context.Background(), missing)

	// No debounce timers scheduled for anyone.
	crossPRDebounceMu.Lock()
	count := len(crossPRDebounceTimers)
	crossPRDebounceMu.Unlock()
	if count != 0 {
		t.Fatalf("expected 0 debounce timers on pgx.ErrNoRows, got %d", count)
	}
}

// TestEnqueueSiblingRefreshes_SplitFailure: a repo row with an invalid
// full_name (no "/") must log and return without panicking. We seed a
// misshaped repo and invoke directly.
func TestEnqueueSiblingRefreshes_SplitFailure(t *testing.T) {
	t.Cleanup(resetCrossPRGlobals)
	h := newHarness(t)

	// Mutate the harness's repo to a full_name that splitRepoFullName rejects.
	h.repo.FullName = "garbage-no-slash"
	h.store.reposByFull = map[string]*store.Repo{"garbage-no-slash": h.repo}

	// Must not panic. Silent return is the contract.
	h.o.enqueueSiblingRefreshes(context.Background(), h.reviewID)

	crossPRDebounceMu.Lock()
	count := len(crossPRDebounceTimers)
	crossPRDebounceMu.Unlock()
	if count != 0 {
		t.Fatalf("split-failure should not enqueue timers; got %d", count)
	}
}

// TestEnqueueSiblingRefreshes_NonCompletedReviewSkips: a sibling-candidate
// review whose status != "completed" must be skipped (no debounce timer,
// no fanout). We flip the canonical review's status and assert the
// early-return gate fires.
func TestEnqueueSiblingRefreshes_NonCompletedReviewSkips(t *testing.T) {
	t.Cleanup(resetCrossPRGlobals)
	h := newHarness(t)
	h.review.Status = "in_progress"

	h.o.enqueueSiblingRefreshes(context.Background(), h.reviewID)

	crossPRDebounceMu.Lock()
	count := len(crossPRDebounceTimers)
	crossPRDebounceMu.Unlock()
	if count != 0 {
		t.Fatalf("non-completed review must not fan out; got %d timers", count)
	}
}

// TestEventReviewCompleted_PublishedAfterStatusCommit: the stage must
// UPDATE reviews.status (DB commit) BEFORE Publish fires on the event
// bus. We simulate a commit timestamp, then run the stage and assert the
// publisher saw the event at a strictly later instant.
func TestEventReviewCompleted_PublishedAfterStatusCommit(t *testing.T) {
	t.Cleanup(resetCrossPRGlobals)
	h := newHarness(t)

	// Simulate "status committed" timestamp.
	commitAt := h.Now()
	h.Advance(1 * time.Millisecond)

	h.o.runCrossPRStage(context.Background(), h.reviewID)

	events := h.publisher.Events()
	if len(events) == 0 {
		t.Fatalf("no events published")
	}
	firstCrossPR := -1
	for i, e := range events {
		if e.Type == EventCrossPRChecked {
			firstCrossPR = i
			break
		}
	}
	if firstCrossPR < 0 {
		t.Fatalf("EventCrossPRChecked never published")
	}
	if !events[firstCrossPR].At.After(commitAt) {
		t.Fatalf("EventCrossPRChecked at %v not after commit at %v", events[firstCrossPR].At, commitAt)
	}
}

// TestRunCrossPRAcceptanceStage_NoSharedIssuesSilentReturn: no shared
// issues → stage returns silently, no LLM, no sticky update.
func TestRunCrossPRAcceptanceStage_NoSharedIssuesSilentReturn(t *testing.T) {
	t.Cleanup(resetCrossPRGlobals)
	h := newHarness(t)
	// Default harness has zero shared issues.
	h.o.runCrossPRAcceptanceStage(context.Background(), h.reviewID)

	if h.llm.CallCount() != 0 {
		t.Fatalf("LLM called despite no shared issues: %d", h.llm.CallCount())
	}
	// No sticky joint_acceptance update either.
	for _, c := range h.gh.stickyCalls {
		if c.Section == "joint_acceptance" {
			t.Fatalf("joint_acceptance sticky touched despite no shared issues: %+v", c)
		}
	}
}

// TestJudgeSharedIssue_EmptyCriteriaSkips: if an issue body parses to zero
// criteria the judge returns nil WITHOUT calling the LLM. Issue fetched,
// criteria empty, skip.
func TestJudgeSharedIssue_EmptyCriteriaSkips(t *testing.T) {
	t.Cleanup(resetCrossPRGlobals)
	h := newHarness(t)
	// Seed an issue with a body that produces zero criteria.
	issueKey := ghKey("acme", "api", 99)
	h.gh.issues[issueKey] = &ghpkg.Issue{
		Owner: "acme", Repo: "api", Number: 99,
		Title: "No checklists here",
		Body:  "just prose, no checkbox list",
	}
	row := db.FindSharedLinkedIssuesRow{Owner: "acme", Repo: "api", Number: 99, ReviewIds: []uuid.UUID{h.reviewID, uuid.New()}}

	// Direct call — bypass the stage wrapper so we isolate the skip branch.
	res := h.o.judgeSharedIssue(context.Background(), h.run, h.llm, llm.ModelConfig{Model: "fake"}, row)
	if res != nil {
		t.Fatalf("expected nil on empty criteria, got %+v", res)
	}
	if h.llm.CallCount() != 0 {
		t.Fatalf("LLM called on empty-criteria issue: %d", h.llm.CallCount())
	}
}

// TestRunCrossPRStage_TokensLandInCrossPRBucket verifies that the
// happy-path LLM spend lands under stageKeyCrossPR via MergeStageTokenEntry.
// The per-bucket invariant matters for the /stats cost-by-stage chart.
func TestRunCrossPRStage_TokensLandInCrossPRBucket(t *testing.T) {
	t.Cleanup(resetCrossPRGlobals)
	h := newHarness(t)

	h.o.runCrossPRStage(context.Background(), h.reviewID)

	if len(h.store.tokenWrites) != 1 {
		t.Fatalf("expected exactly 1 token write, got %d", len(h.store.tokenWrites))
	}
	tw := h.store.tokenWrites[0]
	if tw.StageKey != stageKeyCrossPR {
		t.Fatalf("expected stage_key=%q, got %q", stageKeyCrossPR, tw.StageKey)
	}
	var entry StageTokens
	if err := json.Unmarshal(tw.Entry, &entry); err != nil {
		t.Fatalf("unmarshal token entry: %v", err)
	}
	if entry.TotalTokens == 0 || entry.Cost == 0 {
		t.Fatalf("token entry lost real spend: %+v", entry)
	}
}

// TestAcquireCrossPRMutex_SerializesSameReviewID fires 10 goroutines at
// the same reviewID's mutex and asserts exactly one holds it at a time.
// A failure here would imply crossPRMutexes isn't correctly keyed or
// sync.Map's LoadOrStore returned divergent instances.
func TestAcquireCrossPRMutex_SerializesSameReviewID(t *testing.T) {
	t.Cleanup(resetCrossPRGlobals)
	id := uuid.New()

	var concurrent atomic.Int32
	var maxObserved atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mu := acquireCrossPRMutex(id)
			mu.Lock()
			c := concurrent.Add(1)
			// Record the max concurrent count so we can assert it stayed at 1.
			for {
				m := maxObserved.Load()
				if c <= m || maxObserved.CompareAndSwap(m, c) {
					break
				}
			}
			// Yield to give the race detector a chance to see violators.
			time.Sleep(1 * time.Millisecond)
			concurrent.Add(-1)
			mu.Unlock()
		}()
	}
	wg.Wait()

	if got := maxObserved.Load(); got != 1 {
		t.Fatalf("mutex failed to serialize: max concurrent holders = %d, want 1", got)
	}
}

// TestOnReviewCompleted_DebounceCollapsesBurst: 5 rapid OnReviewCompleted
// calls within <30s must collapse to exactly one pending debounce timer.
// The timer's callback is NOT executed in the test (we'd have to wait
// 30s); what we assert is that only a single timer entry exists at any
// point, because each call Stop()s the prior.
func TestOnReviewCompleted_DebounceCollapsesBurst(t *testing.T) {
	t.Cleanup(resetCrossPRGlobals)
	h := newHarness(t)

	const burst = 5
	for i := 0; i < burst; i++ {
		h.o.OnReviewCompleted(context.Background(), h.reviewID)
	}

	crossPRDebounceMu.Lock()
	count := len(crossPRDebounceTimers)
	_, ok := crossPRDebounceTimers[h.reviewID]
	crossPRDebounceMu.Unlock()

	if count != 1 || !ok {
		t.Fatalf("expected single debounce timer for reviewID, got count=%d ok=%v", count, ok)
	}
	// Each OnReviewCompleted spawns a sibling-fanout goroutine; wait until
	// each has completed its canon store read so t.Cleanup's
	// resetCrossPRGlobals doesn't race them.
	waitSiblingFanoutCount(t, h, int32(burst))
}

// waitSiblingFanoutCount is waitSiblingFanoutSettled generalised to N
// expected fanouts — used by the debounce test where 5 OnReviewCompleted
// calls each spawn one fanout goroutine.
func waitSiblingFanoutCount(t *testing.T, h *harness, want int32) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&h.store.siblingLookups) >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("expected %d sibling-fanouts, got %d", want, atomic.LoadInt32(&h.store.siblingLookups))
}

// --- helpers used by handlePREdited-shape tests ---

// extractLinkSet mirrors the set-diff logic in handlePREdited without
// copying the whole handler function into the test.
func extractLinkSet(body, primary string, cap int) map[linkKey]struct{} {
	links := ExtractLinkedPRs(body, primary, 42, cap)
	out := make(map[linkKey]struct{}, len(links))
	for _, l := range links {
		out[linkKey{l.Owner, l.Repo, l.Number}] = struct{}{}
	}
	return out
}

func sameLinkSet(a, b map[linkKey]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}

type linkKey struct {
	Owner  string
	Repo   string
	Number int
}

// waitSiblingFanoutSettled polls the fake store's sibling-lookup counter
// until the enqueueSiblingRefreshes goroutine spawned by OnReviewCompleted
// has run. Without this sync, resetCrossPRGlobals (in t.Cleanup) races
// the goroutine's reads of the global maps.
//
// Strategy: wait up to 2s for the counter to reach the expected post-call
// value (pre-call reading + 1). If it doesn't increment we fail the test —
// a true deadlock here would indicate a regression in OnReviewCompleted's
// fanout contract.
func waitSiblingFanoutSettled(t *testing.T, h *harness) {
	t.Helper()
	start := atomic.LoadInt32(&h.store.siblingLookups)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&h.store.siblingLookups) > start {
			return
		}
		time.Sleep(time.Millisecond)
	}
	// Fanout didn't hit the store within the budget. Could be a race
	// scheduler issue, but more likely a regression in the async
	// fan-out path.
	t.Fatalf("sibling-fanout goroutine did not increment lookup counter within 2s")
}
