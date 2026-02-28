import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useAuth } from "@clerk/nextjs";
import { api } from "../api";
import type { Review, ReviewComment } from "../types";

export function useReviews(repoId: number, limit = 20, offset = 0) {
  const { getToken } = useAuth();
  return useQuery({
    queryKey: ["reviews", repoId, limit, offset],
    queryFn: async () => {
      const token = await getToken();
      return api.get<Review[]>(
        `/api/v1/repos/${repoId}/reviews?limit=${limit}&offset=${offset}`,
        token ?? undefined,
      );
    },
    enabled: repoId > 0,
  });
}

export function useReview(id: string) {
  const { getToken } = useAuth();
  return useQuery({
    queryKey: ["review", id],
    queryFn: async () => {
      const token = await getToken();
      return api.get<{ review: Review; comments: ReviewComment[] }>(
        `/api/v1/reviews/${id}`,
        token ?? undefined,
      );
    },
    enabled: !!id,
  });
}

export function useTriggerReview() {
  const { getToken } = useAuth();
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
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (reviewId: string) => {
      const token = await getToken();
      return api.post(
        `/api/v1/reviews/${reviewId}/retry`,
        {},
        token ?? undefined,
      );
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["reviews"] });
      qc.invalidateQueries({ queryKey: ["stats"] });
    },
  });
}
