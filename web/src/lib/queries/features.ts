import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useApi } from "@/lib/hooks/use-api";

export type FeatureFlags = {
  issue_acceptance: boolean;
  cross_pr_checks: boolean;
  max_linked_prs: number;
};

export function useFeatureFlags() {
  const api = useApi();
  return useQuery({
    queryKey: ["feature-flags", api.active?.id],
    queryFn: () =>
      api.get<FeatureFlags>(
        `/api/v1/installations/${api.active?.id}/features`,
      ),
    enabled: !!api.active,
  });
}

export function useSaveFeatureFlags() {
  const api = useApi();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (flags: FeatureFlags) =>
      api.put<FeatureFlags>(
        `/api/v1/installations/${api.active?.id}/features`,
        flags,
      ),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["feature-flags"] }),
  });
}
