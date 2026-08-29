package pipeline

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/BeLazy167/argus/backend/internal/store"
	"github.com/google/uuid"
)

// thread_registry.go — ThreadRegistry (#162) consumer plumbing.
//
// At review-post time hydrateThreadNodeIDs binds each just-posted finding to
// its GitHub GraphQL review-thread node id (an authoritative REST-id join, not
// a proximity guess) and stores it on review_comments. Lifecycle consumers then
// look a thread up BY finding via the store, instead of re-listing every thread
// on the PR and re-matching by line proximity.

// threadLinkReader is the store seam ThreadRegistry consumers read through — the
// exact GitHub thread identity for a persisted finding. Implemented by
// *store.Store; faked in thread_registry_test.go.
type threadLinkReader interface {
	GetThreadLinkForComment(ctx context.Context, commentID uuid.UUID) (*store.ThreadLink, error)
}

// threadNodeIDForFinding returns the GraphQL review-thread node id that
// dismissing finding (review_comment) X must target, and whether one is stored.
// Authoritative: the id is X's own hydrated thread, never a neighbouring
// finding's picked by line proximity. Returns ("", false) when X has no
// hydrated thread — a suppressed finding, a pre-migration row, or a post-time
// hydrate miss — so the caller can fall back to the proximity path.
func threadNodeIDForFinding(ctx context.Context, r threadLinkReader, commentID uuid.UUID) (string, bool) {
	link, err := r.GetThreadLinkForComment(ctx, commentID)
	if err != nil || link == nil || link.ThreadNodeID == nil || *link.ThreadNodeID == "" {
		return "", false
	}
	return *link.ThreadNodeID, true
}

// hydrateThreadNodeIDs binds each just-posted finding to its GitHub review-thread
// node id — the durable handle dismissal + auto-resolve need. Authoritative, not
// a proximity guess: ListReviewThreads returns each thread's node id alongside
// its first comment's REST database id, and every inline comment Argus posts
// starts its own thread, so thread.FirstCommentID == the finding's
// github_comment_id is an exact join. Must run AFTER backfillGitHubCommentIDs
// (which fills github_comment_id) so the join key is present.
//
// Threads from other reviews / sibling bots on the same PR are harmless: the
// UPDATE is scoped to this review_id, so their FirstCommentIDs simply match no
// row. The helper reports API and persistence failures. Normal posting logs and
// continues; crash recovery propagates them and withholds terminal completion.
func (o *Orchestrator) hydrateThreadNodeIDs(ctx context.Context, run *PipelineRun, owner, repo string) error {
	o.logger.InfoContext(ctx, "thread registry hydration started", "event", "pipeline.thread_registry.hydration_started", "review_id", run.ReviewID, "attempt_generation", run.AttemptGeneration, "repo", run.PREvent.RepoFullName, "pr_number", run.PREvent.PRNumber)
	client := o.postedReviewBindingClient()
	if client == nil {
		return fmt.Errorf("thread-registry: binding client unavailable")
	}
	threads, err := client.ListReviewThreads(ctx, run.PREvent.InstallationID, owner, repo, run.PREvent.PRNumber)
	if err != nil {
		return fmt.Errorf("thread-registry: listing threads to hydrate: %w", err)
	}
	o.logger.InfoContext(ctx, "thread registry GitHub threads listed", "event", "pipeline.thread_registry.threads_listed", "review_id", run.ReviewID, "thread_count", len(threads))
	hydrated := 0
	skipped := 0
	var hydrateErrors []error
	for _, t := range threads {
		// Need both handles to bind: the node id we store and the REST id we
		// join on. GraphQL omits databaseId for some threads (FirstCommentID 0).
		if t.ID == "" || t.FirstCommentID == 0 {
			skipped++
			continue
		}
		n, hydrateErr := o.st.HydrateThreadNodeID(ctx, run.ReviewID, t.FirstCommentID, t.ID)
		if hydrateErr != nil {
			hydrateErrors = append(hydrateErrors, fmt.Errorf("thread %s: %w", t.ID, hydrateErr))
			continue
		}
		hydrated += int(n)
	}
	o.logger.InfoContext(ctx, "thread-registry: hydrated thread node ids", "event", "pipeline.thread_registry.hydrated", "count", hydrated, "skipped_count", skipped, "error_count", len(hydrateErrors), "review_id", run.ReviewID)
	if err := errors.Join(hydrateErrors...); err != nil {
		o.logger.WarnContext(ctx, "thread registry hydration incomplete", "event", "pipeline.thread_registry.hydration_failed", "review_id", run.ReviewID, "error_count", len(hydrateErrors), "error", err)
		return fmt.Errorf("thread-registry: hydrating node ids: %w", err)
	}
	o.logger.InfoContext(ctx, "thread registry hydration completed", "event", "pipeline.thread_registry.hydration_completed", "review_id", run.ReviewID, "hydrated_count", hydrated)
	return nil
}

// unboundCommentRow is a persisted review_comments row awaiting its GitHub REST
// comment id, projected for 1:1 binding: its PK, (path, line) anchor, and stored
// body.
type unboundCommentRow struct {
	ID   uuid.UUID
	Path string
	Line int
	Body string
}

// postedComment is one GitHub review comment ListReviewComments returned, to be
// bound to exactly one unboundCommentRow. The reported line fields are retained
// so an anchor mismatch identifies the PR-level payload GitHub returned.
type postedComment struct {
	GithubID     int64
	Path         string
	Line         int
	GitHubLine   int
	OriginalLine int
	Position     int
	Body         string
}

// pairCommentsToRows binds each posted GitHub review comment to EXACTLY ONE
// review_comments row on the same (path, line). This is the #171 fold-forward
// fix: the old backfill matched on (review_id, file_path, end_line) with no
// LIMIT, so two findings on the SAME line collapsed onto a single
// github_comment_id — and thus a single hydrated thread node id, so dismissing
// finding B would resolve finding A's thread. Binding 1:1 keeps the
// finding↔comment↔thread chain distinct even for same-line findings.
//
// Within a (path, line) group, an unclaimed exact body match is required (the
// posted body is the stored body — formatCommentBody renders both). Multiple
// matches fail closed because choosing one could cross-wire finding threads.
// Zero matches also fail closed: the manifest and unbound-count checks require
// a complete 1:1 binding, so skipping would only defer the same integrity error
// and hide the mismatched GitHub anchor. Returns rowID → githubCommentID.
func normalizePostedCommentBody(body string) string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	lines := strings.Split(body, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

func pairCommentsToRows(rows []unboundCommentRow, comments []postedComment) (map[uuid.UUID]int64, error) {
	type loc struct {
		path string
		line int
	}
	byLoc := make(map[loc][]unboundCommentRow, len(rows))
	for _, r := range rows {
		k := loc{r.Path, r.Line}
		byLoc[k] = append(byLoc[k], r)
	}
	claimed := make(map[uuid.UUID]bool, len(comments))
	out := make(map[uuid.UUID]int64, len(comments))
	for _, c := range comments {
		group := byLoc[loc{c.Path, c.Line}]
		matches := make([]uuid.UUID, 0, 1)
		for _, r := range group {
			if !claimed[r.ID] && normalizePostedCommentBody(r.Body) == normalizePostedCommentBody(c.Body) {
				matches = append(matches, r.ID)
			}
		}
		if len(matches) != 1 {
			pathLines := make([]int, 0)
			for candidateLoc := range byLoc {
				if candidateLoc.path == c.Path {
					pathLines = append(pathLines, candidateLoc.line)
				}
			}
			slices.Sort(pathLines)
			pathLines = slices.Compact(pathLines)
			return nil, fmt.Errorf("comment binding at %s:%d has %d normalized body matches (github line=%d original_line=%d position=%d; stored candidate lines for path=%v)", c.Path, c.Line, len(matches), c.GitHubLine, c.OriginalLine, c.Position, pathLines)
		}
		picked := matches[0]
		claimed[picked] = true
		out[picked] = c.GithubID
	}
	return out, nil
}

// storedThreadIDsForReview loads the ThreadRegistry links hydrated at post time
// for a review and returns a REST-comment-id → GraphQL-node-id map. Empty when
// the review predates ThreadRegistry or nothing was hydrated — auto-resolve then
// uses the live node id from ListReviewThreads. Best-effort: a load error logs
// and yields nil (treated as "no stored links").
func (o *Orchestrator) storedThreadIDsForReview(ctx context.Context, reviewID uuid.UUID) map[int64]string {
	o.logger.DebugContext(ctx, "thread registry link load started", "event", "pipeline.thread_registry.links_started", "review_id", reviewID)
	links, err := o.st.ListThreadLinksForReview(ctx, reviewID)
	if err != nil {
		o.logger.Warn("thread-registry: loading stored thread links", "error", err, "review_id", reviewID)
		return nil
	}
	m := make(map[int64]string, len(links))
	for _, l := range links {
		if l.RestCommentID != nil && l.ThreadNodeID != nil {
			m[*l.RestCommentID] = *l.ThreadNodeID
		}
	}
	o.logger.InfoContext(ctx, "thread registry links loaded", "event", "pipeline.thread_registry.links_loaded", "review_id", reviewID, "link_count", len(m), "row_count", len(links))
	return m
}
