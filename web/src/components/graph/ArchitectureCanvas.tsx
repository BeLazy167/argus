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

/**
 * Approximate rendered width (px) of a monospace label at 12px, so a node box
 * can be sized to fit its filename. Keeping the box wide enough for the text is
 * what lets names stay readable at the default zoom without hovering.
 */
const LABEL_CHAR_PX = 7.4;
function labelBoxWidth(label: string): number {
  const shown = Math.min(label.length, 30); // FileNode middle-truncates beyond this
  // h-padding(28) + lang dot & gap(18) + text + gap(10) + risk badge(34)
  return 28 + 18 + shown * LABEL_CHAR_PX + 10 + 34;
}

/**
 * Node box size. Width fits the label first (so filenames never truncate at
 * default zoom) but still grows with risk; height scales with risk too, so
 * riskier files read as larger without sacrificing legibility.
 */
function nodeDims(riskScore: number, label: string): { width: number; height: number } {
  const riskW = 150 + riskScore * 8;
  const w = Math.max(labelBoxWidth(label), riskW, 160);
  const h = 54 + riskScore * 3;
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
  // Imperative store handle — the initial view reads live pane size + viewport
  const colorMode = useColorMode();
  const [selectedNodeId, setSelectedNodeId] = useState<string | null>(null);
  const maxDensity = useMemo(() => Math.max(...files.map((f) => f.bug_density), 0.01), [files]);

  const layout = useMemo(() => {
    const isHorizontal = direction === "LR";
    const dimsFor = (f: ArchFile) => nodeDims(f.risk_score, f.path.split("/").pop() ?? f.path);

    // Node data/styling (positions are assigned by the two-pass layout below).
    const rfNodes: Node[] = files.map((f) => {
      const fileName = f.path.split("/").pop() ?? f.path;
      const size = dimsFor(f);
      const lensStyle = lensHighlightStyle(f, lens);
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

    // ---------------------------------------------------------------------
    // Two-pass clustered layout. A single flat dagre over 200+ files produced
    // a ~35k-px-wide map because dagre knows nothing about directories: rank
    // width grows with every node it places side by side. Instead:
    //   Pass A lays each directory out on its own (compact dagre over the
    //          intra-directory edges, or a grid when a directory has no
    //          internal structure), yielding a tight cluster per directory.
    //   Pass B lays the CLUSTERS out as super-nodes sized by their bounding
    //          boxes, connected by aggregated cross-directory edges.
    // Total canvas size is then bounded by the group-level layout, not by the
    // widest file rank.
    // ---------------------------------------------------------------------
    const PAD_X = 40;
    const PAD_Y = 66; // extra headroom for the group caption
    const fileDims = new Map(files.map((f) => [f.path, dimsFor(f)]));

    const groupsMap = new Map<string, string[]>();
    for (const f of files) {
      const dir = dirGroup(f.path);
      const bucket = groupsMap.get(dir);
      if (bucket) bucket.push(f.path);
      else groupsMap.set(dir, [f.path]);
    }
    const dirOf = (p: string) => dirGroup(p);

    // Pass A: local layout per directory → positions relative to the cluster
    // origin plus the cluster's content bounding box.
    type LocalLayout = { local: Map<string, { x: number; y: number }>; w: number; h: number };
    const gridLayout = (members: string[]): LocalLayout => {
      // Row-major grid, hottest files first; column count keeps clusters
      // roughly square-ish rather than one long rank.
      const byRisk = [...members].sort(
        (a, b) =>
          (files.find((f) => f.path === b)?.risk_score ?? 0) -
          (files.find((f) => f.path === a)?.risk_score ?? 0),
      );
      const cols = Math.max(1, Math.min(isHorizontal ? 2 : 3, Math.ceil(Math.sqrt(byRisk.length))));
      const GX = 36;
      const GY = 26;
      const local = new Map<string, { x: number; y: number }>();
      const colW: number[] = new Array(cols).fill(0);
      const rowH: number[] = [];
      byRisk.forEach((p, i) => {
        const d = fileDims.get(p) ?? { width: 160, height: 54 };
        const c = i % cols;
        const r = Math.floor(i / cols);
        colW[c] = Math.max(colW[c] ?? 0, d.width);
        rowH[r] = Math.max(rowH[r] ?? 0, d.height);
      });
      const colX: number[] = [];
      let acc = 0;
      for (let c = 0; c < cols; c++) {
        colX[c] = acc;
        acc += (colW[c] ?? 0) + GX;
      }
      const rowY: number[] = [];
      acc = 0;
      for (let r = 0; r < rowH.length; r++) {
        rowY[r] = acc;
        acc += (rowH[r] ?? 0) + GY;
      }
      byRisk.forEach((p, i) => {
        local.set(p, { x: colX[i % cols] ?? 0, y: rowY[Math.floor(i / cols)] ?? 0 });
      });
      const usedCols = Math.min(cols, byRisk.length);
      return {
        local,
        w: (colX[usedCols - 1] ?? 0) + (colW[usedCols - 1] ?? 0),
        h: Math.max(acc - GY, 0),
      };
    };

    const dagreLocal = (members: string[], intra: { s: string; t: string }[]): LocalLayout => {
      const lg = new dagre.graphlib.Graph().setDefaultEdgeLabel(() => ({}));
      lg.setGraph({ rankdir: direction, nodesep: 40, ranksep: 70, marginx: 0, marginy: 0 });
      for (const p of members) {
        const d = fileDims.get(p) ?? { width: 160, height: 54 };
        lg.setNode(p, { width: d.width, height: d.height });
      }
      for (const e of intra) lg.setEdge(e.s, e.t);
      dagre.layout(lg);
      let minX = Infinity;
      let minY = Infinity;
      let maxX = -Infinity;
      let maxY = -Infinity;
      const centers = new Map<string, { x: number; y: number }>();
      for (const p of members) {
        const n = lg.node(p);
        const d = fileDims.get(p) ?? { width: 160, height: 54 };
        centers.set(p, { x: n.x, y: n.y });
        minX = Math.min(minX, n.x - d.width / 2);
        minY = Math.min(minY, n.y - d.height / 2);
        maxX = Math.max(maxX, n.x + d.width / 2);
        maxY = Math.max(maxY, n.y + d.height / 2);
      }
      const local = new Map<string, { x: number; y: number }>();
      for (const p of members) {
        const c = centers.get(p);
        if (!c) continue;
        const d = fileDims.get(p) ?? { width: 160, height: 54 };
        local.set(p, { x: c.x - d.width / 2 - minX, y: c.y - d.height / 2 - minY });
      }
      return { local, w: maxX - minX, h: maxY - minY };
    };

    const localLayouts = new Map<string, LocalLayout>();
    groupsMap.forEach((members, dir) => {
      const intra = edges
        .filter((e) => dirOf(e.source) === dir && dirOf(e.target) === dir)
        .map((e) => ({ s: e.source, t: e.target }));
      let ll = intra.length > 0 ? dagreLocal(members, intra) : gridLayout(members);
      // Even a connected cluster can go wide when dagre puts many files on one
      // rank; past a sane width the grid reads better and keeps Pass B bounded.
      if (ll.w > 2400) ll = gridLayout(members);
      localLayouts.set(dir, ll);
    });

    // Pass B: arrange clusters. Super-node size = content bbox + group chrome.
    const sg = new dagre.graphlib.Graph().setDefaultEdgeLabel(() => ({}));
    sg.setGraph({ rankdir: direction, nodesep: 70, ranksep: 130, marginx: 60, marginy: 60 });
    groupsMap.forEach((members, dir) => {
      const ll = localLayouts.get(dir);
      if (!ll) return;
      const multi = members.length >= 2;
      sg.setNode(dir, {
        width: ll.w + (multi ? PAD_X * 2 : 0),
        height: ll.h + (multi ? PAD_Y + PAD_X : 0),
      });
    });
    const crossSeen = new Set<string>();
    for (const e of edges) {
      const a = dirOf(e.source);
      const b = dirOf(e.target);
      if (a === b) continue;
      const k = `${a}→${b}`;
      if (crossSeen.has(k)) continue;
      crossSeen.add(k);
      sg.setEdge(a, b);
    }
    dagre.layout(sg);

    // Compose: absolute file position = cluster origin + chrome pad + local.
    const groupOrigin = new Map<string, { x: number; y: number }>();
    groupsMap.forEach((members, dir) => {
      const n = sg.node(dir);
      const ll = localLayouts.get(dir);
      if (!n || !ll) return;
      const multi = members.length >= 2;
      const w = ll.w + (multi ? PAD_X * 2 : 0);
      const h = ll.h + (multi ? PAD_Y + PAD_X : 0);
      groupOrigin.set(dir, { x: n.x - w / 2, y: n.y - h / 2 });
    });

    const positionedNodes = rfNodes.map((node) => {
      const dir = dirOf(node.id);
      const origin = groupOrigin.get(dir);
      const ll = localLayouts.get(dir);
      const loc = ll?.local.get(node.id);
      if (!origin || !loc) return node;
      const multi = (groupsMap.get(dir)?.length ?? 0) >= 2;
      return {
        ...node,
        targetPosition: isHorizontal ? Position.Left : Position.Top,
        sourcePosition: isHorizontal ? Position.Right : Position.Bottom,
        position: {
          x: origin.x + (multi ? PAD_X : 0) + loc.x,
          y: origin.y + (multi ? PAD_Y : 0) + loc.y,
        },
      };
    });

    const groupNodes: Node[] = [];
    groupsMap.forEach((members, dir) => {
      if (members.length < 2) return;
      const origin = groupOrigin.get(dir);
      const ll = localLayouts.get(dir);
      if (!origin || !ll) return;
      groupNodes.push({
        id: `group:${dir}`,
        type: "group",
        position: { x: origin.x, y: origin.y },
        data: { label: groupLabel(dir) },
        style: {
          width: ll.w + PAD_X * 2,
          height: ll.h + PAD_Y + PAD_X,
          // Quiet outline instead of a filled box — keeps the grouping legible
          // without making the canvas read as a grid of nested tiles.
          backgroundColor: "transparent",
          borderRadius: 12,
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

  // Smart default view: frame the top-risk cluster instead of fitting all nodes.
  // Initial view: handled by React Flow's built-in `fitView` prop (see the
  // <ReactFlow> element below). Three generations of bespoke initial-fit
  // effects all lost races against panZoom initialization; the library runs
  // its one-shot fit at the correct internal lifecycle moment instead.

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
        fitView
        fitViewOptions={{ padding: 0.2, minZoom: 0.45, maxZoom: 1 }}
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
