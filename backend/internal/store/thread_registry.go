package store

import (
	"context"

	"github.com/BeLazy167/argus/backend/internal/store/db"
	"github.com/google/uuid"
)

// ThreadLink is the durable mapping (ThreadRegistry, #162) between a posted
// review finding and its GitHub thread identity: the GraphQL ReviewThread node
// id plus the REST comment id and path/line anchor. Every field but the node id
// already lived on review_comments; migration 054 added the node id, so a link
// is just a projection of one review_comments row. Consumers look a thread up BY
// finding (GetThreadLinkForComment) or list all links for a review
// (ListThreadLinksForReview) instead of re-listing every thread on the PR and
// re-matching by line proximity.
type ThreadLink struct {
	// CommentID is the review_comments PK — the finding's identity.
	CommentID uuid.UUID `json:"comment_id"`
	ReviewID  uuid.UUID `json:"review_id"`
	FilePath  string    `json:"file_path"`
	// EndLine is the finding's line anchor (nil for file-level findings).
	EndLine *int `json:"end_line,omitempty"`
	// RestCommentID is the GitHub REST review-comment database id
	// (review_comments.github_comment_id). Nil until backfilled.
	RestCommentID *int64 `json:"rest_comment_id,omitempty"`
	// ThreadNodeID is the GitHub GraphQL ReviewThread node id — the handle
	// ResolveReviewThread / dismissal target. Nil for pre-migration rows,
	// suppressed findings, and post-time hydrate misses.
	ThreadNodeID *string `json:"thread_node_id,omitempty"`
}

// HydrateThreadNodeID authoritatively binds one just-posted finding to its
// GraphQL review-thread node id, keyed on the REST comment id (an exact match,
// not a proximity guess). Only fills NULL rows, so a re-post or webhook retry
// can't overwrite an established link. Returns rows affected.
func (s *Store) HydrateThreadNodeID(ctx context.Context, reviewID uuid.UUID, githubCommentID int64, threadNodeID string) (int64, error) {
	return s.Q.HydrateThreadNodeID(ctx, db.HydrateThreadNodeIDParams{
		GraphqlThreadNodeID: &threadNodeID,
		ReviewID:            reviewID,
		GithubCommentID:     &githubCommentID,
	})
}

// GetThreadLinkForComment returns the thread identity for a single finding —
// the "thread for finding X" lookup. The node id is X's own; dismissal targets
// exactly X's thread rather than a neighbouring finding's.
func (s *Store) GetThreadLinkForComment(ctx context.Context, commentID uuid.UUID) (*ThreadLink, error) {
	row, err := s.Q.GetThreadLinkForComment(ctx, commentID)
	if err != nil {
		return nil, err
	}
	return &ThreadLink{
		CommentID:     row.ID,
		ReviewID:      row.ReviewID,
		FilePath:      row.FilePath,
		EndLine:       row.EndLine,
		RestCommentID: row.GithubCommentID,
		ThreadNodeID:  row.GraphqlThreadNodeID,
	}, nil
}

// ListThreadLinksForReview returns every hydrated thread link for a review —
// the "threads for review R" lookup. Only rows with a stored node id are
// returned; unhydrated / suppressed findings are omitted.
func (s *Store) ListThreadLinksForReview(ctx context.Context, reviewID uuid.UUID) ([]ThreadLink, error) {
	rows, err := s.Q.ListThreadLinksForReview(ctx, reviewID)
	if err != nil {
		return nil, err
	}
	links := make([]ThreadLink, 0, len(rows))
	for _, row := range rows {
		links = append(links, ThreadLink{
			CommentID:     row.ID,
			ReviewID:      row.ReviewID,
			FilePath:      row.FilePath,
			EndLine:       row.EndLine,
			RestCommentID: row.GithubCommentID,
			ThreadNodeID:  row.GraphqlThreadNodeID,
		})
	}
	return links, nil
}
