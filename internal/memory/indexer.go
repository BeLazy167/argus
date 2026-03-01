package memory

import (
	"context"
	"fmt"
	"log/slog"
)

// Indexer manages Supermemory documents: stores reviews, rules, patterns, and topology for future RAG retrieval.
type Indexer struct {
	client *Client
	logger *slog.Logger
}

func NewIndexer(client *Client, logger *slog.Logger) *Indexer {
	return &Indexer{client: client, logger: logger}
}

// IndexReviewComment stores a single review comment for future RAG retrieval.
func (idx *Indexer) IndexReviewComment(ctx context.Context, owner, repo string, comment ReviewMemory) error {
	content := fmt.Sprintf("File: %s\nSeverity: %s\nCategory: %s\n\n%s\n\nContext:\n%s",
		comment.FilePath, comment.Severity, comment.Category, comment.Body, comment.DiffContext)

	_, err := idx.client.AddMemory(ctx, AddRequest{
		Content:       content,
		ContainerTags: []string{RepoTag(owner, repo, "reviews")},
		Metadata: map[string]string{
			"file_path": comment.FilePath,
			"severity":  comment.Severity,
			"category":  comment.Category,
			"pr_number": fmt.Sprintf("%d", comment.PRNumber),
			"review_id": comment.ReviewID,
		},
	})
	if err != nil {
		return fmt.Errorf("indexing review comment: %w", err)
	}

	idx.logger.Debug("indexed review comment", "owner", owner, "repo", repo, "file", comment.FilePath)
	return nil
}

// IndexRule stores an owner-scoped rule for semantic matching during review.
func (idx *Indexer) IndexRule(ctx context.Context, owner string, rule RuleMemory) error {
	content := fmt.Sprintf("Category: %s\nPriority: %d\n\n%s",
		rule.Category, rule.Priority, rule.Content)

	_, err := idx.client.AddMemory(ctx, AddRequest{
		Content:       content,
		ContainerTags: []string{OwnerTag(owner, "rules")},
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

// IndexRepoPattern stores a pattern scoped to a specific repo.
func (idx *Indexer) IndexRepoPattern(ctx context.Context, owner, repo, content string, metadata map[string]string) (*AddResponse, error) {
	resp, err := idx.client.AddMemory(ctx, AddRequest{
		Content:       content,
		ContainerTags: []string{RepoTag(owner, repo, "patterns")},
		Metadata:      metadata,
	})
	if err != nil {
		return nil, fmt.Errorf("indexing repo pattern: %w", err)
	}
	idx.logger.Debug("indexed repo pattern", "owner", owner, "repo", repo)
	return resp, nil
}

// IndexOwnerPattern stores a pattern at owner scope (applies to all repos in the org).
func (idx *Indexer) IndexOwnerPattern(ctx context.Context, owner, content string, metadata map[string]string) (*AddResponse, error) {
	resp, err := idx.client.AddMemory(ctx, AddRequest{
		Content:       content,
		ContainerTags: []string{OwnerTag(owner, "patterns")},
		Metadata:      metadata,
	})
	if err != nil {
		return nil, fmt.Errorf("indexing owner pattern: %w", err)
	}
	idx.logger.Debug("indexed owner pattern", "owner", owner)
	return resp, nil
}

// DeleteDocument removes a document from Supermemory by ID.
func (idx *Indexer) DeleteDocument(ctx context.Context, documentID string) error {
	if err := idx.client.DeleteMemory(ctx, documentID); err != nil {
		return fmt.Errorf("deleting document: %w", err)
	}
	idx.logger.Debug("deleted document", "id", documentID)
	return nil
}

// IndexRepoTopology stores inferred repo role/dependencies at owner scope.
func (idx *Indexer) IndexRepoTopology(ctx context.Context, owner, content string) error {
	_, err := idx.client.AddMemory(ctx, AddRequest{
		Content:       content,
		ContainerTags: []string{OwnerTag(owner, "patterns")},
		Metadata:      map[string]string{"type": "topology"},
	})
	return err
}

