package api

import (
	"github.com/BeLazy167/argus/backend/internal/store"
	"github.com/BeLazy167/argus/backend/internal/store/db"
)

// This file holds the typed JSON response envelopes returned by the API
// handlers. They replace anonymous map[string]any payloads so the wire shape
// is declared once, in one place, and the compiler guards every field.

// GaugeResponse wraps GET /api/v1/stats/gauge.
type GaugeResponse struct {
	Gauge []store.GaugeRow `json:"gauge"`
}

// GraphResponse wraps GET /api/v1/repos/{repoID}/graph.
type GraphResponse struct {
	Nodes []db.ListGraphNodesRow `json:"nodes"`
	Edges []db.ListGraphEdgesRow `json:"edges"`
}

// SyncReposResponse wraps POST /api/v1/installations/{id}/sync.
type SyncReposResponse struct {
	Synced int `json:"synced"`
}

// ReviewDetailResponse wraps GET /api/v1/reviews/{reviewID}.
type ReviewDetailResponse struct {
	Review   *store.Review         `json:"review"`
	Comments []store.ReviewComment `json:"comments"`
}

// ReviewExportResponse is the JSON body of the review export download.
type ReviewExportResponse struct {
	ReviewID      string          `json:"review_id"`
	PRNumber      int             `json:"pr_number"`
	PRTitle       string          `json:"pr_title"`
	Score         *int            `json:"score"`
	Status        string          `json:"status"`
	TotalFindings int             `json:"total_findings"`
	Findings      []ExportFinding `json:"findings"`
}

// ExportFinding is one finding row in the review export payload.
type ExportFinding struct {
	File       string `json:"file"`
	Line       int    `json:"line,omitempty"`
	Priority   string `json:"priority"`
	Confidence int    `json:"confidence,omitempty"`
	Category   string `json:"category,omitempty"`
	Severity   string `json:"severity,omitempty"`
	Body       string `json:"body"`
	Specialist string `json:"specialist,omitempty"`
	// Dropped = generated but filtered out (dedup/scoring) before posting.
	Dropped bool `json:"dropped,omitempty"`
	// Folded = persisted + posted, but as a summary-body bullet rather than an
	// inline GitHub comment (because the target line was outside the PR diff).
	// Distinguished from Dropped: the author still sees the finding, but not
	// as a resolvable inline thread.
	Folded bool `json:"folded,omitempty"`
}

// ProviderKeyResponse is the masked provider-key shape returned by the list and
// upsert endpoints. The raw key is never serialized — only its last-4 hint.
type ProviderKeyResponse struct {
	ID             int64   `json:"id"`
	InstallationID int64   `json:"installation_id"`
	RepoID         *int64  `json:"repo_id,omitempty"`
	Provider       string  `json:"provider"`
	APIKeyMasked   string  `json:"api_key_masked"`
	BaseURL        *string `json:"base_url,omitempty"`
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      string  `json:"updated_at"`
}

// newProviderKeyResponse masks a stored key for the wire.
func newProviderKeyResponse(k store.ProviderKey) ProviderKeyResponse {
	return ProviderKeyResponse{
		ID:             k.ID,
		InstallationID: k.InstallationID,
		RepoID:         k.RepoID,
		Provider:       k.Provider,
		APIKeyMasked:   maskKey(k.KeyHint),
		BaseURL:        k.BaseURL,
		CreatedAt:      k.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:      k.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	}
}

// ConfigTestResponse wraps the model-config connectivity test. Error is set on
// failure; Response/Tokens on success.
type ConfigTestResponse struct {
	Success   bool   `json:"success"`
	Error     string `json:"error,omitempty"`
	Response  string `json:"response"`
	LatencyMs int64  `json:"latency_ms"`
	Tokens    int    `json:"tokens"`
}

// StatsOverviewResponse wraps GET /api/v1/stats/overview.
type StatsOverviewResponse struct {
	TotalReviews  int     `json:"total_reviews"`
	TotalCost     float64 `json:"total_cost"`
	AvgScore      float64 `json:"avg_score"`
	AvgReviewSecs int     `json:"avg_review_secs"`
	TotalTokens   int     `json:"total_tokens"`
	CriticalFinds int     `json:"critical_finds"`
	CatchRate     float64 `json:"catch_rate"`
	// Automated hygiene (auto-resolve).
	AutoResolveEvents   int `json:"auto_resolve_events"`
	AutoResolves        int `json:"auto_resolves"`
	AutoResolveAttempts int `json:"auto_resolve_attempts"`
	AutoResolveAPICalls int `json:"auto_resolve_api_calls"`
	// Learn layer — rows the memory/learn path produced this period.
	PatternsLearned int `json:"patterns_learned"`
	ScenariosStored int `json:"scenarios_stored"`
	DecisionTraces  int `json:"decision_traces"`
	FeedbackIndexed int `json:"feedback_indexed"`
}

// SeverityCount / CategoryCount are the buckets of the findings breakdown.
type SeverityCount struct {
	Severity string `json:"severity"`
	Count    int    `json:"count"`
}

type CategoryCount struct {
	Category string `json:"category"`
	Count    int    `json:"count"`
}

// FindingsBreakdownResponse wraps GET /api/v1/stats/findings.
type FindingsBreakdownResponse struct {
	BySeverity     []SeverityCount `json:"by_severity"`
	ByCategory     []CategoryCount `json:"by_category"`
	NewFindings    int             `json:"new_findings"`
	PatternMatches int             `json:"pattern_matches"`
}

// AdoptionResponse wraps GET /api/v1/stats/adoption.
type AdoptionResponse struct {
	DeepReviewPct     float64 `json:"deep_review_pct"`
	IncrementalPct    float64 `json:"incremental_pct"`
	AvgFilesPerReview float64 `json:"avg_files_per_review"`
	ActiveRepos       int     `json:"active_repos"`
	TotalEnabledRepos int     `json:"total_enabled_repos"`
	TotalRepos        int     `json:"total_repos"`
}

// LatencyPercentilesResponse wraps GET /api/v1/stats/review-times.
type LatencyPercentilesResponse struct {
	Count int `json:"count"`
	P50   int `json:"p50"`
	P75   int `json:"p75"`
	P95   int `json:"p95"`
}
