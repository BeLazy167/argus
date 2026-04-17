package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	"github.com/BeLazy167/argus/backend/internal/store/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// CreateScenario inserts a new active scenario and returns its id.
func (s *Store) CreateScenario(ctx context.Context, installationID int64, repoID *int64, description, source, sourceRef string, files, modules []string, severity string) (int64, error) {
	var id int64
	err := s.Pool.QueryRow(ctx,
		`INSERT INTO scenarios (installation_id, repo_id, description, source, source_ref, files, modules, severity)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 RETURNING id`,
		installationID, repoID, description, source, sourceRef, files, modules, severity).
		Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("create scenario: %w", err)
	}
	return id, nil
}

// CreatePendingScenario stores a scenario as inactive (pending dev approval).
func (s *Store) CreatePendingScenario(ctx context.Context, installationID int64, repoID *int64, description, source, sourceRef string, files, modules []string, severity string) (int64, error) {
	var id int64
	err := s.Pool.QueryRow(ctx,
		`INSERT INTO scenarios (installation_id, repo_id, description, source, source_ref, files, modules, severity, active)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, FALSE)
		 RETURNING id`,
		installationID, repoID, description, source, sourceRef, files, modules, severity).
		Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("create pending scenario: %w", err)
	}
	return id, nil
}

// ActivateScenario sets a pending scenario to active (after dev approval).
func (s *Store) ActivateScenario(ctx context.Context, id int64) error {
	if _, err := s.Pool.Exec(ctx, `UPDATE scenarios SET active = TRUE WHERE id = $1`, id); err != nil {
		return fmt.Errorf("activate scenario: %w", err)
	}
	return nil
}

// ListScenariosForFiles returns active scenarios whose files array overlaps with the given paths.
// Ordered by trigger_count DESC so the most-relevant (most-triggered) scenarios surface first.
func (s *Store) ListScenariosForFiles(ctx context.Context, repoID int64, filePaths []string) ([]Scenario, error) {
	rows, err := s.Q.ListScenariosForFiles(ctx, db.ListScenariosForFilesParams{
		RepoID:  &repoID,
		Column2: filePaths,
	})
	if err != nil {
		return nil, fmt.Errorf("list scenarios for files: %w", err)
	}
	out := make([]Scenario, len(rows))
	for i, r := range rows {
		out[i] = scenarioFromListFilesRow(r)
	}
	return out, nil
}

// ListScenariosForRepo returns all scenarios for a repo (active + pending).
func (s *Store) ListScenariosForRepo(ctx context.Context, repoID int64, limit int) ([]Scenario, error) {
	rows, err := s.Q.ListScenariosForRepo(ctx, db.ListScenariosForRepoParams{
		RepoID: &repoID,
		Limit:  int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list scenarios for repo: %w", err)
	}
	out := make([]Scenario, len(rows))
	for i, r := range rows {
		out[i] = scenarioFromListRepoRow(r)
	}
	return out, nil
}

// GetScenario loads a single scenario by id.
func (s *Store) GetScenario(ctx context.Context, id int64) (*Scenario, error) {
	// Single-row query kept inline — no sqlc binding needed.
	var sc Scenario
	var stepsRaw []byte
	var lastVerdict, lastWhy, lastFix *string
	var lastConfidence *float64
	var lastPR *int
	var lastReview *uuid.UUID
	err := s.Pool.QueryRow(ctx,
		`SELECT id, installation_id, repo_id, description, source,
		        COALESCE(source_ref,''), files, modules,
		        COALESCE(severity,'medium'), active, created_at,
		        COALESCE(steps,'[]'), COALESCE(initial_state,''), COALESCE(expected_outcome,''),
		        COALESCE(is_outdated,FALSE), last_run_at,
		        last_verdict, last_confidence::float8, last_why, last_fix,
		        last_pr_number, last_review_id, trigger_count
		 FROM scenarios WHERE id = $1`, id).Scan(
		&sc.ID, &sc.InstallationID, &sc.RepoID, &sc.Description, &sc.Source,
		&sc.SourceRef, &sc.Files, &sc.Modules,
		&sc.Severity, &sc.Active, &sc.CreatedAt,
		&stepsRaw, &sc.InitialState, &sc.ExpectedOutcome,
		&sc.IsOutdated, &sc.LastRunAt,
		&lastVerdict, &lastConfidence, &lastWhy, &lastFix,
		&lastPR, &lastReview, &sc.TriggerCount,
	)
	if err != nil {
		// pgx.ErrNoRows propagates unwrapped so callers can match it; any other error is wrapped.
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, pgx.ErrNoRows
		}
		return nil, fmt.Errorf("get scenario: %w", err)
	}
	if len(stepsRaw) > 0 {
		if err := json.Unmarshal(stepsRaw, &sc.Steps); err != nil {
			slog.Warn("parse scenario steps", "scenario_id", sc.ID, "error", err)
		}
	}
	sc.LastVerdict = ScenarioVerdict(derefString(lastVerdict))
	sc.LastConfidence = lastConfidence
	sc.LastWhy = derefString(lastWhy)
	sc.LastFix = derefString(lastFix)
	sc.LastPRNumber = lastPR
	sc.LastReviewID = lastReview
	return &sc, nil
}

// DeactivateScenario soft-deletes a scenario by setting active = false.
func (s *Store) DeactivateScenario(ctx context.Context, id int64) error {
	if _, err := s.Pool.Exec(ctx, `UPDATE scenarios SET active = FALSE WHERE id = $1`, id); err != nil {
		return fmt.Errorf("deactivate scenario: %w", err)
	}
	return nil
}

// DeactivateScenarioScoped soft-deletes a scenario only if it belongs to one of the given installations.
func (s *Store) DeactivateScenarioScoped(ctx context.Context, id int64, installationIDs []int64) error {
	tag, err := s.Pool.Exec(ctx,
		`UPDATE scenarios SET active = FALSE WHERE id = $1 AND installation_id = ANY($2)`,
		id, installationIDs)
	if err != nil {
		return fmt.Errorf("deactivate scenario scoped: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// MarkScenarioOutdated marks scenarios as outdated if their files overlap with changed paths.
func (s *Store) MarkScenarioOutdated(ctx context.Context, repoID int64, filePaths []string) error {
	if _, err := s.Pool.Exec(ctx,
		`UPDATE scenarios SET is_outdated = TRUE
		 WHERE repo_id = $1 AND active = TRUE AND files && $2::text[]`,
		repoID, filePaths); err != nil {
		return fmt.Errorf("mark scenario outdated: %w", err)
	}
	return nil
}

// UpdateScenarioLastRun writes the denormalized last-run summary onto the scenario row and
// clears the outdated flag. Called once per scenario per review, alongside CreateScenarioRun.
func (s *Store) UpdateScenarioLastRun(ctx context.Context, id int64, verdict string, confidence float64, why, fix string, prNumber int, reviewID uuid.UUID) error {
	if err := s.Q.UpdateScenarioLastRun(ctx, db.UpdateScenarioLastRunParams{
		ID:             id,
		LastVerdict:    strPtr(verdict),
		LastConfidence: numericFromFloat(confidence),
		LastWhy:        strPtr(why),
		LastFix:        strPtr(fix),
		LastPrNumber:   intPtr(prNumber),
		LastReviewID:   &reviewID,
	}); err != nil {
		return fmt.Errorf("update scenario last run: %w", err)
	}
	return nil
}

// IncrementScenarioTriggerCount bumps the trigger counter. Safe to call after any sim outcome
// (both "broken" and "fixed" count as triggers — the scenario was exercised).
func (s *Store) IncrementScenarioTriggerCount(ctx context.Context, id int64) error {
	if err := s.Q.IncrementScenarioTriggerCount(ctx, id); err != nil {
		return fmt.Errorf("increment scenario trigger count: %w", err)
	}
	return nil
}

// CreateScenarioRun persists one simulation outcome. Idempotent on (scenario_id, review_id) —
// re-running simulation for the same review overwrites the previous verdict.
func (s *Store) CreateScenarioRun(ctx context.Context, scenarioID int64, reviewID uuid.UUID, prNumber int, verdict string, confidence float64, why, fix, rootCause, impact string) (ScenarioRun, error) {
	row, err := s.Q.CreateScenarioRun(ctx, db.CreateScenarioRunParams{
		ScenarioID: scenarioID,
		ReviewID:   reviewID,
		PRNumber:   prNumber,
		Verdict:    verdict,
		Confidence: numericFromFloat(confidence),
		Why:        strPtr(why),
		Fix:        strPtr(fix),
		RootCause:  strPtr(rootCause),
		Impact:     strPtr(impact),
	})
	if err != nil {
		return ScenarioRun{}, fmt.Errorf("create scenario run: %w", err)
	}
	return scenarioRunFromDB(row), nil
}

// GetScenarioRuns returns the last N simulation outcomes for a scenario, newest first.
func (s *Store) GetScenarioRuns(ctx context.Context, scenarioID int64, limit int) ([]ScenarioRun, error) {
	rows, err := s.Q.GetScenarioRuns(ctx, db.GetScenarioRunsParams{
		ScenarioID: scenarioID,
		Limit:      int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("get scenario runs: %w", err)
	}
	out := make([]ScenarioRun, len(rows))
	for i, r := range rows {
		out[i] = scenarioRunFromDB(r)
	}
	return out, nil
}

// GetScenarioKPIs returns the 4 summary counts that power the dashboard KPI cards.
func (s *Store) GetScenarioKPIs(ctx context.Context, repoID int64) (ScenarioKPIs, error) {
	row, err := s.Q.GetScenarioKPIs(ctx, &repoID)
	if err != nil {
		return ScenarioKPIs{}, fmt.Errorf("get scenario kpis: %w", err)
	}
	return ScenarioKPIs{
		Active:         int(row.Active),
		BrokenThisWeek: int(row.BrokenThisWeek),
		FixedThisWeek:  int(row.FixedThisWeek),
		Outdated:       int(row.Outdated),
	}, nil
}

// --- internal conversion helpers ---

func scenarioRunFromDB(r db.ScenarioRun) ScenarioRun {
	return ScenarioRun{
		ID:         r.ID,
		ScenarioID: r.ScenarioID,
		ReviewID:   r.ReviewID,
		PRNumber:   int(r.PRNumber),
		Verdict:    ScenarioVerdict(r.Verdict),
		Confidence: floatFromNumeric(r.Confidence),
		Why:        derefString(r.Why),
		Fix:        derefString(r.Fix),
		RootCause:  derefString(r.RootCause),
		Impact:     derefString(r.Impact),
		CreatedAt:  r.CreatedAt,
	}
}

func scenarioFromListFilesRow(r db.ListScenariosForFilesRow) Scenario {
	return Scenario{
		ID:              r.ID,
		InstallationID:  r.InstallationID,
		RepoID:          r.RepoID,
		Description:     r.Description,
		Source:          r.Source,
		SourceRef:       r.SourceRef,
		Files:           r.Files,
		Modules:         r.Modules,
		Severity:        r.Severity,
		Active:          derefBool(r.Active),
		CreatedAt:       derefTime(r.CreatedAt),
		Steps:           parseSteps(r.Steps),
		InitialState:    r.InitialState,
		ExpectedOutcome: r.ExpectedOutcome,
		IsOutdated:      r.IsOutdated,
		LastRunAt:       r.LastRunAt,
		LastVerdict:     ScenarioVerdict(derefString(r.LastVerdict)),
		LastConfidence:  floatPtrFromNumeric(r.LastConfidence),
		LastWhy:         derefString(r.LastWhy),
		LastFix:         derefString(r.LastFix),
		LastPRNumber:    r.LastPrNumber,
		LastReviewID:    r.LastReviewID,
		TriggerCount:    int(r.TriggerCount),
	}
}

func scenarioFromListRepoRow(r db.ListScenariosForRepoRow) Scenario {
	return Scenario{
		ID:              r.ID,
		InstallationID:  r.InstallationID,
		RepoID:          r.RepoID,
		Description:     r.Description,
		Source:          r.Source,
		SourceRef:       r.SourceRef,
		Files:           r.Files,
		Modules:         r.Modules,
		Severity:        r.Severity,
		Active:          derefBool(r.Active),
		CreatedAt:       derefTime(r.CreatedAt),
		Steps:           parseSteps(r.Steps),
		InitialState:    r.InitialState,
		ExpectedOutcome: r.ExpectedOutcome,
		IsOutdated:      r.IsOutdated,
		LastRunAt:       r.LastRunAt,
		LastVerdict:     ScenarioVerdict(derefString(r.LastVerdict)),
		LastConfidence:  floatPtrFromNumeric(r.LastConfidence),
		LastWhy:         derefString(r.LastWhy),
		LastFix:         derefString(r.LastFix),
		LastPRNumber:    r.LastPrNumber,
		LastReviewID:    r.LastReviewID,
		TriggerCount:    int(r.TriggerCount),
	}
}

// parseSteps decodes the JSONB steps column. Unmarshal failures are logged (not propagated)
// because the list query already returns dozens of rows — one corrupt row must not break the
// whole page. A NULL or missing steps column returns nil.
func parseSteps(raw json.RawMessage) []ScenarioStep {
	if len(raw) == 0 {
		return nil
	}
	var steps []ScenarioStep
	if err := json.Unmarshal(raw, &steps); err != nil {
		slog.Warn("parse scenario steps", "error", err)
	}
	return steps
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func derefBool(p *bool) bool {
	if p == nil {
		return false
	}
	return *p
}

// numericFromFloat builds a pgtype.Numeric backed by a 3-decimal-place integer (matches numeric(4,3)).
func numericFromFloat(f float64) pgtype.Numeric {
	if f != f { // NaN
		return pgtype.Numeric{NaN: true, Valid: true}
	}
	scaled := int64(f*1000 + 0.5)
	return pgtype.Numeric{
		Int:   big.NewInt(scaled),
		Exp:   -3,
		Valid: true,
	}
}

func floatFromNumeric(n pgtype.Numeric) float64 {
	if !n.Valid || n.NaN {
		return 0
	}
	f, _ := n.Float64Value()
	return f.Float64
}

func floatPtrFromNumeric(n pgtype.Numeric) *float64 {
	if !n.Valid || n.NaN || n.Int == nil {
		return nil
	}
	f, _ := n.Float64Value()
	v := f.Float64
	return &v
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func intPtr(i int) *int {
	return &i
}

func derefTime(p *time.Time) time.Time {
	if p == nil {
		return time.Time{}
	}
	return *p
}

