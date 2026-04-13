package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// DecisionTrace represents a single decision trace entry.
type DecisionTrace struct {
	ID         int64          `json:"id"`
	RepoID     int64          `json:"repo_id"`
	FilePath   string         `json:"file_path"`
	SymbolName string         `json:"symbol_name,omitempty"`
	TraceType  string         `json:"trace_type"`
	Content    string         `json:"content"`
	Severity   string         `json:"severity,omitempty"`
	ReviewID   *uuid.UUID     `json:"review_id,omitempty"`
	PRNumber   int            `json:"pr_number,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
}

// FileRisk represents a file's risk score based on trace density.
type FileRisk struct {
	FilePath   string    `json:"file_path"`
	TraceCount int       `json:"trace_count"`
	LastTrace  time.Time `json:"last_trace"`
}

// CreateTrace inserts a new decision trace.
func (s *Store) CreateTrace(ctx context.Context, repoID int64, filePath string, symbolName string, traceType string, content string, severity string, reviewID *uuid.UUID, prNumber int, metadata map[string]any) error {
	metaJSON, err := json.Marshal(metadata)
	if err != nil {
		metaJSON = []byte("{}")
	}
	_, err = s.Pool.Exec(ctx,
		`INSERT INTO decision_traces (repo_id, file_path, symbol_name, trace_type, content, severity, review_id, pr_number, metadata)
		 VALUES ($1, $2, NULLIF($3, ''), $4, $5, NULLIF($6, ''), $7, $8, $9)`,
		repoID, filePath, symbolName, traceType, content, severity, reviewID, prNumber, metaJSON)
	return err
}

// ListTracesForFiles returns recent traces for given files, ordered by created_at DESC.
func (s *Store) ListTracesForFiles(ctx context.Context, repoID int64, filePaths []string, limit int) ([]DecisionTrace, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.Pool.Query(ctx,
		`SELECT id, repo_id, file_path, COALESCE(symbol_name, ''), trace_type, content, COALESCE(severity, ''), review_id, COALESCE(pr_number, 0), COALESCE(metadata, '{}'), created_at
		 FROM decision_traces
		 WHERE repo_id = $1 AND file_path = ANY($2)
		 ORDER BY created_at DESC
		 LIMIT $3`, repoID, filePaths, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectOrEmpty(rows, scanTrace)
}

// ListTracesForRepo returns the most recent traces across the repo.
func (s *Store) ListTracesForRepo(ctx context.Context, repoID int64, limit int) ([]DecisionTrace, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.Pool.Query(ctx,
		`SELECT id, repo_id, file_path, COALESCE(symbol_name, ''), trace_type, content, COALESCE(severity, ''), review_id, COALESCE(pr_number, 0), COALESCE(metadata, '{}'), created_at
		 FROM decision_traces
		 WHERE repo_id = $1
		 ORDER BY created_at DESC
		 LIMIT $2`, repoID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectOrEmpty(rows, scanTrace)
}

// GetFileRiskScore returns the weighted trace count for a file over the last 90 days.
// Weights: critical=5, warning=3, suggestion=1, other=1.
func (s *Store) GetFileRiskScore(ctx context.Context, repoID int64, filePath string) (int, error) {
	var score int
	err := s.Pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(
			CASE severity
				WHEN 'critical' THEN 5
				WHEN 'warning' THEN 3
				WHEN 'suggestion' THEN 1
				ELSE 1
			END
		), 0)::int
		 FROM decision_traces
		 WHERE repo_id = $1 AND file_path = $2 AND created_at > NOW() - INTERVAL '90 days'`, repoID, filePath).Scan(&score)
	return score, err
}

// GetHotFiles returns files with the most traces, indicating fragility.
func (s *Store) GetHotFiles(ctx context.Context, repoID int64, limit int) ([]FileRisk, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.Pool.Query(ctx,
		`SELECT file_path, COUNT(*)::int AS trace_count, MAX(created_at) AS last_trace
		 FROM decision_traces
		 WHERE repo_id = $1 AND created_at > NOW() - INTERVAL '90 days'
		 GROUP BY file_path
		 ORDER BY trace_count DESC
		 LIMIT $2`, repoID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectOrEmpty(rows, pgx.RowToStructByPos[FileRisk])
}

// scanTrace scans a decision_traces row into a DecisionTrace struct.
func scanTrace(row pgx.CollectableRow) (DecisionTrace, error) {
	var t DecisionTrace
	var metaJSON []byte
	err := row.Scan(&t.ID, &t.RepoID, &t.FilePath, &t.SymbolName, &t.TraceType, &t.Content, &t.Severity, &t.ReviewID, &t.PRNumber, &metaJSON, &t.CreatedAt)
	if err != nil {
		return t, err
	}
	if len(metaJSON) > 0 {
		_ = json.Unmarshal(metaJSON, &t.Metadata)
	}
	return t, nil
}
