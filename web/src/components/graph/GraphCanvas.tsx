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
import GroupNode from "./GroupNode";
import { getLayoutedElements } from "./layout";

const nodeTypes = { module: ModuleNode, group: GroupNode };

/** Edge styles by relationship kind — muted, elegant */
const EDGE_STYLES: Record<string, Partial<Edge>> = {
  imports: {
    style: { stroke: "#334155", strokeWidth: 1.5 },
    markerEnd: { type: MarkerType.ArrowClosed, width: 10, height: 10, color: "#334155" },
  },
  calls: {
    style: { stroke: "#854d0e", strokeWidth: 1.5, strokeDasharray: "6 4" },
    animated: true,
    markerEnd: { type: MarkerType.ArrowClosed, width: 10, height: 10, color: "#854d0e" },
  },
  uses_type: {
    style: { stroke: "#4c1d95", strokeWidth: 1, strokeDasharray: "3 3" },
    markerEnd: { type: MarkerType.ArrowClosed, width: 8, height: 8, color: "#4c1d95" },
  },
  implements: {
    style: { stroke: "#14532d", strokeWidth: 1.5 },
    markerEnd: { type: MarkerType.ArrowClosed, width: 10, height: 10, color: "#14532d" },
  },
};

type Props = {
  graphNodes: GraphNode[];
  graphEdges: GraphEdge[];
  repoFullName: string;
  defaultBranch: string;
};

function transformData(
  graphNodes: GraphNode[],
  graphEdges: GraphEdge[],
  repoFullName: string,
  defaultBranch: string
): { nodes: Node[]; edges: Edge[] } {
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
    type: "smoothstep",
    ...(EDGE_STYLES[e.kind] || EDGE_STYLES.imports),
  }));

  return { nodes, edges };
}

function langColor(n: Node): string {
  const lang = n.data?.language as string;
  if (lang === "typescript" || lang === "javascript") return "#3b82f6";
  if (lang === "go") return "#06b6d4";
  if (lang === "python") return "#10b981";
  if (lang === "rust") return "#f97316";
  return "#475569";
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
      fitViewOptions={{ padding: 0.15 }}
      proOptions={{ hideAttribution: true }}
      className="!bg-[#0a0a12]"
      minZoom={0.2}
      maxZoom={2.5}
      defaultEdgeOptions={{ type: "smoothstep" }}
    >
      <Background color="#1a1a2e" gap={32} size={1} />
      <Controls
        position="bottom-left"
        className="!bg-[#12121a] !border-slate-800 !shadow-2xl !rounded-lg
          [&>button]:!bg-[#12121a] [&>button]:!border-slate-800 [&>button]:!text-slate-500
          [&>button:hover]:!bg-slate-800/50 [&>button:hover]:!text-slate-300"
      />
      <MiniMap
        position="bottom-right"
        className="!bg-[#0a0a12]/90 !border-slate-800 !rounded-lg"
        nodeColor={langColor}
        maskColor="rgba(0,0,0,0.8)"
        pannable
        zoomable
      />
    </ReactFlow>
  );
}

export default function GraphCanvas(props: Props) {
  const [direction, setDirection] = useState<"TB" | "LR">("TB");

  return (
    <div className="h-full w-full relative">
      {/* Direction toggle */}
      <div className="absolute top-4 right-4 z-10 flex gap-1 bg-[#12121a]/80 backdrop-blur-sm rounded-lg border border-slate-800 p-0.5">
        {([
          { key: "TB" as const, label: "↕" },
          { key: "LR" as const, label: "↔" },
        ]).map(({ key, label }) => (
          <button
            key={key}
            onClick={() => setDirection(key)}
            className={`rounded-md px-2.5 py-1 text-[11px] font-mono transition-all duration-200 ${
              direction === key
                ? "bg-slate-800 text-slate-200 shadow-sm"
                : "text-slate-500 hover:text-slate-300"
            }`}
          >
            {label}
          </button>
        ))}
      </div>

      {/* Legend */}
      <div className="absolute bottom-4 left-16 z-10 flex gap-3 bg-[#12121a]/80 backdrop-blur-sm rounded-lg border border-slate-800 px-3 py-1.5">
        {[
          { color: "bg-blue-400", label: "TS" },
          { color: "bg-emerald-400", label: "PY" },
          { color: "bg-cyan-400", label: "Go" },
          { color: "bg-orange-400", label: "RS" },
        ].map(({ color, label }) => (
          <div key={label} className="flex items-center gap-1">
            <span className={`w-1.5 h-1.5 rounded-full ${color}`} />
            <span className="text-[9px] font-mono text-slate-500">{label}</span>
          </div>
        ))}
      </div>

      <ReactFlowProvider key={direction}>
        <GraphCanvasInner {...props} direction={direction} />
      </ReactFlowProvider>
    </div>
  );
}
