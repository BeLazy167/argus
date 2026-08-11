import { describe, expect, it, vi } from "vitest";
import { buildReviewStreamURL, ReviewEventCursor } from "./review-stream-cursor";

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
