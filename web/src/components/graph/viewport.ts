/**
 * Pure viewport math for the architecture graph's initial view.
 *
 * These helpers are deliberately React- and React-Flow-free so the initial
 * viewport can be computed from the layout we generate (absolute dagre
 * positions) instead of read back from React Flow's internal store. Reading the
 * store via `fitView({ nodes })` proved fragile on large repos: the top-risk
 * "focus" set expands across a ~35k-px-wide layout, and the resulting fit landed
 * the viewport on empty canvas. Computing bounds from our own layout output is
 * deterministic and unit-testable.
 */

/** Axis-aligned rectangle in flow coordinates. */
export type Rect = { x: number; y: number; width: number; height: number };

/** A node placed in flow space: absolute top-left position plus box size. */
export type PlacedNode = { x: number; y: number; width: number; height: number };

/**
 * Union bounding box of a set of placed nodes, or null when the set is empty.
 */
export function boundsOfNodes(nodes: PlacedNode[]): Rect | null {
	let minX = Number.POSITIVE_INFINITY;
	let minY = Number.POSITIVE_INFINITY;
	let maxX = Number.NEGATIVE_INFINITY;
	let maxY = Number.NEGATIVE_INFINITY;
	for (const n of nodes) {
		minX = Math.min(minX, n.x);
		minY = Math.min(minY, n.y);
		maxX = Math.max(maxX, n.x + n.width);
		maxY = Math.max(maxY, n.y + n.height);
	}
	if (!Number.isFinite(minX)) return null;
	return { x: minX, y: minY, width: maxX - minX, height: maxY - minY };
}

function clamp(value: number, lo: number, hi: number): number {
	return Math.min(Math.max(value, lo), hi);
}

/**
 * Viewport transform ({ x, y, zoom }) that frames `bounds` centered in a pane of
 * `pane` px, leaving `padding` fraction of slack on each axis, with zoom clamped
 * to [minZoom, maxZoom].
 *
 * The center-based translate is what fixes the stranded-viewport bug: even when
 * the bounds are far wider than minZoom can show (so zoom pins to minZoom), the
 * viewport still centers on the bounds' centroid — landing on populated canvas
 * rather than the near-origin empty margin the old fitView path produced.
 */
export function viewportForBounds(
	bounds: Rect,
	pane: { width: number; height: number },
	opts: { padding: number; minZoom: number; maxZoom: number },
): { x: number; y: number; zoom: number } {
	const bw = Math.max(bounds.width, 1);
	const bh = Math.max(bounds.height, 1);
	const scaleX = pane.width / (bw * (1 + opts.padding * 2));
	const scaleY = pane.height / (bh * (1 + opts.padding * 2));
	const zoom = clamp(Math.min(scaleX, scaleY), opts.minZoom, opts.maxZoom);
	const cx = bounds.x + bounds.width / 2;
	const cy = bounds.y + bounds.height / 2;
	return {
		x: pane.width / 2 - cx * zoom,
		y: pane.height / 2 - cy * zoom,
		zoom,
	};
}

/** A viewport transform is usable only if every field is finite and zoom > 0. */
export function isFiniteViewport(vp: { x: number; y: number; zoom: number }): boolean {
	return Number.isFinite(vp.x) && Number.isFinite(vp.y) && Number.isFinite(vp.zoom) && vp.zoom > 0;
}

/**
 * True when the transform is the untouched React Flow default (translate 0,0 /
 * scale 1). Used to confirm an imperative viewport write actually took effect —
 * a write that no-ops (e.g. panZoom not yet ready) leaves the identity transform
 * behind, so we must NOT treat the one-shot initial view as done.
 */
export function isIdentityViewport(
	vp: { x: number; y: number; zoom: number },
	eps = 1e-3,
): boolean {
	return Math.abs(vp.x) < eps && Math.abs(vp.y) < eps && Math.abs(vp.zoom - 1) < eps;
}

/** A finite, positive pane size, or null if either dimension is unusable. */
export function usablePane(
	width: number | undefined,
	height: number | undefined,
): { width: number; height: number } | null {
	if (typeof width !== "number" || !Number.isFinite(width) || width <= 0) return null;
	if (typeof height !== "number" || !Number.isFinite(height) || height <= 0) return null;
	return { width, height };
}
