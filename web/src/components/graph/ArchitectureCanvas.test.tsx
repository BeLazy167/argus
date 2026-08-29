import { fireEvent, render, screen } from "@testing-library/react";
import type { Node } from "@xyflow/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ArchEdge, ArchFile } from "@/lib/queries/architecture";

const { fitViewMock } = vi.hoisted(() => ({
	fitViewMock: vi.fn().mockResolvedValue(true),
}));

vi.mock("@xyflow/react", () => ({
	applyNodeChanges: (_changes: unknown, nodes: Node[]) => nodes,
	Background: () => null,
	Controls: () => null,
	MarkerType: { ArrowClosed: "arrow" },
	MiniMap: () => null,
	Position: { Bottom: "bottom", Left: "left", Right: "right", Top: "top" },
	ReactFlow: ({
		nodes,
		edges,
		onNodeClick,
	}: {
		nodes: Node[];
		edges: { id: string; source: string; target: string }[];
		onNodeClick: (event: unknown, node: Node) => void;
	}) => (
		<>
			<output data-testid="flow">
				{JSON.stringify({
					nodes: nodes.map(({ id, data, style }) => ({
						id,
						selected: data.selected,
						opacity: style?.opacity,
					})),
					edges: edges.map(({ source, target }) => `${source}->${target}`),
				})}
			</output>
			{nodes
				.filter(({ type }) => type !== "group")
				.map((node) => (
					<button
						key={node.id}
						data-testid={`node-${node.id}`}
						onClick={(event) => onNodeClick(event, node)}
					>
						{node.id}
					</button>
				))}
		</>
	),
	ReactFlowProvider: ({ children }: { children: ReactNode }) => children,
	useReactFlow: () => ({ fitView: fitViewMock }),
}));

import ArchitectureCanvas, { mergeLayoutPositions } from "./ArchitectureCanvas";

function node(id: string, x: number): Node {
	return { id, position: { x, y: 0 }, data: {} };
}

function file(path: string): ArchFile {
	return {
		path,
		language: "typescript",
		symbols: [],
		fan_in: 0,
		fan_out: 0,
		bug_density: 0,
		change_frequency: 0,
		coupling: [],
		risk_score: 1,
		percentiles: { fan_in: 0, bug_density: 0, change_frequency: 0, coupling: 0 },
	};
}

function edge(source: string, target: string): ArchEdge {
	return { source, target, kinds: ["imports"], weight: 1 };
}

function searchRequest(id: number, query: string) {
	return { id, query };
}

function setReducedMotion(matches: boolean) {
	vi.stubGlobal("matchMedia", vi.fn().mockReturnValue({ matches }));
}

describe("architecture canvas search viewport", () => {
	beforeEach(() => {
		fitViewMock.mockClear();
		setReducedMotion(false);
	});

	it("fits the viewport to matching files when a search is requested", () => {
		const { rerender } = render(
			<ArchitectureCanvas
				files={[file("src/alpha.ts"), file("src/beta.ts"), file("test/alpha.test.ts")]}
				edges={[]}
				lens="risk"
				searchRequest={searchRequest(0, "")}
			/>,
		);

		rerender(
			<ArchitectureCanvas
				files={[file("src/alpha.ts"), file("src/beta.ts"), file("test/alpha.test.ts")]}
				edges={[]}
				lens="risk"
				searchRequest={searchRequest(1, "ALPHA")}
			/>,
		);

		expect(fitViewMock).toHaveBeenCalledTimes(1);
		expect(fitViewMock).toHaveBeenCalledWith({
			nodes: [{ id: "src/alpha.ts" }, { id: "test/alpha.test.ts" }],
			padding: 0.3,
			duration: 300,
		});
	});

	it("clears node selection when a new search request starts", () => {
		const files = [file("src/alpha.ts"), file("src/beta.ts")];
		const { rerender } = render(
			<ArchitectureCanvas
				files={files}
				edges={[]}
				lens="risk"
				searchRequest={searchRequest(0, "")}
			/>,
		);
		fireEvent.click(screen.getByTestId("node-src/alpha.ts"));

		rerender(
			<ArchitectureCanvas
				files={files}
				edges={[]}
				lens="risk"
				searchRequest={searchRequest(1, "beta")}
			/>,
		);

		const searched = JSON.parse(screen.getByTestId("flow").textContent ?? "{}");
		expect(searched.nodes.find(({ id }: Node) => id === "src/alpha.ts")).toMatchObject({
			selected: false,
			opacity: 0.1,
		});
		expect(searched.nodes.find(({ id }: Node) => id === "src/beta.ts")).toMatchObject({
			selected: false,
			opacity: 1,
		});
	});

	it("does not move the viewport when search is cleared or has no matches", () => {
		const files = [file("src/alpha.ts"), file("src/beta.ts")];
		const { rerender } = render(
			<ArchitectureCanvas
				files={files}
				edges={[]}
				lens="risk"
				searchRequest={searchRequest(1, "alpha")}
			/>,
		);
		expect(fitViewMock).toHaveBeenCalledTimes(1);

		rerender(
			<ArchitectureCanvas
				files={files}
				edges={[]}
				lens="risk"
				searchRequest={searchRequest(2, "")}
			/>,
		);
		rerender(
			<ArchitectureCanvas
				files={files}
				edges={[]}
				lens="risk"
				searchRequest={searchRequest(3, "missing")}
			/>,
		);

		expect(fitViewMock).toHaveBeenCalledTimes(1);
	});

	it("does not repeat a search fit when topology or lens data refreshes", () => {
		const request = searchRequest(1, "alpha");
		const { rerender } = render(
			<ArchitectureCanvas
				files={[file("src/alpha.ts"), file("src/beta.ts")]}
				edges={[]}
				lens="risk"
				searchRequest={request}
			/>,
		);
		expect(fitViewMock).toHaveBeenCalledTimes(1);

		rerender(
			<ArchitectureCanvas
				files={[file("src/alpha.ts"), file("src/gamma.ts"), file("test/alpha.test.ts")]}
				edges={[edge("src/alpha.ts", "src/gamma.ts")]}
				lens="hotspot"
				searchRequest={request}
			/>,
		);

		expect(fitViewMock).toHaveBeenCalledTimes(1);
	});

	it("disables viewport animation when reduced motion is requested", () => {
		setReducedMotion(true);

		render(
			<ArchitectureCanvas
				files={[file("src/alpha.ts"), file("src/beta.ts")]}
				edges={[]}
				lens="risk"
				searchRequest={searchRequest(1, "alpha")}
			/>,
		);

		expect(fitViewMock).toHaveBeenCalledWith(expect.objectContaining({ duration: 0 }));
	});
});

describe("architecture canvas reconciliation", () => {
	it("uses refreshed node membership while retaining positions for surviving files", () => {
		const current = [node("removed.ts", 10), node("kept.ts", 40)];
		const refreshed = [node("kept.ts", 100), node("added.ts", 200)];

		const result = mergeLayoutPositions(refreshed, current);

		expect(result.map(({ id }) => id)).toEqual(["kept.ts", "added.ts"]);
		expect(result.find(({ id }) => id === "kept.ts")?.position.x).toBe(40);
		expect(result.find(({ id }) => id === "added.ts")?.position.x).toBe(200);
	});

	it("renders added and removed files and edges after query data refreshes", () => {
		const { rerender } = render(
			<ArchitectureCanvas
				files={[file("a.ts"), file("b.ts")]}
				edges={[edge("a.ts", "b.ts")]}
				lens="risk"
			/>,
		);
		const initial = JSON.parse(screen.getByTestId("flow").textContent ?? "{}");
		expect(initial.nodes.map(({ id }: Node) => id)).toEqual(
			expect.arrayContaining(["a.ts", "b.ts"]),
		);
		expect(initial.edges).toEqual(["a.ts->b.ts"]);
		fireEvent.click(screen.getByTestId("node-a.ts"));
		const selected = JSON.parse(screen.getByTestId("flow").textContent ?? "{}");
		expect(selected.nodes.find(({ id }: Node) => id === "a.ts")?.selected).toBe(true);

		rerender(
			<ArchitectureCanvas
				files={[file("b.ts"), file("c.ts")]}
				edges={[edge("b.ts", "c.ts")]}
				lens="risk"
			/>,
		);

		const refreshed = JSON.parse(screen.getByTestId("flow").textContent ?? "{}");
		const refreshedFileNodes = refreshed.nodes.filter(({ id }: Node) => !id.startsWith("group:"));
		expect(refreshedFileNodes.map(({ id }: Node) => id)).toEqual(["b.ts", "c.ts"]);
		expect(refreshedFileNodes.every(({ selected }: { selected: boolean }) => !selected)).toBe(true);
		expect(refreshedFileNodes.every(({ opacity }: { opacity: number }) => opacity === 1)).toBe(
			true,
		);
		expect(refreshed.edges).toEqual(["b.ts->c.ts"]);
	});
});

describe("decoration cache freshness", () => {
	beforeEach(() => {
		fitViewMock.mockClear();
		setReducedMotion(false);
	});

	it("propagates refreshed metrics and topology for surviving nodes", () => {
		// Regression: the decoration cache once keyed only on emphasis
		// signature, so a data refresh that kept a node's id/position (but
		// changed its metrics or an index-reused edge's endpoints) served the
		// stale object. The cache must invalidate whenever layout rebuilds.
		const before = [file("src/a.ts"), file("src/b.ts")];
		const { rerender } = render(
			<ArchitectureCanvas
				files={before}
				edges={[edge("src/a.ts", "src/b.ts")]}
				lens="risk"
				searchRequest={searchRequest(0, "")}
			/>,
		);
		const initial = JSON.parse(screen.getByTestId("flow").textContent ?? "{}");
		expect(initial.edges).toEqual(["src/a.ts->src/b.ts"]);

		const refreshedB = { ...file("src/b.ts"), fan_in: 9, risk_score: 8 };
		rerender(
			<ArchitectureCanvas
				files={[refreshedB, file("src/c.ts")]}
				edges={[edge("src/b.ts", "src/c.ts")]}
				lens="risk"
				searchRequest={searchRequest(0, "")}
			/>,
		);
		const refreshed = JSON.parse(screen.getByTestId("flow").textContent ?? "{}");
		// Old edge (same index-based id e-0) must be replaced, not reused.
		expect(refreshed.edges).toEqual(["src/b.ts->src/c.ts"]);
		expect(refreshed.nodes.map((n: { id: string }) => n.id)).toEqual(
			expect.arrayContaining(["src/b.ts", "src/c.ts"]),
		);
		expect(refreshed.nodes.map((n: { id: string }) => n.id)).not.toContain("src/a.ts");
	});
});
