import { useQuery } from "@tanstack/react-query";
import { useApi } from "@/lib/hooks/use-api";

export type Period = "7d" | "30d" | "90d";

export interface StatsOverview {
  total_reviews: number;
  total_cost: number;
  avg_score: number;
  avg_review_secs: number;
  total_tokens: number;
  critical_finds: number;
  catch_rate: number;
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

export function useStatsOverview(period: Period) {
  const api = useApi();
  return useQuery({
    queryKey: ["stats", "overview", api.active?.id, period],
    queryFn: () => api.get<StatsOverview>(`/api/v1/stats/overview?period=${period}`),
    enabled: !!api.active,
    staleTime: 60_000,
  });
}

export function useStatsTimeseries(period: Period) {
  const api = useApi();
  return useQuery({
    queryKey: ["stats", "timeseries", api.active?.id, period],
    queryFn: () => api.get<TimeseriesPoint[]>(`/api/v1/stats/timeseries?period=${period}`),
    enabled: !!api.active,
    staleTime: 60_000,
  });
}

export function useStatsUsers(period: Period) {
  const api = useApi();
  return useQuery({
    queryKey: ["stats", "users", api.active?.id, period],
    queryFn: () => api.get<UserStat[]>(`/api/v1/stats/users?period=${period}`),
    enabled: !!api.active,
    staleTime: 60_000,
  });
}

export function useStatsModels(period: Period) {
  const api = useApi();
  return useQuery({
    queryKey: ["stats", "models", api.active?.id, period],
    queryFn: () => api.get<ModelStat[]>(`/api/v1/stats/models?period=${period}`),
    enabled: !!api.active,
    staleTime: 60_000,
  });
}

export function useStatsFindings(period: Period) {
  const api = useApi();
  return useQuery({
    queryKey: ["stats", "findings", api.active?.id, period],
    queryFn: () => api.get<FindingsData>(`/api/v1/stats/findings?period=${period}`),
    enabled: !!api.active,
    staleTime: 60_000,
  });
}

export function useStatsAdoption(period: Period) {
  const api = useApi();
  return useQuery({
    queryKey: ["stats", "adoption", api.active?.id, period],
    queryFn: () => api.get<AdoptionData>(`/api/v1/stats/adoption?period=${period}`),
    enabled: !!api.active,
    staleTime: 60_000,
  });
}

export function useStatsRepos(period: Period) {
  const api = useApi();
  return useQuery({
    queryKey: ["stats", "repos", api.active?.id, period],
    queryFn: () => api.get<RepoStat[]>(`/api/v1/stats/repos?period=${period}`),
    enabled: !!api.active,
    staleTime: 60_000,
  });
}

export function useStatsReviewTimes(period: Period) {
  const api = useApi();
  return useQuery({
    queryKey: ["stats", "review-times", api.active?.id, period],
    queryFn: () => api.get<ReviewTimesData>(`/api/v1/stats/review-times?period=${period}`),
    enabled: !!api.active,
    staleTime: 60_000,
  });
}

export function useStatsCostPerStage(period: Period) {
  const api = useApi();
  return useQuery({
    queryKey: ["stats", "cost-per-stage", api.active?.id, period],
    queryFn: () => api.get<StageCost[]>(`/api/v1/stats/cost-per-stage?period=${period}`),
    enabled: !!api.active,
    staleTime: 60_000,
  });
}
