import type { FileRisk, DecisionTrace } from "../types";
import { useActiveRepo } from "@/lib/hooks/use-active-repo";
import { createAuthQuery, getApi } from "@/lib/query-kit";

const useRepoRiskQuery = createAuthQuery<FileRisk[], { repoId: number }>({
  queryKey: ["repo-risk"],
  fetcher: ({ repoId }, ctx) => getApi(ctx).get<FileRisk[]>(`/api/v1/repos/${repoId}/risk`),
  staleTime: 2 * 60 * 1000,
});

export const useRepoRisk = () => {
  const { activeId } = useActiveRepo();
  return useRepoRiskQuery({
    variables: { repoId: activeId ?? 0 },
    enabled: !!activeId,
  });
};

const useTracesQuery = createAuthQuery<DecisionTrace[], { repoId: number; file?: string }>({
  queryKey: ["traces"],
  fetcher: ({ repoId, file }, ctx) => {
    const params = file ? `?file=${encodeURIComponent(file)}` : "";
    return getApi(ctx).get<DecisionTrace[]>(`/api/v1/repos/${repoId}/traces${params}`);
  },
  staleTime: 2 * 60 * 1000,
});

export const useTraces = (file?: string) => {
  const { activeId } = useActiveRepo();
  return useTracesQuery({
    variables: { repoId: activeId ?? 0, file },
    enabled: !!activeId,
  });
};
