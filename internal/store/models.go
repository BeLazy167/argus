package store

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type Installation struct {
	ID             int64      `json:"id"`
	InstallationID int64      `json:"installation_id"`
	OrgLogin       string     `json:"org_login"`
	ClerkOrgID     *string    `json:"clerk_org_id,omitempty"`
	PlanTier       string     `json:"plan_tier"`
	CreatedAt      time.Time  `json:"created_at"`
	SuspendedAt    *time.Time `json:"suspended_at,omitempty"`
}

type Repo struct {
	ID             int64     `json:"id"`
	InstallationID int64     `json:"installation_id"`
	GithubID       int64     `json:"github_id"`
	FullName       string    `json:"full_name"`
	DefaultBranch  string    `json:"default_branch"`
	Enabled        bool      `json:"enabled"`
	SettingsJSON   json.RawMessage `json:"settings_json"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type Review struct {
	ID             uuid.UUID        `json:"id"`
	RepoID         int64            `json:"repo_id"`
	PRNumber       int              `json:"pr_number"`
	PRTitle        string           `json:"pr_title"`
	PRAuthor       string           `json:"pr_author"`
	HeadSHA        string           `json:"head_sha"`
	BaseSHA        string           `json:"base_sha"`
	HeadRef        string           `json:"head_ref"`
	GithubReviewID *int64           `json:"github_review_id,omitempty"`
	Status         string           `json:"status"`
	Summary        *string          `json:"summary,omitempty"`
	Score          *int             `json:"score,omitempty"`
	TokenUsage     *json.RawMessage `json:"token_usage,omitempty"`
	Trigger        string           `json:"trigger"`
	TriggeredBy    *string          `json:"triggered_by,omitempty"`
	DurationMs     *int             `json:"duration_ms,omitempty"`
	Error          *string          `json:"error,omitempty"`
	DeepReview     bool             `json:"deep_review"`
	Persona        *string          `json:"persona,omitempty"`
	IsIncremental  bool             `json:"is_incremental"`
	CreatedAt      time.Time        `json:"created_at"`
	CompletedAt    *time.Time       `json:"completed_at,omitempty"`
}

type ReviewComment struct {
	ID              uuid.UUID `json:"id"`
	ReviewID        uuid.UUID `json:"review_id"`
	FilePath        string    `json:"file_path"`
	StartLine       *int      `json:"start_line,omitempty"`
	EndLine         *int      `json:"end_line,omitempty"`
	Side            *string   `json:"side,omitempty"`
	Body            string    `json:"body"`
	Severity        *string   `json:"severity,omitempty"`
	Category        *string   `json:"category,omitempty"`
	Specialist      *string   `json:"specialist,omitempty"`
	ConfidenceScore *int      `json:"confidence_score,omitempty"`
	CodeSnippet     *string   `json:"code_snippet,omitempty"`
	GithubCommentID     *int64    `json:"github_comment_id,omitempty"`
	MatchedPatternID    *int64    `json:"matched_pattern_id,omitempty"`
	MatchedPatternScore *float32  `json:"matched_pattern_score,omitempty"`
	EnforcedRuleContent *string   `json:"enforced_rule_content,omitempty"`
	IsNewFinding        bool      `json:"is_new_finding"`
	CreatedAt           time.Time `json:"created_at"`
}

type Rule struct {
	ID        int64     `json:"id"`
	Category  string    `json:"category"`
	Content   string    `json:"content"`
	Priority  int       `json:"priority"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ActivityLog struct {
	ID        int64     `json:"id"`
	Action    string    `json:"action"`
	Actor     *string   `json:"actor,omitempty"`
	Resource  *string   `json:"resource,omitempty"`
	Metadata  []byte    `json:"metadata,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type ModelConfig struct {
	ID          int64     `json:"id"`
	RepoID      *int64    `json:"repo_id,omitempty"`
	Stage       string    `json:"stage"`
	Provider    string    `json:"provider"`
	Model       string    `json:"model"`
	BaseURL     *string   `json:"base_url,omitempty"`
	MaxTokens   int       `json:"max_tokens"`
	Temperature float32   `json:"temperature"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type ProviderKey struct {
	ID             int64     `json:"id"`
	InstallationID int64     `json:"installation_id"`
	RepoID         *int64    `json:"repo_id,omitempty"`
	Provider       string    `json:"provider"`
	APIKeyEnc      string    `json:"-"`
	BaseURL        *string   `json:"base_url,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type UserInstallation struct {
	ID             int64     `json:"id"`
	ClerkUserID    string    `json:"clerk_user_id"`
	InstallationID int64     `json:"installation_id"`
	Role           string    `json:"role"`
	CreatedAt      time.Time `json:"created_at"`
}

type CommentOutcome struct {
	ID              int64     `json:"id"`
	ReviewCommentID uuid.UUID `json:"review_comment_id"`
	Outcome         string    `json:"outcome"`
	CreatedAt       time.Time `json:"created_at"`
}

type PromptTemplate struct {
	ID         int64     `json:"id"`
	RepoID     int64     `json:"repo_id"`
	Stage      string    `json:"stage"`
	PromptText string    `json:"prompt_text"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type ScenarioStep struct {
	Action string `json:"action"`
	Hint   string `json:"hint,omitempty"`
}

type Scenario struct {
	ID              int64          `json:"id"`
	InstallationID  int64          `json:"installation_id"`
	RepoID          *int64         `json:"repo_id,omitempty"`
	Description     string         `json:"description"`
	Source          string         `json:"source"`
	SourceRef       string         `json:"source_ref,omitempty"`
	Files           []string       `json:"files,omitempty"`
	Modules         []string       `json:"modules,omitempty"`
	Severity        string         `json:"severity"`
	Active          bool           `json:"active"`
	CreatedAt       time.Time      `json:"created_at"`
	Steps           []ScenarioStep `json:"steps"`
	InitialState    string         `json:"initial_state"`
	ExpectedOutcome string         `json:"expected_outcome"`
	IsOutdated      bool           `json:"is_outdated"`
	LastRunAt       *time.Time     `json:"last_run_at,omitempty"`
}

type Stats struct {
	TotalReviews    int `json:"total_reviews"`
	CompletedToday  int `json:"completed_today"`
	AvgScore        int `json:"avg_score"`
	ActiveRepos     int `json:"active_repos"`
	CriticalFinds   int `json:"critical_finds"`
	PendingReviews  int `json:"pending_reviews"`
	CatchRate       int `json:"catch_rate"`
	PRsThisWeek     int `json:"prs_this_week"`
	HighRiskCount   int `json:"high_risk_count"`
	AvgReviewTimeMs int `json:"avg_review_time_ms"`
	DeepReviewCount int `json:"deep_review_count"`
}

type CodeNode struct {
	ID        int64  `json:"id"`
	RepoID    int64  `json:"repo_id"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	FilePath  string `json:"file_path"`
	LineStart int    `json:"line_start"`
	LineEnd   int    `json:"line_end"`
	Language  string `json:"language"`
	Depth     int    `json:"depth,omitempty"`
}
