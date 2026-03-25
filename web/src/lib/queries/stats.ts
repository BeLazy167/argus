import { useQuery } from "@tanstack/react-query";
import type { Stats, ActivityLog } from "../types";
import { useApi } from "@/lib/hooks/use-api";

export function useStats(repoId?: number) {
  const api = useApi();
  return useQuery({
    queryKey: ["stats", api.active?.id, repoId],
    queryFn: () => {
      const path = repoId && repoId > 0
        ? `/api/v1/stats?repo_id=${repoId}`
        : `/api/v1/stats`;
      return api.get<Stats>(path);
    },
    enabled: !!api.active,
  });
}

export function useActivity(limit = 50) {
  const api = useApi();
  return useQuery({
    queryKey: ["activity", limit, api.active?.id],
    queryFn: () => api.get<ActivityLog[]>(`/api/v1/activity?limit=${limit}`),
    enabled: !!api.active,
  });
}
