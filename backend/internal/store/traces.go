package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/BeLazy167/argus/backend/internal/store/db"
	"github.com/google/uuid"
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
func (s *Store) CreateTrace(ctx context.Context, repoID int64, filePath string, symbolName string, traceType string, content string, severity string, reviewID *uuid.UUID, prNumber int, metadata map[string]any) (storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx,
			"CreateTrace",
			"repo_id", storeLogValue(repoID), "file_path",
			storeLogValue(filePath),
			"trace_type", storeLogValue(traceType), "severity", storeLogValue(severity), "review_id",
			storeLogValue(reviewID), "pr_number",
			storeLogValue(prNumber))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered)
			panic(recovered)
		}
		storeFinish(storeErr)
	}()

	metaJSON, err := json.Marshal(metadata)
	if err != nil {
		metaJSON = []byte("{}")
	}
	return s.q.CreateTrace(ctx, db.CreateTraceParams{RepoID: repoID, FilePath: filePath, SymbolName: symbolName, TraceType: traceType, Content: content, Severity: severity, ReviewID: reviewID, PRNumber: &prNumber, Metadata: metaJSON})
}

// ListTracesForFiles returns recent traces for given files, ordered by created_at DESC.
func (s *Store) ListTracesForFiles(ctx context.Context, repoID int64, filePaths []string, limit int) (storeResult0 []DecisionTrace, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx,
			"ListTracesForFiles",
			"repo_id", storeLogValue(repoID),
			"file_paths_count", len(filePaths), "limit", storeLogValue(limit))
	defer func() {
		if recovered := recover(); recovered !=
			nil {
			storeFinishPanic(storeFinish,
				recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

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
func (s *Store) ListTracesForRepo(ctx context.Context, repoID int64, limit int) (storeResult0 []DecisionTrace, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx,
			"ListTracesForRepo",
			"repo_id", storeLogValue(repoID), "limit",
			storeLogValue(limit),
		)
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish,
				recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

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
func (s *Store) GetFileRiskScore(ctx context.Context, repoID int64, filePath string) (storeResult0 int, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx,
			"GetFileRiskScore",
			"repo_id", storeLogValue(repoID), "file_path",
			storeLogValue(filePath))
	defer func() {
		if recovered := recover(); recovered !=
			nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr,
			storeResult0)
	}()

	return s.q.GetFileRiskScore(ctx, db.GetFileRiskScoreParams{RepoID: repoID, FilePath: filePath})
}

// GetHotFiles returns files with the most traces, indicating fragility.
func (s *Store) GetHotFiles(ctx context.Context, repoID int64, limit int) (storeResult0 []FileRisk, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx,
			"GetHotFiles",
			"repo_id", storeLogValue(repoID), "limit",
			storeLogValue(limit))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish,
				recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

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
