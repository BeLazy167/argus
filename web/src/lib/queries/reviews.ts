import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useAuth } from "@clerk/nextjs";
import { api } from "../api";
import type { Review, ReviewComment } from "../types";
import { useInstallation } from "@/providers/installation-provider";

export function useReviews(repoId: number, limit = 20, offset = 0) {
  const { getToken } = useAuth();
  const { active } = useInstallation();
  return useQuery({
    queryKey: ["reviews", repoId, limit, offset, active?.id],
    queryFn: async () => {
      const token = await getToken();
      return api.get<Review[]>(
        `/api/v1/repos/${repoId}/reviews?limit=${limit}&offset=${offset}`,
        token ?? undefined,
        active?.id,
      );
    },
    enabled: repoId > 0 && !!active,
  });
}

export function useReview(id: string) {
  const { getToken } = useAuth();
  const { active } = useInstallation();
  return useQuery({
    queryKey: ["review", id, active?.id],
    queryFn: async () => {
      const token = await getToken();
      return api.get<{ review: Review; comments: ReviewComment[] }>(
        `/api/v1/reviews/${id}`,
        token ?? undefined,
        active?.id,
      );
    },
    enabled: !!id && !!active,
  });
}

export function useTriggerReview() {
  const { getToken } = useAuth();
  const { active } = useInstallation();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({
      repoId,
      prNumber,
    }: { repoId: number; prNumber: number }) => {
      const token = await getToken();
      return api.post(
        `/api/v1/repos/${repoId}/reviews`,
        { pr_number: prNumber },
        token ?? undefined,
        active?.id,
      );
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["reviews"] });
      qc.invalidateQueries({ queryKey: ["stats"] });
    },
  });
}

export function useRetryReview() {
  const { getToken } = useAuth();
  const { active } = useInstallation();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (reviewId: string) => {
      const token = await getToken();
      return api.post(
        `/api/v1/reviews/${reviewId}/retry`,
        {},
        token ?? undefined,
        active?.id,
      );
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["reviews"] });
      qc.invalidateQueries({ queryKey: ["stats"] });
    },
  });
}
