import type { Stats } from "../types";
import { createAuthQuery, getApi } from "@/lib/query-kit";

export const useStats = createAuthQuery<Stats, { repoId?: number }>({
  queryKey: ["stats"],
  fetcher: ({ repoId }, ctx) => {
    const path = repoId && repoId > 0 ? `/api/v1/stats?repo_id=${repoId}` : `/api/v1/stats`;
    return getApi(ctx).get<Stats>(path);
  },
  staleTime: 60 * 1000,
  refetchOnWindowFocus: true,
});
