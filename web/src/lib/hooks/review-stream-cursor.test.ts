import { describe, expect, it, vi } from "vitest";
import {
	buildReviewStreamURL,
	ReviewEventCursor,
	shouldReconnectReviewStream,
} from "./review-stream-cursor";

describe("ReviewEventCursor", () => {
	it("advances only after a durable event is processed and dedupes replay", () => {
		const cursor = new ReviewEventCursor();
		const process = vi.fn();

		expect(cursor.process({ id: 41 }, process)).toBe(true);
		expect(cursor.after).toBe(41);
		expect(cursor.process({ id: 41 }, process)).toBe(false);
		expect(process).toHaveBeenCalledTimes(1);
	});

	it("leaves failed events replayable and does not advance for ephemeral delivery", () => {
		const cursor = new ReviewEventCursor();
		expect(() =>
			cursor.process({ id: 7 }, () => {
				throw new Error("render failed");
			}),
		).toThrow("render failed");
		expect(cursor.after).toBe(0);

		const process = vi.fn();
		expect(cursor.process({ id: 7 }, process)).toBe(true);
		expect(cursor.process({}, process)).toBe(true);
		expect(cursor.after).toBe(7);
		expect(process).toHaveBeenCalledTimes(2);
	});
});

describe("buildReviewStreamURL", () => {
	it("sends the latest durable after cursor on reconnect", () => {
		const cursor = new ReviewEventCursor();
		cursor.process({ id: 91 }, () => {});
		const url = new URL(
			buildReviewStreamURL("https://api.argus.dev", "review-1", "a token", 23, cursor.after),
		);
		expect(url.protocol).toBe("wss:");
		expect(url.searchParams.get("token")).toBe("a token");
		expect(url.searchParams.get("installation_id")).toBe("23");
		expect(url.searchParams.get("after")).toBe("91");
	});
});

describe("logical review event delivery", () => {
	it("dedupes an ambiguous local fallback against its durable replay and advances the cursor", () => {
		const cursor = new ReviewEventCursor();
		const process = vi.fn();
		const local = { delivery_id: "0190f5f0-942d-7b58-9d29-d69f47f3f177" };
		const replay = { id: 73, delivery_id: local.delivery_id };

		expect(cursor.process(local, process)).toBe(true);
		expect(cursor.after).toBe(0);
		expect(cursor.process(replay, process)).toBe(false);
		expect(cursor.after).toBe(73);
		expect(process).toHaveBeenCalledTimes(1);
	});

	it("keeps logical identity tracking bounded and accepts legacy events", () => {
		const cursor = new ReviewEventCursor();
		const process = vi.fn();
		for (let id = 1; id <= 1001; id++) {
			cursor.process({ id, delivery_id: `delivery-${id}` }, process);
		}
		expect(cursor.process({ id: 2001, delivery_id: "delivery-1" }, process)).toBe(true);
		expect(cursor.process({ id: 2002 }, process)).toBe(true);
	});
});

describe("review stream reconnect policy", () => {
	it("reconnects a non-terminal stream after the backend retryable close", () => {
		expect(shouldReconnectReviewStream(1013, false)).toBe(true);
	});

	it("does not loop after a terminal event even when a proxy rewrites the clean close", () => {
		expect(shouldReconnectReviewStream(1006, true)).toBe(false);
		expect(shouldReconnectReviewStream(1000, false)).toBe(false);
	});
});
