import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useAuth } from "@clerk/nextjs";
import { api } from "../api";
import type { Pattern, PatternStat } from "../types";
import { useInstallation } from "@/providers/installation-provider";

export function usePatterns() {
  const { getToken } = useAuth();
  const { active } = useInstallation();
  return useQuery({
    queryKey: ["patterns", active?.id],
    queryFn: async () => {
      const token = await getToken();
      return api.get<Pattern[]>("/api/v1/patterns", token ?? undefined, active?.id);
    },
    enabled: !!active,
  });
}

export function useCreatePattern() {
  const { getToken } = useAuth();
  const { active } = useInstallation();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (body: { content: string; repo_id?: number }) => {
      const token = await getToken();
      return api.post<Pattern>(
        "/api/v1/patterns",
        { ...body, installation_id: active?.id },
        token ?? undefined,
        active?.id,
      );
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ["patterns"] }),
  });
}

export function usePatternStats() {
  const { getToken } = useAuth();
  const { active } = useInstallation();
  return useQuery({
    queryKey: ["pattern-stats", active?.id],
    queryFn: async () => {
      const token = await getToken();
      return api.get<PatternStat[]>("/api/v1/patterns/stats", token ?? undefined, active?.id);
    },
    enabled: !!active,
  });
}

export function useDeletePattern() {
  const { getToken } = useAuth();
  const { active } = useInstallation();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (id: number) => {
      const token = await getToken();
      return api.delete(`/api/v1/patterns/${id}`, token ?? undefined, active?.id);
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ["patterns"] }),
  });
}
