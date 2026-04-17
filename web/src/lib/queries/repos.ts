import { useQueryClient } from "@tanstack/react-query";
import type { Repo } from "../types";
import { createAuthQuery, createAuthMutation, getApi } from "@/lib/query-kit";

export const useRepos = createAuthQuery<Repo[]>({
  queryKey: ["repos"],
  fetcher: (_vars, ctx) => getApi(ctx).get<Repo[]>("/api/v1/repos"),
  staleTime: 2 * 60 * 1000,
});

export const useSyncRepos = () => {
  const qc = useQueryClient();
  return useSyncReposMutation({
    onSuccess: () => qc.invalidateQueries({ queryKey: useRepos.getKey() }),
    onError: (err) => console.error("[sync-repos] failed:", err.message),
  });
};

const useSyncReposMutation = createAuthMutation<{ synced: number }, void>({
  mutationFn: async (_vars, ctx) => {
    const api = getApi(ctx);
    return api.post<{ synced: number }>(`/api/v1/installations/${api.active?.id}/sync-repos`, {});
  },
});

type UpdateRepoVars = {
  id: number;
  enabled?: boolean;
  default_branch?: string;
  settings_json?: Record<string, unknown>;
};

export const useUpdateRepo = () => {
  const qc = useQueryClient();
  return useUpdateRepoMutation({
    onSuccess: () => qc.invalidateQueries({ queryKey: useRepos.getKey() }),
    onError: (err) => console.error("[update-repo] failed:", err.message),
  });
};

const useUpdateRepoMutation = createAuthMutation<Repo, UpdateRepoVars>({
  mutationFn: ({ id, ...body }, ctx) => getApi(ctx).patch<Repo>(`/api/v1/repos/${id}`, body),
});
