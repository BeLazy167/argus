package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/BeLazy167/argus/backend/internal/pipeline"
	"github.com/BeLazy167/argus/backend/internal/store/db"
	"github.com/jackc/pgx/v5/pgtype"
)

// parsePeriod resolves the ?period= query param to both the pgtype.Interval
// form (for sqlc-generated calls) and the text form (for raw-SQL store
// methods that take a `text::interval` parameter). Single source of truth
// so a new period value can't drift between the two paths.
func parsePeriod(r *http.Request) (pgtype.Interval, string) {
	switch r.URL.Query().Get("period") {
	case "7d":
		return pgtype.Interval{Days: 7, Valid: true}, "7 days"
	case "90d":
		return pgtype.Interval{Days: 90, Valid: true}, "90 days"
	default:
		return pgtype.Interval{Days: 30, Valid: true}, "30 days"
	}
}

// parsePeriodInterval is a thin wrapper for callers that only need the
// pgtype.Interval form. Kept so the other stats handlers (timeseries,
// users, models, ...) don't need a second return value they don't use.
func parsePeriodInterval(r *http.Request) pgtype.Interval {
	iv, _ := parsePeriod(r)
	return iv
}

func (s *Server) statsOverview(w http.ResponseWriter, r *http.Request) {
	q := db.New(s.store.Pool)
	instIDs := getInstallationIDs(r.Context())
	period, periodStr := parsePeriod(r)

	row, err := q.StatsOverview(r.Context(), db.StatsOverviewParams{
		InstallationIds: instIDs,
		Period:          period,
	})
	if err != nil {
		s.logger.Error("stats overview", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}

	// Additive counters. Zero-value the row on error rather than 500 the
	// whole overview — these are secondary signals next to the core
	// numbers above, and a DB hiccup on one shouldn't blank the page.
	// Logging at Error so a sustained outage (missing table, missing
	// column, auth regression) is visible in alerting, not just swallowed
	// as "all-zero metrics".
	ar, arErr := s.store.GetAutoResolveStats(r.Context(), instIDs, periodStr)
	if arErr != nil {
		s.logger.Error("stats overview: auto-resolve stats", "error", arErr)
	}
	ll, llErr := s.store.GetLearnLayerCounts(r.Context(), instIDs, periodStr)
	if llErr != nil {
		s.logger.Error("stats overview: learn-layer counts", "error", llErr)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"total_reviews":   row.TotalReviews,
		"total_cost":      row.TotalCost,
		"avg_score":       row.AvgScore,
		"avg_review_secs": row.AvgReviewSecs,
		"total_tokens":    row.TotalTokens,
		"critical_finds":  row.CriticalFinds,
		"catch_rate":      toFloat64(row.CatchRate),
		// Automated hygiene (auto-resolve).
		"auto_resolve_events":    ar.EventCount,
		"auto_resolves":          ar.ResolvedTotal,
		"auto_resolve_attempts":  ar.AttemptedTotal,
		"auto_resolve_api_calls": ar.APICallsTotal,
		// Learn layer — rows that the memory/learn path produced
		// this period. All BYOK-paid side-effects users can reconcile.
		"patterns_learned": ll.PatternsLearned,
		"scenarios_stored": ll.ScenariosStored,
		"decision_traces":  ll.DecisionTraces,
		"feedback_indexed": ll.FeedbackIndexed,
	})
}

func (s *Server) statsTimeseries(w http.ResponseWriter, r *http.Request) {
	q := db.New(s.store.Pool)
	rows, err := q.StatsTimeseries(r.Context(), db.StatsTimeseriesParams{
		InstallationIds: getInstallationIDs(r.Context()),
		Period:          parsePeriodInterval(r),
	})
	if err != nil {
		s.logger.Error("stats timeseries", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	type entry struct {
		Day         string  `json:"day"`
		ReviewCount int     `json:"review_count"`
		AvgScore    float64 `json:"avg_score"`
		TotalCost   float64 `json:"total_cost"`
		TotalTokens int     `json:"total_tokens"`
	}
	out := make([]entry, 0, len(rows))
	for _, r := range rows {
		day := ""
		if r.Day.Valid {
			day = r.Day.Time.Format("2006-01-02")
		}
		out = append(out, entry{
			Day:         day,
			ReviewCount: r.ReviewCount,
			AvgScore:    toFloat64(r.AvgScore),
			TotalCost:   r.TotalCost,
			TotalTokens: r.TotalTokens,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) statsUsers(w http.ResponseWriter, r *http.Request) {
	q := db.New(s.store.Pool)
	period := parsePeriodInterval(r)
	instIDs := getInstallationIDs(r.Context())

	userRows, err := q.StatsUsers(r.Context(), db.StatsUsersParams{
		InstallationIds: instIDs,
		Period:          period,
	})
	if err != nil {
		s.logger.Error("stats users", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}

	critRows, err := q.StatsUserCriticals(r.Context(), db.StatsUserCriticalsParams{
		InstallationIds: instIDs,
		Period:          period,
	})
	if err != nil {
		s.logger.Error("stats user criticals", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	critMap := make(map[string]int)
	for _, c := range critRows {
		critMap[c.PRAuthor] = c.CriticalCount
	}

	type userStat struct {
		PRAuthor      string  `json:"pr_author"`
		ReviewCount   int     `json:"review_count"`
		AvgScore      float64 `json:"avg_score"`
		ScoreStddev   float64 `json:"score_stddev"`
		TotalCost     float64 `json:"total_cost"`
		CriticalCount int     `json:"critical_count"`
	}
	out := make([]userStat, 0, len(userRows))
	for _, u := range userRows {
		out = append(out, userStat{
			PRAuthor:      u.PRAuthor,
			ReviewCount:   u.ReviewCount,
			AvgScore:      toFloat64(u.AvgScore),
			ScoreStddev:   toFloat64(u.ScoreStddev),
			TotalCost:     u.TotalCost,
			CriticalCount: critMap[u.PRAuthor],
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) statsModels(w http.ResponseWriter, r *http.Request) {
	q := db.New(s.store.Pool)
	rawRows, err := q.StatsModelsRaw(r.Context(), db.StatsModelsRawParams{
		InstallationIds: getInstallationIDs(r.Context()),
		Period:          parsePeriodInterval(r),
	})
	if err != nil {
		s.logger.Error("stats models", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}

	type modelAgg struct {
		Model       string  `json:"model"`
		TotalTokens int     `json:"total_tokens"`
		TotalCost   float64 `json:"total_cost"`
		ReviewCount int     `json:"review_count"`
	}
	agg := make(map[string]*modelAgg)

	for _, raw := range rawRows {
		var usage pipeline.RunTokenUsage
		if err := json.Unmarshal(raw, &usage); err != nil {
			s.logger.Warn("skipping malformed token_usage in model stats", "error", err)
			continue
		}
		// Aggregate all stages that have a model set
		stages := []pipeline.StageTokens{usage.Triage, usage.Scoring, usage.Synthesis, usage.Enrichment, usage.Conventions, usage.Patterns, usage.Graph}
		for _, st := range stages {
			if st.Model == "" || st.TotalTokens == 0 {
				continue
			}
			a, ok := agg[st.Model]
			if !ok {
				a = &modelAgg{Model: st.Model}
				agg[st.Model] = a
			}
			a.TotalTokens += st.TotalTokens
			a.TotalCost += st.Cost
		}
		// Review stage is an array
		for _, st := range usage.Review {
			if st.Model == "" || st.TotalTokens == 0 {
				continue
			}
			a, ok := agg[st.Model]
			if !ok {
				a = &modelAgg{Model: st.Model}
				agg[st.Model] = a
			}
			a.TotalTokens += st.TotalTokens
			a.TotalCost += st.Cost
		}
		// FileSynthesis array
		for _, st := range usage.FileSynthesis {
			if st.Model == "" || st.TotalTokens == 0 {
				continue
			}
			a, ok := agg[st.Model]
			if !ok {
				a = &modelAgg{Model: st.Model}
				agg[st.Model] = a
			}
			a.TotalTokens += st.TotalTokens
			a.TotalCost += st.Cost
		}
		// Count unique reviews per model — track via triage model as primary
		if usage.Triage.Model != "" {
			if a, ok := agg[usage.Triage.Model]; ok {
				a.ReviewCount++
			}
		}
	}

	out := make([]modelAgg, 0, len(agg))
	for _, a := range agg {
		out = append(out, *a)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) statsFindings(w http.ResponseWriter, r *http.Request) {
	q := db.New(s.store.Pool)
	period := parsePeriodInterval(r)
	instIDs := getInstallationIDs(r.Context())

	sevRows, err := q.StatsFindingsBySeverity(r.Context(), db.StatsFindingsBySeverityParams{
		InstallationIds: instIDs, Period: period,
	})
	if err != nil {
		s.logger.Error("stats findings severity", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}

	catRows, err := q.StatsFindingsByCategory(r.Context(), db.StatsFindingsByCategoryParams{
		InstallationIds: instIDs, Period: period,
	})
	if err != nil {
		s.logger.Error("stats findings category", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}

	newVsPattern, err := q.StatsFindingsNewVsPattern(r.Context(), db.StatsFindingsNewVsPatternParams{
		InstallationIds: instIDs, Period: period,
	})
	if err != nil {
		s.logger.Error("stats findings new vs pattern", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}

	type sevEntry struct {
		Severity string `json:"severity"`
		Count    int    `json:"count"`
	}
	type catEntry struct {
		Category string `json:"category"`
		Count    int    `json:"count"`
	}
	sevOut := make([]sevEntry, 0, len(sevRows))
	for _, r := range sevRows {
		sev := ""
		if r.Severity != nil {
			sev = *r.Severity
		}
		sevOut = append(sevOut, sevEntry{Severity: sev, Count: r.Count})
	}
	catOut := make([]catEntry, 0, len(catRows))
	for _, r := range catRows {
		cat := ""
		if r.Category != nil {
			cat = *r.Category
		}
		catOut = append(catOut, catEntry{Category: cat, Count: r.Count})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"by_severity":    sevOut,
		"by_category":    catOut,
		"new_findings":   newVsPattern.NewFindings,
		"pattern_matches": newVsPattern.PatternMatches,
	})
}

func (s *Server) statsAdoption(w http.ResponseWriter, r *http.Request) {
	q := db.New(s.store.Pool)
	row, err := q.StatsAdoption(r.Context(), db.StatsAdoptionParams{
		InstallationIds: getInstallationIDs(r.Context()),
		Period:          parsePeriodInterval(r),
	})
	if err != nil {
		s.logger.Error("stats adoption", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"deep_review_pct":      toFloat64(row.DeepReviewPct),
		"incremental_pct":      toFloat64(row.IncrementalPct),
		"avg_files_per_review": toFloat64(row.AvgFilesPerReview),
		"active_repos":         row.ActiveRepos,
		"total_enabled_repos":  row.TotalEnabledRepos,
		"total_repos":          row.TotalRepos,
	})
}

func (s *Server) statsRepos(w http.ResponseWriter, r *http.Request) {
	q := db.New(s.store.Pool)
	rows, err := q.StatsPerRepo(r.Context(), db.StatsPerRepoParams{
		InstallationIds: getInstallationIDs(r.Context()),
		Period:          parsePeriodInterval(r),
	})
	if err != nil {
		s.logger.Error("stats per repo", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	type repoStat struct {
		RepoID        int64   `json:"repo_id"`
		FullName      string  `json:"full_name"`
		ReviewCount   int     `json:"review_count"`
		AvgScore      float64 `json:"avg_score"`
		TotalCost     float64 `json:"total_cost"`
		AvgReviewSecs int     `json:"avg_review_secs"`
		TotalTokens   int     `json:"total_tokens"`
	}
	out := make([]repoStat, 0, len(rows))
	for _, r := range rows {
		out = append(out, repoStat{
			RepoID:        r.RepoID,
			FullName:      r.FullName,
			ReviewCount:   r.ReviewCount,
			AvgScore:      toFloat64(r.AvgScore),
			TotalCost:     r.TotalCost,
			AvgReviewSecs: int(toFloat64(r.AvgReviewSecs)),
			TotalTokens:   r.TotalTokens,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) statsReviewTimes(w http.ResponseWriter, r *http.Request) {
	q := db.New(s.store.Pool)
	rows, err := q.StatsReviewTimes(r.Context(), db.StatsReviewTimesParams{
		InstallationIds: getInstallationIDs(r.Context()),
		Period:          parsePeriodInterval(r),
	})
	if err != nil {
		s.logger.Error("stats review times", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	// Calculate percentiles
	n := len(rows)
	p50, p75, p95 := 0, 0, 0
	if n > 0 {
		p50 = rows[n*50/100]
		p75 = rows[n*75/100]
		if n*95/100 < n {
			p95 = rows[n*95/100]
		} else {
			p95 = rows[n-1]
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"count": n,
		"p50":   p50,
		"p75":   p75,
		"p95":   p95,
	})
}

// stageCostAgg is the per-stage row returned by statsCostPerStage.
type stageCostAgg struct {
	Stage       string  `json:"stage"`
	TotalTokens int     `json:"total_tokens"`
	TotalCost   float64 `json:"total_cost"`
}

// aggregateStageCosts decodes each raw token_usage JSONB row and returns a
// per-stage summation. Pure function extracted from the handler so the
// composite-key and missing-stage behavior can be unit-tested without a DB.
// Returns (rows, unmarshalFailures) — callers decide how to log failures.
func aggregateStageCosts(rawRows [][]byte) ([]stageCostAgg, int) {
	agg := map[string]*stageCostAgg{}
	addStage := func(name string, st pipeline.StageTokens) {
		// Gate on tokens AND cost. A provider can return cost without usage
		// counts (gpt-5.x reasoning path, see commit 1070dac); gating on
		// tokens alone would hide real spend from the chart.
		if st.TotalTokens == 0 && st.Cost == 0 {
			return
		}
		a, ok := agg[name]
		if !ok {
			a = &stageCostAgg{Stage: name}
			agg[name] = a
		}
		a.TotalTokens += st.TotalTokens
		a.TotalCost += st.Cost
	}

	var unmarshalErrs int
	for _, raw := range rawRows {
		var usage pipeline.RunTokenUsage
		if err := json.Unmarshal(raw, &usage); err != nil {
			unmarshalErrs++
			continue
		}
		// Scalar stages — every non-array field of RunTokenUsage. The
		// TestStatsCostPerStageCoversAllStages reflection guard catches
		// any future struct-field drift.
		addStage("intent", usage.Intent)
		addStage("triage", usage.Triage)
		addStage("enrichment", usage.Enrichment)
		addStage("conventions", usage.Conventions)
		addStage("patterns", usage.Patterns)
		addStage("lead_agent", usage.LeadAgent)
		addStage("graph", usage.Graph)
		addStage("acceptance", usage.Acceptance)
		addStage("cross_pr", usage.CrossPR)
		addStage("scoring", usage.Scoring)
		addStage("synthesis", usage.Synthesis)
		addStage("reply", usage.Reply)
		// review[] — split by specialist into composite keys like
		// "review.bug_hunter". Bounded at 4-5 values, high signal. An entry
		// with empty Specialist (skim single-pass review) falls into the
		// plain "review" bucket.
		for _, st := range usage.Review {
			key := "review"
			if st.Specialist != "" {
				key = "review." + st.Specialist
			}
			addStage(key, st)
		}
		// file_synthesis[] and simulation[] — intentionally lumped at org
		// scope. Per-file/per-scenario rows are unbounded (hundreds in a
		// busy month) and the signal isn't actionable at aggregate scale.
		// The per-review detail page (web/src/app/(dashboard)/reviews/[id]/page.tsx
		// TokenPill) expands these fully.
		for _, st := range usage.FileSynthesis {
			addStage("file_synthesis", st)
		}
		for _, st := range usage.Simulation {
			addStage("simulation", st)
		}
	}

	out := make([]stageCostAgg, 0, len(agg))
	for _, a := range agg {
		out = append(out, *a)
	}
	return out, unmarshalErrs
}

func (s *Server) statsCostPerStage(w http.ResponseWriter, r *http.Request) {
	q := db.New(s.store.Pool)
	rawRows, err := q.StatsModelsRaw(r.Context(), db.StatsModelsRawParams{
		InstallationIds: getInstallationIDs(r.Context()),
		Period:          parsePeriodInterval(r),
	})
	if err != nil {
		s.logger.Error("stats cost per stage", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}

	out, unmarshalErrs := aggregateStageCosts(rawRows)
	if unmarshalErrs > 0 {
		// Billing-sensitive silent drop. Log so operators can spot schema
		// drift or DB corruption before it hides meaningful spend.
		s.logger.Warn("stats cost per stage: token_usage unmarshal failures",
			"failures", unmarshalErrs, "rows_total", len(rawRows))
	}
	writeJSON(w, http.StatusOK, out)
}

// toFloat64 safely converts interface{} values from sqlc (COALESCE expressions) to float64.
func toFloat64(v any) float64 {
	switch val := v.(type) {
	case float64:
		return val
	case float32:
		return float64(val)
	case int:
		return float64(val)
	case int32:
		return float64(val)
	case int64:
		return float64(val)
	case json.Number:
		f, _ := val.Float64()
		return f
	default:
		slog.Warn("toFloat64: unhandled type", "type", fmt.Sprintf("%T", v), "value", v)
		return 0
	}
}
