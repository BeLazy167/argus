import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { PromptTemplate } from "../types";
import { useApi } from "@/lib/hooks/use-api";

export function usePrompts(repoId: number) {
  const api = useApi();
  return useQuery({
    queryKey: ["prompts", repoId, api.active?.id],
    queryFn: () =>
      api.get<PromptTemplate[]>(`/api/v1/repos/${repoId}/prompts`),
    enabled: repoId > 0 && !!api.active,
  });
}

export function useDefaultPrompts() {
  const api = useApi();
  return useQuery({
    queryKey: ["prompts-defaults", api.active?.id],
    queryFn: () =>
      api.get<PromptTemplate[]>("/api/v1/prompts/defaults"),
    enabled: !!api.active,
  });
}

export function useUpsertPrompt() {
  const api = useApi();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      repoId,
      stage,
      prompt_text,
    }: {
      repoId: number;
      stage: string;
      prompt_text: string;
    }) =>
      api.put<PromptTemplate>(
        `/api/v1/repos/${repoId}/prompts/${stage}`,
        { prompt_text },
      ),
    onSuccess: (_data, vars) => {
      qc.invalidateQueries({ queryKey: ["prompts", vars.repoId] });
    },
  });
}

export function useDeletePrompt() {
  const api = useApi();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      repoId,
      stage,
    }: { repoId: number; stage: string }) =>
      api.delete(`/api/v1/repos/${repoId}/prompts/${stage}`),
    onSuccess: (_data, vars) => {
      qc.invalidateQueries({ queryKey: ["prompts", vars.repoId] });
    },
  });
}
