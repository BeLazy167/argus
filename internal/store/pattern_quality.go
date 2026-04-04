package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

type PatternQualityStats struct {
	ID             int64      `json:"id"`
	InstallationID int64      `json:"installation_id"`
	RepoID         *int64     `json:"repo_id,omitempty"`
	SupermemoryID  string     `json:"supermemory_id"`
	ContentHash    string     `json:"content_hash"`
	Category       string     `json:"category"`
	TimesMatched   int        `json:"times_matched"`
	TimesConfirmed int        `json:"times_confirmed"`
	TimesDismissed int        `json:"times_dismissed"`
	QualityScore   float64    `json:"quality_score"`
	LastMatchedAt  *time.Time `json:"last_matched_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type PatternHealthStats struct {
	PatternsLearned   int        `json:"patterns_learned"`
	ReviewsProcessed  int        `json:"reviews_processed"`
	LearningRate      float64    `json:"learning_rate"`
	LastLearnedAt     *time.Time `json:"last_learned_at,omitempty"`
	StalePatternCount int        `json:"stale_pattern_count"`
}

// RecalculateQuality uses a Bayesian formula with prior=0.5, weight=5.
func RecalculateQuality(confirmed, dismissed int) float64 {
	return (float64(confirmed) + 2.5) / (float64(confirmed) + float64(dismissed) + 5.0)
}

func (s *Store) UpsertPatternStats(ctx context.Context, stats PatternQualityStats) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO pattern_stats (installation_id, repo_id, supermemory_id, content_hash, category, times_matched, times_confirmed, times_dismissed, quality_score, last_matched_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (supermemory_id) DO UPDATE SET
			repo_id         = EXCLUDED.repo_id,
			content_hash    = EXCLUDED.content_hash,
			category        = EXCLUDED.category,
			times_matched   = EXCLUDED.times_matched,
			times_confirmed = EXCLUDED.times_confirmed,
			times_dismissed = EXCLUDED.times_dismissed,
			quality_score   = EXCLUDED.quality_score,
			last_matched_at = EXCLUDED.last_matched_at,
			updated_at      = NOW()
	`, stats.InstallationID, stats.RepoID, stats.SupermemoryID, stats.ContentHash, stats.Category,
		stats.TimesMatched, stats.TimesConfirmed, stats.TimesDismissed, stats.QualityScore, stats.LastMatchedAt)
	return err
}

func (s *Store) IncrementPatternMatch(ctx context.Context, supermemoryID string, confirmed bool) error {
	var confirmInc, dismissInc int
	if confirmed {
		confirmInc = 1
	} else {
		dismissInc = 1
	}
	_, err := s.Pool.Exec(ctx, `
		UPDATE pattern_stats SET
			times_matched   = times_matched + 1,
			times_confirmed = times_confirmed + $2,
			times_dismissed = times_dismissed + $3,
			quality_score   = (times_confirmed + $2 + 2.5) / (times_confirmed + $2 + times_dismissed + $3 + 5.0),
			last_matched_at = NOW(),
			updated_at      = NOW()
		WHERE supermemory_id = $1
	`, supermemoryID, confirmInc, dismissInc)
	return err
}

func (s *Store) GetPatternHealthStats(ctx context.Context, installationID int64, since time.Time) (PatternHealthStats, error) {
	var stats PatternHealthStats

	// Patterns learned in the window
	err := s.Pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM pattern_stats
		WHERE installation_id = $1 AND created_at >= $2
	`, installationID, since).Scan(&stats.PatternsLearned)
	if err != nil {
		return stats, err
	}

	// Reviews processed in the window
	err = s.Pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM reviews
		WHERE installation_id = $1 AND created_at >= $2
	`, installationID, since).Scan(&stats.ReviewsProcessed)
	if err != nil {
		return stats, err
	}

	if stats.ReviewsProcessed > 0 {
		stats.LearningRate = float64(stats.PatternsLearned) / float64(stats.ReviewsProcessed)
	}

	// Last learned timestamp
	err = s.Pool.QueryRow(ctx, `
		SELECT MAX(created_at) FROM pattern_stats WHERE installation_id = $1
	`, installationID).Scan(&stats.LastLearnedAt)
	if err != nil {
		return stats, err
	}

	// Stale patterns (not matched in 90 days)
	err = s.Pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM pattern_stats
		WHERE installation_id = $1
		  AND (last_matched_at IS NULL OR last_matched_at < NOW() - INTERVAL '90 days')
	`, installationID).Scan(&stats.StalePatternCount)
	if err != nil {
		return stats, err
	}

	return stats, nil
}

// DecayStalePatterns deletes pattern_stats rows where last_matched_at is older than staleAfter
// and quality_score is below minQuality. Returns count of deleted rows.
func (s *Store) DecayStalePatterns(ctx context.Context, installationID int64, staleAfter time.Duration, minQuality float64) (int, error) {
	cutoff := time.Now().Add(-staleAfter)
	tag, err := s.Pool.Exec(ctx, `
		DELETE FROM pattern_stats
		WHERE installation_id = $1
		  AND quality_score < $2
		  AND COALESCE(last_matched_at, created_at) < $3
	`, installationID, minQuality, cutoff)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

func (s *Store) GetLowQualityPatterns(ctx context.Context, installationID int64, maxQuality float64, limit int) ([]PatternQualityStats, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, installation_id, repo_id, supermemory_id, content_hash, category,
		       times_matched, times_confirmed, times_dismissed, quality_score,
		       last_matched_at, created_at, updated_at
		FROM pattern_stats
		WHERE installation_id = $1 AND quality_score <= $2
		ORDER BY quality_score ASC
		LIMIT $3
	`, installationID, maxQuality, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectOrEmpty(rows, pgx.RowToStructByPos[PatternQualityStats])
}
