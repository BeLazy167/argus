import { useQueryClient } from "@tanstack/react-query";
import { createAuthQuery, createAuthMutation, getApi } from "@/lib/query-kit";

export const useSupermemoryKeyStatus = createAuthQuery<{ configured: boolean }>({
  queryKey: ["supermemory-key"],
  fetcher: (_vars, ctx) => {
    const api = getApi(ctx);
    return api.get<{ configured: boolean }>(`/api/v1/installations/${api.active!.id}/supermemory-key`);
  },
  staleTime: 5 * 60 * 1000,
});

const useSetSupermemoryKeyMutation = createAuthMutation<unknown, string>({
  mutationFn: (apiKey, ctx) => {
    const api = getApi(ctx);
    return api.put(`/api/v1/installations/${api.active!.id}/supermemory-key`, { api_key: apiKey });
  },
});

export const useSetSupermemoryKey = () => {
  const qc = useQueryClient();
  return useSetSupermemoryKeyMutation({
    onSuccess: () => qc.invalidateQueries({ queryKey: useSupermemoryKeyStatus.getKey() }),
    onError: (err) => console.error("[set-supermemory-key] failed:", err.message),
  });
};

const useDeleteSupermemoryKeyMutation = createAuthMutation<unknown, void>({
  mutationFn: (_vars, ctx) => {
    const api = getApi(ctx);
    return api.delete(`/api/v1/installations/${api.active!.id}/supermemory-key`);
  },
});

export const useDeleteSupermemoryKey = () => {
  const qc = useQueryClient();
  return useDeleteSupermemoryKeyMutation({
    onSuccess: () => qc.invalidateQueries({ queryKey: useSupermemoryKeyStatus.getKey() }),
    onError: (err) => console.error("[delete-supermemory-key] failed:", err.message),
  });
};
