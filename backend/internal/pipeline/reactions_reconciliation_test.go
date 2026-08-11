package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/store"
	gh "github.com/google/go-github/v68/github"
	"github.com/google/uuid"
)

type reactionStoreStub struct {
	comment  *store.ReviewComment
	ids      []int64
	outcomes []string
}

func (s *reactionStoreStub) GetCommentByGithubID(context.Context, int64) (*store.ReviewComment, error) {
	return s.comment, nil
}
func (s *reactionStoreStub) RecordCommentOutcome(_ context.Context, _ uuid.UUID, outcome string) (bool, error) {
	s.outcomes = append(s.outcomes, outcome)
	return true, nil
}
func (*reactionStoreStub) GetInstallationByGitHubID(context.Context, int64) (*store.Installation, error) {
	return &store.Installation{ID: 7, InstallationID: 91}, nil
}
func (*reactionStoreStub) GetCommentChangeClass(context.Context, uuid.UUID) (string, error) {
	return "production", nil
}
func (s *reactionStoreStub) ListPRGithubCommentIDs(context.Context, string, int) ([]int64, error) {
	return s.ids, nil
}
func (*reactionStoreStub) RecordPatternOutcome(context.Context, uuid.UUID, int64, bool) (float64, bool, error) {
	return 0, false, nil
}

type reactionGitHubStub struct {
	reactions []ghpkg.CommentReaction
	err       error
	calls     int
}

func (g *reactionGitHubStub) ListCommentReactions(context.Context, int64, string, string, int64) ([]ghpkg.CommentReaction, error) {
	g.calls++
	return g.reactions, g.err
}

type reactionPermissionCall struct {
	installationID int64
	owner          string
	repo           string
	login          string
}

type reactionPermissionStub struct {
	allowedByLogin map[string]bool
	errByLogin     map[string]error
	calls          map[string]int
	requests       []reactionPermissionCall
}

func (s *reactionPermissionStub) HasRepoWriteAccess(_ context.Context, installationID int64, owner, repo, login string) (bool, error) {
	if s.calls == nil {
		s.calls = make(map[string]int)
	}
	s.calls[login]++
	s.requests = append(s.requests, reactionPermissionCall{installationID: installationID, owner: owner, repo: repo, login: login})
	return s.allowedByLogin[login], s.errByLogin[login]
}

type recordingReactionLifecycle struct {
	events []LifecycleEvent
}

func (l *recordingReactionLifecycle) Transition(_ context.Context, transition FindingTransition) (TransitionResult, error) {
	l.events = append(l.events, transition.Event)
	return TransitionResult{}, nil
}

type reactionRegistryStub struct{ indexer memory.Indexer }

func (r reactionRegistryStub) GetIndexer(context.Context, int64) memory.Indexer { return r.indexer }

type reactionLifecycleStub struct{}

func (reactionLifecycleStub) Transition(context.Context, FindingTransition) (TransitionResult, error) {
	return TransitionResult{}, nil
}

type reactionIndexerStub struct {
	memory.Indexer
	actions       []string
	dismissalLive bool
}

func (i *reactionIndexerStub) ReconcileFeedbackSignal(_ context.Context, _, _ string, feedback memory.FeedbackMemory) error {
	i.actions = append(i.actions, feedback.Action)
	i.dismissalLive = feedback.Action == "dismissed"
	return nil
}

func TestSweepPRReactionsRemovedThumbsDownRetractsDismissal(t *testing.T) {
	category := "bug_risk"
	idx := &reactionIndexerStub{}
	gh := &reactionGitHubStub{reactions: []ghpkg.CommentReaction{{Content: "-1", User: "maintainer"}}}
	ra := &ReactionAnalyzer{
		store: &reactionStoreStub{
			ids: []int64{501},
			comment: &store.ReviewComment{
				ID: uuid.New(), FilePath: "handler.go", Body: "The guard is inverted", Category: &category,
			},
		},
		ghClient:        gh,
		repoPermissions: &reactionPermissionStub{allowedByLogin: map[string]bool{"maintainer": true}},
		memRegistry:     reactionRegistryStub{indexer: idx},
		logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		lifecycle:       reactionLifecycleStub{},
	}

	if err := ra.SweepPRReactions(context.Background(), 91, "acme/api", 17); err != nil {
		t.Fatalf("initial thumbs-down sweep: %v", err)
	}
	if !idx.dismissalLive {
		t.Fatal("thumbs-down did not install reaction-owned dismissal")
	}

	gh.reactions = nil // GitHub's current aggregate after reaction removal.
	if err := ra.SweepPRReactions(context.Background(), 91, "acme/api", 17); err != nil {
		t.Fatalf("removal sweep: %v", err)
	}
	if idx.dismissalLive {
		t.Fatal("removed thumbs-down remains available for suppression")
	}
	if got := strings.Join(idx.actions, ","); got != "dismissed," {
		t.Fatalf("reconciled actions = %q, want dismissed then neutral", got)
	}
}

func TestSweepPRReactionsReturnsIncompleteReconciliationError(t *testing.T) {
	category := "bug_risk"
	githubErr := errors.New("upstream unavailable")
	ra := &ReactionAnalyzer{
		store: &reactionStoreStub{
			ids:     []int64{501},
			comment: &store.ReviewComment{ID: uuid.New(), Category: &category},
		},
		ghClient: &reactionGitHubStub{err: githubErr},
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	err := ra.SweepPRReactions(context.Background(), 91, "acme/api", 17)
	if !errors.Is(err, githubErr) {
		t.Fatalf("sweep error = %v, want wrapped GitHub failure", err)
	}
}

func TestSweepPRReactionsDeletedCommentRetractsReactionFeedback(t *testing.T) {
	category := "bug_risk"
	idx := &reactionIndexerStub{dismissalLive: true}
	ra := &ReactionAnalyzer{
		store: &reactionStoreStub{
			ids: []int64{501},
			comment: &store.ReviewComment{
				ID: uuid.New(), FilePath: "handler.go", Body: "The guard is inverted", Category: &category,
			},
		},
		ghClient:    &reactionGitHubStub{err: fmt.Errorf("listing reactions: %w", ghpkg.ErrReviewCommentNotFound)},
		memRegistry: reactionRegistryStub{indexer: idx},
		logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		lifecycle:   reactionLifecycleStub{},
	}

	if err := ra.SweepPRReactions(context.Background(), 91, "acme/api", 17); err != nil {
		t.Fatalf("deleted comment sweep blocked review: %v", err)
	}
	if idx.dismissalLive {
		t.Fatal("deleted comment left reaction-owned dismissal available for suppression")
	}
	if len(idx.actions) != 1 || idx.actions[0] != "" {
		t.Fatalf("reconciled actions = %v, want one neutral reconciliation", idx.actions)
	}

	// Historical github_comment_id values remain in the finding ledger. A later
	// sweep sees the retained ID again; repeated deletion reconciliation is a
	// harmless neutral upsert rather than a permanent launch failure.
	if err := ra.SweepPRReactions(context.Background(), 91, "acme/api", 17); err != nil {
		t.Fatalf("repeat deleted comment sweep blocked review: %v", err)
	}
	if gh := ra.ghClient.(*reactionGitHubStub); gh.calls != 2 {
		t.Fatalf("reaction fetch calls = %d, want retained historical ID swept twice", gh.calls)
	}
	if len(idx.actions) != 2 || idx.actions[1] != "" {
		t.Fatalf("repeat reconciled actions = %v, want neutral idempotence", idx.actions)
	}
}

func TestSweepPRReactionsTransientServerErrorStillBlocksReview(t *testing.T) {
	category := "bug_risk"
	idx := &reactionIndexerStub{dismissalLive: true}
	serverErr := &gh.ErrorResponse{Response: &http.Response{StatusCode: http.StatusServiceUnavailable}}
	ra := &ReactionAnalyzer{
		store: &reactionStoreStub{
			ids:     []int64{501},
			comment: &store.ReviewComment{ID: uuid.New(), Category: &category},
		},
		ghClient:    &reactionGitHubStub{err: serverErr},
		memRegistry: reactionRegistryStub{indexer: idx},
		logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		lifecycle:   reactionLifecycleStub{},
	}

	err := ra.SweepPRReactions(context.Background(), 91, "acme/api", 17)
	if !errors.Is(err, serverErr) {
		t.Fatalf("sweep error = %v, want wrapped transient GitHub failure", err)
	}
	if len(idx.actions) != 0 {
		t.Fatalf("transient error reconciled unobserved state: actions=%v", idx.actions)
	}
	if !idx.dismissalLive {
		t.Fatal("transient error silently retracted reaction state")
	}
}

func TestSweepPRReactionsCountsOnlyRepoWriteAuthorizedReactors(t *testing.T) {
	category := "bug_risk"
	st := &reactionStoreStub{
		ids: []int64{501},
		comment: &store.ReviewComment{
			ID: uuid.New(), FilePath: "handler.go", Body: "The guard is inverted", Category: &category,
		},
	}
	idx := &reactionIndexerStub{dismissalLive: true}
	lifecycle := &recordingReactionLifecycle{}
	permissions := &reactionPermissionStub{allowedByLogin: map[string]bool{"maintainer": true}}
	ra := &ReactionAnalyzer{
		store: st,
		ghClient: &reactionGitHubStub{reactions: []ghpkg.CommentReaction{
			{Content: "-1", User: "drive-by"},
			{Content: "+1", User: "maintainer"},
			{Content: "-1", User: "argus[bot]"},
		}},
		repoPermissions: permissions,
		memRegistry:     reactionRegistryStub{indexer: idx},
		logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		lifecycle:       lifecycle,
	}

	if err := ra.SweepPRReactions(context.Background(), 91, "acme/api", 17); err != nil {
		t.Fatalf("reaction sweep: %v", err)
	}
	if got := strings.Join(st.outcomes, ","); got != "confirmed" {
		t.Fatalf("recorded outcomes = %q, want only authorized confirmation", got)
	}
	if idx.dismissalLive {
		t.Fatal("untrusted thumbs-down remained authoritative over authorized reactor tally")
	}
	if got := strings.Join(idx.actions, ","); got != "confirmed" {
		t.Fatalf("reconciled actions = %q, want confirmed", got)
	}
	if len(lifecycle.events) != 0 {
		t.Fatalf("untrusted thumbs-down dismissed finding lifecycle: %v", lifecycle.events)
	}
	if permissions.calls["drive-by"] != 1 || permissions.calls["maintainer"] != 1 {
		t.Fatalf("permission calls = %#v, want one per unique non-bot signal actor", permissions.calls)
	}
	if permissions.calls["argus[bot]"] != 0 {
		t.Fatalf("bot permission calls = %d, want zero", permissions.calls["argus[bot]"])
	}
	for _, request := range permissions.requests {
		if request.installationID != 91 || request.owner != "acme" || request.repo != "api" {
			t.Fatalf("permission request = %+v, want actual installation and repository", request)
		}
	}
}

func TestSweepPRReactionsUntrustedThumbsDownCannotCreateDurableDismissal(t *testing.T) {
	category := "bug_risk"
	st := &reactionStoreStub{
		ids:     []int64{501},
		comment: &store.ReviewComment{ID: uuid.New(), Body: "race", Category: &category},
	}
	idx := &reactionIndexerStub{dismissalLive: true}
	lifecycle := &recordingReactionLifecycle{}
	ra := &ReactionAnalyzer{
		store:           st,
		ghClient:        &reactionGitHubStub{reactions: []ghpkg.CommentReaction{{Content: "-1", User: "drive-by"}}},
		repoPermissions: &reactionPermissionStub{},
		memRegistry:     reactionRegistryStub{indexer: idx},
		logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		lifecycle:       lifecycle,
	}

	if err := ra.SweepPRReactions(context.Background(), 91, "acme/api", 17); err != nil {
		t.Fatalf("reaction sweep: %v", err)
	}
	if len(st.outcomes) != 0 || len(lifecycle.events) != 0 {
		t.Fatalf("untrusted reaction created durable writes: outcomes=%v events=%v", st.outcomes, lifecycle.events)
	}
	if idx.dismissalLive || len(idx.actions) != 1 || idx.actions[0] != "" {
		t.Fatalf("untrusted reaction did not reconcile to neutral: live=%v actions=%v", idx.dismissalLive, idx.actions)
	}
}

func TestSweepPRReactionsCachesPermissionByUniqueReactorAcrossComments(t *testing.T) {
	category := "bug_risk"
	permissions := &reactionPermissionStub{allowedByLogin: map[string]bool{"Maintainer": true}}
	ra := &ReactionAnalyzer{
		store: &reactionStoreStub{
			ids:     []int64{501, 502, 503},
			comment: &store.ReviewComment{ID: uuid.New(), Body: "race", Category: &category},
		},
		ghClient:        &reactionGitHubStub{reactions: []ghpkg.CommentReaction{{Content: "-1", User: "Maintainer"}, {Content: "+1", User: "maintainer"}}},
		repoPermissions: permissions,
		logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		lifecycle:       reactionLifecycleStub{},
	}

	if err := ra.SweepPRReactions(context.Background(), 91, "acme/api", 17); err != nil {
		t.Fatalf("reaction sweep: %v", err)
	}
	if permissions.calls["Maintainer"]+permissions.calls["maintainer"] != 1 {
		t.Fatalf("permission calls = %#v, want one case-insensitive lookup across entire sweep", permissions.calls)
	}
}

func TestSweepPRReactionsPermissionLookupFailureBlocksReconciliation(t *testing.T) {
	category := "bug_risk"
	lookupErr := errors.New("GitHub permission endpoint unavailable")
	st := &reactionStoreStub{
		ids:     []int64{501},
		comment: &store.ReviewComment{ID: uuid.New(), Body: "race", Category: &category},
	}
	idx := &reactionIndexerStub{dismissalLive: true}
	lifecycle := &recordingReactionLifecycle{}
	ra := &ReactionAnalyzer{
		store:           st,
		ghClient:        &reactionGitHubStub{reactions: []ghpkg.CommentReaction{{Content: "-1", User: "maintainer"}}},
		repoPermissions: &reactionPermissionStub{errByLogin: map[string]error{"maintainer": lookupErr}},
		memRegistry:     reactionRegistryStub{indexer: idx},
		logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		lifecycle:       lifecycle,
	}

	err := ra.SweepPRReactions(context.Background(), 91, "acme/api", 17)
	if !errors.Is(err, lookupErr) {
		t.Fatalf("sweep error = %v, want wrapped permission lookup failure", err)
	}
	if len(st.outcomes) != 0 || len(idx.actions) != 0 || len(lifecycle.events) != 0 {
		t.Fatalf("failed-closed lookup mutated state: outcomes=%v actions=%v events=%v", st.outcomes, idx.actions, lifecycle.events)
	}
	if !idx.dismissalLive {
		t.Fatal("permission outage retracted prior state as if actors were unauthorized")
	}
}

func TestSweepPRReactionsPermissionBudgetExhaustionDegradesCommentToNeutral(t *testing.T) {
	category := "bug_risk"
	reactions := make([]ghpkg.CommentReaction, 0, maxReactionPermissionLookupsPerSweep+2)
	// Put a real maintainer first to prove the verified prefix is discarded too:
	// once an unverified tail exists, no partial tally from this comment is safe.
	reactions = append(reactions, ghpkg.CommentReaction{Content: "-1", User: "maintainer"})
	for i := 0; i <= maxReactionPermissionLookupsPerSweep; i++ {
		reactions = append(reactions, ghpkg.CommentReaction{
			Content: "-1",
			User:    fmt.Sprintf("drive-by-%03d", i),
		})
	}
	permissions := &reactionPermissionStub{allowedByLogin: map[string]bool{"maintainer": true}}
	st := &reactionStoreStub{
		ids:     []int64{501},
		comment: &store.ReviewComment{ID: uuid.New(), Body: "race", Category: &category},
	}
	idx := &reactionIndexerStub{dismissalLive: true}
	lifecycle := &recordingReactionLifecycle{}
	ra := &ReactionAnalyzer{
		store:           st,
		ghClient:        &reactionGitHubStub{reactions: reactions},
		repoPermissions: permissions,
		memRegistry:     reactionRegistryStub{indexer: idx},
		logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		lifecycle:       lifecycle,
	}

	if err := ra.SweepPRReactions(context.Background(), 91, "acme/api", 17); err != nil {
		t.Fatalf("attacker-controlled permission population blocked review: %v", err)
	}
	calls := 0
	for _, n := range permissions.calls {
		calls += n
	}
	if calls != 0 {
		t.Fatalf("permission calls = %d, want zero after preflight detects known overload", calls)
	}
	if len(st.outcomes) != 0 || len(lifecycle.events) != 0 {
		t.Fatalf("budget-exhausted partial tally mutated ledger: outcomes=%v events=%v", st.outcomes, lifecycle.events)
	}
	if idx.dismissalLive || len(idx.actions) != 1 || idx.actions[0] != "" {
		t.Fatalf("budget-exhausted comment did not retract stale reaction-only state: live=%v actions=%v", idx.dismissalLive, idx.actions)
	}
}
