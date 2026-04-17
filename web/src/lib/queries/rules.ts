import { useQueryClient } from "@tanstack/react-query";
import type { Rule } from "../types";
import { createAuthQuery, createAuthMutation, getApi } from "@/lib/query-kit";

export const useRules = createAuthQuery<Rule[]>({
  queryKey: ["rules"],
  fetcher: (_vars, ctx) => getApi(ctx).get<Rule[]>("/api/v1/rules"),
  staleTime: 2 * 60 * 1000,
});

type CreateRuleVars = Pick<Rule, "category" | "content" | "priority"> & { enabled?: boolean };

const useCreateRuleMutation = createAuthMutation<Rule, CreateRuleVars>({
  mutationFn: (body, ctx) => getApi(ctx).post<Rule>("/api/v1/rules", body),
});

export const useCreateRule = () => {
  const qc = useQueryClient();
  return useCreateRuleMutation({
    onSuccess: () => qc.invalidateQueries({ queryKey: useRules.getKey() }),
    onError: (err) => console.error("[create-rule] failed:", err.message),
  });
};

type UpdateRuleVars = {
  id: number;
  category?: string;
  content?: string;
  priority?: number;
  enabled?: boolean;
};

const useUpdateRuleMutation = createAuthMutation<Rule, UpdateRuleVars>({
  mutationFn: ({ id, ...body }, ctx) => getApi(ctx).put<Rule>(`/api/v1/rules/${id}`, body),
});

export const useUpdateRule = () => {
  const qc = useQueryClient();
  return useUpdateRuleMutation({
    onSuccess: () => qc.invalidateQueries({ queryKey: useRules.getKey() }),
    onError: (err) => console.error("[update-rule] failed:", err.message),
  });
};

const useDeleteRuleMutation = createAuthMutation<unknown, number>({
  mutationFn: (id, ctx) => getApi(ctx).delete(`/api/v1/rules/${id}`),
});

export const useDeleteRule = () => {
  const qc = useQueryClient();
  return useDeleteRuleMutation({
    onSuccess: () => qc.invalidateQueries({ queryKey: useRules.getKey() }),
    onError: (err) => console.error("[delete-rule] failed:", err.message),
  });
};
