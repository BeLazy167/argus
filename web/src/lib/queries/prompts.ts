import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useAuth } from "@clerk/nextjs";
import { api } from "../api";
import type { PromptTemplate } from "../types";
import { useInstallation } from "@/providers/installation-provider";

export function usePrompts(repoId: number) {
  const { getToken } = useAuth();
  const { active } = useInstallation();
  return useQuery({
    queryKey: ["prompts", repoId, active?.id],
    queryFn: async () => {
      const token = await getToken();
      return api.get<PromptTemplate[]>(
        `/api/v1/repos/${repoId}/prompts`,
        token ?? undefined,
        active?.id,
      );
    },
    enabled: repoId > 0 && !!active,
  });
}

export function useDefaultPrompts() {
  const { getToken } = useAuth();
  const { active } = useInstallation();
  return useQuery({
    queryKey: ["prompts-defaults", active?.id],
    queryFn: async () => {
      const token = await getToken();
      return api.get<PromptTemplate[]>(
        "/api/v1/prompts/defaults",
        token ?? undefined,
        active?.id,
      );
    },
    enabled: !!active,
  });
}

export function useUpsertPrompt() {
  const { getToken } = useAuth();
  const { active } = useInstallation();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({
      repoId,
      stage,
      prompt_text,
    }: {
      repoId: number;
      stage: string;
      prompt_text: string;
    }) => {
      const token = await getToken();
      return api.put<PromptTemplate>(
        `/api/v1/repos/${repoId}/prompts/${stage}`,
        { prompt_text },
        token ?? undefined,
        active?.id,
      );
    },
    onSuccess: (_data, vars) => {
      qc.invalidateQueries({ queryKey: ["prompts", vars.repoId] });
    },
  });
}

export function useDeletePrompt() {
  const { getToken } = useAuth();
  const { active } = useInstallation();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({
      repoId,
      stage,
    }: { repoId: number; stage: string }) => {
      const token = await getToken();
      return api.delete(
        `/api/v1/repos/${repoId}/prompts/${stage}`,
        token ?? undefined,
        active?.id,
      );
    },
    onSuccess: (_data, vars) => {
      qc.invalidateQueries({ queryKey: ["prompts", vars.repoId] });
    },
  });
}
