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
	Triage        StageTokens   `json:"triage"`
	Review        []StageTokens `json:"review"`
	Scoring       StageTokens   `json:"scoring,omitempty"`
	Synthesis     StageTokens   `json:"synthesis,omitempty"`
	Enrichment    StageTokens   `json:"enrichment,omitempty"`
	Conventions   StageTokens   `json:"conventions,omitempty"`
	Patterns      StageTokens   `json:"patterns,omitempty"`
	FileSynthesis []StageTokens `json:"file_synthesis,omitempty"`
	Graph         StageTokens   `json:"graph,omitempty"`
	Total         StageTokens   `json:"total"`
}

// StageTokens holds token counts and cost for a single LLM call or stage aggregate.
type StageTokens struct {
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	Cost             float64 `json:"cost"`
	Model            string  `json:"model,omitempty"`
	Provider         string  `json:"provider,omitempty"`
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
	AllFileReviews   []FileReview // pre-scoring snapshot: all comments with scores, before threshold drop
	Synthesis        *SynthesisResult
	Tokens           RunTokenUsage
	Persona             Persona
	CustomPersonaPrompt string
	DeepReview          bool
	CrossFileContext    bool
	BlastRadius         bool
	ScenarioMemory      bool
	CodeSimulation      bool
	PREnrichment      bool
	LearnPatterns     bool
	LearnConventions  bool
	FileSynthesis     bool
	ArchitectureGraph bool
	LeadBrief        *LeadBrief `json:"lead_brief,omitempty"`
	LeadAgentError   string     `json:"lead_agent_error,omitempty"`
	ScoringSkipped   bool // true when scoring provider unavailable — synthesis uses all comments
	Prompts          map[string]string // custom prompt overrides per stage
	IsIncremental    bool
	PreviousReviewID *uuid.UUID
	StartedCommentNodeID string    `json:"-"` // node ID of the "review started" GH comment, for minimizing later
	EventBus             *EventBus `json:"-"` // not persisted
	Error            string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// LeadBrief is the output of the Lead Agent's briefing phase.
// Stored as a flat array — each item is either a file brief or a cross-cutting concern.
type LeadBrief struct {
	Items []BriefItem `json:"items"`
}

// BriefItem is a single element in the lead brief array.
// Either a file brief (File non-empty) or a cross-cutting concern (CrossCutting non-empty).
type BriefItem struct {
	File         string `json:"file,omitempty"`
	Summary      string `json:"summary,omitempty"`
	Bug          string `json:"bug,omitempty"`
	Security     string `json:"security,omitempty"`
	Arch         string `json:"arch,omitempty"`
	Regression   string `json:"regression,omitempty"`
	CrossCutting string `json:"cross_cutting,omitempty"`
}

// FileBrief returns the brief for a specific file, or nil if not found.
func (b *LeadBrief) FileBrief(path string) *BriefItem {
	for i := range b.Items {
		if b.Items[i].File == path {
			return &b.Items[i]
		}
	}
	return nil
}

// CrossCuttingConcerns returns all cross-cutting items from the brief.
func (b *LeadBrief) CrossCuttingConcerns() []string {
	var cc []string
	for _, item := range b.Items {
		if item.CrossCutting != "" {
			cc = append(cc, item.CrossCutting)
		}
	}
	return cc
}

// CrossAgentSignal represents a finding from one agent that another should investigate.
type CrossAgentSignal struct {
	FromAgent    string   `json:"from_agent"`
	ToAgent      string   `json:"to_agent"`
	Signal       string   `json:"signal"`
	Question     string   `json:"question"`
	FilesToCheck []string `json:"files_to_check"`
}

// BlastRadiusImpact represents a concrete breaking change found by the blast radius agent.
type BlastRadiusImpact struct {
	DependentFile      string `json:"dependent_file"`
	DependentSymbol    string `json:"dependent_symbol"`
	AssumptionViolated string `json:"assumption_violated"`
	FailureMode        string `json:"failure_mode"`
	Severity           string `json:"severity"`
}

// AgentResult is the unified output from any agent in the team.
type AgentResult struct {
	AgentName    string
	FileReviews  []FileReview
	SimResults   []SimulationResult
	BlastImpacts []BlastRadiusImpact
}

// FileReview holds the review output for a single file.
type FileReview struct {
	Path     string
	Comments []FileComment
}

// FileComment is a single review comment on a file.
type FileComment struct {
	Line        int      `json:"line"`
	StartLine   int      `json:"start_line"`
	Body        string   `json:"body"`
	What        string   `json:"what,omitempty"`
	Why         string   `json:"why,omitempty"`
	Severity    Severity `json:"severity"`
	Category    Category `json:"category"`
	CodeSnippet string   `json:"code_snippet,omitempty"`
	Suggestion  string   `json:"suggestion,omitempty"`
	Specialist          Specialist `json:"specialist,omitempty"`
	Score               int        `json:"score"`
	MatchedPatternID    int64      `json:"-"`
	MatchedPatternScore float64    `json:"-"`
	BlastRadius         int        `json:"blast_radius,omitempty"` // number of downstream dependents affected
	EnforcedRuleContent string     `json:"-"`
	IsNewFinding        bool       `json:"-"`
	DedupCount          int        `json:"dedup_count,omitempty"` // how many duplicate findings were merged into this one
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
	Summary           string
	Brief             string
	Score             int // 1-10
	TokenUsage        map[string]int
	SimulationResults []SimulationResult
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

// customOrDefault returns the custom prompt for key if set, otherwise the fallback.
func customOrDefault(prompts map[string]string, key, fallback string) string {
	if p, ok := prompts[key]; ok && p != "" {
		return p
	}
	return fallback
}

// unmarshalLLMArray parses a JSON array from LLM output, handling markdown code fences.
func unmarshalLLMArray[T any](content string) ([]T, error) {
	if content == "" {
		return nil, nil
	}
	// Strip markdown code fences: ```json ... ``` or ``` ... ```
	cleaned := stripCodeFences(content)
	var result []T
	if err := json.Unmarshal([]byte(cleaned), &result); err == nil {
		return result, nil
	}
	start := strings.Index(cleaned, "[")
	end := strings.LastIndex(cleaned, "]")
	if start >= 0 && end > start {
		var result []T
		if err := json.Unmarshal([]byte(cleaned[start:end+1]), &result); err != nil {
			return nil, fmt.Errorf("parsing JSON from response: %w", err)
		}
		return result, nil
	}
	return nil, fmt.Errorf("no JSON array found in response")
}

// stripCodeFences removes markdown code fences (```json\n...\n```) from LLM output.
func stripCodeFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		// Remove opening fence (```json, ```JSON, ```, etc.)
		if idx := strings.Index(s, "\n"); idx >= 0 {
			s = s[idx+1:]
		}
		// Remove closing fence
		if idx := strings.LastIndex(s, "```"); idx >= 0 {
			s = s[:idx]
		}
	}
	return strings.TrimSpace(s)
}
