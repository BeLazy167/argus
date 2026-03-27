"use client";
import { useCallback, useMemo, useState } from "react";
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
  type NodeMouseHandler,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";

import type { GraphNode, GraphEdge } from "@/lib/types";
import ModuleNode from "./ModuleNode";
import GroupNode from "./GroupNode";
import { getLayoutedElements } from "./layout";

const nodeTypes = { module: ModuleNode, group: GroupNode };

/** Base edge styles by relationship kind */
const EDGE_STYLES: Record<string, { stroke: string; width: number; dash?: string; animated?: boolean }> = {
  imports: { stroke: "#334155", width: 1.5 },
  calls: { stroke: "#854d0e", width: 1.5, dash: "6 4", animated: true },
  uses_type: { stroke: "#4c1d95", width: 1, dash: "3 3" },
  implements: { stroke: "#14532d", width: 1.5 },
};

/** Highlighted edge colors (brighter versions) */
const EDGE_HIGHLIGHT: Record<string, string> = {
  imports: "#64748b",
  calls: "#f59e0b",
  uses_type: "#8b5cf6",
  implements: "#22c55e",
};

type Props = {
  graphNodes: GraphNode[];
  graphEdges: GraphEdge[];
  repoFullName: string;
  defaultBranch: string;
};

function buildEdge(e: GraphEdge, highlighted: boolean, dimmed: boolean, kind: string): Edge {
  const style = EDGE_STYLES[kind] ?? EDGE_STYLES.imports!;
  const highlightColor = EDGE_HIGHLIGHT[kind] ?? "#64748b";

  return {
    id: String(e.id),
    source: String(e.source_id),
    target: String(e.target_id),
    type: "smoothstep",
    animated: highlighted ? true : (style.animated || false),
    style: {
      stroke: highlighted ? highlightColor : style.stroke,
      strokeWidth: highlighted ? style.width + 1 : style.width,
      strokeDasharray: style.dash,
      opacity: dimmed ? 0.1 : 1,
      transition: "opacity 0.2s, stroke 0.2s, stroke-width 0.2s",
    },
    markerEnd: {
      type: MarkerType.ArrowClosed,
      width: highlighted ? 14 : 10,
      height: highlighted ? 14 : 10,
      color: highlighted ? highlightColor : style.stroke,
    },
  };
}

function transformNodes(
  graphNodes: GraphNode[],
  repoFullName: string,
  defaultBranch: string,
): Node[] {
  return graphNodes.map((n) => ({
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
  const [selectedNodeId, setSelectedNodeId] = useState<string | null>(null);

  // Compute connected node/edge sets for the selected node
  const { connectedNodeIds, connectedEdgeIds } = useMemo(() => {
    if (!selectedNodeId) return { connectedNodeIds: new Set<string>(), connectedEdgeIds: new Set<string>() };
    const nodeIds = new Set<string>([selectedNodeId]);
    const edgeIds = new Set<string>();
    for (const e of graphEdges) {
      const src = String(e.source_id);
      const tgt = String(e.target_id);
      if (src === selectedNodeId || tgt === selectedNodeId) {
        edgeIds.add(String(e.id));
        nodeIds.add(src);
        nodeIds.add(tgt);
      }
    }
    return { connectedNodeIds: nodeIds, connectedEdgeIds: edgeIds };
  }, [selectedNodeId, graphEdges]);

  const { nodes: layoutedNodes, edges: layoutedEdges } = useMemo(() => {
    const rawNodes = transformNodes(graphNodes, repoFullName, defaultBranch);

    const edges = graphEdges.map((e) => {
      const isHighlighted = connectedEdgeIds.has(String(e.id));
      const isDimmed = selectedNodeId !== null && !connectedEdgeIds.has(String(e.id));
      return buildEdge(e, isHighlighted, isDimmed, e.kind);
    });

    // Apply dimming to nodes
    const nodes = rawNodes.map((n) => {
      const isDimmed = selectedNodeId !== null && !connectedNodeIds.has(n.id);
      return {
        ...n,
        style: {
          ...(n.style || {}),
          opacity: isDimmed ? 0.15 : 1,
          transition: "opacity 0.2s",
        },
      };
    });

    return getLayoutedElements(nodes, edges, direction);
  }, [graphNodes, graphEdges, repoFullName, defaultBranch, direction, selectedNodeId, connectedNodeIds, connectedEdgeIds]);

  const [nodes, , onNodesChange] = useNodesState(layoutedNodes);
  const [edges, , onEdgesChange] = useEdgesState(layoutedEdges);

  const onNodeClick: NodeMouseHandler = useCallback((_event, node) => {
    if (node.type === "group") return;
    setSelectedNodeId((prev) => (prev === node.id ? null : node.id));
  }, []);

  const onPaneClick = useCallback(() => {
    setSelectedNodeId(null);
  }, []);

  return (
    <ReactFlow
      nodes={nodes}
      edges={edges}
      onNodesChange={onNodesChange}
      onEdgesChange={onEdgesChange}
      onNodeClick={onNodeClick}
      onPaneClick={onPaneClick}
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
