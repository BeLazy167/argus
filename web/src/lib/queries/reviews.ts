import { useQueryClient } from "@tanstack/react-query";
import type { Review, ReviewComment } from "../types";
import { createAuthQuery, createAuthMutation, getApi } from "@/lib/query-kit";

type ReviewsVars = { repoId: number; limit?: number; offset?: number };

export const useReviews = createAuthQuery<Review[], ReviewsVars>({
  queryKey: ["reviews"],
  fetcher: ({ repoId, limit = 20, offset = 0 }, ctx) => {
    const path = repoId > 0
      ? `/api/v1/repos/${repoId}/reviews?limit=${limit}&offset=${offset}`
      : `/api/v1/reviews?limit=${limit}&offset=${offset}`;
    return getApi(ctx).get<Review[]>(path);
  },
  refetchOnWindowFocus: true,
});

type ReviewVars = { id: string };

export const useReview = createAuthQuery<{ review: Review; comments: ReviewComment[] }, ReviewVars>({
  queryKey: ["review"],
  fetcher: ({ id }, ctx) =>
    getApi(ctx).get<{ review: Review; comments: ReviewComment[] }>(`/api/v1/reviews/${id}`),
  refetchOnWindowFocus: true,
});

type TriggerReviewVars = { repoId: number; prNumber: number };

const useTriggerReviewMutation = createAuthMutation<unknown, TriggerReviewVars>({
  mutationFn: ({ repoId, prNumber }, ctx) =>
    getApi(ctx).post(`/api/v1/repos/${repoId}/reviews`, { pr_number: prNumber }),
});

export const useTriggerReview = () => {
  const qc = useQueryClient();
  return useTriggerReviewMutation({
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: useReviews.getKey() });
      qc.invalidateQueries({ queryKey: ["stats"] });
    },
    onError: (err) => console.error("[trigger-review] failed:", err.message),
  });
};

const useRetryReviewMutation = createAuthMutation<unknown, string>({
  mutationFn: (reviewId, ctx) => getApi(ctx).post(`/api/v1/reviews/${reviewId}/retry`, {}),
});

export const useRetryReview = () => {
  const qc = useQueryClient();
  return useRetryReviewMutation({
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: useReviews.getKey() });
      qc.invalidateQueries({ queryKey: ["stats"] });
    },
    onError: (err) => console.error("[retry-review] failed:", err.message),
  });
};

const useCancelReviewMutation = createAuthMutation<unknown, string>({
  mutationFn: (reviewId, ctx) => getApi(ctx).post(`/api/v1/reviews/${reviewId}/cancel`, {}),
});

export const useCancelReview = () => {
  const qc = useQueryClient();
  return useCancelReviewMutation({
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: useReviews.getKey() });
      qc.invalidateQueries({ queryKey: useReview.getKey() });
      qc.invalidateQueries({ queryKey: ["stats"] });
    },
    onError: (err) => console.error("[cancel-review] failed:", err.message),
  });
};
