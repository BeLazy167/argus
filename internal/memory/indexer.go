package memory

import (
	"context"
	"fmt"
	"log/slog"
)

// Indexer stores completed reviews and rules in Supermemory for future retrieval.
type Indexer struct {
	client *Client
	logger *slog.Logger
}

func NewIndexer(client *Client, logger *slog.Logger) *Indexer {
	return &Indexer{client: client, logger: logger}
}

// IndexReviewComment stores a single review comment for future RAG retrieval.
func (idx *Indexer) IndexReviewComment(ctx context.Context, repoFullName string, comment ReviewMemory) error {
	content := fmt.Sprintf("File: %s\nSeverity: %s\nCategory: %s\n\n%s\n\nContext:\n%s",
		comment.FilePath, comment.Severity, comment.Category, comment.Body, comment.DiffContext)

	_, err := idx.client.AddMemory(ctx, AddRequest{
		Content:      content,
		ContainerTags: []string{ContainerTag("repo", repoFullName+":reviews")},
		Metadata: map[string]string{
			"file_path":  comment.FilePath,
			"severity":   comment.Severity,
			"category":   comment.Category,
			"pr_number":  fmt.Sprintf("%d", comment.PRNumber),
			"review_id":  comment.ReviewID,
		},
	})
	if err != nil {
		return fmt.Errorf("indexing review comment: %w", err)
	}

	idx.logger.Debug("indexed review comment", "repo", repoFullName, "file", comment.FilePath)
	return nil
}

// IndexRule stores an org-wide rule for semantic matching during review.
func (idx *Indexer) IndexRule(ctx context.Context, rule RuleMemory) error {
	content := fmt.Sprintf("Category: %s\nPriority: %d\n\n%s",
		rule.Category, rule.Priority, rule.Content)

	_, err := idx.client.AddMemory(ctx, AddRequest{
		Content:      content,
		ContainerTags: []string{ContainerTag("org", "rules")},
		Metadata: map[string]string{
			"rule_id":  fmt.Sprintf("%d", rule.RuleID),
			"category": rule.Category,
		},
	})
	if err != nil {
		return fmt.Errorf("indexing rule: %w", err)
	}
	return nil
}

// SearchPastReviews finds similar past review comments for a given diff context.
func (idx *Indexer) SearchPastReviews(ctx context.Context, repoFullName, query string, limit int) ([]SearchResult, error) {
	resp, err := idx.client.Search(ctx, SearchRequest{
		Query:        query,
		ContainerTag: ContainerTag("repo", repoFullName+":reviews"),
		SearchMode:   "hybrid",
		Limit:        limit,
		Threshold:    0.6,
	})
	if err != nil {
		return nil, err
	}
	return resp.Results, nil
}

// SearchRules finds relevant org rules for a given code context.
func (idx *Indexer) SearchRules(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	resp, err := idx.client.Search(ctx, SearchRequest{
		Query:        query,
		ContainerTag: ContainerTag("org", "rules"),
		SearchMode:   "hybrid",
		Limit:        limit,
		Threshold:    0.5,
	})
	if err != nil {
		return nil, err
	}
	return resp.Results, nil
}

// ReviewMemory represents a review comment to be stored in Supermemory.
type ReviewMemory struct {
	ReviewID    string
	PRNumber    int
	FilePath    string
	Body        string
	Severity    string
	Category    string
	DiffContext string
}

// RuleMemory represents a rule to be stored in Supermemory.
type RuleMemory struct {
	RuleID   int64
	Category string
	Priority int
	Content  string
}
