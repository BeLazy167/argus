"use client";
import { useCallback, useEffect, useMemo, useState, useSyncExternalStore } from "react";
import {
  ReactFlow,
  Controls,
  Background,
  MiniMap,
  useNodesState,
  useEdgesState,
  useReactFlow,
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
import type { ColorMode } from "@xyflow/react";

/** Subscribe to theme changes on <html> class list */
function useColorMode(): ColorMode {
  const subscribe = useCallback((cb: () => void) => {
    const obs = new MutationObserver(cb);
    obs.observe(document.documentElement, { attributes: true, attributeFilter: ["class"] });
    return () => obs.disconnect();
  }, []);
  const getSnapshot = useCallback(
    () => (document.documentElement.classList.contains("light") ? "light" as const : "dark" as const),
    []
  );
  return useSyncExternalStore(subscribe, getSnapshot, () => "dark" as const);
}

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
  typescript: "var(--graph-lang-ts)",
  javascript: "var(--graph-lang-ts)",
  go: "var(--graph-lang-go)",
  python: "var(--graph-lang-py)",
  rust: "var(--graph-lang-rs)",
};

const DEFAULT_EDGE_COLORS = { base: "var(--graph-edge-import)", highlight: "var(--graph-edge-import-hi)" } as const;
const EDGE_COLORS: Record<string, { base: string; highlight: string }> = {
  imports: { base: "var(--graph-edge-import)", highlight: "var(--graph-edge-import-hi)" },
  calls: { base: "var(--graph-edge-call)", highlight: "var(--graph-edge-call-hi)" },
  uses_type: { base: "var(--graph-edge-type)", highlight: "var(--graph-edge-type-hi)" },
  implements: { base: "var(--graph-edge-impl)", highlight: "var(--graph-edge-impl-hi)" },
};
function edgeColorsFor(kind: string) {
  return EDGE_COLORS[kind] ?? DEFAULT_EDGE_COLORS;
}

export type Lens = "risk" | "choke" | "hotspot" | "coupling";

type Props = {
  files: ArchFile[];
  edges: ArchEdge[];
  lens: Lens;
  searchQuery?: string;
  onSelectFile?: (filePath: string | null) => void;
};

/** Per-lens border/glow for highlighted nodes */
function lensHighlightStyle(file: ArchFile, lens: Lens): { borderColor?: string; boxShadow?: string } {
  if (lensNodeOpacity(file, lens) < 1) return {};
  switch (lens) {
    case "choke":
      return file.fan_in >= 5
        ? { borderColor: "rgba(245,158,11,0.6)", boxShadow: "0 0 10px rgba(245,158,11,0.2)" }
        : {};
    case "hotspot":
      return file.bug_density > 0 && file.percentiles.bug_density >= 90
        ? { borderColor: "rgba(239,68,68,0.6)", boxShadow: "0 0 10px rgba(239,68,68,0.2)" }
        : {};
    case "coupling":
      return file.coupling.length > 0
        ? { borderColor: "rgba(139,92,246,0.6)", boxShadow: "0 0 10px rgba(139,92,246,0.2)" }
        : {};
    default:
      return {};
  }
}

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

type InnerProps = Props & {
  direction: "TB" | "LR";
  setDirection: (d: "TB" | "LR") => void;
};

function ArchCanvasInner({ files, edges, lens, direction, setDirection, searchQuery, onSelectFile }: InnerProps) {
  const { fitView } = useReactFlow();
  const colorMode = useColorMode();
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
      const lensStyle = lensHighlightStyle(f, lens);
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
          selected: false,
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
          transition: "opacity 0.3s, border-color 0.3s, box-shadow 0.3s",
          ...(lensStyle.borderColor ? { borderColor: lensStyle.borderColor } : {}),
          ...(lensStyle.boxShadow ? { boxShadow: lensStyle.boxShadow } : {}),
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
          backgroundColor: "var(--graph-group-bg)",
          borderRadius: 0,
          border: "1px solid var(--graph-border)",
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

  // Smart default zoom: focus on top-risk cluster instead of fitting all nodes.
  const initialZoomDone = useMemo(() => ({ current: false }), []);
  useEffect(() => {
    if (initialZoomDone.current) return;
    initialZoomDone.current = true;
    const sorted = [...files].sort((a, b) => b.risk_score - a.risk_score);
    const topN = sorted.slice(0, Math.min(15, files.length));
    if (topN.length < 15 || files.length <= 15) {
      // Small repo — fit everything
      fitView({ padding: 0.15, duration: 400 });
      return;
    }
    const focusSet = new Set(topN.map((f) => f.path));
    for (const e of edges) {
      if (focusSet.has(e.source) || focusSet.has(e.target)) {
        focusSet.add(e.source);
        focusSet.add(e.target);
      }
    }
    fitView({
      nodes: Array.from(focusSet).map((id) => ({ id })),
      padding: 0.2,
      duration: 400,
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [files, edges]);

  // Highlight connected nodes on selection, search, or lens change.
  //
  // Reads `layout.edges` (stable memo) instead of stateful `rfEdges` to
  // avoid infinite loops. setNodes/setEdges are stable refs from hooks.
  const searchLower = searchQuery?.toLowerCase().trim() ?? "";
  useEffect(() => {
    // Search mode: dim non-matching nodes
    if (searchLower && !selectedNodeId) {
      const matchIds = new Set(
        files.filter((f) => f.path.toLowerCase().includes(searchLower)).map((f) => f.path)
      );
      setNodes((nds) =>
        nds.map((n) => ({
          ...n,
          data: { ...n.data, selected: false },
          style: { ...n.style, opacity: matchIds.has(n.id) ? 1 : 0.1, transition: "opacity 0.3s" },
        }))
      );
      setEdges((eds) =>
        eds.map((e) => {
          const colors = edgeColorsFor((e.data?.kinds as string[])?.[0] ?? "imports");
          const isConn = matchIds.has(e.source) && matchIds.has(e.target);
          return { ...e, animated: false, style: { ...e.style, stroke: colors.base, opacity: isConn ? 1 : 0.05, transition: "all 0.3s" } };
        })
      );
      // Auto-fit to matches
      if (matchIds.size > 0 && matchIds.size < files.length) {
        fitView({ nodes: Array.from(matchIds).map((id) => ({ id })), padding: 0.3, duration: 300 });
      }
      return;
    }

    if (!selectedNodeId) {
      setNodes((nds) =>
        nds.map((n) => {
          const f = files.find((f) => f.path === n.id);
          return {
            ...n,
            data: { ...n.data, selected: false },
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
        data: { ...n.data, selected: n.id === selectedNodeId },
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
  }, [selectedNodeId, lens, layout, searchLower]);

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
    <>
      <ReactFlow
        nodes={nodes}
        edges={rfEdges}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        onNodeClick={onNodeClick}
        onPaneClick={onPaneClick}
        nodeTypes={nodeTypes}
        colorMode={colorMode}
        proOptions={{ hideAttribution: true }}
        className="!bg-[var(--graph-bg)]"
        minZoom={0.15}
        maxZoom={2.5}
        defaultEdgeOptions={{ type: "smoothstep" }}
      >
        <Background color="var(--graph-dots)" gap={32} size={1} />
        <Controls
          position="bottom-left"
          className="!bg-[var(--graph-surface)] !border-[var(--graph-border)] !shadow-2xl
            [&>button]:!bg-[var(--graph-surface)] [&>button]:!border-[var(--graph-border)] [&>button]:!text-[var(--graph-text-dim)]
            [&>button:hover]:!bg-[var(--graph-control-bg)] [&>button:hover]:!text-[var(--graph-text)]"
        />
        <MiniMap
          position="bottom-right"
          className="!bg-[var(--graph-bg)]/90 !border-[var(--graph-border)]"
          nodeColor={(n) => LANG_COLORS[n.data?.language as string] ?? "var(--graph-lang-default)"}
          maskColor="rgba(0,0,0,0.7)"
          pannable
          zoomable
        />
      </ReactFlow>

      {/* Direction toggle + fit-all */}
      <div className="absolute top-4 right-4 z-10 flex gap-1 bg-[var(--graph-surface)]/80 backdrop-blur-sm border border-[var(--graph-border)] p-0.5">
        {([
          { key: "TB" as const, label: "↕", title: "Top-to-bottom layout" },
          { key: "LR" as const, label: "↔", title: "Left-to-right layout" },
        ]).map(({ key, label, title }) => (
          <button
            key={key}
            onClick={() => setDirection(key)}
            title={title}
            className={`px-2.5 py-1 text-[11px] font-mono transition-all duration-200 ${
              direction === key ? "bg-[var(--graph-control-bg)] text-[var(--graph-text)] shadow-sm" : "text-[var(--graph-text-dim)] hover:text-[var(--graph-text)]"
            }`}
          >
            {label}
            <span className="hidden lg:inline ml-1">{key === "TB" ? "Vertical" : "Horizontal"}</span>
          </button>
        ))}
        <div className="w-px bg-[var(--graph-border)] mx-0.5" />
        <button
          onClick={() => fitView({ padding: 0.15, duration: 300 })}
          title="Fit all nodes"
          className="px-2.5 py-1 text-[11px] font-mono text-[var(--graph-text-dim)] hover:text-[var(--graph-text)] transition-all duration-200"
        >
          ⊞
        </button>
      </div>

      {/* Language + risk legend */}
      <div className="absolute top-4 left-4 z-10 bg-[var(--graph-surface)]/80 backdrop-blur-sm border border-[var(--graph-border)] px-3 py-2">
        <div className="flex gap-3 items-center">
          {[
            { color: "bg-blue-500", label: "TS" },
            { color: "bg-emerald-500", label: "PY" },
            { color: "bg-cyan-500", label: "Go" },
            { color: "bg-orange-500", label: "RS" },
          ].map(({ color, label }) => (
            <div key={label} className="flex items-center gap-1">
              <span className={`w-1.5 h-1.5 rounded-full ${color}`} />
              <span className="text-[10px] font-mono text-[var(--graph-text-dim)]">{label}</span>
            </div>
          ))}
        </div>
        <div className="flex items-center gap-1.5 mt-1.5 pt-1.5 border-t border-[var(--graph-border)]">
          <span className="text-[9px] font-mono text-[var(--graph-text-muted)]">0</span>
          <div className="h-1.5 w-20 bg-gradient-to-r from-emerald-500 via-yellow-500 to-red-500 opacity-70" />
          <span className="text-[9px] font-mono text-[var(--graph-text-muted)]">10</span>
          <span className="text-[9px] font-mono text-[var(--graph-text-muted)] ml-1">risk</span>
        </div>
      </div>
    </>
  );
}

const nodeTypes = { archFile: FileNode, group: GroupNode };

export default function ArchitectureCanvas(props: Props) {
  const [direction, setDirection] = useState<"TB" | "LR">("TB");

  return (
    <div className="h-full w-full relative">
      {/* Only remount on direction change (which recomputes layout); lens changes update in place. */}
      <ReactFlowProvider key={direction}>
        <ArchCanvasInner {...props} direction={direction} setDirection={setDirection} />
      </ReactFlowProvider>
    </div>
  );
}
