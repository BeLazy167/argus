package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/BeLazy167/argus/backend/internal/store/db"
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
	return s.q.CreateTrace(ctx, db.CreateTraceParams{RepoID: repoID, FilePath: filePath, SymbolName: symbolName, TraceType: traceType, Content: content, Severity: severity, ReviewID: reviewID, PRNumber: &prNumber, Metadata: metaJSON})
}

// ListTracesForFiles returns recent traces for given files, ordered by created_at DESC.
func (s *Store) ListTracesForFiles(ctx context.Context, repoID int64, filePaths []string, limit int) ([]DecisionTrace, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.q.ListTracesForFiles(ctx, db.ListTracesForFilesParams{RepoID: repoID, Column2: filePaths, RowLimit: int64(limit)})
	if err != nil {
		return nil, err
	}
	traces := make([]DecisionTrace, 0, len(rows))
	for _, row := range rows {
		trace, err := decisionTraceFromValues(row.ID, row.RepoID, row.FilePath, row.SymbolName, row.TraceType, row.Content, row.Severity, row.ReviewID, row.PRNumber, row.Metadata, row.CreatedAt)
		if err != nil {
			return nil, err
		}
		traces = append(traces, trace)
	}
	return traces, nil
}

// ListTracesForRepo returns the most recent traces across the repo.
func (s *Store) ListTracesForRepo(ctx context.Context, repoID int64, limit int) ([]DecisionTrace, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.q.ListTracesForRepo(ctx, db.ListTracesForRepoParams{RepoID: repoID, RowLimit: int64(limit)})
	if err != nil {
		return nil, err
	}
	traces := make([]DecisionTrace, 0, len(rows))
	for _, row := range rows {
		trace, err := decisionTraceFromValues(row.ID, row.RepoID, row.FilePath, row.SymbolName, row.TraceType, row.Content, row.Severity, row.ReviewID, row.PRNumber, row.Metadata, row.CreatedAt)
		if err != nil {
			return nil, err
		}
		traces = append(traces, trace)
	}
	return traces, nil
}

// GetFileRiskScore returns the weighted trace count for a file over the last 90 days.
// Weights: critical=5, warning=3, suggestion=1, other=1.
func (s *Store) GetFileRiskScore(ctx context.Context, repoID int64, filePath string) (int, error) {
	return s.q.GetFileRiskScore(ctx, db.GetFileRiskScoreParams{RepoID: repoID, FilePath: filePath})
}

// GetHotFiles returns files with the most traces, indicating fragility.
func (s *Store) GetHotFiles(ctx context.Context, repoID int64, limit int) ([]FileRisk, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.q.GetHotFiles(ctx, db.GetHotFilesParams{RepoID: repoID, RowLimit: int64(limit)})
	if err != nil {
		return nil, err
	}
	files := make([]FileRisk, 0, len(rows))
	for _, row := range rows {
		files = append(files, FileRisk{FilePath: row.FilePath, TraceCount: row.TraceCount, LastTrace: row.LastTrace})
	}
	return files, nil
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
