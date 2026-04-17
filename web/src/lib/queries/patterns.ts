import { useQueryClient } from "@tanstack/react-query";
import type { Pattern, PatternStat } from "../types";
import { createAuthQuery, createAuthMutation, getApi } from "@/lib/query-kit";

type PatternsVars = { repoId?: number };

export const usePatterns = createAuthQuery<Pattern[], PatternsVars>({
  queryKey: ["patterns"],
  fetcher: ({ repoId }, ctx) => {
    const path = repoId ? `/api/v1/patterns?repo_id=${repoId}` : "/api/v1/patterns";
    return getApi(ctx).get<Pattern[]>(path);
  },
  staleTime: 2 * 60 * 1000,
});

type CreatePatternVars = { content: string; repo_id?: number };

const useCreatePatternMutation = createAuthMutation<Pattern, CreatePatternVars>({
  mutationFn: (body, ctx) => {
    const api = getApi(ctx);
    return api.post<Pattern>("/api/v1/patterns", { ...body, installation_id: api.active?.id });
  },
});

export const useCreatePattern = () => {
  const qc = useQueryClient();
  return useCreatePatternMutation({
    onSuccess: () => qc.invalidateQueries({ queryKey: usePatterns.getKey() }),
    onError: (err) => console.error("[create-pattern] failed:", err.message),
  });
};

export const usePatternStats = createAuthQuery<PatternStat[]>({
  queryKey: ["pattern-stats"],
  fetcher: (_vars, ctx) => getApi(ctx).get<PatternStat[]>("/api/v1/patterns/stats"),
  staleTime: 2 * 60 * 1000,
});

type PatternVars = { id: number };

export const usePattern = createAuthQuery<Pattern, PatternVars>({
  queryKey: ["pattern"],
  fetcher: ({ id }, ctx) => getApi(ctx).get<Pattern>(`/api/v1/patterns/${id}`),
  staleTime: 2 * 60 * 1000,
});

const useDeletePatternMutation = createAuthMutation<unknown, number>({
  mutationFn: (id, ctx) => getApi(ctx).delete(`/api/v1/patterns/${id}`),
});

export const useDeletePattern = () => {
  const qc = useQueryClient();
  return useDeletePatternMutation({
    onSuccess: () => qc.invalidateQueries({ queryKey: usePatterns.getKey() }),
    onError: (err) => console.error("[delete-pattern] failed:", err.message),
  });
};
