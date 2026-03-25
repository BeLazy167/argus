import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import type { Review, ReviewComment } from "../types";
import { useApi } from "@/lib/hooks/use-api";

export function useReviews(repoId: number, limit = 20, offset = 0) {
  const api = useApi();
  return useQuery({
    queryKey: ["reviews", repoId, limit, offset, api.active?.id],
    queryFn: () => {
      const path = repoId > 0
        ? `/api/v1/repos/${repoId}/reviews?limit=${limit}&offset=${offset}`
        : `/api/v1/reviews?limit=${limit}&offset=${offset}`;
      return api.get<Review[]>(path);
    },
    enabled: !!api.active,
  });
}

export function useReview(id: string) {
  const api = useApi();
  return useQuery({
    queryKey: ["review", id, api.active?.id],
    queryFn: () =>
      api.get<{ review: Review; comments: ReviewComment[] }>(
        `/api/v1/reviews/${id}`,
      ),
    enabled: !!id && !!api.active,
  });
}

export function useTriggerReview() {
  const api = useApi();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      repoId,
      prNumber,
    }: { repoId: number; prNumber: number }) =>
      api.post(
        `/api/v1/repos/${repoId}/reviews`,
        { pr_number: prNumber },
      ),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["reviews"] });
      qc.invalidateQueries({ queryKey: ["stats"] });
    },
  });
}

export function useRetryReview() {
  const api = useApi();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (reviewId: string) =>
      api.post(`/api/v1/reviews/${reviewId}/retry`, {}),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["reviews"] });
      qc.invalidateQueries({ queryKey: ["stats"] });
    },
  });
}
