import { useQuery } from "@tanstack/react-query";
import type { FileRisk, DecisionTrace } from "../types";
import { useApi } from "@/lib/hooks/use-api";
import { useActiveRepo } from "@/lib/hooks/use-active-repo";

export function useRepoRisk() {
  const api = useApi();
  const { activeId } = useActiveRepo();
  return useQuery({
    queryKey: ["repo-risk", api.active?.id, activeId],
    queryFn: () => api.get<FileRisk[]>(`/api/v1/repos/${activeId}/risk`),
    enabled: !!activeId,
  });
}

export function useTraces(file?: string) {
  const api = useApi();
  const { activeId } = useActiveRepo();
  const params = file ? `?file=${encodeURIComponent(file)}` : "";
  return useQuery({
    queryKey: ["traces", api.active?.id, activeId, file],
    queryFn: () => api.get<DecisionTrace[]>(`/api/v1/repos/${activeId}/traces${params}`),
    enabled: !!activeId,
  });
}
