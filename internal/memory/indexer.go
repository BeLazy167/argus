package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BeLazy167/argus/internal/util"
)

// ScenarioSearchResult holds a semantic search result with the parsed Postgres scenario ID.
type ScenarioSearchResult struct {
	ID         int64
	Content    string
	Similarity float64
}

// Indexer manages Supermemory documents: stores reviews, rules, patterns, and topology for future RAG retrieval.
type Indexer struct {
	client *Client
	logger *slog.Logger
}

func NewIndexer(client *Client, logger *slog.Logger) *Indexer {
	return &Indexer{client: client, logger: logger}
}

// Client returns the underlying Supermemory client.
func (idx *Indexer) Client() *Client { return idx.client }

// ConfigureFilterPrompt sets the org-level filter prompt so Supermemory knows
// what kind of content Argus ingests. Call once on startup.
func (idx *Indexer) ConfigureFilterPrompt(ctx context.Context) {
	if idx.client == nil {
		return
	}
	err := idx.client.UpdateSettings(ctx, map[string]any{
		"shouldLLMFilter": true,
		"filterPrompt": `You are ingesting content for Argus, an AI code review platform.

Index:
- Code review findings with file paths, severity, and category
- Learned code patterns, conventions, and best practices
- Developer feedback signals (confirmations and dismissals)
- Known issues, edge cases, and past incident descriptions (scenarios)
- Decision traces: review findings, developer replies, pattern matches

Skip:
- Raw diffs or code content without analysis
- Duplicate findings that are semantically identical to existing memories
- Generic boilerplate comments without specific insights
- PR metadata that doesn't contain reviewable insights`,
	})
	if err != nil {
		idx.logger.Warn("failed to configure supermemory filter prompt", "error", err)
	} else {
		idx.logger.Info("supermemory filter prompt configured")
	}
}

// SetRepoEntityContext sets per-repo context that guides memory extraction.
func (idx *Indexer) SetRepoEntityContext(ctx context.Context, owner, repo, language, description string) {
	if idx.client == nil {
		return
	}
	for _, kind := range []string{"reviews", "patterns", "scenarios", "traces"} {
		tag := RepoTag(owner, repo, kind)
		entityCtx := fmt.Sprintf("Code review data for %s/%s", owner, repo)
		if language != "" {
			entityCtx += fmt.Sprintf(" (primary language: %s)", language)
		}
		if description != "" {
			entityCtx += ". " + description
		}
		if err := idx.client.UpdateEntityContext(ctx, tag, entityCtx); err != nil {
			idx.logger.Debug("failed to set entity context", "tag", tag, "error", err)
		}
	}
}

// SearchPatternMatch searches for the best matching pattern across repo and owner scopes.
// Returns the matched content and similarity score. Returns ("", 0) if no match above threshold.
func (idx *Indexer) SearchPatternMatch(ctx context.Context, owner, repo, query string) (content string, score float64) {
	if idx.client == nil || owner == "" || repo == "" {
		return "", 0
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	type result struct {
		content string
		score   float64
	}

	var repoRes, ownerRes result
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		resp, err := idx.client.Search(ctx, SearchRequest{
			Query:        query,
			ContainerTag: RepoTag(owner, repo, "patterns"),
			SearchMode:   "hybrid",
			Limit:        1,
			Threshold:    0.5,
			Rerank:       true,
		})
		if err != nil || len(resp.Results) == 0 {
			return
		}
		repoRes = result{content: resp.Results[0].Content(), score: resp.Results[0].Similarity}
	}()

	go func() {
		defer wg.Done()
		resp, err := idx.client.Search(ctx, SearchRequest{
			Query:        query,
			ContainerTag: OwnerTag(owner, "patterns"),
			SearchMode:   "hybrid",
			Limit:        1,
			Threshold:    0.5,
			Rerank:       true,
		})
		if err != nil || len(resp.Results) == 0 {
			return
		}
		ownerRes = result{content: resp.Results[0].Content(), score: resp.Results[0].Similarity}
	}()

	wg.Wait()

	if repoRes.score >= ownerRes.score {
		return repoRes.content, repoRes.score
	}
	return ownerRes.content, ownerRes.score
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

	idx.logger.Info("indexed review comment", "owner", owner, "repo", repo, "file", comment.FilePath)
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
// Uses v4/memories for immediate searchability. Falls back to v3 if customID is provided (for upsert dedup).
func (idx *Indexer) IndexRepoPattern(ctx context.Context, owner, repo, content, customID string, metadata map[string]string) (*AddResponse, error) {
	tag := RepoTag(owner, repo, "patterns")
	if customID == "" {
		return idx.indexImmediate(ctx, tag, content, metadata, "repo pattern", owner, repo)
	}
	resp, err := idx.client.AddMemory(ctx, AddRequest{
		Content:       content,
		CustomID:      customID,
		ContainerTags: []string{tag},
		Metadata:      metadata,
	})
	if err != nil {
		return nil, fmt.Errorf("indexing repo pattern: %w", err)
	}
	idx.logger.Info("indexed repo pattern (v3 upsert)", "owner", owner, "repo", repo)
	return resp, nil
}

// IndexOwnerPattern stores a pattern at owner scope (applies to all repos in the org).
// Uses v4/memories for immediate searchability. Falls back to v3 if customID is provided (for upsert dedup).
func (idx *Indexer) IndexOwnerPattern(ctx context.Context, owner, content, customID string, metadata map[string]string) (*AddResponse, error) {
	tag := OwnerTag(owner, "patterns")
	if customID == "" {
		return idx.indexImmediate(ctx, tag, content, metadata, "owner pattern", owner, "")
	}
	resp, err := idx.client.AddMemory(ctx, AddRequest{
		Content:       content,
		CustomID:      customID,
		ContainerTags: []string{tag},
		Metadata:      metadata,
	})
	if err != nil {
		return nil, fmt.Errorf("indexing owner pattern: %w", err)
	}
	idx.logger.Info("indexed owner pattern (v3 upsert)", "owner", owner)
	return resp, nil
}

// indexImmediate uses v4/memories for immediate searchability (no queue delay).
func (idx *Indexer) indexImmediate(ctx context.Context, tag, content string, metadata map[string]string, kind, owner, repo string) (*AddResponse, error) {
	resp, err := idx.client.AddMemoryImmediate(ctx, AddImmediateRequest{
		ContainerTag: tag,
		Memories: []ImmediateMemory{{
			Content:  content,
			Metadata: metadata,
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("indexing %s (v4): %w", kind, err)
	}
	idx.logger.Info("indexed "+kind+" (v4 immediate)", "owner", owner, "repo", repo)
	return &AddResponse{ID: resp.DocumentID, Status: "created"}, nil
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
	if err != nil {
		return fmt.Errorf("indexing repo topology: %w", err)
	}
	return nil
}



// FeedbackCustomID returns a stable customId for a feedback signal on a finding.
func FeedbackCustomID(owner, repo, filePath, category, body string) string {
	h := sha256.Sum256([]byte(filePath + "|" + category + "|" + normalizeBody(body) + "|feedback"))
	hash := hex.EncodeToString(h[:6])
	prefix := fmt.Sprintf("%s--%s--feedback", owner, repo)
	return truncateIDWithSuffix(prefix, hash)
}

// FeedbackMemory represents developer feedback on a review comment.
type FeedbackMemory struct {
	FilePath       string
	Category       string
	OriginalBody   string
	Action         string // "confirmed" or "dismissed"
	DeveloperReply string
	PRNumber       int
}

// IndexFeedbackSignal stores a developer feedback signal (confirmation or dismissal)
// as a pattern in Supermemory. Confirmed findings are stored as positive patterns;
// dismissed findings are stored as suppression signals so future reviews can avoid
// repeating false positives.
func (idx *Indexer) IndexFeedbackSignal(ctx context.Context, owner, repo string, feedback FeedbackMemory) error {
	var content string
	var source string
	switch feedback.Action {
	case "confirmed":
		content = fmt.Sprintf("CONFIRMED pattern [%s] in %s: %s\nDeveloper agreed: %s",
			feedback.Category, feedback.FilePath, feedback.OriginalBody,
			util.Truncate(feedback.DeveloperReply, 200, false))
		source = "feedback_confirmed"
	case "dismissed":
		content = fmt.Sprintf("DISMISSED finding [%s] in %s: %s\nDeveloper explanation: %s\nFuture reviews should NOT flag similar patterns in this context.",
			feedback.Category, feedback.FilePath, feedback.OriginalBody,
			util.Truncate(feedback.DeveloperReply, 200, false))
		source = "feedback_dismissed"
	default:
		return nil // no signal to store
	}

	customID := FeedbackCustomID(owner, repo, feedback.FilePath, feedback.Category, feedback.OriginalBody)
	metadata := map[string]string{
		"source":    source,
		"file_path": feedback.FilePath,
		"category":  feedback.Category,
		"pr_number": fmt.Sprintf("%d", feedback.PRNumber),
		"action":    feedback.Action,
	}

	// Store to general patterns container (existing behavior)
	_, err := idx.client.AddMemory(ctx, AddRequest{
		Content:       content,
		CustomID:      customID,
		ContainerTags: []string{RepoTag(owner, repo, "patterns")},
		Metadata:      metadata,
	})
	if err != nil {
		return fmt.Errorf("indexing feedback signal: %w", err)
	}

	// Also store to dedicated negative/positive pattern container for targeted suppression/boosting
	var patternTag string
	var patternContent string
	switch feedback.Action {
	case "dismissed":
		patternTag = NegativePatternTag(owner, repo)
		patternContent = fmt.Sprintf("NEGATIVE_PATTERN: [%s] in %s — %s. Developer: %s",
			feedback.Category, feedback.FilePath,
			util.Truncate(feedback.OriginalBody, 200, true),
			util.Truncate(feedback.DeveloperReply, 150, false))
	case "confirmed":
		patternTag = PositivePatternTag(owner, repo)
		patternContent = fmt.Sprintf("POSITIVE_PATTERN: [%s] in %s — %s. Developer: %s",
			feedback.Category, feedback.FilePath,
			util.Truncate(feedback.OriginalBody, 200, true),
			util.Truncate(feedback.DeveloperReply, 150, false))
	}
	if patternTag != "" {
		patternID := PatternCustomID(owner, repo, feedback.Action, patternContent)
		_, err = idx.client.AddMemory(ctx, AddRequest{
			Content:       patternContent,
			CustomID:      patternID,
			ContainerTags: []string{patternTag},
			Metadata:      metadata,
		})
		if err != nil {
			idx.logger.Warn("indexing pattern signal to dedicated container", "action", feedback.Action, "error", err)
		}
	}

	idx.logger.Info("indexed feedback signal", "action", feedback.Action, "owner", owner, "repo", repo, "file", feedback.FilePath)
	return nil
}

// IndexScenario stores a scenario in Supermemory for semantic retrieval.
// Scenarios are also stored in PostgreSQL for structured queries — this enables
// "find scenarios related to billing" even when file paths don't overlap.
func (idx *Indexer) IndexScenario(ctx context.Context, owner, repo string, scenarioID int64, description, severity string, files []string) error {
	if idx.client == nil {
		return nil
	}
	content := fmt.Sprintf("[scenario_id:%d] Scenario [%s]: %s\nRelated files: %s",
		scenarioID, severity, description, strings.Join(files, ", "))

	customID := fmt.Sprintf("%s--%s--scenario--%d", owner, repo, scenarioID)

	_, err := idx.client.AddMemory(ctx, AddRequest{
		Content:       content,
		CustomID:      customID,
		ContainerTags: []string{RepoTag(owner, repo, "scenarios")},
		Metadata: map[string]string{
			"severity":    severity,
			"scenario_id": fmt.Sprintf("%d", scenarioID),
		},
	})
	if err != nil {
		idx.logger.Warn("indexing scenario in supermemory", "error", err)
		return fmt.Errorf("indexing scenario: %w", err)
	}
	return nil
}

// IndexDecisionTrace stores a decision trace in Supermemory for semantic retrieval.
func (idx *Indexer) IndexDecisionTrace(ctx context.Context, owner, repo, filePath, traceType, content, severity string) error {
	if idx.client == nil {
		return nil
	}
	doc := fmt.Sprintf("[%s] %s: %s (severity: %s)", traceType, filePath, content, severity)

	_, err := idx.client.AddMemory(ctx, AddRequest{
		Content:       doc,
		ContainerTags: []string{RepoTag(owner, repo, "traces")},
		Metadata: map[string]string{
			"file_path":  filePath,
			"trace_type": traceType,
			"severity":   severity,
		},
	})
	if err != nil {
		idx.logger.Warn("indexing trace in supermemory", "error", err)
		return fmt.Errorf("indexing decision trace: %w", err)
	}
	return nil
}

// SearchScenarios performs semantic search over scenarios with reranking for precision.
// Optional severity filter narrows results (e.g., only "critical" scenarios).
func (idx *Indexer) SearchScenarios(ctx context.Context, owner, repo, query, severity string, limit int) []string {
	if idx.client == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req := SearchRequest{
		Query:        query,
		ContainerTag: RepoTag(owner, repo, "scenarios"),
		SearchMode:   "hybrid",
		Rerank:       true,
		Limit:        limit,
	}
	if severity != "" {
		req.Filters = &SearchFilters{
			AND: []FilterCondition{{Key: "severity", Value: severity}},
		}
	}

	resp, err := idx.client.Search(ctx, req)
	if err != nil {
		idx.logger.Warn("searching scenarios in supermemory", "error", err)
		return nil
	}
	var results []string
	for _, r := range resp.Results {
		results = append(results, r.Content())
	}
	return results
}

// SearchScenariosWithIDs performs semantic search over scenarios and returns results with
// parsed Postgres IDs extracted from the embedded [scenario_id:N] prefix in content.
func (idx *Indexer) SearchScenariosWithIDs(ctx context.Context, owner, repo, query, severity string, limit int) []ScenarioSearchResult {
	if idx.client == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req := SearchRequest{
		Query:        query,
		ContainerTag: RepoTag(owner, repo, "scenarios"),
		SearchMode:   "hybrid",
		Rerank:       true,
		Limit:        limit,
	}
	if severity != "" {
		req.Filters = &SearchFilters{
			AND: []FilterCondition{{Key: "severity", Value: severity}},
		}
	}

	resp, err := idx.client.Search(ctx, req)
	if err != nil {
		idx.logger.Warn("searching scenarios with IDs in supermemory", "error", err)
		return nil
	}

	idRe := regexp.MustCompile(`\[scenario_id:(\d+)\]`)
	var results []ScenarioSearchResult
	for _, r := range resp.Results {
		content := r.Content()
		matches := idRe.FindStringSubmatch(content)
		if len(matches) < 2 {
			continue
		}
		id, err := strconv.ParseInt(matches[1], 10, 64)
		if err != nil {
			continue
		}
		results = append(results, ScenarioSearchResult{
			ID:         id,
			Content:    content,
			Similarity: r.Similarity,
		})
	}
	return results
}

// IndexSimulationResult indexes a simulation result into Supermemory traces for future review context.
// Both passes and failures are indexed so future reviews can see the full history of what changes
// are safe vs risky for each scenario.
func (idx *Indexer) IndexSimulationResult(ctx context.Context, owner, repo string, prNumber int, changedFiles []string, passes bool, scenario string, confidence float64, rootCause, impact string) error {
	if idx.client == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	status := "PASS"
	if !passes {
		status = "FAIL"
	}
	content := fmt.Sprintf(
		"Simulation %s on PR #%d: scenario '%s' (%.0f%% confidence).\nRoot cause: %s\nImpact: %s\nChanged files: %s",
		status, prNumber, util.Truncate(scenario, 200, true),
		confidence*100, rootCause, impact,
		strings.Join(changedFiles, ", "))

	_, err := idx.client.AddMemoryImmediate(ctx, AddImmediateRequest{
		ContainerTag: RepoTag(owner, repo, "traces"),
		Memories: []ImmediateMemory{{
			Content: content,
			Metadata: map[string]string{
				"trace_type": "simulation_result",
				"passes":     fmt.Sprintf("%t", passes),
				"pr_number":  fmt.Sprintf("%d", prNumber),
				"confidence": fmt.Sprintf("%.2f", confidence),
			},
		}},
	})
	if err != nil {
		return fmt.Errorf("indexing simulation result: %w", err)
	}
	return nil
}

// SearchTraces performs semantic search over decision traces with reranking.
// Optional traceType filter narrows results (e.g., only "review_finding" traces).
func (idx *Indexer) SearchTraces(ctx context.Context, owner, repo, query, traceType string, limit int) []string {
	if idx.client == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req := SearchRequest{
		Query:        query,
		ContainerTag: RepoTag(owner, repo, "traces"),
		SearchMode:   "hybrid",
		Rerank:       true,
		Limit:        limit,
	}
	if traceType != "" {
		req.Filters = &SearchFilters{
			AND: []FilterCondition{{Key: "trace_type", Value: traceType}},
		}
	}

	resp, err := idx.client.Search(ctx, req)
	if err != nil {
		idx.logger.Warn("searching traces in supermemory", "error", err)
		return nil
	}
	var results []string
	for _, r := range resp.Results {
		results = append(results, r.Content())
	}
	return results
}

// SearchPatternsFiltered performs semantic search over patterns with metadata filtering.
func (idx *Indexer) SearchPatternsFiltered(ctx context.Context, owner, repo, query, category string, limit int) []string {
	if idx.client == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req := SearchRequest{
		Query:        query,
		ContainerTag: RepoTag(owner, repo, "patterns"),
		SearchMode:   "hybrid",
		Rerank:       true,
		Limit:        limit,
	}
	if category != "" {
		req.Filters = &SearchFilters{
			AND: []FilterCondition{{Key: "category", Value: category}},
		}
	}

	resp, err := idx.client.Search(ctx, req)
	if err != nil {
		idx.logger.Warn("searching patterns filtered", "error", err)
		return nil
	}
	var results []string
	for _, r := range resp.Results {
		results = append(results, r.Content())
	}
	return results
}

