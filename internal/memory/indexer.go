package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
)

// Indexer manages Supermemory documents: stores reviews, rules, patterns, and topology for future RAG retrieval.
type Indexer struct {
	client *Client
	logger *slog.Logger
}

func NewIndexer(client *Client, logger *slog.Logger) *Indexer {
	return &Indexer{client: client, logger: logger}
}

var lineNumRegex = regexp.MustCompile(`(?i)\b(?:line|L)\s*\d+`)

// truncateIDWithSuffix caps a customId to 100 chars, preserving the suffix (hash/tag).
// Truncates the prefix to make room (rune-safe) rather than chopping the suffix.
func truncateIDWithSuffix(prefix, suffix string) string {
	sep := "--"
	maxPrefix := 100 - len(suffix) - len(sep)
	if maxPrefix < 0 {
		maxPrefix = 0
	}
	if len(prefix) > maxPrefix {
		// Walk backward to avoid splitting a multi-byte UTF-8 rune
		cut := maxPrefix
		for cut > 0 && prefix[cut]&0xC0 == 0x80 {
			cut--
		}
		prefix = prefix[:cut]
	}
	return prefix + sep + suffix
}

// normalizeBody strips line numbers and excess whitespace for stable fingerprinting.
func normalizeBody(body string) string {
	s := lineNumRegex.ReplaceAllString(body, "")
	return strings.Join(strings.Fields(s), " ")
}

// FindingFingerprint produces a stable customId for a review finding.
// Format: {owner}/{repo}/{sanitized-file}/{hash12} (max 100 chars).
// Returns empty string if owner, repo, or filePath is empty.
func FindingFingerprint(owner, repo, filePath, category, body string) string {
	if owner == "" || repo == "" || filePath == "" {
		return ""
	}
	h := sha256.Sum256([]byte(filePath + "|" + category + "|" + normalizeBody(body)))
	hash := hex.EncodeToString(h[:6]) // 12 hex chars
	prefix := fmt.Sprintf("%s--%s--%s", owner, repo, tagSanitizer.Replace(filePath))
	return truncateIDWithSuffix(prefix, hash)
}

// SynthesisCustomID returns a stable customId for a file synthesis document.
// Uses a path hash when the path is too long, to avoid collisions from truncation.
func SynthesisCustomID(owner, repo, filePath string) string {
	suffix := "synthesis"
	prefix := fmt.Sprintf("%s--%s--%s", owner, repo, tagSanitizer.Replace(filePath))
	id := prefix + "--" + suffix
	if len(id) <= 100 {
		return id
	}
	// Path too long — include a hash for uniqueness
	h := sha256.Sum256([]byte(filePath))
	hash := hex.EncodeToString(h[:6])
	return truncateIDWithSuffix(fmt.Sprintf("%s--%s--%s", owner, repo, hash), suffix)
}

// PRSummaryCustomID returns a stable customId for a PR summary document.
func PRSummaryCustomID(owner, repo string, prNumber int) string {
	suffix := fmt.Sprintf("pr-%d-summary", prNumber)
	prefix := fmt.Sprintf("%s--%s", owner, repo)
	return truncateIDWithSuffix(prefix, suffix)
}

// PatternCustomID returns a stable customId for a learned/confirmed pattern.
func PatternCustomID(owner, repo, source, content string) string {
	h := sha256.Sum256([]byte(normalizeBody(content)))
	hash := hex.EncodeToString(h[:6])
	prefix := fmt.Sprintf("%s--%s--%s", owner, repo, source)
	return truncateIDWithSuffix(prefix, hash)
}

// IndexReviewComment stores a single review comment for future RAG retrieval.
// Uses customId for idempotent upserts — re-reviews overwrite instead of duplicating.
func (idx *Indexer) IndexReviewComment(ctx context.Context, owner, repo string, comment ReviewMemory) error {
	content := fmt.Sprintf("File: %s\nSeverity: %s\nCategory: %s\n\n%s\n\nContext:\n%s",
		comment.FilePath, comment.Severity, comment.Category, comment.Body, comment.DiffContext)

	customID := FindingFingerprint(owner, repo, comment.FilePath, comment.Category, comment.Body)

	_, err := idx.client.AddMemory(ctx, AddRequest{
		Content:       content,
		CustomID:      customID,
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
// If customID is non-empty, the document is upserted (deduplicated).
func (idx *Indexer) IndexRepoPattern(ctx context.Context, owner, repo, content, customID string, metadata map[string]string) (*AddResponse, error) {
	resp, err := idx.client.AddMemory(ctx, AddRequest{
		Content:       content,
		CustomID:      customID,
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
// If customID is non-empty, the document is upserted (deduplicated).
func (idx *Indexer) IndexOwnerPattern(ctx context.Context, owner, content, customID string, metadata map[string]string) (*AddResponse, error) {
	resp, err := idx.client.AddMemory(ctx, AddRequest{
		Content:       content,
		CustomID:      customID,
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

