import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import type { Scenario } from "../types";
import { useApi } from "@/lib/hooks/use-api";
import { useActiveRepo } from "@/lib/hooks/use-active-repo";

export function useScenarios() {
  const api = useApi();
  const { activeId } = useActiveRepo();
  return useQuery({
    queryKey: ["scenarios", api.active?.id, activeId],
    queryFn: () => api.get<Scenario[]>(`/api/v1/repos/${activeId}/scenarios`),
    enabled: !!activeId,
    staleTime: 2 * 60 * 1000,
  });
}

export function useCreateScenario() {
  const api = useApi();
  const { activeId } = useActiveRepo();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { description: string; severity: string; files: string[] }) =>
      api.post(`/api/v1/repos/${activeId}/scenarios`, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["scenarios"] }),
    onError: (err: Error) => {
      console.error("[create-scenario] failed:", err.message);
    },
  });
}

export function useDeleteScenario() {
  const api = useApi();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api.delete(`/api/v1/scenarios/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["scenarios"] }),
    onError: (err: Error) => {
      console.error("[delete-scenario] failed:", err.message);
    },
  });
}
