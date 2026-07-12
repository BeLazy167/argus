import { useQueryClient } from "@tanstack/react-query";
import type { ModelConfig } from "../types";
import { createAuthQuery, createAuthMutation, getApi } from "@/lib/query-kit";

export type TestResult = {
  success: boolean;
  error?: string;
  response?: string;
  latency_ms: number;
  tokens?: number;
};

type TestConfigVars = { provider: string; model: string };

export const useTestConfig = createAuthMutation<TestResult, TestConfigVars>({
  mutationFn: (body, ctx) => {
    const api = getApi(ctx);
    return api.post<TestResult>(`/api/v1/installations/${api.active?.id}/test-config`, body);
  },
  onError: (err) => console.error("[test-config] failed:", err.message),
});

export const useModelConfigs = createAuthQuery<ModelConfig[], { repoId: number }>({
  queryKey: ["model-configs"],
  fetcher: ({ repoId }, ctx) => getApi(ctx).get<ModelConfig[]>(`/api/v1/repos/${repoId}/config`),
  staleTime: 5 * 60 * 1000,
});

type UpsertModelConfigVars = {
  repoId: number;
  stage: string;
  provider: string;
  model: string;
  base_url?: string;
  max_tokens: number;
  temperature: number;
};

const useUpsertModelConfigMutation = createAuthMutation<ModelConfig, UpsertModelConfigVars>({
  mutationFn: ({ repoId, stage, ...body }, ctx) =>
    getApi(ctx).put<ModelConfig>(`/api/v1/repos/${repoId}/config/${stage}`, body),
});

export const useUpsertModelConfig = () => {
  const qc = useQueryClient();
  return useUpsertModelConfigMutation({
    onSuccess: (_data, vars) =>
      qc.invalidateQueries({ queryKey: useModelConfigs.getKey({ repoId: vars.repoId }) }),
    onError: (err) => console.error("[upsert-model-config] failed:", err.message),
  });
};

type DeleteModelConfigVars = { repoId: number; stage: string };

const useDeleteModelConfigMutation = createAuthMutation<unknown, DeleteModelConfigVars>({
  mutationFn: ({ repoId, stage }, ctx) => getApi(ctx).delete(`/api/v1/repos/${repoId}/config/${stage}`),
});

export const useDeleteModelConfig = () => {
  const qc = useQueryClient();
  return useDeleteModelConfigMutation({
    onSuccess: (_data, vars) =>
      qc.invalidateQueries({ queryKey: useModelConfigs.getKey({ repoId: vars.repoId }) }),
    onError: (err) => console.error("[delete-model-config] failed:", err.message),
  });
};

export const useOrgModelConfigs = createAuthQuery<ModelConfig[]>({
  queryKey: ["org-model-configs"],
  fetcher: (_vars, ctx) => {
    const api = getApi(ctx);
    return api.get<ModelConfig[]>(`/api/v1/installations/${api.active?.id}/config`);
  },
  staleTime: 5 * 60 * 1000,
});

type UpsertOrgModelConfigVars = {
  stage: string;
  provider: string;
  model: string;
  base_url?: string;
  max_tokens: number;
  temperature: number;
};

/**
 * Base org upsert WITHOUT per-call cache invalidation. Batch flows (e.g.
 * "Apply to all stages") await several of these via mutateAsync and refresh
 * the org config list once at the end; single-stage saves should use
 * useUpsertOrgModelConfig.
 */
export const useUpsertOrgModelConfigMutation = createAuthMutation<ModelConfig, UpsertOrgModelConfigVars>({
  mutationFn: ({ stage, ...body }, ctx) => {
    const api = getApi(ctx);
    return api.put<ModelConfig>(`/api/v1/installations/${api.active?.id}/config/${stage}`, body);
  },
});

export const useUpsertOrgModelConfig = () => {
  const qc = useQueryClient();
  return useUpsertOrgModelConfigMutation({
    onSuccess: () => qc.invalidateQueries({ queryKey: useOrgModelConfigs.getKey() }),
    onError: (err) => console.error("[upsert-org-model-config] failed:", err.message),
  });
};

const useDeleteOrgModelConfigMutation = createAuthMutation<unknown, { stage: string }>({
  mutationFn: ({ stage }, ctx) => {
    const api = getApi(ctx);
    return api.delete(`/api/v1/installations/${api.active?.id}/config/${stage}`);
  },
});

export const useDeleteOrgModelConfig = () => {
  const qc = useQueryClient();
  return useDeleteOrgModelConfigMutation({
    onSuccess: () => qc.invalidateQueries({ queryKey: useOrgModelConfigs.getKey() }),
    onError: (err) => console.error("[delete-org-model-config] failed:", err.message),
  });
};
