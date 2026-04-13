import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useApi } from "@/lib/hooks/use-api";

export function useOrgDefaults() {
  const api = useApi();
  return useQuery({
    queryKey: ["org-defaults", api.active?.id],
    queryFn: () =>
      api.get<Record<string, unknown>>(
        `/api/v1/installations/${api.active?.id}/defaults`,
      ),
    enabled: !!api.active,
    staleTime: 5 * 60 * 1000,
  });
}

export function useSaveOrgDefaults() {
  const api = useApi();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (settings: Record<string, unknown>) =>
      api.put<{ status: string }>(
        `/api/v1/installations/${api.active?.id}/defaults`,
        settings,
      ),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["org-defaults"] }),
    onError: (err: Error) => {
      console.error("[save-org-defaults] failed:", err.message);
    },
  });
}
