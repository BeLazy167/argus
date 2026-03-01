package rules

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	ghpkg "github.com/BeLazy167/argus/internal/github"
)

// Rule represents a review rule from either org DB or repo .argus/rules.md.
type Rule struct {
	ID       int64
	Source   string // "org" or "repo"
	Category string
	Content  string
	Priority int
}

// Engine merges org-wide rules (from DB) with repo-level rules (.argus/rules.md).
type Engine struct {
	db       *pgxpool.Pool
	ghClient *ghpkg.Client
	logger   *slog.Logger
}

func NewEngine(db *pgxpool.Pool, ghClient *ghpkg.Client, logger *slog.Logger) *Engine {
	return &Engine{db: db, ghClient: ghClient, logger: logger}
}

// GetMergedRules returns all applicable rules for a repo, with repo rules overriding org rules in the same category.
func (e *Engine) GetMergedRules(ctx context.Context, installationID int64, repoFullName, ref string) ([]Rule, error) {
	orgRules, err := e.getOrgRules(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetching org rules: %w", err)
	}

	repoRules, err := e.getRepoRules(ctx, installationID, repoFullName, ref)
	if err != nil {
		e.logger.Warn("failed to fetch repo rules, using org rules only", "error", err)
		return orgRules, nil
	}

	return mergeRules(orgRules, repoRules), nil
}

func (e *Engine) getOrgRules(ctx context.Context) ([]Rule, error) {
	rows, err := e.db.Query(ctx, `SELECT id, category, content, priority FROM rules WHERE enabled = true ORDER BY priority DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rules []Rule
	for rows.Next() {
		var r Rule
		if err := rows.Scan(&r.ID, &r.Category, &r.Content, &r.Priority); err != nil {
			return nil, err
		}
		r.Source = "org"
		rules = append(rules, r)
	}
	return rules, rows.Err()
}

func (e *Engine) getRepoRules(ctx context.Context, installationID int64, repoFullName, ref string) ([]Rule, error) {
	parts := strings.SplitN(repoFullName, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid repo name: %s", repoFullName)
	}

	content, err := e.ghClient.GetFileContent(ctx, installationID, parts[0], parts[1], ".argus/rules.md", ref)
	if err != nil {
		return nil, err // File doesn't exist or not accessible
	}

	return parseRulesMarkdown(content), nil
}

// parseRulesMarkdown parses a .argus/rules.md file into structured rules.
// Format: ## Category\n\n- Rule content\n- Rule content
func parseRulesMarkdown(content string) []Rule {
	var rules []Rule
	var currentCategory string
	priority := 100 // Repo rules get higher priority by default

	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "## ") {
			currentCategory = strings.TrimPrefix(line, "## ")
			currentCategory = strings.TrimSpace(currentCategory)
		} else if strings.HasPrefix(line, "- ") && currentCategory != "" {
			rules = append(rules, Rule{
				Source:   "repo",
				Category: currentCategory,
				Content:  strings.TrimPrefix(line, "- "),
				Priority: priority,
			})
		}
	}
	return rules
}

// mergeRules combines org and repo rules. Repo rules take precedence: org rules in categories covered by repo rules are dropped.
func mergeRules(orgRules, repoRules []Rule) []Rule {
	repoCategories := make(map[string]bool)
	for _, r := range repoRules {
		repoCategories[r.Category] = true
	}

	var merged []Rule
	for _, r := range orgRules {
		if !repoCategories[r.Category] {
			merged = append(merged, r)
		}
	}
	merged = append(merged, repoRules...)
	return merged
}

// FormatRulesForPrompt converts rules into a string suitable for LLM system prompts.
func FormatRulesForPrompt(rules []Rule) string {
	if len(rules) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("Review rules to follow:\n\n")
	for _, r := range rules {
		sb.WriteString(fmt.Sprintf("[%s] %s\n", r.Category, r.Content))
	}
	return sb.String()
}
