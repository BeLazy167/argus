import { createAuthQuery, getApi } from "@/lib/query-kit";

export type Period = "7d" | "30d" | "90d";

export interface StatsOverview {
  total_reviews: number;
  total_cost: number;
  avg_score: number;
  avg_review_secs: number;
  total_tokens: number;
  critical_finds: number;
  catch_rate: number;
  // Automated hygiene — auto-resolve (diff-based, no LLM cost).
  auto_resolve_events: number;
  auto_resolves: number;
  auto_resolve_attempts: number;
  auto_resolve_api_calls: number;
  // Learn layer — BYOK-paid side effects of the memory/learn path.
  patterns_learned: number;
  scenarios_stored: number;
  decision_traces: number;
  feedback_indexed: number;
}

export interface TimeseriesPoint {
  day: string;
  review_count: number;
  avg_score: number;
  total_cost: number;
  total_tokens: number;
}

export interface UserStat {
  pr_author: string;
  review_count: number;
  avg_score: number;
  // Population stddev of score across this author's reviews in the period.
  // 0 when review_count is 1 (single-value series).
  score_stddev: number;
  total_cost: number;
  critical_count: number;
}

export interface ModelStat {
  model: string;
  total_tokens: number;
  total_cost: number;
  review_count: number;
}

export interface FindingsData {
  by_severity: { severity: string; count: number }[];
  by_category: { category: string; count: number }[];
  new_findings: number;
  pattern_matches: number;
}

export interface AdoptionData {
  deep_review_pct: number;
  incremental_pct: number;
  avg_files_per_review: number;
  active_repos: number;
  total_enabled_repos: number;
  total_repos: number;
}

export interface RepoStat {
  repo_id: number;
  full_name: string;
  review_count: number;
  avg_score: number;
  total_cost: number;
  avg_review_secs: number;
  total_tokens: number;
}

export interface ReviewTimesData {
  count: number;
  p50: number;
  p75: number;
  p95: number;
}

export interface StageCost {
  stage: string;
  total_tokens: number;
  total_cost: number;
}

type PeriodVars = { period: Period };
const STATS_STALE = 60_000;

export const useStatsOverview = createAuthQuery<StatsOverview, PeriodVars>({
  queryKey: ["stats", "overview"],
  fetcher: ({ period }, ctx) => getApi(ctx).get<StatsOverview>(`/api/v1/stats/overview?period=${period}`),
  staleTime: STATS_STALE,
});

export const useStatsTimeseries = createAuthQuery<TimeseriesPoint[], PeriodVars>({
  queryKey: ["stats", "timeseries"],
  fetcher: ({ period }, ctx) => getApi(ctx).get<TimeseriesPoint[]>(`/api/v1/stats/timeseries?period=${period}`),
  staleTime: STATS_STALE,
});

export const useStatsUsers = createAuthQuery<UserStat[], PeriodVars>({
  queryKey: ["stats", "users"],
  fetcher: ({ period }, ctx) => getApi(ctx).get<UserStat[]>(`/api/v1/stats/users?period=${period}`),
  staleTime: STATS_STALE,
});

export const useStatsModels = createAuthQuery<ModelStat[], PeriodVars>({
  queryKey: ["stats", "models"],
  fetcher: ({ period }, ctx) => getApi(ctx).get<ModelStat[]>(`/api/v1/stats/models?period=${period}`),
  staleTime: STATS_STALE,
});

export const useStatsFindings = createAuthQuery<FindingsData, PeriodVars>({
  queryKey: ["stats", "findings"],
  fetcher: ({ period }, ctx) => getApi(ctx).get<FindingsData>(`/api/v1/stats/findings?period=${period}`),
  staleTime: STATS_STALE,
});

export const useStatsAdoption = createAuthQuery<AdoptionData, PeriodVars>({
  queryKey: ["stats", "adoption"],
  fetcher: ({ period }, ctx) => getApi(ctx).get<AdoptionData>(`/api/v1/stats/adoption?period=${period}`),
  staleTime: STATS_STALE,
});

export const useStatsRepos = createAuthQuery<RepoStat[], PeriodVars>({
  queryKey: ["stats", "repos"],
  fetcher: ({ period }, ctx) => getApi(ctx).get<RepoStat[]>(`/api/v1/stats/repos?period=${period}`),
  staleTime: STATS_STALE,
});

export const useStatsReviewTimes = createAuthQuery<ReviewTimesData, PeriodVars>({
  queryKey: ["stats", "review-times"],
  fetcher: ({ period }, ctx) => getApi(ctx).get<ReviewTimesData>(`/api/v1/stats/review-times?period=${period}`),
  staleTime: STATS_STALE,
});

export const useStatsCostPerStage = createAuthQuery<StageCost[], PeriodVars>({
  queryKey: ["stats", "cost-per-stage"],
  fetcher: ({ period }, ctx) => getApi(ctx).get<StageCost[]>(`/api/v1/stats/cost-per-stage?period=${period}`),
  staleTime: STATS_STALE,
});
