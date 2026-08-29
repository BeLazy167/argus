package memory

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/BeLazy167/argus/backend/internal/obs"
	"github.com/BeLazy167/argus/backend/internal/util"
)

// MemoryQuery is the typed request for the deep Search read — the single
// error-honest entry point behind every value-level reader adapter (pattern
// enrich, dismissal suppression, scenario dedup, triage/scoring hints, rule
// lookup, agentic search). It expresses container scope, a single type filter
// plus extra AND conditions, and the retrieval knobs (limit, threshold, rerank,
// enrich); per-call result shaping (top-1, truncation, id parsing) lives in the
// pure adapters, never here.
type MemoryQuery struct {
	Query string
	// Repo is the repo short name; required for ScopeRepo / ScopeBoth. Empty repo
	// on a repo scope is treated as "memory disabled" and returns (nil, nil).
	Repo  string
	Scope ContainerScope
	// Type pins the metadata `type` filter; "" leaves the search untyped.
	Type MemoryType
	// Filters are extra equality/numeric conditions ANDed with Type (e.g.
	// action=dismissed, severity=high, confidence>=floor).
	Filters   []FilterCondition
	Limit     int
	Threshold float64
	// Enrich requests related memories + summaries so each Match carries
	// RichContent (the hint-render path); off keeps the response lean.
	Enrich bool
	// PointLookup declares that Filters UNIQUELY PIN a row, making this a
	// lookup rather than a ranking read — see SearchRequest.PointLookup. Set
	// it only when that is true: it licenses a predicate-scan fallback, and
	// on a ranking read that returns arbitrary rows.
	PointLookup bool
}

// containerTags resolves the query scope to concrete container tags. Returns
// (nil, nil) when the scope needs a repo but none was supplied — the "memory
// off" no-op that keeps callers' disabled-memory behavior — and an error only on
// an unknown scope.
func (q MemoryQuery) containerTags() ([]string, error) {
	switch q.Scope {
	case ScopeShared:
		return []string{SharedTag}, nil
	case ScopeRepo:
		if q.Repo == "" {
			return nil, nil
		}
		return []string{RepoTagNew(q.Repo)}, nil
	case ScopeBoth:
		if q.Repo == "" {
			return nil, nil
		}
		return []string{RepoTagNew(q.Repo), SharedTag}, nil
	default:
		return nil, fmt.Errorf("memory: unknown container scope %q", q.Scope)
	}
}

// request builds the container-agnostic SearchRequest from the query. The caller
// stamps ContainerTag per leg. Type + Filters combine as a single AND group.
func (q MemoryQuery) request() SearchRequest {
	req := SearchRequest{
		Query:       q.Query,
		Limit:       q.Limit,
		Threshold:   q.Threshold,
		PointLookup: q.PointLookup,
	}
	and := make([]FilterCondition, 0, len(q.Filters)+1)
	if q.Type != "" {
		and = append(and, FilterCondition{Key: "type", Value: string(q.Type)})
	}
	and = append(and, q.Filters...)
	if len(and) > 0 {
		req.Filters = &SearchFilters{AND: and}
	}
	if q.Enrich {
		req.Include = &SearchInclude{RelatedMemories: true, Summaries: true}
	}
	return req
}

// runSearchFn is the one transport-shaped hole in the shared read
// orchestration: execute a single-container SearchRequest and convert to
// matches. The Postgres backend satisfies it with memoRunSearch (hybrid SQL),
// and tests satisfy it directly — so container resolution, timeouts, fan-out
// merge, and leg-degradation policy are shared by construction, exactly like
// the write path's Doc builders.
//
// Whether the read wants enriched content is carried by the request itself
// (Include != nil), never as a second argument: a caller that set one and
// forgot the other produced empty RichContent, which HintStrings then
// filters out — a silently blank briefing section with no error anywhere.
type runSearchFn func(ctx context.Context, req SearchRequest) ([]PatternMatch, error)

// searchWith is the shared Search orchestration over a transport core.
func searchWith(ctx context.Context, run runSearchFn, q MemoryQuery) (matches []PatternMatch, err error) {
	operationID := obs.NewLogID()
	started := time.Now()
	logger := slog.Default()
	logger.InfoContext(ctx, "memory search orchestration started",
		"operation_id", operationID, "scope", q.Scope, "repo", q.Repo,
		"memory_type", q.Type, "query_len", len(q.Query), "limit", q.Limit,
		"threshold", q.Threshold, "enrich", q.Enrich, "point_lookup", q.PointLookup,
		"filter_count", len(q.Filters))
	defer func() {
		attrs := []any{"operation_id", operationID, "scope", q.Scope, "memory_type", q.Type,
			"result_count", len(matches), "duration_ms", time.Since(started).Milliseconds(), "error", err}
		if err != nil {
			logger.WarnContext(ctx, "memory search orchestration failed", attrs...)
			return
		}
		logger.InfoContext(ctx, "memory search orchestration completed", attrs...)
	}()

	tags, err := q.containerTags()
	if err != nil {
		return nil, err
	}
	if len(tags) == 0 {
		logger.InfoContext(ctx, "memory search skipped",
			"operation_id", operationID, "reason", "scope_requires_repo", "scope", q.Scope)
		return nil, nil
	}
	logger.DebugContext(ctx, "memory retrieval plan selected",
		"operation_id", operationID, "strategy", map[bool]string{true: "fan_out", false: "single_container"}[len(tags) > 1],
		"container_count", len(tags), "containers", tags)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req := q.request()
	if len(tags) == 1 {
		req.ContainerTag = tags[0]
		matches, err = run(ctx, req)
		beforeFilter := len(matches)
		matches = retrievableMatches(matches)
		logger.DebugContext(ctx, "memory retrieval filtering completed", "operation_id", operationID,
			"container", tags[0], "raw_count", beforeFilter, "retrievable_count", len(matches))
		return matches, err
	}
	matches, err = searchFanOut(ctx, run, req, tags)
	beforeFilter := len(matches)
	matches = retrievableMatches(matches)
	logger.DebugContext(ctx, "memory retrieval filtering completed", "operation_id", operationID,
		"container_count", len(tags), "raw_count", beforeFilter, "retrievable_count", len(matches))
	return matches, err
}

// retrievableMatches quarantines legacy reply-derived learnings. Unauthorized
// legacy rows have SourceLegacyReplyFeedback. Pattern rows with the older
// SourceTrustedReplyFeedback were authorized but incorrectly promoted to the
// installation-wide container. Both remain stored for audit; current trusted
// learnings use their repo-specific source and container. Trusted finding
// feedback keeps the older source and remains retrievable because its type is
// feedback, not pattern.
func retrievableMatches(matches []PatternMatch) []PatternMatch {
	out := matches[:0]
	for _, match := range matches {
		source := match.Metadata["source"]
		legacySharedPattern := match.Metadata["type"] == string(TypePattern) && source == SourceTrustedReplyFeedback
		if source != SourceLegacyReplyFeedback && !legacySharedPattern {
			out = append(out, match)
		}
	}
	return out
}

// searchFanOut runs one search per container concurrently (write-partitioned
// slots; wg.Wait is the happens-before edge) and merges the hits best-first. A
// single leg error fails the whole call — a partial merge would let a broken
// container masquerade as a genuine no-match on the enrich novelty path.
func searchFanOut(ctx context.Context, run runSearchFn, base SearchRequest, tags []string) ([]PatternMatch, error) {
	type legResult struct {
		matches  []PatternMatch
		err      error
		duration time.Duration
	}
	operationID := obs.NewLogID()
	started := time.Now()
	slog.InfoContext(ctx, "memory search fan-out started", "operation_id", operationID,
		"container_count", len(tags), "containers", tags, "query_len", len(base.Query))
	legs := make([]legResult, len(tags))
	var wg sync.WaitGroup
	wg.Add(len(tags))
	for i, tag := range tags {
		go func(i int, tag string) {
			defer wg.Done()
			legStarted := time.Now()
			req := base
			req.ContainerTag = tag
			slog.DebugContext(ctx, "memory search fan-out leg started", "operation_id", operationID, "container", tag)
			m, err := run(ctx, req)
			legs[i] = legResult{matches: m, err: err, duration: time.Since(legStarted)}
		}(i, tag)
	}
	wg.Wait()

	var out []PatternMatch
	for i, leg := range legs {
		attrs := []any{"operation_id", operationID, "container", tags[i], "result_count", len(leg.matches),
			"duration_ms", leg.duration.Milliseconds(), "error", leg.err}
		if leg.err != nil {
			slog.WarnContext(ctx, "memory search fan-out leg failed", attrs...)
			slog.WarnContext(ctx, "memory search fan-out failed", "operation_id", operationID,
				"failed_container", tags[i], "duration_ms", time.Since(started).Milliseconds(), "error", leg.err)
			return nil, leg.err
		}
		slog.DebugContext(ctx, "memory search fan-out leg completed", attrs...)
		out = append(out, leg.matches...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	slog.InfoContext(ctx, "memory search fan-out completed", "operation_id", operationID,
		"container_count", len(tags), "result_count", len(out), "duration_ms", time.Since(started).Milliseconds())
	return out, nil
}

// logSearchResult is the shared per-search Debug line both transport cores
// emit (empty-vs-hit visibility, tagged by container) — one shape, like
// logDegrade below on the degrade path.
func logSearchResult(logger *slog.Logger, req SearchRequest, count int) {
	if logger == nil {
		return
	}
	logger.Debug("memory search",
		"container", req.ContainerTag, "query_len", len(req.Query), "count", count)
}

// logDegrade emits the single canonical "memory read degraded" Warn shared by
// BestEffort (whole-read degradation) and the per-leg degradation sites in the
// shared briefing orchestration, so the paths can never drift in field shape. A nil logger is a no-op, keeping
// bus-less/test paths silent.
func logDegrade(logger *slog.Logger, caller, container string, queryLen int, err error) {
	if logger != nil {
		logger.Warn("memory read degraded",
			"caller", caller, "container", container, "query_len", queryLen, "error", err)
	}
}

// BestEffort degrades a memory read to its zero value on error, logging the
// failure once at Warn with the standard read-failure fields (caller, container,
// query_len) via logDegrade. It is the SINGLE owner of the log-and-degrade
// policy: callers for whom a failed read is a non-fatal omission (briefing
// blocks, triage/scoring hints, dismissal suppression, scenario dedup) wrap the
// read here, while callers that must distinguish failure from empty (enrich
// novelty gating) call the read directly and inspect the error.
func BestEffort[T any](logger *slog.Logger, caller, container string, queryLen int, read func() (T, error)) T {
	v, err := read()
	if err != nil {
		logDegrade(logger, caller, container, queryLen, err)
		var zero T
		return zero
	}
	return v
}

// BestMatch returns the highest-scoring PatternMatch among the candidates (the
// zero match when none score above zero). The top-1 shaping for the pattern
// enrich read after a ScopeBoth fan-out.
func BestMatch(candidates ...PatternMatch) PatternMatch {
	var best PatternMatch
	for _, c := range candidates {
		if c.Score > best.Score {
			best = c
		}
	}
	return best
}

// HintStrings shapes hint matches into the render-ready string list: each hit's
// RichContent (summary + related context), truncated to 500 chars, empties
// dropped. The adapter behind triage/scoring hints and the review-profile
// rules/past-review side-searches.
func HintStrings(matches []PatternMatch) []string {
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if c := util.Truncate(m.RichContent, 500, true); c != "" {
			out = append(out, c)
		}
	}
	return out
}

// TopContent returns the top match's plain content truncated to maxChars, or ""
// when there is no hit. The single-rule shaping behind finding enrichment.
func TopContent(matches []PatternMatch, maxChars int) string {
	for _, m := range matches {
		if c := util.Truncate(m.Content, maxChars, true); c != "" {
			return c
		}
	}
	return ""
}

// ScenarioResults shapes scenario matches into []ScenarioSearchResult, reading
// the scenario id from `metadata.scenario_id`, deduping by id, and capping at
// limit. Matches arrive already sorted by similarity descending; a hit missing
// its id is skipped. Pure shaping — the scenario dedup/trigger adapter.
func ScenarioResults(matches []PatternMatch, limit int) []ScenarioSearchResult {
	seen := map[int64]struct{}{}
	var out []ScenarioSearchResult
	for _, m := range matches {
		id, err := strconv.ParseInt(m.Metadata["scenario_id"], 10, 64)
		if err != nil || id == 0 {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, ScenarioSearchResult{ID: id, Content: m.Content, Similarity: m.Score})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}
