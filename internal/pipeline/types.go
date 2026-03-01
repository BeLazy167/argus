package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/BeLazy167/argus/internal/github"
	"github.com/BeLazy167/argus/pkg/diff"
)

// Severity classifies the impact of a review comment.
type Severity string

const (
	SeverityCritical   Severity = "critical"
	SeverityWarning    Severity = "warning"
	SeveritySuggestion Severity = "suggestion"
	SeverityPraise     Severity = "praise"
)

// Category classifies the kind of review comment.
type Category string

const (
	CategorySecurity      Category = "security"
	CategoryPerformance   Category = "performance"
	CategoryStyle         Category = "style"
	CategoryBug           Category = "bug"
	CategoryReadability   Category = "readability"
	CategoryErrorHandling Category = "error_handling"
	CategoryTypeDesign    Category = "type_design"
	CategoryTesting       Category = "testing"
)

// RunTokenUsage tracks token consumption and cost across pipeline stages.
type RunTokenUsage struct {
	Triage StageTokens   `json:"triage"`
	Review []StageTokens `json:"review"`
	Total  StageTokens   `json:"total"`
}

// StageTokens holds token counts and cost for a single LLM call or stage aggregate.
type StageTokens struct {
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	Cost             float64 `json:"cost"`
	File             string  `json:"file,omitempty"`
}

// PipelineRun tracks the state and intermediate results of a single review.
type PipelineRun struct {
	ID               uuid.UUID
	ReviewID         uuid.UUID
	State            PipelineState
	PREvent          github.PREvent
	DBInstallationID int64 // DB serial ID (for provider_keys, model_configs lookups)
	DBRepoID         int64 // DB serial ID (for model_configs, reviews lookups)
	Diff             *diff.PatchSet
	RawDiff          string
	TriageResults    []TriageResult
	FileReviews      []FileReview
	Synthesis        *SynthesisResult
	Tokens           RunTokenUsage
	IsIncremental    bool
	PreviousReviewID *uuid.UUID
	Error            string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// FileReview holds the review output for a single file.
type FileReview struct {
	Path     string
	Comments []FileComment
}

// FileComment is a single review comment on a file.
type FileComment struct {
	Line      int      `json:"line"`
	StartLine int      `json:"start_line"`
	Body      string   `json:"body"`
	Severity  Severity `json:"severity"`
	Category    Category `json:"category"`
	CodeSnippet string   `json:"code_snippet,omitempty"`
}

// ValidSeverities is the set of valid severity values.
var ValidSeverities = map[Severity]bool{
	SeverityCritical: true, SeverityWarning: true, SeveritySuggestion: true, SeverityPraise: true,
}

// ValidCategories is the set of valid category values.
var ValidCategories = map[Category]bool{
	CategorySecurity: true, CategoryPerformance: true, CategoryStyle: true, CategoryBug: true,
	CategoryReadability: true, CategoryErrorHandling: true, CategoryTypeDesign: true, CategoryTesting: true,
}

// SynthesisResult is the combined review output.
type SynthesisResult struct {
	Summary    string
	Score      int // 1-10
	TokenUsage map[string]int
}

// StageFunc is a function that executes a single pipeline stage.
type StageFunc func(ctx context.Context, run *PipelineRun) error

// addToTotal accumulates stage tokens into the total.
func (r *RunTokenUsage) addToTotal(s StageTokens) {
	r.Total.PromptTokens += s.PromptTokens
	r.Total.CompletionTokens += s.CompletionTokens
	r.Total.TotalTokens += s.TotalTokens
	r.Total.Cost += s.Cost
}

// unmarshalLLMArray parses a JSON array from LLM output, handling markdown code fences.
func unmarshalLLMArray[T any](content string) ([]T, error) {
	if content == "" {
		return nil, nil
	}
	var result []T
	if err := json.Unmarshal([]byte(content), &result); err == nil {
		return result, nil
	}
	start := strings.Index(content, "[")
	end := strings.LastIndex(content, "]")
	if start >= 0 && end > start {
		var result []T
		if err := json.Unmarshal([]byte(content[start:end+1]), &result); err != nil {
			return nil, fmt.Errorf("parsing JSON from response: %w", err)
		}
		return result, nil
	}
	return nil, fmt.Errorf("no JSON array found in response")
}
