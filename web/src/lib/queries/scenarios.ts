import { useQueryClient } from "@tanstack/react-query";
import type { Scenario, ScenarioRun, ScenarioKPIs } from "../types";
import { useActiveRepo } from "@/lib/hooks/use-active-repo";
import { createAuthQuery, createAuthMutation, getApi } from "@/lib/query-kit";

type ScenariosVars = { repoId: number };

const useScenariosQuery = createAuthQuery<Scenario[], ScenariosVars>({
  queryKey: ["scenarios"],
  fetcher: ({ repoId }, ctx) => getApi(ctx).get<Scenario[]>(`/api/v1/repos/${repoId}/scenarios`),
  staleTime: 2 * 60 * 1000,
});

export const useScenarios = () => {
  const { activeId } = useActiveRepo();
  return useScenariosQuery({
    variables: { repoId: activeId ?? 0 },
    enabled: !!activeId,
  });
};

type CreateScenarioVars = { repoId: number; description: string; severity: string; files: string[] };

const useCreateScenarioMutation = createAuthMutation<unknown, CreateScenarioVars>({
  mutationFn: ({ repoId, ...body }, ctx) => getApi(ctx).post(`/api/v1/repos/${repoId}/scenarios`, body),
});

export const useCreateScenario = () => {
  const { activeId } = useActiveRepo();
  const qc = useQueryClient();
  const mutation = useCreateScenarioMutation({
    onSuccess: () => qc.invalidateQueries({ queryKey: useScenariosQuery.getKey() }),
    onError: (err) => console.error("[create-scenario] failed:", err.message),
  });
  // Thin wrapper so callers can keep passing the old { description, severity, files } shape.
  return {
    ...mutation,
    mutate: (body: { description: string; severity: string; files: string[] }, opts?: Parameters<typeof mutation.mutate>[1]) =>
      mutation.mutate({ ...body, repoId: activeId ?? 0 }, opts),
    mutateAsync: (body: { description: string; severity: string; files: string[] }, opts?: Parameters<typeof mutation.mutateAsync>[1]) =>
      mutation.mutateAsync({ ...body, repoId: activeId ?? 0 }, opts),
  };
};

const useDeleteScenarioMutation = createAuthMutation<unknown, number>({
  mutationFn: (id, ctx) => getApi(ctx).delete(`/api/v1/scenarios/${id}`),
});

export const useDeleteScenario = () => {
  const qc = useQueryClient();
  return useDeleteScenarioMutation({
    onSuccess: () => qc.invalidateQueries({ queryKey: useScenariosQuery.getKey() }),
    onError: (err) => console.error("[delete-scenario] failed:", err.message),
  });
};

// Per-scenario run history for the expanded drawer.
const useScenarioRunsQuery = createAuthQuery<ScenarioRun[], { scenarioId: number; limit?: number }>({
  queryKey: ["scenario-runs"],
  fetcher: ({ scenarioId, limit = 20 }, ctx) =>
    getApi(ctx).get<ScenarioRun[]>(`/api/v1/scenarios/${scenarioId}/runs?limit=${limit}`),
  staleTime: 60 * 1000,
});

export const useScenarioRuns = (scenarioId: number | null) =>
  useScenarioRunsQuery({
    variables: { scenarioId: scenarioId ?? 0 },
    enabled: !!scenarioId,
  });

// KPI summary cards (active / broken this week / fixed this week / outdated) scoped to the
// currently-selected repo.
const useScenarioKPIsQuery = createAuthQuery<ScenarioKPIs, { repoId: number }>({
  queryKey: ["scenario-kpis"],
  fetcher: ({ repoId }, ctx) =>
    getApi(ctx).get<ScenarioKPIs>(`/api/v1/repos/${repoId}/scenarios/kpis`),
  staleTime: 60 * 1000,
});

export const useScenarioKPIs = () => {
  const { activeId } = useActiveRepo();
  return useScenarioKPIsQuery({
    variables: { repoId: activeId ?? 0 },
    enabled: !!activeId,
  });
};
