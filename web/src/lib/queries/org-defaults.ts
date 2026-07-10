import { useQueryClient } from "@tanstack/react-query";
import { createAuthQuery, createAuthMutation, getApi } from "@/lib/query-kit";

/** Scoped by installation so an org switch can't serve stale cross-org
 * defaults from cache. Callers pass `active?.id`. */
type OrgDefaultsVars = { installationId?: number };

export const useOrgDefaults = createAuthQuery<Record<string, unknown>, OrgDefaultsVars>({
  queryKey: ["org-defaults"],
  fetcher: (_vars, ctx) => {
    const api = getApi(ctx);
    return api.get<Record<string, unknown>>(`/api/v1/installations/${api.active?.id}/defaults`);
  },
  staleTime: 5 * 60 * 1000,
});

const useSaveOrgDefaultsMutation = createAuthMutation<{ status: string }, Record<string, unknown>>({
  mutationFn: (settings, ctx) => {
    const api = getApi(ctx);
    return api.put<{ status: string }>(`/api/v1/installations/${api.active?.id}/defaults`, settings);
  },
});

export const useSaveOrgDefaults = () => {
  const qc = useQueryClient();
  return useSaveOrgDefaultsMutation({
    onSuccess: () => qc.invalidateQueries({ queryKey: useOrgDefaults.getKey() }),
    onError: (err) => console.error("[save-org-defaults] failed:", err.message),
  });
};
