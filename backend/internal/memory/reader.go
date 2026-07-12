package memory

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"sync"
	"time"

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
	Rerank    bool
	// Enrich requests related memories + summaries so each Match carries
	// RichContent (the hint-render path); off keeps the response lean.
	Enrich bool
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
		Query:      q.Query,
		SearchMode: "hybrid",
		Limit:      q.Limit,
		Threshold:  q.Threshold,
		Rerank:     q.Rerank,
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

// Search is the deep, error-honest read behind the memory reader seam. It owns
// container-tag resolution, its own 5s timeout, and the retrieval → convert
// path, returning the raw matches and any search error verbatim so each caller
// decides the policy: propagate (enrich novelty gating must not confuse a broken
// search with a genuine no-match) or degrade via BestEffort (briefing, hints,
// suppression, scenario dedup). Returns (nil, nil) on a disabled indexer.
func (idx *indexerImpl) Search(ctx context.Context, q MemoryQuery) ([]PatternMatch, error) {
	if idx.client == nil {
		return nil, nil
	}
	tags, err := q.containerTags()
	if err != nil {
		return nil, err
	}
	if len(tags) == 0 {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req := q.request()
	if len(tags) == 1 {
		req.ContainerTag = tags[0]
		return idx.runSearch(ctx, req, q.Enrich)
	}
	return idx.searchFanOut(ctx, req, tags, q.Enrich)
}

// searchFanOut runs one search per container concurrently (write-partitioned
// slots; wg.Wait is the happens-before edge) and merges the hits best-first. A
// single leg error fails the whole call — a partial merge would let a broken
// container masquerade as a genuine no-match on the enrich novelty path.
func (idx *indexerImpl) searchFanOut(ctx context.Context, base SearchRequest, tags []string, enrich bool) ([]PatternMatch, error) {
	type legResult struct {
		matches []PatternMatch
		err     error
	}
	legs := make([]legResult, len(tags))
	var wg sync.WaitGroup
	wg.Add(len(tags))
	for i, tag := range tags {
		go func(i int, tag string) {
			defer wg.Done()
			req := base
			req.ContainerTag = tag
			m, err := idx.runSearch(ctx, req, enrich)
			legs[i] = legResult{m, err}
		}(i, tag)
	}
	wg.Wait()

	var out []PatternMatch
	for _, leg := range legs {
		if leg.err != nil {
			return nil, leg.err
		}
		out = append(out, leg.matches...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out, nil
}

// runSearch executes one SearchRequest and converts the results to
// []PatternMatch, RETURNING the client error instead of swallowing it — the ONE
// place a reader-path search error originates. When enrich is set each match
// also carries RichContent(2) (summary + related memories) for the hint render
// path. Result counts log at Debug (empty-vs-hit visibility, tagged by
// container); the error path is the caller's to log (BestEffort on degrade, or
// the enrich Warn on propagate) so the log-and-degrade policy stays single-owned.
func (idx *indexerImpl) runSearch(ctx context.Context, req SearchRequest, enrich bool) ([]PatternMatch, error) {
	resp, err := idx.client.Search(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, nil
	}
	out := make([]PatternMatch, 0, len(resp.Results))
	for _, r := range resp.Results {
		pm := resultToPatternMatch(r)
		if enrich {
			pm.RichContent = r.RichContent(2)
		}
		out = append(out, pm)
	}
	idx.logger.Debug("memory search",
		"container", req.ContainerTag, "query_len", len(req.Query), "count", len(out))
	return out, nil
}

// logDegrade emits the single canonical "memory read degraded" Warn shared by
// BestEffort (whole-read degradation) and warnLeg (per-leg degradation), so the
// two paths can never drift in field shape. A nil logger is a no-op, keeping
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
