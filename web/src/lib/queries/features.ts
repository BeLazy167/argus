import { useQueryClient } from "@tanstack/react-query";
import { createAuthQuery, createAuthMutation, getApi } from "@/lib/query-kit";

export type FeatureFlags = {
  issue_acceptance: boolean;
  cross_pr_checks: boolean;
  max_linked_prs: number;
};

export const useFeatureFlags = createAuthQuery<FeatureFlags>({
  queryKey: ["feature-flags"],
  fetcher: (_vars, ctx) => {
    const api = getApi(ctx);
    return api.get<FeatureFlags>(`/api/v1/installations/${api.active?.id}/features`);
  },
  staleTime: 5 * 60 * 1000,
});

const useSaveFeatureFlagsMutation = createAuthMutation<FeatureFlags, FeatureFlags>({
  mutationFn: (flags, ctx) => {
    const api = getApi(ctx);
    return api.put<FeatureFlags>(`/api/v1/installations/${api.active?.id}/features`, flags);
  },
});

export const useSaveFeatureFlags = () => {
  const qc = useQueryClient();
  return useSaveFeatureFlagsMutation({
    onSuccess: () => qc.invalidateQueries({ queryKey: useFeatureFlags.getKey() }),
    onError: (err) => console.error("[save-feature-flags] failed:", err.message),
  });
};
