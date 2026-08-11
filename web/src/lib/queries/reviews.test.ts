import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import { reconcileTerminalReview, reviewQueryKeys, useReview } from "./reviews";

describe("review query cache coordinates", () => {
  it("uses one detail key for reads and live updates", () => {
    const id = "review-123";
    const client = new QueryClient();
    const detail = { review: { id, status: "pending" }, comments: [] };

    expect(reviewQueryKeys.detail(id)).toEqual(useReview.getKey({ id }));

    client.setQueryData(reviewQueryKeys.detail(id), detail);
    client.setQueryData(reviewQueryKeys.detail(id), (current: typeof detail | undefined) =>
      current
        ? { ...current, review: { ...current.review, status: "in_progress" } }
        : current,
    );

    expect(client.getQueryData(useReview.getKey({ id }))).toMatchObject({
      review: { status: "in_progress" },
    });
  });

  it("invalidates the exact detail and every review list at terminal status", () => {
    const invalidateQueries = vi.fn();

    reconcileTerminalReview(
      { invalidateQueries } as unknown as QueryClient,
      "review-123",
    );

    expect(invalidateQueries).toHaveBeenNthCalledWith(1, {
      queryKey: reviewQueryKeys.detail("review-123"),
    });
    expect(invalidateQueries).toHaveBeenNthCalledWith(2, {
      queryKey: reviewQueryKeys.lists(),
    });
  });
});
