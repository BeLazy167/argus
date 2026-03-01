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
	GithubReviewID *int64           `json:"github_review_id,omitempty"`
	Status         string           `json:"status"`
	Summary        *string          `json:"summary,omitempty"`
	Score          *int             `json:"score,omitempty"`
	TokenUsage     *json.RawMessage `json:"token_usage,omitempty"`
	Trigger        string           `json:"trigger"`
	TriggeredBy    *string          `json:"triggered_by,omitempty"`
	DurationMs     *int             `json:"duration_ms,omitempty"`
	Error          *string          `json:"error,omitempty"`
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
	CodeSnippet     *string   `json:"code_snippet,omitempty"`
	GithubCommentID *int64    `json:"github_comment_id,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
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
}
