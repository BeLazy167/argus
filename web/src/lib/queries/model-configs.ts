import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { ModelConfig } from "../types";
import { useApi } from "@/lib/hooks/use-api";

export type TestResult = {
  success: boolean;
  error?: string;
  response?: string;
  latency_ms: number;
  tokens?: number;
};

export function useTestConfig() {
  const api = useApi();
  return useMutation({
    mutationFn: ({ provider, model }: { provider: string; model: string }) =>
      api.post<TestResult>(
        `/api/v1/installations/${api.active?.id}/test-config`,
        { provider, model },
      ),
  });
}

export function useModelConfigs(repoId: number) {
  const api = useApi();
  return useQuery({
    queryKey: ["model-configs", repoId, api.active?.id],
    queryFn: () =>
      api.get<ModelConfig[]>(`/api/v1/repos/${repoId}/config`),
    enabled: repoId > 0 && !!api.active,
  });
}

export function useUpsertModelConfig() {
  const api = useApi();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      repoId,
      stage,
      ...body
    }: {
      repoId: number;
      stage: string;
      provider: string;
      model: string;
      base_url?: string;
      max_tokens: number;
      temperature: number;
    }) =>
      api.put<ModelConfig>(
        `/api/v1/repos/${repoId}/config/${stage}`,
        body,
      ),
    onSuccess: (_data, vars) => {
      qc.invalidateQueries({ queryKey: ["model-configs", vars.repoId] });
    },
  });
}

export function useDeleteModelConfig() {
  const api = useApi();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      repoId,
      stage,
    }: { repoId: number; stage: string }) =>
      api.delete(`/api/v1/repos/${repoId}/config/${stage}`),
    onSuccess: (_data, vars) => {
      qc.invalidateQueries({ queryKey: ["model-configs", vars.repoId] });
    },
  });
}

export function useOrgModelConfigs() {
  const api = useApi();
  return useQuery({
    queryKey: ["org-model-configs", api.active?.id],
    queryFn: () =>
      api.get<ModelConfig[]>(
        `/api/v1/installations/${api.active?.id}/config`,
      ),
    enabled: !!api.active,
  });
}

export function useUpsertOrgModelConfig() {
  const api = useApi();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      stage,
      ...body
    }: {
      stage: string;
      provider: string;
      model: string;
      base_url?: string;
      max_tokens: number;
      temperature: number;
    }) =>
      api.put<ModelConfig>(
        `/api/v1/installations/${api.active?.id}/config/${stage}`,
        body,
      ),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["org-model-configs"] });
    },
  });
}

export function useDeleteOrgModelConfig() {
  const api = useApi();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ stage }: { stage: string }) =>
      api.delete(
        `/api/v1/installations/${api.active?.id}/config/${stage}`,
      ),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["org-model-configs"] });
    },
  });
}
