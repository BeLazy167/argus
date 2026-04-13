package store

import (
	"context"

	"github.com/jackc/pgx/v5"
)

const scenarioCols = `id, installation_id, repo_id, description, source, COALESCE(source_ref,''), files, modules, COALESCE(severity,'medium'), active, created_at, COALESCE(steps,'[]'), COALESCE(initial_state,''), COALESCE(expected_outcome,''), COALESCE(is_outdated,FALSE), last_run_at`

func (s *Store) CreateScenario(ctx context.Context, installationID int64, repoID *int64, description, source, sourceRef string, files, modules []string, severity string) (int64, error) {
	var id int64
	err := s.Pool.QueryRow(ctx,
		`INSERT INTO scenarios (installation_id, repo_id, description, source, source_ref, files, modules, severity)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 RETURNING id`,
		installationID, repoID, description, source, sourceRef, files, modules, severity).
		Scan(&id)
	return id, err
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
	return id, err
}

// ActivateScenario sets a pending scenario to active (after dev approval).
func (s *Store) ActivateScenario(ctx context.Context, id int64) error {
	_, err := s.Pool.Exec(ctx, `UPDATE scenarios SET active = TRUE WHERE id = $1`, id)
	return err
}

// ListScenariosForFiles returns active scenarios whose files array overlaps with the given paths.
func (s *Store) ListScenariosForFiles(ctx context.Context, repoID int64, filePaths []string) ([]Scenario, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT `+scenarioCols+`
		 FROM scenarios
		 WHERE repo_id = $1 AND active = TRUE AND files && $2::text[]
		 ORDER BY created_at DESC
		 LIMIT 20`,
		repoID, filePaths)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectOrEmpty(rows, pgx.RowToStructByPos[Scenario])
}

// ListScenariosForRepo returns all scenarios for a repo (active + pending).
func (s *Store) ListScenariosForRepo(ctx context.Context, repoID int64, limit int) ([]Scenario, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT `+scenarioCols+`
		 FROM scenarios
		 WHERE repo_id = $1
		 ORDER BY active DESC, created_at DESC
		 LIMIT $2`,
		repoID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectOrEmpty(rows, pgx.RowToStructByPos[Scenario])
}

func (s *Store) GetScenario(ctx context.Context, id int64) (*Scenario, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+scenarioCols+` FROM scenarios WHERE id = $1`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sc, err := pgx.CollectOneRow(rows, pgx.RowToStructByPos[Scenario])
	if err != nil {
		return nil, err
	}
	return &sc, nil
}

// DeactivateScenario soft-deletes a scenario by setting active = false.
func (s *Store) DeactivateScenario(ctx context.Context, id int64) error {
	_, err := s.Pool.Exec(ctx, `UPDATE scenarios SET active = FALSE WHERE id = $1`, id)
	return err
}

// DeactivateScenarioScoped soft-deletes a scenario only if it belongs to one of the given installations.
func (s *Store) DeactivateScenarioScoped(ctx context.Context, id int64, installationIDs []int64) error {
	tag, err := s.Pool.Exec(ctx,
		`UPDATE scenarios SET active = FALSE WHERE id = $1 AND installation_id = ANY($2)`,
		id, installationIDs)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// MarkScenarioOutdated marks scenarios as outdated if their files overlap with changed paths.
func (s *Store) MarkScenarioOutdated(ctx context.Context, repoID int64, filePaths []string) error {
	_, err := s.Pool.Exec(ctx,
		`UPDATE scenarios SET is_outdated = TRUE
		 WHERE repo_id = $1 AND active = TRUE AND files && $2::text[]`,
		repoID, filePaths)
	return err
}

// UpdateScenarioLastRun records a scenario run and clears the outdated flag.
func (s *Store) UpdateScenarioLastRun(ctx context.Context, id int64) error {
	_, err := s.Pool.Exec(ctx, `UPDATE scenarios SET last_run_at = NOW(), is_outdated = FALSE WHERE id = $1`, id)
	return err
}
