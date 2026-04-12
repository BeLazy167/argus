import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useApi } from "@/lib/hooks/use-api";

export function useSupermemoryKeyStatus() {
  const api = useApi();
  return useQuery({
    queryKey: ["supermemory-key", api.active?.id],
    queryFn: () =>
      api.get<{ configured: boolean }>(
        `/api/v1/installations/${api.active!.id}/supermemory-key`,
      ),
    enabled: !!api.active,
  });
}

export function useSetSupermemoryKey() {
  const api = useApi();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (apiKey: string) =>
      api.put(
        `/api/v1/installations/${api.active!.id}/supermemory-key`,
        { api_key: apiKey },
      ),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["supermemory-key", api.active?.id] });
    },
  });
}

export function useDeleteSupermemoryKey() {
  const api = useApi();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () =>
      api.delete(
        `/api/v1/installations/${api.active!.id}/supermemory-key`,
      ),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["supermemory-key", api.active?.id] });
    },
  });
}
