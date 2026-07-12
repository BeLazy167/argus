// Package pipeline — enricher.go owns the per-finding memory-enrichment pass
// lifted out of the orchestrator: pattern/rule linking, novelty gating, and
// dismissal-suppression decisions over a set of FileReviews. It reaches memory
// ONLY through the error-honest reader seam (memory.Indexer.Search) and the
// patterns table ONLY through the narrow PatternLinker, so the whole pass is a
// DB-less table test away. The orchestrator call site shrinks to: build deps →
// Enricher.Run → apply (merge suppression keys, log + publish the aggregate).
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/util"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// enrichConcurrency bounds the per-finding fan-out: at most this many
// Supermemory reads run at once so a large review can't stampede the API.
const enrichConcurrency = 5

// Enricher annotates each finding with pattern/rule matches, a novelty flag,
// and dismissal-suppression decisions. It owns the per-finding fan-out, the
// self-match guard, and the suppression bookkeeping.
//
// Non-fatal by contract: a pattern- OR rule-search error leaves a finding's
// novelty UNSET (never novel-on-error, preserving the #128/#147 semantics), and
// a dismissal-search error degrades to no suppression. Construct it with the
// memory reader seam, a narrow PatternLinker, the resolved thresholds, and the
// run coordinates the per-finding decisions and their observability need.
type Enricher struct {
	reader     memory.Indexer
	linker     PatternLinker
	logger     *slog.Logger
	thresholds memory.Thresholds

	repo         string    // repo short name — drives the container tags
	repoFullName string    // owner/repo — for the suppression log
	prNumber     int       // for the suppression log
	reviewID     uuid.UUID // for the suppression log
	repoID       int64     // DB repo id for auto-suppressed categories; 0 => skip
	changeClass  string    // contract change class for the dismissal lifecycle filter
	traceID      string    // for the goroutine-panic event

	// concurrency bounds the per-finding fan-out; <=0 falls back to enrichConcurrency.
	concurrency int
	// publish emits a pipeline event (EventMemoryMatched); nil = no event bus.
	publish func(evt EventType, data map[string]any)
}

// EnrichResult is the aggregate outcome of a Run: the per-category counters the
// orchestrator logs + publishes, plus the suppression keys of dropped findings
// so pattern-learning (which reads the pre-enrich snapshot without the
// Suppressed flag) can skip them and never re-learn a dropped finding.
type EnrichResult struct {
	Matched        int
	Enforced       int
	Novel          int
	Suppressed     int
	Downgraded     int
	SuppressedKeys map[string]struct{}
}

// Total is matched+enforced+novel — the "N findings annotated" summary count.
func (r EnrichResult) Total() int { return r.Matched + r.Enforced + r.Novel }

// Run enriches every comment in reviews IN PLACE, fanning out per finding under
// the concurrency bound. It fetches the repo's auto-suppressed categories once
// (memory-gated on repoID), then for each finding runs the pattern + rule reads
// (errors gate novelty), applies the self-match guard, links + counts a pattern
// hit, records rule attribution, decides dismissal drop/downgrade, and publishes
// EventMemoryMatched. Returns the aggregate counters + suppression keys; the
// caller applies them.
func (e *Enricher) Run(ctx context.Context, reviews []FileReview) EnrichResult {
	// Suppression-v2 input shared by every goroutine (read-only): the categories
	// this repo auto-suppressed via consecutive-ignore streaks.
	autoSuppressed := map[string]bool{}
	if e.repoID != 0 {
		if m, err := e.linker.GetAutoSuppressedCategories(ctx, e.repoID); err != nil {
			e.logger.Warn("auto-suppressed categories lookup", "error", err, "repo_id", e.repoID)
		} else {
			autoSuppressed = m
		}
	}

	bound := e.concurrency
	if bound <= 0 {
		bound = enrichConcurrency
	}
	sem := make(chan struct{}, bound)
	var wg sync.WaitGroup

	for i := range reviews {
		fr := &reviews[i]
		for j := range fr.Comments {
			c := &fr.Comments[j]
			wg.Add(1)
			sem <- struct{}{}
			go func(c *FileComment, filePath string) {
				defer func() {
					if r := recover(); r != nil {
						e.logger.Error("enrichFindings goroutine panic", "recover", r, "file", filePath)
						emitPipelinePanicEvent(ctx, e.logger, "enrich_findings", r, e.traceID)
					}
				}()
				defer wg.Done()
				defer func() { <-sem }()

				e.enrichComment(ctx, c, filePath, autoSuppressed)
			}(c, fr.Path)
		}
	}
	wg.Wait()

	return e.tally(reviews)
}

// enrichComment runs the whole per-finding decision for a single comment,
// mutating it in place. This is the body of the fan-out in Run: pattern + rule
// reads, the self-match guard, pattern linking + stats, rule attribution,
// novelty, then dismissal suppression.
func (e *Enricher) enrichComment(ctx context.Context, c *FileComment, filePath string, autoSuppressed map[string]bool) {
	// Build a richer query: category + file + body gives Supermemory more semantic signal.
	query := fmt.Sprintf("[%s|%s] %s:%d %s", c.Severity, c.Category, filePath, c.Line, c.Body)

	// Pattern enrichment: best type=pattern match across repo + shared. Errors
	// PROPAGATE — a broken/timed-out search must never mark a finding novel, so
	// patErr gates the novel branch below rather than degrading to a zero match.
	// Top-1 shaping across the two containers is the pure BestMatch adapter.
	patternMatches, patErr := e.reader.Search(ctx, memory.MemoryQuery{
		Query: query, Repo: e.repo, Scope: memory.ScopeBoth, Type: memory.TypePattern,
		Limit: 1, Threshold: e.thresholds.FindingEnrich, Rerank: true,
	})
	match := memory.BestMatch(patternMatches...)

	// Rules live in the shared container under type=rule metadata. A rule-search
	// error must NOT be conflated with "no rule matched" (the #128 gate gap);
	// ruleErr joins the novelty gate below. TopContent shapes the top-1 rule
	// body to 300 chars.
	ruleMatches, ruleErr := e.reader.Search(ctx, memory.MemoryQuery{
		Query: query, Scope: memory.ScopeShared, Type: memory.TypeRule,
		Limit: 1, Threshold: e.thresholds.FindingEnrich,
	})
	ruleContent := memory.TopContent(ruleMatches, 300)

	// Novelty is decidable only when BOTH reads SUCCEEDED — a genuine no-match.
	// Either error leaves IsNewFinding unset. Log each failure at Warn so prod
	// still distinguishes empty-vs-error per call site.
	searchOK := patErr == nil && ruleErr == nil
	if patErr != nil {
		e.logger.Warn("memory search failed",
			"caller", "pattern-enrich", "file", filePath, "line", c.Line, "query_len", len(query), "error", patErr)
	}
	if ruleErr != nil {
		e.logger.Warn("memory search failed",
			"caller", "rule-enrich", "file", filePath, "line", c.Line, "query_len", len(query), "error", ruleErr)
	}

	// Self-match guard: a near-identical hit is this code's own prior review
	// comment (re-review noise), not a learned pattern. Zero it so it neither
	// persists, attributes, nor bumps stats.
	score := match.Score
	if score > 0 && match.Content != "" &&
		wordOverlap(strings.ToLower(match.Content), strings.ToLower(c.Body)) > 0.7 {
		score = 0
	}

	// Persist best-match pattern id + score for every hit at/above the
	// FindingEnrich floor. Public footer attribution stays gated at the
	// Attribution threshold below. The doc→pattern-id lookup can miss (synthesis
	// docs never mirror to the patterns table); a miss skips the FK and
	// pattern-stats without failing.
	if score > 0 {
		patternID, found := e.resolvePatternID(ctx, match)
		linkMatchedPattern(c, patternID, found, score)
		if found {
			if serr := e.linker.IncrementPatternMatch(ctx, patternID); serr != nil {
				e.logger.Warn("increment pattern match", "error", serr, "pattern_id", patternID)
			}
		}
	}

	if score > e.thresholds.Attribution {
		c.MatchedPatternKind = inferMatchKind(match.Metadata)
		// New-shape docs flatten via Metadata.ToMap, which emits the PR number
		// under "pr_number".
		c.MatchedPatternPR = metaInt(match.Metadata, "pr_number")
		c.MatchedPatternAuthor = match.Metadata["pr_author"]
		c.MatchedPatternAgeDays = metaAgeDays(match.Metadata, "created_at")
		e.logger.Debug("pattern match found",
			"file", filePath, "line", c.Line,
			"score", fmt.Sprintf("%.3f", score),
			"kind", c.MatchedPatternKind,
			"source_pr", c.MatchedPatternPR,
			"pattern_prefix", util.Truncate(match.Content, 80, true))
		e.publishEvent(EventMemoryMatched, map[string]any{
			"file":  filePath,
			"line":  c.Line,
			"kind":  c.MatchedPatternKind,
			"pr":    c.MatchedPatternPR,
			"score": score,
		})
	}
	if ruleContent != "" {
		c.EnforcedRuleContent = ruleContent
		// Rule-kind trumps pattern: author intent beats past behaviour when both match.
		if c.MatchedPatternKind == "" {
			c.MatchedPatternKind = "rule"
		}
		e.logger.Debug("rule enforced", "file", filePath, "line", c.Line, "rule_prefix", util.Truncate(ruleContent, 80, true))
		e.publishEvent(EventMemoryMatched, map[string]any{
			"file":  filePath,
			"line":  c.Line,
			"kind":  "rule",
			"pr":    c.MatchedPatternPR,
			"score": score,
		})
	}
	// Novel = SUCCESSFUL empty pattern search (score cleared to 0) and no rule.
	// score>0 means a match at/above the FindingEnrich floor, so a 0.50–0.80 hit
	// is a match, not a new finding. searchOK gates the branch: a failed/timed-out
	// search leaves IsNewFinding unset rather than conflating "search broke" with
	// "no prior match".
	if searchOK && score == 0 && ruleContent == "" {
		c.IsNewFinding = true
	}

	// Dismissal suppression v2: retrieve the top dismissed-feedback matches (query
	// by body — the dismissal doc content IS the finding text), lifecycle-filter
	// by change kind, then decide drop (single exact match ≥ SuppressionDrop, a
	// team-feedback streak of similar dismissals, or a category the repo
	// auto-suppressed) vs downgrade (≥ SuppressionDowngrade). Security and Law-12
	// permanent checks are exempt from drops — memory may downgrade, never silence
	// them. Non-fatal; memory-gated.
	dismissals := memory.BestEffort(e.logger, "dismissal", memory.RepoTagNew(e.repo), len(c.Body),
		func() ([]memory.PatternMatch, error) {
			return dismissalSearch(ctx, e.reader, e.repo, c.Body, e.thresholds.FindingEnrich)
		})
	eval := evaluateDismissals(dismissals, e.changeClass,
		suppressionExempt(c.Category, c.Body), autoSuppressed[string(c.Category)], e.thresholds)
	switch applyDismissalEvaluation(c, eval) {
	case dismissalDrop:
		e.logger.InfoContext(ctx, "memory suppressed finding",
			slog.String("event", "memory.suppressed"),
			slog.String("review_id", e.reviewID.String()),
			slog.String("repo", e.repoFullName),
			slog.Int("pr_number", e.prNumber),
			slog.Int("line", c.Line),
			slog.String("reason", eval.reason),
			slog.Int("similar_dismissals", eval.similarCount),
			slog.Float64("score", eval.bestScore))
	case dismissalDowngrade:
		e.logger.Debug("memory downgraded finding",
			"file", filePath, "line", c.Line,
			"score", fmt.Sprintf("%.3f", eval.bestScore),
			"similar_dismissals", eval.similarCount,
			"new_severity", c.Severity)
	}
}

// tally computes the aggregate counters over the enriched reviews and records a
// snapshot-stable suppression key for every dropped finding.
func (e *Enricher) tally(reviews []FileReview) EnrichResult {
	var res EnrichResult
	for _, fr := range reviews {
		for _, c := range fr.Comments {
			if c.MatchedPatternScore > 0 {
				res.Matched++
			}
			if c.EnforcedRuleContent != "" {
				res.Enforced++
			}
			if c.IsNewFinding {
				res.Novel++
			}
			if c.Suppressed {
				res.Suppressed++
				if res.SuppressedKeys == nil {
					res.SuppressedKeys = make(map[string]struct{})
				}
				res.SuppressedKeys[suppressionKey(fr.Path, c.Line, c.Body)] = struct{}{}
			}
			if c.DismissedDowngrade {
				res.Downgraded++
			}
		}
	}
	return res
}

// publishEvent fires a pipeline event when an event bus is wired; a no-op
// otherwise so the Enricher runs the same bus-less in tests and disabled runs.
func (e *Enricher) publishEvent(evt EventType, data map[string]any) {
	if e.publish != nil {
		e.publish(evt, data)
	}
}

// resolvePatternID maps a Supermemory pattern search hit back to its
// patterns-table row id. It PREFERS the deterministic customId (round-tripped
// through result metadata under "custom_id") because a hybrid-search hit's own
// ID may be a chunk id that never equals the stored supermemory_id; it falls
// back to matching match.ID against patterns.supermemory_id for legacy rows
// written before the customId mirror existed. Returns found=false (a non-fatal
// miss — e.g. a synthesis/convention doc never mirrored to the patterns table)
// when neither key resolves.
func (e *Enricher) resolvePatternID(ctx context.Context, match memory.PatternMatch) (int64, bool) {
	if customID := match.Metadata["custom_id"]; customID != "" {
		if pid, err := e.linker.GetPatternIDByCustomID(ctx, customID); err == nil {
			return pid, true
		} else if !errors.Is(err, pgx.ErrNoRows) {
			e.logger.Warn("pattern id lookup by custom_id", "error", err, "custom_id", customID)
		}
		// customId miss (legacy row / not mirrored) — fall through to the
		// supermemory_id match on the result's own id.
	}
	if pid, err := e.linker.GetPatternIDBySupermemoryID(ctx, match.ID); err == nil {
		return pid, true
	} else if !errors.Is(err, pgx.ErrNoRows) {
		e.logger.Warn("pattern id lookup", "error", err, "supermemory_id", match.ID)
	}
	return 0, false
}
