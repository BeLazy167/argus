import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import type { Repo } from "../types";
import { useApi } from "@/lib/hooks/use-api";

export function useRepos() {
  const api = useApi();
  return useQuery({
    queryKey: ["repos", api.active?.id],
    queryFn: () => api.get<Repo[]>("/api/v1/repos"),
    enabled: !!api.active,
  });
}

export function useSyncRepos() {
  const api = useApi();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () =>
      api.post<{ synced: number }>(
        `/api/v1/installations/${api.active?.id}/sync-repos`,
        {},
      ),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["repos"] }),
  });
}

export function useUpdateRepo() {
  const api = useApi();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      id,
      ...body
    }: { id: number; enabled?: boolean; default_branch?: string; settings_json?: Record<string, unknown> }) =>
      api.patch<Repo>(`/api/v1/repos/${id}`, body),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["repos"] });
    },
  });
}
