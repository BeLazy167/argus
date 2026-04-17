import { useQueryClient } from "@tanstack/react-query";
import type { PromptTemplate } from "../types";
import { createAuthQuery, createAuthMutation, getApi } from "@/lib/query-kit";

export const usePrompts = createAuthQuery<PromptTemplate[], { repoId: number }>({
  queryKey: ["prompts"],
  fetcher: ({ repoId }, ctx) => getApi(ctx).get<PromptTemplate[]>(`/api/v1/repos/${repoId}/prompts`),
  staleTime: 2 * 60 * 1000,
});

export const useDefaultPrompts = createAuthQuery<PromptTemplate[]>({
  queryKey: ["prompts-defaults"],
  fetcher: (_vars, ctx) => getApi(ctx).get<PromptTemplate[]>("/api/v1/prompts/defaults"),
  staleTime: 2 * 60 * 1000,
});

type UpsertPromptVars = { repoId: number; stage: string; prompt_text: string };

const useUpsertPromptMutation = createAuthMutation<PromptTemplate, UpsertPromptVars>({
  mutationFn: ({ repoId, stage, prompt_text }, ctx) =>
    getApi(ctx).put<PromptTemplate>(`/api/v1/repos/${repoId}/prompts/${stage}`, { prompt_text }),
});

export const useUpsertPrompt = () => {
  const qc = useQueryClient();
  return useUpsertPromptMutation({
    onSuccess: (_data, vars) => qc.invalidateQueries({ queryKey: usePrompts.getKey({ repoId: vars.repoId }) }),
    onError: (err) => console.error("[upsert-prompt] failed:", err.message),
  });
};

type DeletePromptVars = { repoId: number; stage: string };

const useDeletePromptMutation = createAuthMutation<unknown, DeletePromptVars>({
  mutationFn: ({ repoId, stage }, ctx) => getApi(ctx).delete(`/api/v1/repos/${repoId}/prompts/${stage}`),
});

export const useDeletePrompt = () => {
  const qc = useQueryClient();
  return useDeletePromptMutation({
    onSuccess: (_data, vars) => qc.invalidateQueries({ queryKey: usePrompts.getKey({ repoId: vars.repoId }) }),
    onError: (err) => console.error("[delete-prompt] failed:", err.message),
  });
};
