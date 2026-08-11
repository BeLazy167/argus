const MAX_TRACKED_EVENT_IDS = 1000;

type DurableEvent = { id?: number };

/**
 * Owns one review stream's durable replay position. The handler runs before
 * the cursor advances, so a failed handler remains replayable on reconnect.
 */
export class ReviewEventCursor {
	private afterID = 0;
	private readonly seen = new Set<number>();
	private readonly seenOrder: number[] = [];

	get after(): number {
		return this.afterID;
	}

	process(event: DurableEvent, handler: () => void): boolean {
		const id = event.id;
		const durable = typeof id === "number" && Number.isSafeInteger(id) && id > 0;
		if (durable && this.seen.has(id)) return false;

		handler();
		if (!durable) return true;

		this.seen.add(id);
		this.seenOrder.push(id);
		this.afterID = Math.max(this.afterID, id);
		if (this.seenOrder.length > MAX_TRACKED_EVENT_IDS) {
			const oldest = this.seenOrder.shift();
			if (oldest !== undefined) this.seen.delete(oldest);
		}
		return true;
	}
}

export function buildReviewStreamURL(
	apiURL: string,
	reviewID: string,
	token: string,
	installationID: number,
	afterID: number,
): string {
	const wsBase = apiURL.replace(/^http/, "ws");
	const params = new URLSearchParams({
		token,
		installation_id: String(installationID),
		after: String(afterID),
	});
	return `${wsBase}/api/v1/reviews/${reviewID}/stream?${params.toString()}`;
}
