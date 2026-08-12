import { QueryClient } from "@tanstack/react-query";
import { describe, expect, expectTypeOf, it, vi } from "vitest";
import type { ReviewMinorNote } from "../generated/api-types";
import { reconcileTerminalReview, reviewQueryKeys, useReview, type ReviewDetail } from "./reviews";

describe("review query cache coordinates", () => {
	it("keeps minor notes on the generated response type", () => {
		expectTypeOf<ReviewDetail["minor_notes"]>().toEqualTypeOf<ReviewMinorNote[]>();
	});

	it("uses one detail key for reads and live updates", () => {
		const id = "review-123";
		const client = new QueryClient();
		const detail = { review: { id, status: "pending" }, comments: [] };

		expect(reviewQueryKeys.detail(id)).toEqual(useReview.getKey({ id }));

		client.setQueryData(reviewQueryKeys.detail(id), detail);
		client.setQueryData(reviewQueryKeys.detail(id), (current: typeof detail | undefined) =>
			current ? { ...current, review: { ...current.review, status: "in_progress" } } : current,
		);

		expect(client.getQueryData(useReview.getKey({ id }))).toMatchObject({
			review: { status: "in_progress" },
		});
	});

	it("invalidates the exact detail and every review list at terminal status", () => {
		const invalidateQueries = vi.fn();

		reconcileTerminalReview({ invalidateQueries } as unknown as QueryClient, "review-123");

		expect(invalidateQueries).toHaveBeenNthCalledWith(1, {
			queryKey: reviewQueryKeys.detail("review-123"),
		});
		expect(invalidateQueries).toHaveBeenNthCalledWith(2, {
			queryKey: reviewQueryKeys.lists(),
		});
	});
});
