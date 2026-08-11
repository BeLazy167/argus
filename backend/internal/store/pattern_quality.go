package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type PatternQualityStats struct {
	ID             int64      `json:"id"`
	InstallationID int64      `json:"installation_id"`
	RepoID         *int64     `json:"repo_id,omitempty"`
	MemoryDocID    string     `json:"memory_doc_id"`
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
		INSERT INTO pattern_stats (installation_id, repo_id, memory_doc_id, content_hash, category, times_matched, times_confirmed, times_dismissed, quality_score, last_matched_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (installation_id, memory_doc_id) DO UPDATE SET
			repo_id         = EXCLUDED.repo_id,
			content_hash    = EXCLUDED.content_hash,
			category        = EXCLUDED.category,
			times_matched   = EXCLUDED.times_matched,
			times_confirmed = EXCLUDED.times_confirmed,
			times_dismissed = EXCLUDED.times_dismissed,
			quality_score   = EXCLUDED.quality_score,
			last_matched_at = EXCLUDED.last_matched_at,
			updated_at      = NOW()
	`, stats.InstallationID, stats.RepoID, stats.MemoryDocID, stats.ContentHash, stats.Category,
		stats.TimesMatched, stats.TimesConfirmed, stats.TimesDismissed, stats.QualityScore, stats.LastMatchedAt)
	return err
}

// IncrementPatternMatch records that a learned pattern was matched during a
// review. It self-seeds pattern_stats from the patterns table on first match,
// preferring the deterministic memory_custom_id and falling back to the legacy
// memory_doc_id. This keeps stats working after the outbox repairs a memory
// write that originally failed before memory_doc_id could be recorded. A row
// with neither durable identity returns an error so the enricher logs the
// broken retrieval→resolution→stats chain instead of silently losing the hit.
// The ON CONFLICT update keeps concurrent matches atomic.
func (s *Store) IncrementPatternMatch(ctx context.Context, patternID int64) error {
	tag, err := s.Pool.Exec(ctx, `
		INSERT INTO pattern_stats (installation_id, repo_id, memory_doc_id, content_hash, category, times_matched, quality_score, last_matched_at)
		SELECT p.installation_id, p.repo_id, COALESCE(p.memory_custom_id, p.memory_doc_id), md5(p.content), COALESCE(p.category, ''), 1, 0.5, NOW()
		FROM patterns p
		WHERE p.id = $1 AND COALESCE(p.memory_custom_id, p.memory_doc_id) IS NOT NULL
		ON CONFLICT (installation_id, memory_doc_id) DO UPDATE SET
			times_matched   = pattern_stats.times_matched + 1,
			last_matched_at = NOW(),
			updated_at      = NOW()
	`, patternID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("pattern %d resolved from retrieval but has no durable memory identity", patternID)
	}
	return nil
}

// RecordPatternOutcome applies a developer outcome (confirmed/dismissed) to the
// pattern_stats row behind a matched comment and recomputes quality with the
// same Bayesian formula as RecalculateQuality (prior=0.5, weight=5), atomically.
// patternID is review_comments.matched_pattern_id (a patterns-table id); the
// join maps it to pattern_stats via the shared memory_doc_id. Returns the
// quality AFTER the update and updated=false when no stats row exists yet (a
// match that predates stats wiring, or a pattern never seeded) — non-fatal.
func (s *Store) RecordPatternOutcome(ctx context.Context, patternID int64, confirmed bool) (quality float64, updated bool, err error) {
	var confirmInc, dismissInc int
	if confirmed {
		confirmInc = 1
	} else {
		dismissInc = 1
	}
	err = s.Pool.QueryRow(ctx, `
		UPDATE pattern_stats ps SET
			times_confirmed = ps.times_confirmed + $2,
			times_dismissed = ps.times_dismissed + $3,
			quality_score   = (ps.times_confirmed + $2 + 2.5) / (ps.times_confirmed + $2 + ps.times_dismissed + $3 + 5.0),
			updated_at      = NOW()
		FROM patterns p
		-- installation_id is part of the join, not decoration: memory_doc_id is
		-- a DETERMINISTIC customId under Postgres and PatternCustomID discards
		-- owner, so two installs sharing a repo name hold the SAME id. Joining
		-- on the id alone would apply one tenant's outcome to another's
		-- counters -- the exact leak migration 062's composite UNIQUE closes on
		-- the write path. Both halves must be scoped or neither is.
		WHERE p.id = $1
		  AND ps.memory_doc_id = COALESCE(p.memory_custom_id, p.memory_doc_id)
		  AND ps.installation_id = p.installation_id
		RETURNING ps.quality_score
	`, patternID, confirmInc, dismissInc).Scan(&quality)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return quality, true, nil
}

// CategoryIgnoreStreak is the number of most-recent consecutive negative
// outcomes (dismissed/ignored) after which a whole category is auto-suppressed
// for the repo. Complements the per-pattern Bayesian quality loop above:
// pattern_stats tracks aggregate counters per pattern, while a streak needs
// event ordering — comment_outcomes provides it.
const CategoryIgnoreStreak = 3

// GetAutoSuppressedCategories returns the finding categories whose last
// CategoryIgnoreStreak outcomes in this repo were ALL negative (dismissed or
// ignored) — i.e. the team has consistently rejected the category's findings.
// A category with fewer than CategoryIgnoreStreak recorded outcomes never
// qualifies; a single confirmed outcome resets the streak by construction.
func (s *Store) GetAutoSuppressedCategories(ctx context.Context, repoID int64) (map[string]bool, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT category FROM (
			SELECT rc.category, co.outcome,
			       ROW_NUMBER() OVER (PARTITION BY rc.category ORDER BY co.created_at DESC) AS rn
			FROM comment_outcomes co
			JOIN review_comments rc ON co.review_comment_id = rc.id
			JOIN reviews rv ON rc.review_id = rv.id
			WHERE rv.repo_id = $1 AND rc.category IS NOT NULL AND rc.category <> ''
		) recent
		WHERE rn <= $2
		GROUP BY category
		HAVING COUNT(*) = $2
		   AND COUNT(*) FILTER (WHERE outcome IN ('dismissed','ignored')) = $2
	`, repoID, CategoryIgnoreStreak)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]bool)
	for rows.Next() {
		var cat string
		if err := rows.Scan(&cat); err != nil {
			return nil, err
		}
		out[cat] = true
	}
	return out, rows.Err()
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
		SELECT id, installation_id, repo_id, memory_doc_id, content_hash, category,
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
