package pipeline

import (
	"context"

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
// row. Best-effort — a miss leaves graphql_thread_node_id NULL and consumers
// fall back to the fuzzy path for that row.
func (o *Orchestrator) hydrateThreadNodeIDs(ctx context.Context, run *PipelineRun, owner, repo string) {
	threads, err := o.ghClient.ListReviewThreads(ctx, run.PREvent.InstallationID, owner, repo, run.PREvent.PRNumber)
	if err != nil {
		o.logger.Warn("thread-registry: listing threads to hydrate", "error", err, "review_id", run.ReviewID)
		return
	}
	hydrated := 0
	for _, t := range threads {
		// Need both handles to bind: the node id we store and the REST id we
		// join on. GraphQL omits databaseId for some threads (FirstCommentID 0).
		if t.ID == "" || t.FirstCommentID == 0 {
			continue
		}
		n, err := o.st.HydrateThreadNodeID(ctx, run.ReviewID, t.FirstCommentID, t.ID)
		if err != nil {
			o.logger.Warn("thread-registry: hydrating node id", "error", err, "thread_id", t.ID, "review_id", run.ReviewID)
			continue
		}
		hydrated += int(n)
	}
	if hydrated > 0 {
		o.logger.Info("thread-registry: hydrated thread node ids", "count", hydrated, "review_id", run.ReviewID)
	}
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
// bound to exactly one unboundCommentRow.
type postedComment struct {
	GithubID int64
	Path     string
	Line     int
	Body     string
}

// pairCommentsToRows binds each posted GitHub review comment to EXACTLY ONE
// review_comments row on the same (path, line). This is the #171 fold-forward
// fix: the old backfill matched on (review_id, file_path, end_line) with no
// LIMIT, so two findings on the SAME line collapsed onto a single
// github_comment_id — and thus a single hydrated thread node id, so dismissing
// finding B would resolve finding A's thread. Binding 1:1 keeps the
// finding↔comment↔thread chain distinct even for same-line findings.
//
// Within a (path, line) group the preference is: an unclaimed EXACT body match
// first (the posted body IS the stored body — formatCommentBody renders both),
// then stable input order for the degenerate identical-body case. Each comment
// claims its row, so the next same-line comment necessarily picks a different
// row. Returns rowID → githubCommentID; a comment with no unclaimed row on its
// (path, line) is skipped (leaves the row unbound, as before).
func pairCommentsToRows(rows []unboundCommentRow, comments []postedComment) map[uuid.UUID]int64 {
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
		picked := uuid.Nil
		// Prefer an unclaimed exact body match — the authoritative 1:1 signal.
		for _, r := range group {
			if !claimed[r.ID] && r.Body == c.Body {
				picked = r.ID
				break
			}
		}
		// Degenerate fallback (identical bodies, or GitHub normalised the body):
		// first unclaimed row in insertion order.
		if picked == uuid.Nil {
			for _, r := range group {
				if !claimed[r.ID] {
					picked = r.ID
					break
				}
			}
		}
		if picked == uuid.Nil {
			continue // no row left for this comment
		}
		claimed[picked] = true
		out[picked] = c.GithubID
	}
	return out
}

// storedThreadIDsForReview loads the ThreadRegistry links hydrated at post time
// for a review and returns a REST-comment-id → GraphQL-node-id map. Empty when
// the review predates ThreadRegistry or nothing was hydrated — auto-resolve then
// uses the live node id from ListReviewThreads. Best-effort: a load error logs
// and yields nil (treated as "no stored links").
func (o *Orchestrator) storedThreadIDsForReview(ctx context.Context, reviewID uuid.UUID) map[int64]string {
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
	return m
}
