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
  Position,
  type Edge,
  type Node,
  type NodeMouseHandler,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import dagre from "dagre";

import FileNode from "./FileNode";
import GroupNode from "./GroupNode";
import type { ArchFile, ArchEdge } from "@/lib/queries/architecture";

/** Color for bug density: 0=green, mid=yellow, high=red */
function densityColor(density: number, maxDensity: number): string {
  if (maxDensity === 0) return "#22c55e";
  const t = Math.min(density / maxDensity, 1);
  if (t < 0.33) return "#22c55e"; // green
  if (t < 0.66) return "#eab308"; // yellow
  return "#ef4444"; // red
}

function densityGlow(density: number, maxDensity: number): string {
  if (maxDensity === 0) return "rgba(34,197,94,0.15)";
  const t = Math.min(density / maxDensity, 1);
  if (t < 0.33) return "rgba(34,197,94,0.15)";
  if (t < 0.66) return "rgba(234,179,8,0.15)";
  return "rgba(239,68,68,0.2)";
}

const LANG_COLORS: Record<string, string> = {
  typescript: "#3b82f6",
  javascript: "#3b82f6",
  go: "#06b6d4",
  python: "#10b981",
  rust: "#f97316",
};

const DEFAULT_EDGE_COLORS = { base: "#334155", highlight: "#64748b" } as const;
const EDGE_COLORS: Record<string, { base: string; highlight: string }> = {
  imports: { base: "#334155", highlight: "#64748b" },
  calls: { base: "#854d0e", highlight: "#f59e0b" },
  uses_type: { base: "#4c1d95", highlight: "#8b5cf6" },
  implements: { base: "#14532d", highlight: "#22c55e" },
};
function edgeColorsFor(kind: string) {
  return EDGE_COLORS[kind] ?? DEFAULT_EDGE_COLORS;
}

export type Lens = "risk" | "choke" | "hotspot" | "coupling";

type Props = {
  files: ArchFile[];
  edges: ArchEdge[];
  lens: Lens;
  onSelectFile?: (filePath: string | null) => void;
};

/** Extract directory for grouping */
function dirGroup(filePath: string): string {
  const parts = filePath.split("/");
  if (parts.length <= 1) return "root";
  return parts.slice(0, -1).join("/");
}

function groupLabel(dir: string): string {
  const parts = dir.split("/");
  return parts.slice(-2).join("/");
}

/** Compute node size from risk score (0-10) */
function nodeSize(riskScore: number): { width: number; height: number } {
  const w = 160 + riskScore * 10;
  const h = 48 + riskScore * 3;
  return { width: Math.round(w), height: Math.round(h) };
}

/** Determine visual emphasis per lens */
function lensNodeOpacity(file: ArchFile, lens: Lens): number {
  switch (lens) {
    case "choke":
      return file.fan_in >= 3 ? 1 : 0.25;
    case "hotspot":
      return file.bug_density > 0 ? 1 : 0.25;
    case "coupling":
      return file.coupling.length > 0 ? 1 : 0.25;
    default:
      return 1;
  }
}

type InnerProps = Props & { direction: "TB" | "LR" };

function ArchCanvasInner({ files, edges, lens, direction, onSelectFile }: InnerProps) {
  const [selectedNodeId, setSelectedNodeId] = useState<string | null>(null);
  const maxDensity = useMemo(() => Math.max(...files.map((f) => f.bug_density), 0.01), [files]);

  const layout = useMemo(() => {
    const g = new dagre.graphlib.Graph().setDefaultEdgeLabel(() => ({}));
    const isHorizontal = direction === "LR";
    g.setGraph({ rankdir: direction, nodesep: 80, ranksep: 130, marginx: 60, marginy: 60 });

    // Create file nodes
    const rfNodes: Node[] = files.map((f) => {
      const size = nodeSize(f.risk_score);
      const fileName = f.path.split("/").pop() ?? f.path;
      g.setNode(f.path, { width: size.width, height: size.height });
      return {
        id: f.path,
        type: "archFile",
        position: { x: 0, y: 0 },
        data: {
          label: fileName,
          fullPath: f.path,
          language: f.language,
          riskScore: f.risk_score,
          fanIn: f.fan_in,
          fanOut: f.fan_out,
          bugDensity: f.bug_density,
          changeFrequency: f.change_frequency,
          insight: f.insight,
          isChokePoint: f.fan_in >= 5,
          isHotspot: f.bug_density > 0 && f.percentiles.bug_density >= 90,
          lens,
          borderColor: densityColor(f.bug_density, maxDensity),
          glowColor: densityGlow(f.bug_density, maxDensity),
          borderWidth: Math.min(1 + f.fan_in * 0.4, 4),
          pulse: f.change_frequency >= 10,
        },
        style: {
          width: size.width,
          height: size.height,
          opacity: lensNodeOpacity(f, lens),
          transition: "opacity 0.3s",
        },
      };
    });

    // Create edges
    const rfEdges: Edge[] = edges.map((e, i) => {
      const primaryKind = e.kinds[0] ?? "imports";
      const colors = edgeColorsFor(primaryKind);
      const sw = Math.max(1, 1 + Math.log2(Math.max(e.weight, 1)));
      return {
        id: `e-${i}`,
        source: e.source,
        target: e.target,
        type: "smoothstep",
        style: { stroke: colors.base, strokeWidth: sw },
        markerEnd: { type: MarkerType.ArrowClosed, width: 8, height: 8, color: colors.base },
        data: { kinds: e.kinds, weight: e.weight },
      };
    });

    // Set edges in dagre
    rfEdges.forEach((e) => {
      if (g.hasNode(e.source) && g.hasNode(e.target)) {
        g.setEdge(e.source, e.target);
      }
    });

    dagre.layout(g);

    // Apply positions
    const positionedNodes = rfNodes.map((node) => {
      const pos = g.node(node.id);
      if (!pos) return node;
      const size = nodeSize(files.find((f) => f.path === node.id)?.risk_score ?? 0);
      return {
        ...node,
        targetPosition: isHorizontal ? Position.Left : Position.Top,
        sourcePosition: isHorizontal ? Position.Right : Position.Bottom,
        position: { x: pos.x - size.width / 2, y: pos.y - size.height / 2 },
      };
    });

    // Compute directory groups
    const groups = new Map<string, string[]>();
    files.forEach((f) => {
      const group = dirGroup(f.path);
      if (!groups.has(group)) groups.set(group, []);
      groups.get(group)!.push(f.path);
    });

    const groupNodes: Node[] = [];
    const PAD_X = 30;
    const PAD_Y = 50;
    groups.forEach((memberPaths, dir) => {
      if (memberPaths.length < 2) return;
      let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
      for (const fp of memberPaths) {
        const node = positionedNodes.find((n) => n.id === fp);
        if (!node) continue;
        const size = nodeSize(files.find((f) => f.path === fp)?.risk_score ?? 0);
        minX = Math.min(minX, node.position.x);
        minY = Math.min(minY, node.position.y);
        maxX = Math.max(maxX, node.position.x + size.width);
        maxY = Math.max(maxY, node.position.y + size.height);
      }
      groupNodes.push({
        id: `group:${dir}`,
        type: "group",
        position: { x: minX - PAD_X, y: minY - PAD_Y },
        data: { label: groupLabel(dir) },
        style: {
          width: maxX - minX + PAD_X * 2,
          height: maxY - minY + PAD_Y + PAD_X,
          backgroundColor: "rgba(20, 20, 30, 0.5)",
          borderRadius: 16,
          border: "1px solid rgba(71, 85, 105, 0.15)",
          pointerEvents: "none" as const,
        },
        selectable: false,
        draggable: false,
      });
    });

    return { nodes: [...groupNodes, ...positionedNodes], edges: rfEdges };
  }, [files, edges, lens, direction, maxDensity]);

  const [nodes, setNodes, onNodesChange] = useNodesState(layout.nodes);
  const [rfEdges, setEdges, onEdgesChange] = useEdgesState(layout.edges);

  // Highlight connected nodes on selection.
  //
  // The effect reads `layout.edges` — a stable memo — instead of the stateful
  // `rfEdges`. Using `rfEdges` as a dep + calling `setEdges` inside creates
  // an infinite loop (each setEdges update re-triggers the effect).
  // `setNodes` / `setEdges` are stable refs from useNodesState/useEdgesState
  // and don't need to be in the dep array.
  useEffect(() => {
    if (!selectedNodeId) {
      setNodes((nds) =>
        nds.map((n) => {
          const f = files.find((f) => f.path === n.id);
          return {
            ...n,
            style: { ...n.style, opacity: f ? lensNodeOpacity(f, lens) : 1, transition: "opacity 0.3s" },
          };
        })
      );
      setEdges((eds) =>
        eds.map((e) => {
          const colors = edgeColorsFor((e.data?.kinds as string[])?.[0] ?? "imports");
          return { ...e, animated: false, style: { ...e.style, stroke: colors.base, opacity: 1, transition: "all 0.3s" } };
        })
      );
      return;
    }

    const connectedEdgeIds = new Set<string>();
    const connectedNodeIds = new Set<string>([selectedNodeId]);
    for (const e of layout.edges) {
      if (e.source === selectedNodeId || e.target === selectedNodeId) {
        connectedEdgeIds.add(e.id);
        connectedNodeIds.add(e.source);
        connectedNodeIds.add(e.target);
      }
    }

    setNodes((nds) =>
      nds.map((n) => ({
        ...n,
        style: { ...n.style, opacity: connectedNodeIds.has(n.id) ? 1 : 0.1, transition: "opacity 0.3s" },
      }))
    );
    setEdges((eds) =>
      eds.map((e) => {
        const isConn = connectedEdgeIds.has(e.id);
        const colors = edgeColorsFor((e.data?.kinds as string[])?.[0] ?? "imports");
        return {
          ...e,
          animated: isConn,
          style: { ...e.style, stroke: isConn ? colors.highlight : colors.base, opacity: isConn ? 1 : 0.05, transition: "all 0.3s" },
        };
      })
    );
    // Intentionally excluding setNodes/setEdges (stable from state hooks)
    // and files (subsumed by layout which already depends on files).
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedNodeId, lens, layout]);

  const onNodeClick: NodeMouseHandler = useCallback(
    (_event, node) => {
      if (node.type === "group") return;
      const isDeselect = selectedNodeId === node.id;
      setSelectedNodeId(isDeselect ? null : node.id);
      onSelectFile?.(isDeselect ? null : node.id);
    },
    [selectedNodeId, onSelectFile]
  );

  const onPaneClick = useCallback(() => {
    setSelectedNodeId(null);
    onSelectFile?.(null);
  }, [onSelectFile]);

  return (
    <ReactFlow
      nodes={nodes}
      edges={rfEdges}
      onNodesChange={onNodesChange}
      onEdgesChange={onEdgesChange}
      onNodeClick={onNodeClick}
      onPaneClick={onPaneClick}
      nodeTypes={nodeTypes}
      fitView
      fitViewOptions={{ padding: 0.15 }}
      proOptions={{ hideAttribution: true }}
      className="!bg-[#0a0a12]"
      minZoom={0.15}
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
        nodeColor={(n) => LANG_COLORS[n.data?.language as string] ?? "#475569"}
        maskColor="rgba(0,0,0,0.8)"
        pannable
        zoomable
      />
    </ReactFlow>
  );
}

const nodeTypes = { archFile: FileNode, group: GroupNode };

export default function ArchitectureCanvas(props: Props) {
  const [direction, setDirection] = useState<"TB" | "LR">("TB");

  return (
    <div className="h-full w-full relative">
      {/* Direction toggle */}
      <div className="absolute top-4 right-4 z-10 flex gap-1 bg-[#12121a]/80 backdrop-blur-sm border border-slate-800 p-0.5">
        {([
          { key: "TB" as const, label: "↕" },
          { key: "LR" as const, label: "↔" },
        ]).map(({ key, label }) => (
          <button
            key={key}
            onClick={() => setDirection(key)}
            className={`rounded-md px-2.5 py-1 text-[11px] font-mono transition-all duration-200 ${
              direction === key ? "bg-slate-800 text-slate-200 shadow-sm" : "text-slate-500 hover:text-slate-300"
            }`}
          >
            {label}
          </button>
        ))}
      </div>

      {/* Language legend */}
      <div className="absolute bottom-4 left-16 z-10 flex gap-3 bg-[#12121a]/80 backdrop-blur-sm border border-slate-800 px-3 py-1.5">
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
        <div className="w-px h-3 bg-slate-800 mx-1" />
        <div className="flex items-center gap-1">
          <span className="w-1.5 h-1.5 rounded-full bg-green-400" />
          <span className="text-[9px] font-mono text-slate-500">Low risk</span>
        </div>
        <div className="flex items-center gap-1">
          <span className="w-1.5 h-1.5 rounded-full bg-red-400" />
          <span className="text-[9px] font-mono text-slate-500">High risk</span>
        </div>
      </div>

      {/* Only remount on direction change (which recomputes layout); lens changes update in place. */}
      <ReactFlowProvider key={direction}>
        <ArchCanvasInner {...props} direction={direction} />
      </ReactFlowProvider>
    </div>
  );
}
