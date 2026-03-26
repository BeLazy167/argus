"use client";
import { useMemo, useState } from "react";
import {
  ReactFlow,
  Controls,
  Background,
  MiniMap,
  useNodesState,
  useEdgesState,
  ReactFlowProvider,
  MarkerType,
  type Edge,
  type Node,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";

import type { GraphNode, GraphEdge } from "@/lib/types";
import ModuleNode from "./ModuleNode";
import { getLayoutedElements } from "./layout";

const nodeTypes = { module: ModuleNode };

const EDGE_STYLES: Record<string, Partial<Edge>> = {
  imports: { style: { stroke: "#64748b", strokeWidth: 2 }, animated: false },
  calls: { style: { stroke: "#f59e0b", strokeWidth: 1.5, strokeDasharray: "5 5" }, animated: true },
  uses_type: { style: { stroke: "#8b5cf6", strokeWidth: 1, strokeDasharray: "2 4" }, animated: false },
  implements: { style: { stroke: "#22c55e", strokeWidth: 2 }, animated: false },
};

type Props = {
  graphNodes: GraphNode[];
  graphEdges: GraphEdge[];
  repoFullName: string;
  defaultBranch: string;
};

function transformData(graphNodes: GraphNode[], graphEdges: GraphEdge[], repoFullName: string, defaultBranch: string): { nodes: Node[]; edges: Edge[] } {
  const nodes: Node[] = graphNodes.map((n) => ({
    id: String(n.id),
    type: "module",
    position: { x: 0, y: 0 },
    data: {
      label: n.name,
      kind: n.kind,
      language: n.language,
      filePath: n.file_path,
      githubUrl: `https://github.com/${repoFullName}/blob/${defaultBranch}/${n.file_path}`,
    },
  }));

  const edges: Edge[] = graphEdges.map((e) => ({
    id: String(e.id),
    source: String(e.source_id),
    target: String(e.target_id),
    markerEnd: { type: MarkerType.ArrowClosed, width: 15, height: 15 },
    label: e.kind,
    labelStyle: { fontSize: 9, fill: "#64748b" },
    ...(EDGE_STYLES[e.kind] || EDGE_STYLES.imports),
  }));

  return { nodes, edges };
}

function langColor(n: Node): string {
  const lang = n.data?.language as string;
  if (lang === "typescript" || lang === "javascript") return "#3b82f6";
  if (lang === "go") return "#06b6d4";
  if (lang === "python") return "#22c55e";
  if (lang === "rust") return "#f97316";
  return "#64748b";
}

type InnerProps = Props & { direction: "TB" | "LR" };

function GraphCanvasInner({ graphNodes, graphEdges, repoFullName, defaultBranch, direction }: InnerProps) {
  const { nodes: layoutedNodes, edges: layoutedEdges } = useMemo(() => {
    const { nodes, edges } = transformData(graphNodes, graphEdges, repoFullName, defaultBranch);
    return getLayoutedElements(nodes, edges, direction);
  }, [graphNodes, graphEdges, repoFullName, defaultBranch, direction]);

  const [nodes, , onNodesChange] = useNodesState(layoutedNodes);
  const [edges, , onEdgesChange] = useEdgesState(layoutedEdges);

  return (
    <ReactFlow
      nodes={nodes}
      edges={edges}
      onNodesChange={onNodesChange}
      onEdgesChange={onEdgesChange}
      nodeTypes={nodeTypes}
      fitView
      proOptions={{ hideAttribution: true }}
      className="bg-charcoal"
    >
      <Background color="#333" gap={20} />
      <Controls className="!bg-charcoal !border-iron !shadow-xl [&>button]:!bg-charcoal [&>button]:!border-iron [&>button]:!text-slate-text [&>button:hover]:!bg-iron/30" />
      <MiniMap className="!bg-charcoal !border-iron" nodeColor={langColor} />
    </ReactFlow>
  );
}

export default function GraphCanvas(props: Props) {
  const [direction, setDirection] = useState<"TB" | "LR">("TB");

  return (
    <div className="h-full w-full relative">
      <div className="absolute top-3 right-3 z-10 flex gap-1.5">
        {(["TB", "LR"] as const).map((d) => (
          <button
            key={d}
            onClick={() => setDirection(d)}
            className={`rounded border px-2 py-1 text-[10px] font-mono transition-colors ${
              direction === d ? "border-amber/50 bg-amber/10 text-amber" : "border-iron bg-iron/30 text-slate-text hover:text-foreground"
            }`}
          >
            {d}
          </button>
        ))}
      </div>
      <ReactFlowProvider key={direction}>
        <GraphCanvasInner {...props} direction={direction} />
      </ReactFlowProvider>
    </div>
  );
}
