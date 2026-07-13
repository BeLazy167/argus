// Package pipeline — crosspr_stage_deps.go holds the minimal dependency
// interfaces the async cross-PR stage calls out to. Production callers get
// the concrete *store.Store / *ghpkg.Client / *StateMachine wrapped in
// default adapters; tests set Orchestrator.crossPRHooks to route through
// in-memory fakes without touching production wiring.
//
// The interfaces deliberately carry ONLY the method subset crosspr_stage.go
// (and crosspr.go's hydratePRLink) actually invoke, so they can be
// satisfied by a test fake in a few dozen lines each.
package pipeline

import (
	"context"
	"encoding/json"
	"time"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/llm"
	"github.com/BeLazy167/argus/backend/internal/store"
	"github.com/BeLazy167/argus/backend/internal/store/db"
	"github.com/google/uuid"
)

// crossPRStore is the store surface the async cross-PR stage consumes.
// Method names + signatures mirror *store.Store and *db.Queries verbatim
// so the defaultCrossPRStore adapter below is one-line pass-throughs.
type crossPRStore interface {
	GetReview(ctx context.Context, id uuid.UUID) (*store.Review, error)
	GetRepo(ctx context.Context, id int64) (*store.Repo, error)
	GetRepoByFullName(ctx context.Context, fullName string) (*store.Repo, error)

	GetLatestRunForReview(ctx context.Context, reviewID uuid.UUID) (uuid.UUID, error)
	FindReviewsLinkingToPR(ctx context.Context, arg db.FindReviewsLinkingToPRParams) ([]db.FindReviewsLinkingToPRRow, error)
	SetReviewLinkedPRRefs(ctx context.Context, arg db.SetReviewLinkedPRRefsParams) error
	SetReviewLinkedIssueRefs(ctx context.Context, arg db.SetReviewLinkedIssueRefsParams) error
	UpdateReviewCrossPRHash(ctx context.Context, arg db.UpdateReviewCrossPRHashParams) error
	GetLatestCompletedReviewByPR(ctx context.Context, arg db.GetLatestCompletedReviewByPRParams) (db.GetLatestCompletedReviewByPRRow, error)
	FindSharedLinkedIssues(ctx context.Context, reviewID uuid.UUID) ([]db.FindSharedLinkedIssuesRow, error)
	GetAllFileReviewsForReview(ctx context.Context, reviewID uuid.UUID) (json.RawMessage, error)
	MergeStageTokenEntry(ctx context.Context, arg db.MergeStageTokenEntryParams) (int64, error)

	// LoadFeatureFlags mirrors the free-function loadFeatureFlags; lifted
	// to the interface so tests can inject per-installation toggles without
	// also mocking GetInstallationFeatureFlags's JSONB round-trip.
	LoadFeatureFlags(ctx context.Context, installationDBID int64) FeatureFlags
}

// crossPRGithub is the GitHub surface the async cross-PR stage (and the
// hydratePRLink helper in crosspr.go) consume.
type crossPRGithub interface {
	UpdateStickySection(ctx context.Context, installationID int64, owner, repo string, prNumber int, stickyReviewID int64, section, bodyMD string) error
	GetPullRequest(ctx context.Context, installationID int64, owner, repo string, prNumber int) (*ghpkg.PREvent, error)
	GetPRDiff(ctx context.Context, installationID int64, owner, repo string, prNumber int) (string, error)
	GetIssue(ctx context.Context, installationID int64, owner, repo string, number int) (*ghpkg.Issue, error)
}

// crossPRStateLoader hydrates a PipelineRun by run id. Mirrors
// *StateMachine.loadState (intentionally unexported on the real type —
// we re-declare here as the test seam).
type crossPRStateLoader interface {
	LoadRun(ctx context.Context, runID uuid.UUID) (*PipelineRun, error)
}

// crossPRProviderResolver returns an llm.Provider + config for a stage.
// Matches Orchestrator.resolveLeadProvider's signature verbatim so the
// default hook is a one-line method reference.
type crossPRProviderResolver func(ctx context.Context, run *PipelineRun, stage string) (llm.Provider, llm.ModelConfig, bool)

// crossPREventPublisher is the slice of EventBus the stage publishes
// through. Only Publish is needed; subscription is owned by the
// Orchestrator constructor.
type crossPREventPublisher interface {
	Publish(reviewID uuid.UUID, evtType EventType, data any)
}

// crossPRHooks is the override bundle. Orchestrator.crossPRHooks is nil in
// production; tests construct a crossPRHooks with fakes and assign it to
// skip the concrete adapters. Any field left as the zero value falls back
// to the concrete default (see orchestrator accessors below).
type crossPRHooks struct {
	Store     crossPRStore
	Github    crossPRGithub
	State     crossPRStateLoader
	Resolver  crossPRProviderResolver
	Publisher crossPREventPublisher
	// Now is the time source for debounce + refresh-cap + install-cap
	// arithmetic. nil → time.Now. Integration tests inject a driver clock
	// so the 30-second debounce can be exercised without real sleeps.
	Now func() time.Time
}

// --- Default adapters over concrete production types ---

// defaultCrossPRStore wraps *store.Store and *store.Store.Q into the
// crossPRStore interface. Pure delegation — no business logic here.
type defaultCrossPRStore struct{ st *store.Store }

func (d defaultCrossPRStore) GetReview(ctx context.Context, id uuid.UUID) (*store.Review, error) {
	return d.st.GetReview(ctx, id)
}
func (d defaultCrossPRStore) GetRepo(ctx context.Context, id int64) (*store.Repo, error) {
	return d.st.GetRepo(ctx, id)
}
func (d defaultCrossPRStore) GetRepoByFullName(ctx context.Context, fullName string) (*store.Repo, error) {
	return d.st.GetRepoByFullName(ctx, fullName)
}
func (d defaultCrossPRStore) GetLatestRunForReview(ctx context.Context, reviewID uuid.UUID) (uuid.UUID, error) {
	return d.st.Q.GetLatestRunForReview(ctx, reviewID)
}
func (d defaultCrossPRStore) FindReviewsLinkingToPR(ctx context.Context, arg db.FindReviewsLinkingToPRParams) ([]db.FindReviewsLinkingToPRRow, error) {
	return d.st.Q.FindReviewsLinkingToPR(ctx, arg)
}
func (d defaultCrossPRStore) SetReviewLinkedPRRefs(ctx context.Context, arg db.SetReviewLinkedPRRefsParams) error {
	return d.st.Q.SetReviewLinkedPRRefs(ctx, arg)
}
func (d defaultCrossPRStore) SetReviewLinkedIssueRefs(ctx context.Context, arg db.SetReviewLinkedIssueRefsParams) error {
	return d.st.Q.SetReviewLinkedIssueRefs(ctx, arg)
}
func (d defaultCrossPRStore) UpdateReviewCrossPRHash(ctx context.Context, arg db.UpdateReviewCrossPRHashParams) error {
	return d.st.Q.UpdateReviewCrossPRHash(ctx, arg)
}
func (d defaultCrossPRStore) GetLatestCompletedReviewByPR(ctx context.Context, arg db.GetLatestCompletedReviewByPRParams) (db.GetLatestCompletedReviewByPRRow, error) {
	return d.st.Q.GetLatestCompletedReviewByPR(ctx, arg)
}
func (d defaultCrossPRStore) FindSharedLinkedIssues(ctx context.Context, reviewID uuid.UUID) ([]db.FindSharedLinkedIssuesRow, error) {
	return d.st.Q.FindSharedLinkedIssues(ctx, reviewID)
}
func (d defaultCrossPRStore) GetAllFileReviewsForReview(ctx context.Context, reviewID uuid.UUID) (json.RawMessage, error) {
	return d.st.GetAllFileReviewsForReview(ctx, reviewID)
}
func (d defaultCrossPRStore) MergeStageTokenEntry(ctx context.Context, arg db.MergeStageTokenEntryParams) (int64, error) {
	return d.st.Q.MergeStageTokenEntry(ctx, arg)
}
func (d defaultCrossPRStore) LoadFeatureFlags(ctx context.Context, installationDBID int64) FeatureFlags {
	return loadFeatureFlags(ctx, featureFlagReaderFor(d.st), installationDBID)
}

// defaultCrossPRState wraps *StateMachine's loadState. Lives in the
// pipeline package so it can reach the unexported method.
type defaultCrossPRState struct{ sm *StateMachine }

func (d defaultCrossPRState) LoadRun(ctx context.Context, runID uuid.UUID) (*PipelineRun, error) {
	return d.sm.loadState(ctx, runID)
}

// --- Orchestrator accessors: return hook override if set, else default. ---

// crossPRStoreDep returns the store surface the cross-PR stage uses.
// If Orchestrator.crossPRHooks.Store is non-nil, that fake is returned —
// otherwise a default adapter over o.st is constructed on the fly. The
// hot-path allocation is a tiny struct literal; escapes analysis keeps it
// on the stack.
func (o *Orchestrator) crossPRStoreDep() crossPRStore {
	if o.crossPRHooks != nil && o.crossPRHooks.Store != nil {
		return o.crossPRHooks.Store
	}
	return defaultCrossPRStore{st: o.st}
}

// crossPRGithubDep returns the GitHub surface for the stage. Default path
// returns the concrete *ghpkg.Client — it already satisfies crossPRGithub
// because we copy the method signatures verbatim.
func (o *Orchestrator) crossPRGithubDep() crossPRGithub {
	if o.crossPRHooks != nil && o.crossPRHooks.Github != nil {
		return o.crossPRHooks.Github
	}
	return o.ghClient
}

// crossPRStateDep returns the pipeline-state loader. Default wraps
// *StateMachine; hook can inject a canned *PipelineRun for a given runID.
func (o *Orchestrator) crossPRStateDep() crossPRStateLoader {
	if o.crossPRHooks != nil && o.crossPRHooks.State != nil {
		return o.crossPRHooks.State
	}
	return defaultCrossPRState{sm: o.sm}
}

// crossPRResolveProvider routes the LLM provider resolution. Default uses
// the production resolveLeadProvider; tests swap in a func that returns a
// canned fakeLLMProvider so the stage never hits real HTTP.
func (o *Orchestrator) crossPRResolveProvider(ctx context.Context, run *PipelineRun, stage string) (llm.Provider, llm.ModelConfig, bool) {
	if o.crossPRHooks != nil && o.crossPRHooks.Resolver != nil {
		return o.crossPRHooks.Resolver(ctx, run, stage)
	}
	return o.resolveLeadProvider(ctx, run, stage)
}

// crossPRPublisherDep returns the event publisher. Default is o.eventBus
// (may be nil in production when the bus is disabled; callers must
// nil-check the returned value). Hook override returns a capturing fake.
func (o *Orchestrator) crossPRPublisherDep() crossPREventPublisher {
	if o.crossPRHooks != nil && o.crossPRHooks.Publisher != nil {
		return o.crossPRHooks.Publisher
	}
	if o.eventBus == nil {
		return nil
	}
	return o.eventBus
}

// crossPRNow returns the stage's time source. Defaults to time.Now.
// Injected in the debounce integration test so a 30-second window can be
// stepped in sub-millisecond test time.
func (o *Orchestrator) crossPRNow() time.Time {
	if o.crossPRHooks != nil && o.crossPRHooks.Now != nil {
		return o.crossPRHooks.Now()
	}
	return time.Now()
}
