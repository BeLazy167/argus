package pipeline

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/store"
	gh "github.com/google/go-github/v68/github"
)

type recoveryBindingClient struct {
	mu             sync.Mutex
	comments       []*gh.PullRequestComment
	threads        []ghpkg.ReviewThread
	threadFailures int
	commentCalls   int
	threadCalls    int
}

func (c *recoveryBindingClient) ListReviewComments(context.Context, int64, string, string, int, int64) ([]*gh.PullRequestComment, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.commentCalls++
	return c.comments, nil
}

func (c *recoveryBindingClient) ListReviewThreads(context.Context, int64, string, string, int) ([]ghpkg.ReviewThread, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.threadCalls++
	if c.threadFailures > 0 {
		c.threadFailures--
		return nil, errors.New("GraphQL unavailable")
	}
	return c.threads, nil
}

func TestRecoverPostedReviewBindingsBindsSameLineCommentsAndThreads(t *testing.T) {
	pool, ctx, reviewID := durableEventTestReview(t)
	st := store.NewWithDB(pool)
	const generation = 4
	const githubReviewID int64 = 90041
	if _, err := pool.Exec(ctx, `
		UPDATE reviews SET status='failed', attempt_generation=$2, github_review_id=$3
		WHERE id=$1
	`, reviewID, generation, githubReviewID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO review_comments(review_id,attempt_generation,file_path,end_line,body)
		VALUES ($1,$2,'same.go',17,'first body'),
		       ($1,$2,'same.go',17,'second body'),
		       ($1,$2,'folded.go',29,'summary-only folded body')
	`, reviewID, generation); err != nil {
		t.Fatal(err)
	}

	client := &recoveryBindingClient{
		comments: []*gh.PullRequestComment{
			{ID: gh.Ptr(int64(702)), Path: gh.Ptr("same.go"), Line: gh.Ptr(17), Body: gh.Ptr("second body")},
			{ID: gh.Ptr(int64(701)), Path: gh.Ptr("same.go"), Line: gh.Ptr(17), Body: gh.Ptr("first body")},
		},
		threads: []ghpkg.ReviewThread{
			{ID: "THREAD-701", FirstCommentID: 701},
			{ID: "THREAD-702", FirstCommentID: 702},
		},
	}
	o := &Orchestrator{
		st:                  st,
		reviewBindingClient: client,
		logger:              slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if err := o.RecoverPostedReviewBindings(ctx, reviewID, generation, githubReviewID); err != nil {
		t.Fatal(err)
	}

	rows, err := pool.Query(ctx, `
		SELECT body,github_comment_id,graphql_thread_node_id
		FROM review_comments
		WHERE review_id=$1 AND github_comment_id IS NOT NULL
		ORDER BY body
	`, reviewID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	wantIDs := []int64{701, 702}
	wantThreads := []string{"THREAD-701", "THREAD-702"}
	for i := 0; rows.Next(); i++ {
		var body string
		var githubID int64
		var threadID string
		if err := rows.Scan(&body, &githubID, &threadID); err != nil {
			t.Fatal(err)
		}
		if githubID != wantIDs[i] || threadID != wantThreads[i] {
			t.Fatalf("%s binding=(%d,%q), want (%d,%q)", body, githubID, threadID, wantIDs[i], wantThreads[i])
		}
	}
	var foldedGitHubID *int64
	var foldedThreadID *string
	if err := pool.QueryRow(ctx, `SELECT github_comment_id,graphql_thread_node_id FROM review_comments WHERE review_id=$1 AND body='summary-only folded body'`, reviewID).Scan(&foldedGitHubID, &foldedThreadID); err != nil {
		t.Fatal(err)
	}
	if foldedGitHubID != nil || foldedThreadID != nil {
		t.Fatalf("folded binding=(%v,%v), want nil,nil", foldedGitHubID, foldedThreadID)
	}
	if client.commentCalls != 1 || client.threadCalls != 1 {
		t.Fatalf("API calls comments=%d threads=%d, want 1 each", client.commentCalls, client.threadCalls)
	}
}

func TestRecoverPostedReviewBindingsFailureIsRetriableAndSummaryOnlyCompletesAfterEmptyEnumeration(t *testing.T) {
	pool, ctx, reviewID := durableEventTestReview(t)
	st := store.NewWithDB(pool)
	const generation = 2
	const githubReviewID int64 = 902
	if _, err := pool.Exec(ctx, `UPDATE reviews SET status='failed',attempt_generation=$2,github_review_id=$3 WHERE id=$1`, reviewID, generation, githubReviewID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO review_comments(review_id,attempt_generation,file_path,end_line,body) VALUES($1,$2,'retry.go',8,'retry body')`, reviewID, generation); err != nil {
		t.Fatal(err)
	}
	client := &recoveryBindingClient{
		comments:       []*gh.PullRequestComment{{ID: gh.Ptr(int64(801)), Path: gh.Ptr("retry.go"), Line: gh.Ptr(8), Body: gh.Ptr("retry body")}},
		threads:        []ghpkg.ReviewThread{{ID: "THREAD-801", FirstCommentID: 801}},
		threadFailures: 1,
	}
	o := &Orchestrator{st: st, reviewBindingClient: client, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := o.RecoverPostedReviewBindings(ctx, reviewID, generation, githubReviewID); err == nil {
		t.Fatal("first recovery succeeded, want retriable GraphQL failure")
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM reviews WHERE id=$1`, reviewID).Scan(&status); err != nil || status != "failed" {
		t.Fatalf("status=%q err=%v, want failed", status, err)
	}
	if err := o.RecoverPostedReviewBindings(ctx, reviewID, generation, githubReviewID); err != nil {
		t.Fatalf("second recovery: %v", err)
	}
	var githubID int64
	var threadID string
	if err := pool.QueryRow(ctx, `SELECT github_comment_id,graphql_thread_node_id FROM review_comments WHERE review_id=$1`, reviewID).Scan(&githubID, &threadID); err != nil {
		t.Fatal(err)
	}
	if githubID != 801 || threadID != "THREAD-801" {
		t.Fatalf("binding=(%d,%q)", githubID, threadID)
	}

	summaryReviewID := reviewID // remove inline rows while keeping trusted review metadata
	if _, err := pool.Exec(ctx, `DELETE FROM review_comments WHERE review_id=$1`, summaryReviewID); err != nil {
		t.Fatal(err)
	}
	client.mu.Lock()
	client.comments = nil
	commentCallsBefore := client.commentCalls
	threadCallsBefore := client.threadCalls
	client.mu.Unlock()
	if err := o.RecoverPostedReviewBindings(ctx, summaryReviewID, generation, githubReviewID); err != nil {
		t.Fatalf("summary-only recovery: %v", err)
	}
	client.mu.Lock()
	commentCalls := client.commentCalls
	threadCalls := client.threadCalls
	client.mu.Unlock()
	if commentCalls != commentCallsBefore+1 || threadCalls != threadCallsBefore {
		t.Fatalf("summary-only API calls: comments=%d threads=%d", commentCalls-commentCallsBefore, threadCalls-threadCallsBefore)
	}
}
