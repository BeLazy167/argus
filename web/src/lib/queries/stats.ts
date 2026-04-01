import { useQuery } from "@tanstack/react-query";
import type { Stats } from "../types";
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
