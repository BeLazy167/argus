package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// LearnedMemoryExcerptChars caps the excerpt each memory row contributes to a
// review-detail response. Memory content is derived from private source code,
// so the surface returns a preview, not the document: a file synthesis runs to
// several thousand characters and a review that wrote fifty of them would put
// a small copy of the repository on the wire for a page that shows three lines
// per row.
//
// Applied with Postgres left(), which counts CHARACTERS, so a multi-byte
// excerpt is never cut mid-rune.
const LearnedMemoryExcerptChars = 280

// learnedMemoryLimitMax bounds the row count regardless of what a caller asks
// for. The list is a preview beside the accurate per-type counts, so an
// unbounded caller-supplied limit buys nothing and costs a response.
const learnedMemoryLimitMax = 50

// reviewMemoryPredicates is the ONE tenant + attribution clause both reads
// share. Lifecycle filtering belongs to the live_memories database view, the
// same relation search, re-embedding, and archive reconciliation read.
//
// installationID is not redundant with reviewID even though a review id is
// globally unique. It is the tenancy predicate: a caller that passes someone
// else's review id gets zero rows instead of that tenant's private content.
const reviewMemoryPredicates = `m.installation_id = $1 AND a.review_id = $2`

// learnedNoun maps a memory type to its (singular, plural) display nouns.
//
// This is the ONE table. Both surfaces that describe a review's memory writes —
// the posted PR comment footnote and the dashboard "What Argus learned" panel —
// render the noun this table produces, so the two cannot describe the same
// review differently. The dashboard gets it as the `label` field on the wire
// rather than keeping a second copy in TypeScript; a second copy is a thing that
// must agree, and the pair drifted exactly that way before this existed.
//
// Explicit pairs, not a pluralize() rule: "PR summary" pluralizes to "PR
// summaries", and a generic +"s" prints "PR summarys" on the comment every
// developer reads.
var learnedNoun = map[string][2]string{
	"pattern":    {"pattern", "patterns"},
	"pr_summary": {"PR summary", "PR summaries"},
	"synthesis":  {"file memory", "file memories"},
	"scenario":   {"scenario", "scenarios"},
	"topology":   {"architecture note", "architecture notes"},
	"review":     {"finding memory", "finding memories"},
	"feedback":   {"feedback signal", "feedback signals"},
	"rule":       {"rule", "rules"},
}

// LearnedMemoryLabel returns the display noun for count rows of memory type typ:
// the singular for exactly one, the plural otherwise.
//
// An unmapped type returns the raw type for both forms. A new backend memory
// type is still real knowledge, so it degrades to an honest label rather than
// being dropped from the tally and under-reporting the write.
func LearnedMemoryLabel(typ string, count int) string {
	nouns, ok := learnedNoun[typ]
	if !ok {
		return typ
	}
	if count == 1 {
		return nouns[0]
	}
	return nouns[1]
}

// ListReviewMemories returns what one review wrote into memory, newest first,
// as truncated excerpts. Scoped to the installation: a review id belonging to
// another tenant returns no rows, not that tenant's memories.
//
// limit is clamped to learnedMemoryLimitMax; a non-positive limit means the
// maximum. Returns an empty slice (never nil) when the review wrote nothing —
// which is itself the answer the review page needs to show.
func (s *Store) ListReviewMemories(ctx context.Context, installationID int64, reviewID uuid.UUID, limit int) ([]LearnedMemory, error) {
	if limit <= 0 || limit > learnedMemoryLimitMax {
		limit = learnedMemoryLimitMax
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT m.type, m.container_tag, left(m.content, $3), a.attributed_at
		FROM memory_review_attributions a
		JOIN live_memories m ON m.id = a.memory_id
		WHERE `+reviewMemoryPredicates+`
		ORDER BY a.attributed_at DESC, m.id DESC
		LIMIT $4
	`, installationID, reviewID, LearnedMemoryExcerptChars, limit)
	if err != nil {
		return nil, fmt.Errorf("listing review memories: %w", err)
	}
	defer rows.Close()
	return collectOrEmpty(rows, func(row pgx.CollectableRow) (LearnedMemory, error) {
		var m LearnedMemory
		if err := row.Scan(&m.Type, &m.ContainerTag, &m.Excerpt, &m.WrittenAt); err != nil {
			return LearnedMemory{}, err
		}
		// One row is one memory, so the entry always reads as the singular.
		m.Label = LearnedMemoryLabel(m.Type, 1)
		return m, nil
	})
}

// CountReviewMemoriesByType returns the per-type tally of what one review
// wrote, ordered by count then type so the rendering is stable. Unlike
// ListReviewMemories it is NOT capped, so the counts stay correct for a review
// that wrote more rows than the preview list shows. Same tenant scoping.
func (s *Store) CountReviewMemoriesByType(ctx context.Context, installationID int64, reviewID uuid.UUID) ([]LearnedMemoryCount, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT m.type, COUNT(*)::int
		FROM memory_review_attributions a
		JOIN live_memories m ON m.id = a.memory_id
		WHERE `+reviewMemoryPredicates+`
		GROUP BY m.type
		ORDER BY COUNT(*) DESC, m.type
	`, installationID, reviewID)
	if err != nil {
		return nil, fmt.Errorf("counting review memories: %w", err)
	}
	defer rows.Close()
	return collectOrEmpty(rows, func(row pgx.CollectableRow) (LearnedMemoryCount, error) {
		var c LearnedMemoryCount
		if err := row.Scan(&c.Type, &c.Count); err != nil {
			return LearnedMemoryCount{}, err
		}
		c.Label = LearnedMemoryLabel(c.Type, c.Count)
		return c, nil
	})
}
