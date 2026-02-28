import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useAuth } from "@clerk/nextjs";
import { api } from "../api";
import type { Repo } from "../types";

export function useRepos() {
  const { getToken } = useAuth();
  return useQuery({
    queryKey: ["repos"],
    queryFn: async () => {
      const token = await getToken();
      return api.get<Repo[]>("/api/v1/repos", token ?? undefined);
    },
  });
}

export function useRepo(id: number) {
  const { getToken } = useAuth();
  return useQuery({
    queryKey: ["repos", id],
    queryFn: async () => {
      const token = await getToken();
      return api.get<Repo>(`/api/v1/repos/${id}`, token ?? undefined);
    },
    enabled: id > 0,
  });
}

export function useUpdateRepo() {
  const { getToken } = useAuth();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({
      id,
      ...body
    }: { id: number; enabled?: boolean; default_branch?: string }) => {
      const token = await getToken();
      return api.patch<Repo>(`/api/v1/repos/${id}`, body, token ?? undefined);
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["repos"] });
    },
  });
}
