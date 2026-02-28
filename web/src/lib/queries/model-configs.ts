import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useAuth } from "@clerk/nextjs";
import { api } from "../api";
import type { ModelConfig } from "../types";

export function useModelConfigs(repoId: number) {
  const { getToken } = useAuth();
  return useQuery({
    queryKey: ["model-configs", repoId],
    queryFn: async () => {
      const token = await getToken();
      return api.get<ModelConfig[]>(
        `/api/v1/repos/${repoId}/config`,
        token ?? undefined,
      );
    },
    enabled: repoId > 0,
  });
}

export function useUpsertModelConfig() {
  const { getToken } = useAuth();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({
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
    }) => {
      const token = await getToken();
      return api.put<ModelConfig>(
        `/api/v1/repos/${repoId}/config/${stage}`,
        body,
        token ?? undefined,
      );
    },
    onSuccess: (_data, vars) => {
      qc.invalidateQueries({ queryKey: ["model-configs", vars.repoId] });
    },
  });
}

export function useDeleteModelConfig() {
  const { getToken } = useAuth();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({
      repoId,
      stage,
    }: { repoId: number; stage: string }) => {
      const token = await getToken();
      return api.delete(
        `/api/v1/repos/${repoId}/config/${stage}`,
        token ?? undefined,
      );
    },
    onSuccess: (_data, vars) => {
      qc.invalidateQueries({ queryKey: ["model-configs", vars.repoId] });
    },
  });
}
