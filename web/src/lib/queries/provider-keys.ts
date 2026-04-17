import { useQueryClient } from "@tanstack/react-query";
import type { ProviderKey } from "../types";
import { createAuthQuery, createAuthMutation, getApi } from "@/lib/query-kit";

export const useProviderKeys = createAuthQuery<ProviderKey[]>({
  queryKey: ["provider-keys"],
  fetcher: (_vars, ctx) => {
    const api = getApi(ctx);
    return api.get<ProviderKey[]>(`/api/v1/installations/${api.active!.id}/provider-keys`);
  },
  staleTime: 5 * 60 * 1000,
});

type UpsertKeyVars = { provider: string; api_key: string; base_url?: string; repo_id?: number };

const useUpsertProviderKeyMutation = createAuthMutation<ProviderKey, UpsertKeyVars>({
  mutationFn: (body, ctx) => {
    const api = getApi(ctx);
    return api.put<ProviderKey>(`/api/v1/installations/${api.active!.id}/provider-keys`, body);
  },
});

export const useUpsertProviderKey = () => {
  const qc = useQueryClient();
  return useUpsertProviderKeyMutation({
    onSuccess: () => qc.invalidateQueries({ queryKey: useProviderKeys.getKey() }),
    onError: (err) => console.error("[upsert-provider-key] failed:", err.message),
  });
};

const useDeleteProviderKeyMutation = createAuthMutation<unknown, number>({
  mutationFn: (keyId, ctx) => {
    const api = getApi(ctx);
    return api.delete(`/api/v1/installations/${api.active!.id}/provider-keys/${keyId}`);
  },
});

export const useDeleteProviderKey = () => {
  const qc = useQueryClient();
  return useDeleteProviderKeyMutation({
    onSuccess: () => qc.invalidateQueries({ queryKey: useProviderKeys.getKey() }),
    onError: (err) => console.error("[delete-provider-key] failed:", err.message),
  });
};
