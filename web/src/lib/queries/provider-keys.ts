import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import type { ProviderKey } from "../types";
import { useApi } from "@/lib/hooks/use-api";

export function useProviderKeys() {
  const api = useApi();
  return useQuery({
    queryKey: ["provider-keys", api.active?.id],
    queryFn: () =>
      api.get<ProviderKey[]>(
        `/api/v1/installations/${api.active!.id}/provider-keys`,
      ),
    enabled: !!api.active,
    staleTime: 5 * 60 * 1000,
  });
}

export function useUpsertProviderKey() {
  const api = useApi();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: {
      provider: string;
      api_key: string;
      base_url?: string;
      repo_id?: number;
    }) =>
      api.put<ProviderKey>(
        `/api/v1/installations/${api.active!.id}/provider-keys`,
        body,
      ),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["provider-keys", api.active?.id] });
    },
    onError: (err: Error) => {
      console.error("[upsert-provider-key] failed:", err.message);
    },
  });
}

export function useDeleteProviderKey() {
  const api = useApi();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (keyId: number) =>
      api.delete(
        `/api/v1/installations/${api.active!.id}/provider-keys/${keyId}`,
      ),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["provider-keys", api.active?.id] });
    },
    onError: (err: Error) => {
      console.error("[delete-provider-key] failed:", err.message);
    },
  });
}
