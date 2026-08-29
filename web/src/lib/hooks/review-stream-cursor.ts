const MAX_TRACKED_EVENT_IDENTITIES = 1000;

type DurableEvent = { id?: number; delivery_id?: string };

/**
 * Owns one review stream's durable replay position. The handler runs before
 * the cursor advances, so a failed handler remains replayable on reconnect.
 */
export class ReviewEventCursor {
	private afterID = 0;
	private readonly seenIDs = new Set<number>();
	private readonly seenIDOrder: number[] = [];
	private readonly seenDeliveries = new Set<string>();
	private readonly seenDeliveryOrder: string[] = [];

	get after(): number {
		return this.afterID;
	}

	process(event: DurableEvent, handler: () => void): boolean {
		const id = event.id;
		const durable = typeof id === "number" && Number.isSafeInteger(id) && id > 0;
		const deliveryID = validDeliveryID(event.delivery_id);
		const duplicateID = durable && this.seenIDs.has(id);
		const duplicateDelivery = deliveryID !== undefined && this.seenDeliveries.has(deliveryID);

		if (duplicateID || duplicateDelivery) {
			// An ambiguous committed publish first arrives locally with ID=0. Its
			// durable replay must advance the SQL cursor even though the handler is
			// intentionally skipped for the stable logical delivery identity.
			if (durable) this.rememberDurableID(id);
			if (deliveryID !== undefined) this.rememberDeliveryID(deliveryID);
			return false;
		}

		handler();
		if (deliveryID !== undefined) this.rememberDeliveryID(deliveryID);
		if (durable) this.rememberDurableID(id);
		return true;
	}

	private rememberDurableID(id: number): void {
		this.afterID = Math.max(this.afterID, id);
		if (this.seenIDs.has(id)) return;
		this.seenIDs.add(id);
		this.seenIDOrder.push(id);
		if (this.seenIDOrder.length > MAX_TRACKED_EVENT_IDENTITIES) {
			const oldest = this.seenIDOrder.shift();
			if (oldest !== undefined) this.seenIDs.delete(oldest);
		}
	}

	private rememberDeliveryID(id: string): void {
		if (this.seenDeliveries.has(id)) return;
		this.seenDeliveries.add(id);
		this.seenDeliveryOrder.push(id);
		if (this.seenDeliveryOrder.length > MAX_TRACKED_EVENT_IDENTITIES) {
			const oldest = this.seenDeliveryOrder.shift();
			if (oldest !== undefined) this.seenDeliveries.delete(oldest);
		}
	}
}

function validDeliveryID(value: string | undefined): string | undefined {
	return typeof value === "string" && value.length > 0 && value.length <= 128 ? value : undefined;
}

export function shouldReconnectReviewStream(closeCode: number, terminal: boolean): boolean {
	return !terminal && closeCode !== 1000;
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
