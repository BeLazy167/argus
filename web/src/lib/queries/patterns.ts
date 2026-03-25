import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import type { Pattern, PatternStat } from "../types";
import { useApi } from "@/lib/hooks/use-api";

export function usePatterns(repoId?: number) {
  const api = useApi();
  return useQuery({
    queryKey: ["patterns", api.active?.id, repoId],
    queryFn: () => {
      const path = repoId
        ? `/api/v1/patterns?repo_id=${repoId}`
        : "/api/v1/patterns";
      return api.get<Pattern[]>(path);
    },
    enabled: !!api.active,
  });
}

export function useCreatePattern() {
  const api = useApi();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { content: string; repo_id?: number }) =>
      api.post<Pattern>(
        "/api/v1/patterns",
        { ...body, installation_id: api.active?.id },
      ),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["patterns", api.active?.id] }),
  });
}

export function usePatternStats() {
  const api = useApi();
  return useQuery({
    queryKey: ["pattern-stats", api.active?.id],
    queryFn: () => api.get<PatternStat[]>("/api/v1/patterns/stats"),
    enabled: !!api.active,
  });
}

export function useDeletePattern() {
  const api = useApi();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api.delete(`/api/v1/patterns/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["patterns", api.active?.id] }),
  });
}
