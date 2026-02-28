import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useAuth } from "@clerk/nextjs";
import { api } from "../api";
import type { Rule } from "../types";

export function useRules() {
  const { getToken } = useAuth();
  return useQuery({
    queryKey: ["rules"],
    queryFn: async () => {
      const token = await getToken();
      return api.get<Rule[]>("/api/v1/rules", token ?? undefined);
    },
  });
}

export function useCreateRule() {
  const { getToken } = useAuth();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (
      body: Pick<Rule, "category" | "content" | "priority"> & {
        enabled?: boolean;
      },
    ) => {
      const token = await getToken();
      return api.post<Rule>("/api/v1/rules", body, token ?? undefined);
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ["rules"] }),
  });
}

export function useUpdateRule() {
  const { getToken } = useAuth();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({
      id,
      ...body
    }: {
      id: number;
      category?: string;
      content?: string;
      priority?: number;
      enabled?: boolean;
    }) => {
      const token = await getToken();
      return api.put<Rule>(`/api/v1/rules/${id}`, body, token ?? undefined);
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ["rules"] }),
  });
}

export function useDeleteRule() {
  const { getToken } = useAuth();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (id: number) => {
      const token = await getToken();
      return api.delete(`/api/v1/rules/${id}`, token ?? undefined);
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ["rules"] }),
  });
}
