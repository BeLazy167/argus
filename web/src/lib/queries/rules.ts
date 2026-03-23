import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import type { Rule } from "../types";
import { useApi } from "@/lib/hooks/use-api";

export function useRules() {
  const api = useApi();
  return useQuery({
    queryKey: ["rules", api.active?.id],
    queryFn: () => api.get<Rule[]>("/api/v1/rules"),
    enabled: !!api.active,
  });
}

export function useCreateRule() {
  const api = useApi();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (
      body: Pick<Rule, "category" | "content" | "priority"> & {
        enabled?: boolean;
      },
    ) => api.post<Rule>("/api/v1/rules", body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["rules"] }),
  });
}

export function useUpdateRule() {
  const api = useApi();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      id,
      ...body
    }: {
      id: number;
      category?: string;
      content?: string;
      priority?: number;
      enabled?: boolean;
    }) => api.put<Rule>(`/api/v1/rules/${id}`, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["rules"] }),
  });
}

export function useDeleteRule() {
  const api = useApi();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api.delete(`/api/v1/rules/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["rules"] }),
  });
}
