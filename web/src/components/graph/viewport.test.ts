import assert from "node:assert/strict";
import { test } from "node:test";
import { boundsOfNodes, viewportForBounds } from "./viewport.ts";

test("boundsOfNodes returns null for an empty set", () => {
	assert.equal(boundsOfNodes([]), null);
});

test("boundsOfNodes unions node rectangles", () => {
	const b = boundsOfNodes([
		{ x: 10, y: 20, width: 100, height: 40 },
		{ x: 200, y: 5, width: 50, height: 200 },
	]);
	assert.deepEqual(b, { x: 10, y: 5, width: 240, height: 200 });
});

test("viewportForBounds centers tiny bounds at the max-zoom clamp", () => {
	const vp = viewportForBounds(
		{ x: 0, y: 0, width: 100, height: 100 },
		{ width: 1000, height: 1000 },
		{ padding: 0.2, minZoom: 0.45, maxZoom: 1 },
	);
	assert.equal(vp.zoom, 1);
	// centroid (50,50) maps to pane center (500,500): 500 - 50*1 = 450
	assert.equal(vp.x, 450);
	assert.equal(vp.y, 450);
});

test("viewportForBounds fits mid-size bounds without clamping", () => {
	const vp = viewportForBounds(
		{ x: 0, y: 0, width: 2000, height: 500 },
		{ width: 1400, height: 780 },
		{ padding: 0.2, minZoom: 0.45, maxZoom: 1 },
	);
	// scaleX = 1400/(2000*1.4)=0.5 ; scaleY = 780/(500*1.4)=1.114 ; min=0.5
	assert.equal(vp.zoom, 0.5);
});

test("viewportForBounds clamps to minZoom AND centers on a wide span (the regression)", () => {
	// acmeorg-like: top-risk cluster spans ~24k px in a 1400px pane.
	const bounds = { x: 4655, y: 100, width: 24437, height: 1374 };
	const vp = viewportForBounds(
		bounds,
		{ width: 1400, height: 780 },
		{
			padding: 0.2,
			minZoom: 0.45,
			maxZoom: 1,
		},
	);
	assert.equal(vp.zoom, 0.45);
	const cx = bounds.x + bounds.width / 2; // 16873.5
	assert.equal(vp.x, 1400 / 2 - cx * 0.45);
	// Must center on the far-right centroid — translate is strongly negative,
	// NOT the near-origin (+small) translate the old fitView path produced.
	assert.ok(vp.x < -6000, `expected strongly negative translate, got ${vp.x}`);
});
