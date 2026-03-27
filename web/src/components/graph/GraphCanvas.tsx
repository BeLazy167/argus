"use client";
import { useCallback, useEffect, useMemo, useState } from "react";
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
const EDGE_COLORS: Record<string, { base: string; highlight: string; width: number; dash?: string }> = {
  imports: { base: "#334155", highlight: "#64748b", width: 1.5 },
  calls: { base: "#854d0e", highlight: "#f59e0b", width: 1.5, dash: "6 4" },
  uses_type: { base: "#4c1d95", highlight: "#8b5cf6", width: 1, dash: "3 3" },
  implements: { base: "#14532d", highlight: "#22c55e", width: 1.5 },
};

type Props = {
  graphNodes: GraphNode[];
  graphEdges: GraphEdge[];
  repoFullName: string;
  defaultBranch: string;
  onSelectFile?: (filePath: string | null) => void;
};

function langColor(n: Node): string {
  const lang = n.data?.language as string;
  if (lang === "typescript" || lang === "javascript") return "#3b82f6";
  if (lang === "go") return "#06b6d4";
  if (lang === "python") return "#10b981";
  if (lang === "rust") return "#f97316";
  return "#475569";
}

type InnerProps = Props & { direction: "TB" | "LR" };

function GraphCanvasInner({ graphNodes, graphEdges, repoFullName, defaultBranch, direction, onSelectFile }: InnerProps) {
  const [selectedNodeId, setSelectedNodeId] = useState<string | null>(null);

  // Compute initial layout once (only when data/direction changes)
  const layout = useMemo(() => {
    const rfNodes: Node[] = graphNodes.map((n) => ({
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

    const rfEdges: Edge[] = graphEdges.map((e) => {
      const colors = EDGE_COLORS[e.kind] ?? EDGE_COLORS.imports!;
      return {
        id: String(e.id),
        source: String(e.source_id),
        target: String(e.target_id),
        type: "smoothstep",
        style: { stroke: colors.base, strokeWidth: colors.width, strokeDasharray: colors.dash },
        markerEnd: { type: MarkerType.ArrowClosed, width: 10, height: 10, color: colors.base },
        data: { kind: e.kind },
      };
    });

    return getLayoutedElements(rfNodes, rfEdges, direction);
  }, [graphNodes, graphEdges, repoFullName, defaultBranch, direction]);

  const [nodes, setNodes, onNodesChange] = useNodesState(layout.nodes);
  const [edges, setEdges, onEdgesChange] = useEdgesState(layout.edges);

  // When selection changes, update node opacity and edge styles
  useEffect(() => {
    if (!selectedNodeId) {
      // Reset all to full opacity
      setNodes((nds) =>
        nds.map((n) => ({
          ...n,
          style: { ...(n.style || {}), opacity: 1, transition: "opacity 0.2s" },
        }))
      );
      setEdges((eds) =>
        eds.map((e) => {
          const colors = EDGE_COLORS[e.data?.kind as string] ?? EDGE_COLORS.imports!;
          return {
            ...e,
            animated: false,
            style: { ...e.style, stroke: colors.base, strokeWidth: colors.width, opacity: 1, transition: "all 0.2s" },
            markerEnd: { type: MarkerType.ArrowClosed, width: 10, height: 10, color: colors.base },
          };
        })
      );
      return;
    }

    // Find connected edges and nodes
    const connectedEdgeIds = new Set<string>();
    const connectedNodeIds = new Set<string>([selectedNodeId]);
    for (const e of edges) {
      if (e.source === selectedNodeId || e.target === selectedNodeId) {
        connectedEdgeIds.add(e.id);
        connectedNodeIds.add(e.source);
        connectedNodeIds.add(e.target);
      }
    }

    setNodes((nds) =>
      nds.map((n) => ({
        ...n,
        style: {
          ...(n.style || {}),
          opacity: connectedNodeIds.has(n.id) ? 1 : 0.12,
          transition: "opacity 0.2s",
        },
      }))
    );

    setEdges((eds) =>
      eds.map((e) => {
        const isConnected = connectedEdgeIds.has(e.id);
        const colors = EDGE_COLORS[e.data?.kind as string] ?? EDGE_COLORS.imports!;
        return {
          ...e,
          animated: isConnected,
          style: {
            ...e.style,
            stroke: isConnected ? colors.highlight : colors.base,
            strokeWidth: isConnected ? colors.width + 1 : colors.width,
            opacity: isConnected ? 1 : 0.08,
            transition: "all 0.2s",
          },
          markerEnd: {
            type: MarkerType.ArrowClosed,
            width: isConnected ? 14 : 10,
            height: isConnected ? 14 : 10,
            color: isConnected ? colors.highlight : colors.base,
          },
        };
      })
    );
  }, [selectedNodeId, setNodes, setEdges]);

  const onNodeClick: NodeMouseHandler = useCallback((_event, node) => {
    if (node.type === "group") return;
    const isDeselect = selectedNodeId === node.id;
    setSelectedNodeId(isDeselect ? null : node.id);
    onSelectFile?.(isDeselect ? null : (node.data?.filePath as string) ?? null);
  }, [selectedNodeId, onSelectFile]);

  const onPaneClick = useCallback(() => {
    setSelectedNodeId(null);
    onSelectFile?.(null);
  }, [onSelectFile]);

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
