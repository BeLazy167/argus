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

func parsePeriodInterval(r *http.Request) pgtype.Interval {
	switch r.URL.Query().Get("period") {
	case "7d":
		return pgtype.Interval{Days: 7, Valid: true}
	case "90d":
		return pgtype.Interval{Days: 90, Valid: true}
	default:
		return pgtype.Interval{Days: 30, Valid: true}
	}
}

func (s *Server) statsOverview(w http.ResponseWriter, r *http.Request) {
	q := db.New(s.store.Pool)
	row, err := q.StatsOverview(r.Context(), db.StatsOverviewParams{
		InstallationIds: getInstallationIDs(r.Context()),
		Period:          parsePeriodInterval(r),
	})
	if err != nil {
		s.logger.Error("stats overview", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"total_reviews":   row.TotalReviews,
		"total_cost":      row.TotalCost,
		"avg_score":       row.AvgScore,
		"avg_review_secs": row.AvgReviewSecs,
		"total_tokens":    row.TotalTokens,
		"critical_finds":  row.CriticalFinds,
		"catch_rate":      toFloat64(row.CatchRate),
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
		TotalCost     float64 `json:"total_cost"`
		CriticalCount int     `json:"critical_count"`
	}
	out := make([]userStat, 0, len(userRows))
	for _, u := range userRows {
		out = append(out, userStat{
			PRAuthor:      u.PRAuthor,
			ReviewCount:   u.ReviewCount,
			AvgScore:      toFloat64(u.AvgScore),
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

	type stageAgg struct {
		Stage       string  `json:"stage"`
		TotalTokens int     `json:"total_tokens"`
		TotalCost   float64 `json:"total_cost"`
	}
	agg := map[string]*stageAgg{}
	addStage := func(name string, st pipeline.StageTokens) {
		if st.TotalTokens == 0 {
			return
		}
		a, ok := agg[name]
		if !ok {
			a = &stageAgg{Stage: name}
			agg[name] = a
		}
		a.TotalTokens += st.TotalTokens
		a.TotalCost += st.Cost
	}

	for _, raw := range rawRows {
		var usage pipeline.RunTokenUsage
		if err := json.Unmarshal(raw, &usage); err != nil {
			continue
		}
		addStage("triage", usage.Triage)
		addStage("scoring", usage.Scoring)
		addStage("synthesis", usage.Synthesis)
		addStage("enrichment", usage.Enrichment)
		addStage("conventions", usage.Conventions)
		addStage("patterns", usage.Patterns)
		addStage("graph", usage.Graph)
		for _, st := range usage.Review {
			addStage("review", st)
		}
		for _, st := range usage.FileSynthesis {
			addStage("file_synthesis", st)
		}
	}

	out := make([]stageAgg, 0, len(agg))
	for _, a := range agg {
		out = append(out, *a)
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
