import { fireEvent, render, screen } from "@testing-library/react";
import type { Node } from "@xyflow/react";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import type { ArchEdge, ArchFile } from "@/lib/queries/architecture";

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
	useReactFlow: () => ({ fitView: vi.fn() }),
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
