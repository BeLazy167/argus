package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/util"
	"github.com/BeLazy167/argus/backend/pkg/diff"
	"github.com/google/uuid"
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
	// CategoryIntent marks findings emitted by the intent-verification step
	// when a PR's code does not deliver its stated goal.
	CategoryIntent Category = "intent"
)

// RunTokenUsage tracks token consumption and cost across pipeline stages.
//
// LeadAgent aggregates 5 lead-agent sub-calls (leadBrief, leadBroadcast,
// agentSecondPass, analyzeBlastRadius, leadCrossCheck) so the per-review
// breakdown stays compact — per-sub-step granularity lives on the SSE stream.
//
// Simulation is a slice (like Review and FileSynthesis) — one StageTokens per
// scenario run, preserving UCB1 selection order.
//
// Reply is declared but not yet wired. The comment-reply worker runs outside
// the review pipeline and will be instrumented in a follow-up ship.
//
// Concurrency: mu guards all write paths. validateStage runs acceptance,
// crosspr, and blast-radius workers in parallel and they all call addToTotal.
// Reads (rendering, serialisation) happen after all writers join, so unguarded
// reads are safe in practice — the mutex is write-only.
type RunTokenUsage struct {
	mu            sync.Mutex    `json:"-"`
	Intent        StageTokens   `json:"intent,omitempty"` // extraction + verification sub-calls are summed here
	Triage        StageTokens   `json:"triage"`
	Review        []StageTokens `json:"review"`
	Scoring       StageTokens   `json:"scoring,omitempty"`
	Synthesis     StageTokens   `json:"synthesis,omitempty"`
	Enrichment    StageTokens   `json:"enrichment,omitempty"`
	Conventions   StageTokens   `json:"conventions,omitempty"`
	Patterns      StageTokens   `json:"patterns,omitempty"`
	FileSynthesis []StageTokens `json:"file_synthesis,omitempty"`
	Graph         StageTokens   `json:"graph,omitempty"`
	LeadAgent     StageTokens   `json:"lead_agent,omitempty"`
	Acceptance    StageTokens   `json:"acceptance,omitempty"`
	CrossPR       StageTokens   `json:"cross_pr,omitempty"`
	Simulation    []StageTokens `json:"simulation,omitempty"`
	Reply         StageTokens   `json:"reply,omitempty"` // reserved; reply worker not yet instrumented
	Total         StageTokens   `json:"total"`
}

// StageTokens holds token counts and cost for a single LLM call or stage aggregate.
// For review-stage units, Specialist names the reviewer (correctness, security,
// architecture, regression) or is empty for skim/single-pass reviews.
type StageTokens struct {
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	Cost             float64 `json:"cost"`
	Model            string  `json:"model,omitempty"`
	Provider         string  `json:"provider,omitempty"`
	File             string  `json:"file,omitempty"`
	Specialist       string  `json:"specialist,omitempty"`
}

// PipelineRun tracks the state and intermediate results of a single review.
type PipelineRun struct {
	ID                  uuid.UUID
	ReviewID            uuid.UUID
	State               PipelineState
	PREvent             github.PREvent
	DBInstallationID    int64 // DB serial ID (for provider_keys, model_configs lookups)
	DBRepoID            int64 // DB serial ID (for model_configs, reviews lookups)
	// TraceID carries the X-Argus-Trace-Id header from the initiating HTTP request. Empty
	// when the event entered outside the middleware (e.g. sweeper recovery). Persisted via
	// reviews.trace_id so async stages can continue the same trace from a fresh ctx.
	TraceID string
	Diff                *diff.PatchSet
	RawDiff             string
	TriageResults       []TriageResult
	FileReviews         []FileReview
	AllFileReviews      []FileReview        // pre-scoring snapshot: all comments with scores, before threshold drop
	// MinorNotes collects near-miss findings (scored within the near-miss band
	// below their severity threshold) plus nits demoted from files that carry a
	// blocking finding. Rendered collapsed in the summary, never inline.
	MinorNotes          []MinorNote         `json:"minor_notes,omitempty"`
	SuppressedKeys      map[string]struct{} // path\x00line\x00body of dismissal-dropped findings; gates pattern-learning that reads the pre-enrich AllFileReviews snapshot
	Synthesis           *SynthesisResult
	Tokens              RunTokenUsage
	Persona             Persona
	CustomPersonaPrompt string
	DeepReview          bool
	CrossFileContext    bool
	BlastRadius         bool
	ScenarioMemory      bool
	CodeSimulation      bool
	PREnrichment        bool
	LearnPatterns       bool
	LearnConventions    bool
	FileSynthesis       bool
	ArchitectureGraph   bool
	TruncatedFiles      []string
	LeadBrief           *LeadBrief        `json:"lead_brief,omitempty"`
	LeadAgentError      string            `json:"lead_agent_error,omitempty"`
	ScoringSkipped      bool              // true when scoring provider unavailable — synthesis uses all comments
	ScoringUnconfigured bool
	// ScoringMissingKey narrows the unconfigured cause: the scoring model row
	// exists but no API key resolves for its provider — the remedy is a key,
	// not a model config.
	ScoringMissingKey bool              // true when the skip was resolution failure (no model config/key) — posts a setup notice
	Prompts             map[string]string // custom prompt overrides per stage
	IsIncremental       bool
	PreviousReviewID    *uuid.UUID
	PriorComments       map[string][]PriorComment // file path -> prior unresolved comments from previous review
	// SastFindings holds SAST tool results keyed by file path.
	SastFindings map[string][]SastFinding `json:"-"`
	// ArchContext holds per-file architecture metrics for review prompt enrichment.
	// Populated in pre-review for high-risk files (choke points / hotspots).
	ArchContext map[string]ArchContextEntry `json:"-"`
	// Contract is the per-PR ReviewContract computed by ComputeContract before
	// the pipeline starts. Deterministic signals decide it when possible;
	// otherwise Source=="llm-pending" until IntentExtractionStage fills the
	// change class. Nil on paths that never computed one (tests, replays) —
	// consumers must treat nil as production-class behavior.
	Contract *ReviewContract `json:"-"`
	// PRIntent holds the author's stated motivation for the PR (goal, non-goals,
	// acceptance criteria). Populated by IntentExtractionStage in the pre-review block,
	// consumed by specialists and Synthesis. Nil until extraction runs; after extraction
	// Source may be "empty" when neither PR body nor linked issues provide any context.
	PRIntent *PRIntent `json:"-"`
	// LinkedIssues holds issues this PR references (closes / fixes / resolves / refs).
	// Populated by HandlePREvent via GraphQL + regex fallback.
	LinkedIssues []IssueLink `json:"-"`
	// IssueAcceptance holds per-issue criterion verdicts from the acceptance worker.
	IssueAcceptance []AcceptanceResult `json:"-"`
	// LinkedPRs holds cross-repo PRs referenced from the primary PR body.
	LinkedPRs []PRLink `json:"-"`
	// CrossPRCoverage holds the aggregate compatibility judgment from the crosspr worker.
	CrossPRCoverage *CrossPRCoverage `json:"-"`
	// JointAcceptance holds per-shared-issue verdicts from the joint
	// acceptance stage (crosspr_stage.runCrossPRAcceptanceStage).
	// Additive to IssueAcceptance — per-PR rollup stays on the original
	// field; this one only populates when >=2 linked reviews reference the
	// same issue, and renders into a separate "## Joint Issue Coverage"
	// sticky section alongside the existing "## Issue Coverage".
	JointAcceptance []JointAcceptanceResult `json:"-"`
	// FeatureFlags holds per-installation toggles loaded once per run.
	FeatureFlags         FeatureFlags      `json:"-"`
	StartedCommentNodeID string            `json:"-"` // node ID of the "review started" GH comment, for minimizing later
	Indexer              memory.Indexer    `json:"-"` // per-org indexer resolved from Registry
	Thresholds           memory.Thresholds `json:"-"` // per-run similarity gates (merged org→repo settings, Bundle 3)
	EventBus             *EventBus         `json:"-"` // not persisted
	Error                string
	CreatedAt            time.Time
	UpdatedAt            time.Time
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
	Line      int    `json:"line"`
	StartLine int    `json:"start_line"`
	Body      string `json:"body"`
	What      string `json:"what,omitempty"`
	Why       string `json:"why,omitempty"`
	// Reasoning is the LLM's evidence chain, emitted FIRST in the response
	// schema so the model commits to a concrete failure scenario before it
	// picks a severity (evidence law).
	Reasoning string   `json:"reasoning,omitempty"`
	Severity  Severity `json:"severity"`
	Category  Category `json:"category"`
	// LLMConfidence is the reviewer model's own 0-1 confidence in the finding,
	// parsed from the "confidence" field of the review JSON. Distinct from
	// Confidence (the post-scoring high/medium/low level).
	LLMConfidence       float64    `json:"confidence,omitempty"`
	CodeSnippet         string     `json:"code_snippet,omitempty"`
	Suggestion          string     `json:"suggestion,omitempty"`
	Specialist          Specialist `json:"specialist,omitempty"`
	Score               int        `json:"score"`
	MatchedPatternID    int64      `json:"-"`
	MatchedPatternScore float64    `json:"-"`
	// MatchedPatternKind classifies the kind of memory hit so the review-body
	// renderer can produce the right phrasing: "pattern" | "convention" |
	// "rule" | "similarity". Empty string ⇒ no memory tag rendered.
	MatchedPatternKind    string `json:"-"`
	MatchedPatternPR      int    `json:"-"`                      // source PR number (from Supermemory doc metadata)
	MatchedPatternAuthor  string `json:"-"`                      // source PR author login
	MatchedPatternAgeDays int    `json:"-"`                      // rounded days since source indexed
	BlastRadius           int    `json:"blast_radius,omitempty"` // number of downstream dependents affected
	EnforcedRuleContent   string `json:"-"`
	IsNewFinding          bool   `json:"-"`
	Suppressed            bool   `json:"-"` // dismissal-match drop: persisted flagged, never posted/counted
	SuppressedReason      string `json:"-"` // e.g. "dismissed_match:0.91"; → review_comments.suppressed_reason
	DismissedDowngrade    bool   `json:"-"` // dismissal-match downgrade: severity lowered one level + note
	DismissedMatchPR      int    `json:"-"` // source PR of the dismissed finding (0 = unknown), for the note
	DedupCount            int    `json:"dedup_count,omitempty"` // how many duplicate findings were merged into this one
	// Corroboration is the number of DISTINCT specialists that independently
	// produced this finding, recorded when dedup merges a cluster. Scoring
	// grants a small bounded boost for Corroboration >= 2 — never a gate.
	Corroboration    int    `json:"corroboration,omitempty"`
	SastCorroborated bool   `json:"sast_corroborated,omitempty"`
	Confidence       string `json:"confidence_level,omitempty"` // post-scoring high/medium/low
}

// ValidSeverities is the set of valid severity values.
var ValidSeverities = map[Severity]bool{
	SeverityCritical: true, SeverityWarning: true, SeveritySuggestion: true, SeverityPraise: true,
}

// ValidCategories is the set of valid category values. Style and readability
// are intentionally NOT valid emit categories (Review Laws: style/formatting
// are never findings) — LLM output using them falls back to
// CategoryReadability in validateComments, which the deterministic scoring cap
// then pins at 30 so it can never post as an earned finding.
var ValidCategories = map[Category]bool{
	CategorySecurity: true, CategoryPerformance: true, CategoryBug: true,
	CategoryErrorHandling: true, CategoryTypeDesign: true, CategoryTesting: true,
	CategoryIntent: true,
}

// MinorNote is a finding that was demoted out of the inline comment set —
// either it scored within the near-miss band below its severity threshold, or
// it is a nit on a file that also carries a blocking finding. Minor notes are
// rendered as a collapsed section in the review summary, never inline.
type MinorNote struct {
	Path     string   `json:"path"`
	Line     int      `json:"line"`
	Severity Severity `json:"severity"`
	Title    string   `json:"title"`
}

// SynthesisResult is the combined review output.
type SynthesisResult struct {
	Summary string
	Brief   string
	// Headline is a short (≤80 char) single-sentence verdict used by the
	// posted-comment H2. Captured in synthesize() before FormatIntentHeader
	// is prepended to Brief, so the header shows the synthesis takeaway
	// rather than the intent-block disclaimer that now leads Brief.
	Headline          string
	Score             int // 1-10
	TokenUsage        map[string]int
	SimulationResults []SimulationResult
	IntentVerdict     *IntentVerdict // populated by synthesize when PRIntent.HasIntent() is true
}

// IntentSource classifies the provenance of a PRIntent. A typed enum rather
// than a raw string so callers can switch exhaustively and invalid values are
// caught at parse time via ValidIntentSources.
type IntentSource string

const (
	// IntentSourceAuthor means the goal was extracted from human-written text
	// (PR body or linked issue). Verification is reliable — the goal is what
	// the author actually claims.
	IntentSourceAuthor IntentSource = "author"
	// IntentSourceInferred means the LLM guessed the goal from the diff /
	// commits because no author-written text was available. Verification runs
	// but the verdict should be weighted more leniently.
	IntentSourceInferred IntentSource = "inferred"
	// IntentSourceEmpty means extraction produced no usable signal — no
	// sources, or the extraction LLM / parser / provider failed. HasIntent()
	// returns false; downstream rendering and verification are skipped.
	IntentSourceEmpty IntentSource = "empty"
)

// ValidIntentSources is the closed set of IntentSource values. Used by
// parseIntent to coerce unknown values emitted by a drifting LLM contract.
var ValidIntentSources = map[IntentSource]bool{
	IntentSourceAuthor:   true,
	IntentSourceInferred: true,
	IntentSourceEmpty:    true,
}

// PRIntent captures the author's stated motivation for a PR, extracted from the
// PR body, linked GitHub issues, commit messages, and linked PR titles.
//
// Consumers:
//   - Review specialists receive the structured fields as a prose <pr_intent> block
//     for attention weighting (see buildFileReviewPrompt in review.go).
//   - Synthesis uses goal + acceptance_criteria to judge whether the diff delivers
//     (IntentVerdict) and non_goals to demote out-of-scope findings.
//
// Source uses the IntentSource typed enum so callers get compile-time coverage;
// any unknown value from the LLM is coerced to IntentSourceAuthor by parseIntent.
type PRIntent struct {
	Goal               string       `json:"goal"`
	NonGoals           []string     `json:"non_goals"`
	AcceptanceCriteria []string     `json:"acceptance_criteria"`
	ExpectedFiles      []string     `json:"expected_files"`
	RiskFlags          []string     `json:"risk_flags"`
	Source             IntentSource `json:"source"`
	// ChangeClass is the LLM's read of what kind of change this PR is (see
	// ValidChangeClasses). Only consulted when the deterministic contract pass
	// was silent (ReviewContract.Source == "llm-pending").
	ChangeClass           string  `json:"change_class"`
	ChangeClassConfidence float64 `json:"change_class_confidence"`
	RawContext            string  `json:"-"` // concatenated sources, used to seed downstream prompts
}

// IntentVerdict is Synthesis's judgment on whether the diff delivers PRIntent.Goal.
//
// UnmetCriteria lists PRIntent.AcceptanceCriteria for which the reviewer found
// no evidence. OutOfScopeFindings holds FileComment indices (positional, into
// the flat enumeration produced by BuildIntentVerificationPrompt) whose subject
// matches a PRIntent.NonGoals entry and should be demoted.
//
// BuiltAgainstCount is the total number of findings that existed at verdict
// construction time. DemoteOutOfScopeFindings compares this to the current
// count as a staleness guard — if FileReviews mutated between verdict
// construction and demotion, the positional IDs are meaningless and demotion
// must be skipped.
type IntentVerdict struct {
	Delivers           bool     `json:"delivers"`
	Rationale          string   `json:"rationale"`
	UnmetCriteria      []string `json:"unmet_criteria"`
	OutOfScopeFindings []int    `json:"out_of_scope_finding_ids"`
	BuiltAgainstCount  int      `json:"-"` // set by the caller after parsing
}

// StageFunc is a function that executes a single pipeline stage.
type StageFunc func(ctx context.Context, run *PipelineRun) error

// addToTotal accumulates stage tokens into the total.
func (r *RunTokenUsage) addToTotal(s StageTokens) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Total.PromptTokens += s.PromptTokens
	r.Total.CompletionTokens += s.CompletionTokens
	r.Total.TotalTokens += s.TotalTokens
	r.Total.Cost += s.Cost
}

// addLeadAgent merges tokens from one of the 5 lead-agent sub-calls into the
// aggregate LeadAgent bucket. Model/Provider are stamped on the first write.
// Also calls addToTotal so Total stays in sync; callers must NOT also call
// addToTotal themselves for the same tokens.
func (r *RunTokenUsage) addLeadAgent(s StageTokens) {
	r.mu.Lock()
	r.LeadAgent.PromptTokens += s.PromptTokens
	r.LeadAgent.CompletionTokens += s.CompletionTokens
	r.LeadAgent.TotalTokens += s.TotalTokens
	r.LeadAgent.Cost += s.Cost
	if r.LeadAgent.Model == "" {
		r.LeadAgent.Model = s.Model
		r.LeadAgent.Provider = s.Provider
	}
	r.Total.PromptTokens += s.PromptTokens
	r.Total.CompletionTokens += s.CompletionTokens
	r.Total.TotalTokens += s.TotalTokens
	r.Total.Cost += s.Cost
	r.mu.Unlock()
}

// addAcceptance / addCrossPR mirror addLeadAgent for the Acceptance / CrossPR
// buckets respectively. Both paths can run concurrently from validateStage
// workers, so the lock guards the combined bucket + total write.
func (r *RunTokenUsage) addAcceptance(s StageTokens) {
	r.mu.Lock()
	r.Acceptance.PromptTokens += s.PromptTokens
	r.Acceptance.CompletionTokens += s.CompletionTokens
	r.Acceptance.TotalTokens += s.TotalTokens
	r.Acceptance.Cost += s.Cost
	if r.Acceptance.Model == "" {
		r.Acceptance.Model = s.Model
		r.Acceptance.Provider = s.Provider
	}
	r.Total.PromptTokens += s.PromptTokens
	r.Total.CompletionTokens += s.CompletionTokens
	r.Total.TotalTokens += s.TotalTokens
	r.Total.Cost += s.Cost
	r.mu.Unlock()
}

func (r *RunTokenUsage) addCrossPR(s StageTokens) {
	r.mu.Lock()
	r.CrossPR.PromptTokens += s.PromptTokens
	r.CrossPR.CompletionTokens += s.CompletionTokens
	r.CrossPR.TotalTokens += s.TotalTokens
	r.CrossPR.Cost += s.Cost
	if r.CrossPR.Model == "" {
		r.CrossPR.Model = s.Model
		r.CrossPR.Provider = s.Provider
	}
	r.Total.PromptTokens += s.PromptTokens
	r.Total.CompletionTokens += s.CompletionTokens
	r.Total.TotalTokens += s.TotalTokens
	r.Total.Cost += s.Cost
	r.mu.Unlock()
}

// addSimulation appends a per-scenario StageTokens and rolls into Total.
// Order is scenario-selection order (UCB1 top-5, stable).
func (r *RunTokenUsage) addSimulation(s StageTokens) {
	r.mu.Lock()
	r.Simulation = append(r.Simulation, s)
	r.Total.PromptTokens += s.PromptTokens
	r.Total.CompletionTokens += s.CompletionTokens
	r.Total.TotalTokens += s.TotalTokens
	r.Total.Cost += s.Cost
	r.mu.Unlock()
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
		chunk := cleaned[start : end+1]
		var result []T
		if err := json.Unmarshal([]byte(chunk), &result); err != nil {
			repaired := strings.ReplaceAll(chunk, "}\n{", "},\n{")
			repaired = strings.ReplaceAll(repaired, "} {", "}, {")
			repaired = strings.ReplaceAll(repaired, "}\t{", "},\t{")
			if err2 := json.Unmarshal([]byte(repaired), &result); err2 != nil {
				recovered := recoverTruncatedArray[T](cleaned[start:])
				if recovered != nil {
					return recovered, nil
				}
				return nil, fmt.Errorf("parsing JSON from response: %w", err)
			}
			return result, nil
		}
		return result, nil
	}
	recovered := recoverTruncatedArray[T](cleaned)
	if recovered != nil {
		return recovered, nil
	}
	return nil, fmt.Errorf("no JSON array found in response")
}

func recoverTruncatedArray[T any](content string) []T {
	start := strings.Index(content, "[")
	if start < 0 {
		return nil
	}
	body := content[start+1:]
	depth := 0
	lastClose := -1
	for i, ch := range body {
		switch ch {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				lastClose = i
			}
		}
	}
	if lastClose <= 0 {
		return nil
	}
	closed := content[start:start+1+lastClose+1] + "]"
	var result []T
	if err := json.Unmarshal([]byte(closed), &result); err != nil {
		return nil
	}
	return result
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

// PriorComment represents a comment from a previous review that was not yet
// resolved. Used during incremental reviews to give the LLM awareness of what
// was previously flagged so it can avoid duplicates and verify fixes.
type PriorComment struct {
	FilePath string
	Line     int
	EndLine  int
	Body     string
	Severity string
	Category string
}

// SastFinding represents a single finding from a SAST tool.
type SastFinding struct {
	File     string
	Line     int
	Rule     string
	Message  string
	Severity string
}

// Arch context thresholds: file becomes a choke point when at least this many
// other files depend on it, and a hotspot when at least this many historical
// bugs have been flagged on it.
const (
	ArchChokePointFanIn = 5
	ArchHotspotBugCount = 3
)

// ArchContextEntry carries architecture metrics for a single file used in review prompts.
type ArchContextEntry struct {
	FanIn    int
	BugCount int
}

// IsChokePoint reports whether the file has enough inbound dependencies to
// warrant extra scrutiny during review.
func (a ArchContextEntry) IsChokePoint() bool { return a.FanIn >= ArchChokePointFanIn }

// IsHotspot reports whether the file has accumulated enough historical bug
// findings to warrant extra scrutiny during review.
func (a ArchContextEntry) IsHotspot() bool { return a.BugCount >= ArchHotspotBugCount }

// --- Issue acceptance + cross-PR verification ---

// IssueLink describes a GitHub issue that a PR claims to close or reference.
// Populated by the pipeline via GraphQL closingIssuesReferences (primary) or
// PR body regex (fallback for non-closing mentions like "refs #N").
type IssueLink struct {
	Owner      string
	Repo       string
	Number     int
	URL        string
	Title      string   // populated after GetIssue fetch
	Body       string   // populated after GetIssue fetch
	Criteria   []string // extracted from body via extractCriteria
	Accessible bool
	FetchError string
}

// AcceptanceStatus classifies a single criterion's verdict. Shared by
// per-PR AcceptanceCriterion and joint JointAcceptanceCriterion so both
// paths use the same closed vocabulary. JointVerdict below is a strictly
// narrower rollup (no "ambiguous") that mirrors the joint judge's spec.
type AcceptanceStatus string

const (
	AcceptanceStatusAddressed   AcceptanceStatus = "addressed"
	AcceptanceStatusPartial     AcceptanceStatus = "partial"
	AcceptanceStatusUnaddressed AcceptanceStatus = "unaddressed"
	AcceptanceStatusAmbiguous   AcceptanceStatus = "ambiguous"
)

// ValidAcceptanceStatuses is the closed set of AcceptanceStatus values.
var ValidAcceptanceStatuses = map[AcceptanceStatus]struct{}{
	AcceptanceStatusAddressed:   {},
	AcceptanceStatusPartial:     {},
	AcceptanceStatusUnaddressed: {},
	AcceptanceStatusAmbiguous:   {},
}

// Valid reports whether s is in the ValidAcceptanceStatuses set.
func (s AcceptanceStatus) Valid() bool {
	_, ok := ValidAcceptanceStatuses[s]
	return ok
}

// JointVerdict is the rollup verdict for the joint acceptance judge.
// Narrower than AcceptanceStatus: the prompt disallows "ambiguous" so the
// rollup always lands on one of three buckets.
type JointVerdict string

const (
	JointVerdictAddressed   JointVerdict = "addressed"
	JointVerdictPartial     JointVerdict = "partial"
	JointVerdictUnaddressed JointVerdict = "unaddressed"
)

// ValidJointVerdicts is the closed set of JointVerdict values.
var ValidJointVerdicts = map[JointVerdict]struct{}{
	JointVerdictAddressed:   {},
	JointVerdictPartial:     {},
	JointVerdictUnaddressed: {},
}

// Valid reports whether v is in the ValidJointVerdicts set.
func (v JointVerdict) Valid() bool {
	_, ok := ValidJointVerdicts[v]
	return ok
}

// AcceptanceCriterion is one judged criterion from a linked issue.
type AcceptanceCriterion struct {
	Text     string
	Status   AcceptanceStatus
	Reason   string
	Evidence string // e.g. "internal/auth/login.go:42"
}

// AcceptanceResult is the per-issue judgment rolled up from its criteria.
// Verdict reuses AcceptanceStatus (not JointVerdict) because the per-PR
// rollup can legitimately land on "ambiguous" when the LLM has insufficient
// signal — that's a valid terminal state for the single-PR path.
type AcceptanceResult struct {
	IssueNumber int
	IssueTitle  string
	IssueURL    string
	Criteria    []AcceptanceCriterion
	Verdict     AcceptanceStatus // rollup over Criteria
}

// JointAcceptanceCriterion is one criterion judged against the COMBINED
// change across 2+ linked PRs sharing the same issue. AddressedBy points
// at whichever sibling most directly satisfies it; empty string means no
// sibling addresses the criterion.
type JointAcceptanceCriterion struct {
	Text        string           `json:"text"`
	AddressedBy string           `json:"addressed_by"` // "owner/repo#N" or "" if unaddressed
	Evidence    string           `json:"evidence"`     // "path:line" or "" if unaddressed
	Status      AcceptanceStatus `json:"status"`
}

// JointAcceptanceResult is the joint-judge output for one shared issue.
// Unknown SchemaVersion is treated as an empty result + Warn by the
// parser, never a panic.
type JointAcceptanceResult struct {
	SchemaVersion int                        `json:"schema_version"`
	IssueOwner    string                     `json:"issue_owner"`
	IssueRepo     string                     `json:"issue_repo"`
	IssueNumber   int                        `json:"issue_number"`
	IssueTitle    string                     `json:"issue_title"`
	IssueURL      string                     `json:"issue_url,omitempty"`
	Criteria      []JointAcceptanceCriterion `json:"criteria"`
	Verdict       JointVerdict               `json:"verdict"`
}

// Async stage schema version constants. Envelopes whose schema_version
// doesn't match are treated as empty output + Warn log by the parser
// (never a panic) — see runCrossPRStage and judgeSharedIssue.
const (
	// CrossPRJudgeSchemaV1 is the current crossPRJudgeResponse envelope.
	CrossPRJudgeSchemaV1 = 1
	// JointAcceptanceSchemaV1 is the current JointAcceptanceResult envelope.
	JointAcceptanceSchemaV1 = 1
)

// PriorReviewSnapshot carries prior-review context for one linked PR.
// Nil on PRLink.PriorReview means Argus never produced a completed review
// for the linked PR — callers must fall back to diff-only context. A
// non-nil snapshot with empty Findings means "reviewed, no open findings".
// HeadSHA lets the caller compare against PRLink.HeadSHA to detect
// force-push drift ("reviewed at abc123, now at def456").
//
// ReviewedAt is the completion timestamp from reviews.completed_at. Storing
// the instant (not a pre-computed duration) keeps this struct a pure source-
// of-truth carrier: age drifts while the snapshot sits in memory between
// hydration and rendering, so consumers compute `time.Since(ReviewedAt)` at
// format time.
type PriorReviewSnapshot struct {
	Findings   []Finding `json:"findings,omitempty"`
	ReviewedAt time.Time `json:"reviewed_at,omitempty"`
	HeadSHA    string    `json:"head_sha,omitempty"`
}

// PRLink describes a cross-repo pull request auto-detected from the primary
// PR body. The Diff field is only populated when Accessible is true.
//
// PriorReview collapses the former {Reviewed, PriorFindings, PriorReviewAge,
// PriorReviewHeadSHA} product-type into a single nil-checked snapshot. Nil =
// not reviewed by Argus; non-nil = reviewed (with possibly empty findings).
// Eliminates the class of bugs where callers set Age/HeadSHA without flipping
// Reviewed=true.
type PRLink struct {
	Owner       string
	Repo        string
	Number      int
	URL         string
	Title       string
	HeadSHA     string
	Diff        string
	Accessible  bool
	FetchError  string
	PriorReview *PriorReviewSnapshot `json:"prior_review,omitempty"`
}

// Finding is the cross-PR stage's view of a prior-review finding, built by
// flattening (FileReview.Path + FileComment) from the AllFileReviews JSONB
// payload returned by GetLatestCompletedReviewByPR.
//
// Why a distinct struct, not a type alias for FileComment: FileComment does
// not carry its file path (Path lives on the enclosing FileReview), and the
// cross-PR judge prompt needs path+line per finding. Materializing Finding
// during unmarshal lets the stage carry a flat list and keeps the LLM prompt
// simple.
//
// Field naming is tuned to the on-disk JSONB shape:
//   - FileReview serializes with no json tags, so Path/Comments are capitalized.
//   - FileComment has explicit json tags: line, body, severity, category,
//     specialist, what, confidence.
//
// SourceReviewID is stamped by the cross-PR stage during hydration, not by
// the FileReview unmarshal, so prompts can attribute findings to a specific
// reviewed snapshot.
type Finding struct {
	// Path is lifted from the containing FileReview during unmarshal.
	Path string `json:"path"`
	// Line is the 1-based end-line of the reviewed range (FileComment.Line).
	Line int `json:"line"`
	// Severity carries the FileComment.Severity typed enum. Since Severity is
	// `type Severity string`, the JSON wire shape is identical to an untyped
	// string field — but call sites now get compile-time coverage of the
	// SeverityCritical/Warning/Suggestion/Praise set.
	Severity Severity `json:"severity"`
	// Category carries the FileComment.Category typed enum (security|bug|...).
	Category Category `json:"category"`
	// Specialist names the reviewer (bug_hunter|security|architecture|regression).
	// Empty string when the finding came from a non-specialist single-pass review.
	Specialist Specialist `json:"specialist,omitempty"`
	// Summary is a short description. Populated from FileComment.What when set,
	// otherwise from the first ~240 chars of FileComment.Body.
	Summary string `json:"summary,omitempty"`
	// Detail is the long-form review body (FileComment.Body).
	Detail string `json:"detail,omitempty"`
	// Confidence mirrors FileComment.Confidence when present.
	Confidence string `json:"confidence,omitempty"`
	// SourceReviewID is the reviewID the finding was lifted from. Stamped by
	// the cross-PR stage during hydration; empty when projected from a raw
	// FileReview payload.
	SourceReviewID string `json:"source_review_id,omitempty"`
}

// findingsFromFileReviews flattens [{Path, Comments:[...]}] into []Finding.
// Used by the async cross-PR stage to project the AllFileReviews jsonb
// payload returned by GetLatestCompletedReviewByPR into a prompt-ready shape.
//
// Does NOT filter by auto-resolved source SHAs — the caller layers that
// filter on top, since auto_resolve_events tracks push SHAs not thread IDs
// and per-thread reconciliation requires additional context.
func findingsFromFileReviews(fr []FileReview, sourceReviewID string) []Finding {
	if len(fr) == 0 {
		return nil
	}
	out := make([]Finding, 0, len(fr))
	for _, f := range fr {
		for _, c := range f.Comments {
			summary := c.What
			if summary == "" {
				summary = c.Body
			}
			// UTF-8 safe truncate: byte-slicing a multi-byte rune mid-sequence
			// produces invalid UTF-8 and breaks downstream JSON encoders.
			summary = util.Truncate(summary, 240, false)
			out = append(out, Finding{
				Path:           f.Path,
				Line:           c.Line,
				Severity:       c.Severity,
				Category:       c.Category,
				Specialist:     c.Specialist,
				Summary:        summary,
				Detail:         c.Body,
				Confidence:     c.Confidence,
				SourceReviewID: sourceReviewID,
			})
		}
	}
	return out
}

// CrossPRCoverage aggregates the combination-risk judgment across all linked
// PRs. IsCompatible derives from CombinationRisks so writer sites cannot drift
// out of sync with the risk list. Incompatibilities is the pre-rendered
// projection of CombinationRisks (description + " (" + linked_pr + ")") used
// by formatCrossPRCoverageSection.
type CrossPRCoverage struct {
	LinkedPRs         []PRLink
	Incompatibilities []string
	CombinationRisks  []CombinationRisk
	AccessibleCount   int
	InaccessibleCount int
}

// IsCompatible returns true when no combination risks were surfaced.
// Derived from CombinationRisks so a writer cannot produce an inconsistent
// state where the flag and the risk list disagree.
func (c *CrossPRCoverage) IsCompatible() bool {
	return c != nil && len(c.CombinationRisks) == 0
}

// RiskCategory classifies a cross-PR combination risk. The nine buckets
// mirror crossPRCombinationRiskPrompt — any LLM output outside this set
// is dropped (with Warn) rather than passed through, so a drifting judge
// can't smuggle an unmappable category into downstream renderers.
type RiskCategory string

const (
	RiskCategorySchemaRace            RiskCategory = "schema_race"
	RiskCategorySerializationContract RiskCategory = "serialization_contract"
	RiskCategoryTypeDrift             RiskCategory = "type_drift"
	RiskCategoryConfigContradiction   RiskCategory = "config_contradiction"
	RiskCategoryDeployOrdering        RiskCategory = "deploy_ordering"
	RiskCategorySecurityPosture       RiskCategory = "security_posture"
	RiskCategoryEnumExhaustiveness    RiskCategory = "enum_exhaustiveness"
	RiskCategoryLocaleTemporal        RiskCategory = "locale_temporal"
	RiskCategoryPropagatedFinding     RiskCategory = "propagated_finding"
)

// ValidRiskCategories is the closed set of RiskCategory values.
var ValidRiskCategories = map[RiskCategory]struct{}{
	RiskCategorySchemaRace:            {},
	RiskCategorySerializationContract: {},
	RiskCategoryTypeDrift:             {},
	RiskCategoryConfigContradiction:   {},
	RiskCategoryDeployOrdering:        {},
	RiskCategorySecurityPosture:       {},
	RiskCategoryEnumExhaustiveness:    {},
	RiskCategoryLocaleTemporal:        {},
	RiskCategoryPropagatedFinding:     {},
}

// Valid reports whether c is in the ValidRiskCategories set.
func (c RiskCategory) Valid() bool {
	_, ok := ValidRiskCategories[c]
	return ok
}

// RiskSeverity is the severity bucket a cross-PR combination risk lands in.
// Unknown values from the LLM normalize to Medium (see normalizeRiskSeverity)
// — a safe default that keeps the risk visible without over-weighting it.
type RiskSeverity string

const (
	RiskSeverityLow    RiskSeverity = "low"
	RiskSeverityMedium RiskSeverity = "medium"
	RiskSeverityHigh   RiskSeverity = "high"
)

// ValidRiskSeverities is the closed set of RiskSeverity values.
var ValidRiskSeverities = map[RiskSeverity]struct{}{
	RiskSeverityLow:    {},
	RiskSeverityMedium: {},
	RiskSeverityHigh:   {},
}

// Valid reports whether s is in the ValidRiskSeverities set.
func (s RiskSeverity) Valid() bool {
	_, ok := ValidRiskSeverities[s]
	return ok
}

// normalizeRiskSeverity lowercases + trims raw and maps it to a RiskSeverity.
// Unknown values fall through to RiskSeverityMedium so a drifted LLM contract
// keeps the risk visible at a middle-of-the-road severity (same safe-default
// pattern as normalizeJointVerdict's fallback).
//
// A non-empty, non-canonical input (e.g. "CRITICAL" / "blocker") is logged at
// Warn so a silent severity downgrade can't hide a regression in the judge
// prompt or provider. An empty input is the documented "absent" shape and is
// intentionally silent.
func normalizeRiskSeverity(raw string, logger *slog.Logger) RiskSeverity {
	s := strings.ToLower(strings.TrimSpace(raw))
	switch RiskSeverity(s) {
	case RiskSeverityLow, RiskSeverityMedium, RiskSeverityHigh:
		return RiskSeverity(s)
	}
	if raw != "" && logger != nil {
		logger.Warn("[crosspr] unknown risk severity — defaulting to medium", "raw", raw)
	}
	return RiskSeverityMedium
}

// CombinationRisk is one entry in the cross-PR judge output. Category is one
// of the nine enumerated buckets (see crossPRCombinationRiskPrompt); severity
// is low|medium|high. LinkedPR is the "owner/repo#N" key of the sibling PR
// implicated in the risk.
type CombinationRisk struct {
	Category     RiskCategory `json:"category"`
	Description  string       `json:"description"`
	LinkedPR     string       `json:"linked_pr"`     // owner/repo#N
	PrimaryFiles []string     `json:"primary_files"` // path:line entries
	Severity     RiskSeverity `json:"severity"`
}

// FeatureFlags captures per-installation feature gates loaded once per run.
type FeatureFlags struct {
	CrossPRChecks   bool `json:"cross_pr_checks"`
	IssueAcceptance bool `json:"issue_acceptance"`
	MaxLinkedPRs    int  `json:"max_linked_prs"`
}

// DefaultFeatureFlags returns the backfill defaults for new installations:
// issue acceptance on, cross-PR on, 5 linked PR cap.
//
// CrossPRChecks default true (was false pre-039); existing installations
// whose feature_flags JSON explicitly records false are preserved by the
// loader (see loadFeatureFlags in acceptance.go). Only installations with
// an empty feature_flags blob or a missing cross_pr_checks key flip to on.
func DefaultFeatureFlags() FeatureFlags {
	return FeatureFlags{
		CrossPRChecks:   true,
		IssueAcceptance: true,
		MaxLinkedPRs:    5,
	}
}
