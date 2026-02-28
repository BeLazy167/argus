import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useAuth } from "@clerk/nextjs";
import { api } from "../api";
import type { Repo } from "../types";
import { useInstallation } from "@/providers/installation-provider";

export function useRepos() {
  const { getToken } = useAuth();
  const { active } = useInstallation();
  return useQuery({
    queryKey: ["repos", active?.id],
    queryFn: async () => {
      const token = await getToken();
      return api.get<Repo[]>("/api/v1/repos", token ?? undefined, active?.id);
    },
    enabled: !!active,
  });
}

export function useRepo(id: number) {
  const { getToken } = useAuth();
  const { active } = useInstallation();
  return useQuery({
    queryKey: ["repos", id, active?.id],
    queryFn: async () => {
      const token = await getToken();
      return api.get<Repo>(`/api/v1/repos/${id}`, token ?? undefined, active?.id);
    },
    enabled: id > 0 && !!active,
  });
}

export function useUpdateRepo() {
  const { getToken } = useAuth();
  const { active } = useInstallation();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({
      id,
      ...body
    }: { id: number; enabled?: boolean; default_branch?: string }) => {
      const token = await getToken();
      return api.patch<Repo>(`/api/v1/repos/${id}`, body, token ?? undefined, active?.id);
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["repos"] });
    },
  });
}
