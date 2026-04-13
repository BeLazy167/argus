"use client";
import { memo } from "react";
import { Handle, Position, type NodeProps } from "@xyflow/react";

const LANG_COLORS: Record<string, { dot: string; text: string }> = {
  typescript: { dot: "bg-blue-500", text: "text-blue-600 dark:text-blue-300" },
  javascript: { dot: "bg-amber-500", text: "text-amber-700 dark:text-amber-300" },
  go: { dot: "bg-cyan-500", text: "text-cyan-700 dark:text-cyan-300" },
  python: { dot: "bg-emerald-500", text: "text-emerald-700 dark:text-emerald-300" },
  rust: { dot: "bg-orange-500", text: "text-orange-700 dark:text-orange-300" },
};

const DEFAULT_LANG = { dot: "bg-slate-400", text: "text-[var(--graph-text)]" };

/** Maps bug_density (bugs per 100 lines) to a border color from green→yellow→red. */
function densityBorderColor(d: number): string {
  if (d <= 0.5) return "border-emerald-500/40";
  if (d <= 2.0) return "border-yellow-500/50";
  return "border-red-500/60";
}

/** Maps bug_density to a glow shadow. */
function densityGlow(d: number): string {
  if (d <= 0.5) return "shadow-emerald-500/10";
  if (d <= 2.0) return "shadow-yellow-500/15";
  return "shadow-red-500/20";
}

/** Border width proportional to fan_in: 1px base + 0.5px per fan_in, max 4px. */
function fanInBorder(fanIn: number): string {
  const px = Math.min(1 + fanIn * 0.5, 4);
  return `${px}px`;
}

interface FileNodeData {
  label: string;
  fullPath: string;
  language: string;
  riskScore: number;
  fanIn: number;
  bugDensity: number;
  changeFrequency: number;
  insight?: string;
  isChokePoint: boolean;
  isHotspot: boolean;
  selected: boolean;
  lens: string;
  [key: string]: unknown;
}

function FileNode({ data }: NodeProps) {
  const {
    label,
    fullPath,
    language,
    riskScore,
    fanIn,
    bugDensity,
    changeFrequency,
    isChokePoint,
    isHotspot,
    selected,
  } = data as FileNodeData;

  const lang = LANG_COLORS[language] || DEFAULT_LANG;
  // Pulse when file is in the top tier of churn — 10+ PRs is the high-churn threshold.
  const shouldPulse = changeFrequency >= 10;

  return (
    <div
      className={`group relative bg-[var(--graph-surface)] backdrop-blur-md
        shadow-md ${densityGlow(bugDensity)}
        ${selected ? "border-amber-500/80 shadow-[0_0_12px_rgba(245,158,11,0.15)]" : densityBorderColor(bugDensity)}
        transition-[box-shadow,filter,border-color,transform] duration-200
        [@media(hover:hover)]:hover:border-amber-500/60 [@media(hover:hover)]:hover:shadow-xl [@media(hover:hover)]:hover:scale-[1.03]
        px-3 py-2 min-w-[120px] cursor-pointer
        ${shouldPulse ? "animate-pulse-subtle" : ""}`}
      style={{ borderWidth: fanInBorder(fanIn), borderStyle: "solid" }}
      title={`${fullPath}\nRisk: ${riskScore.toFixed(1)} · Fan-in: ${fanIn} · Bugs: ${bugDensity.toFixed(2)}`}
    >
      <Handle
        type="target"
        position={Position.Top}
        className="!bg-transparent !w-3 !h-1.5 !rounded-full !border-0 !-top-[3px]"
      />

      <div className="flex items-center gap-1.5">
        <span className={`w-1.5 h-1.5 rounded-full ${lang.dot} shrink-0`} />
        <span className={`font-mono font-medium text-[11px] truncate leading-tight ${lang.text}`}>
          {label}
        </span>
        <span className="ml-auto font-mono text-[9px] font-semibold text-[var(--graph-text-dim)] bg-[var(--graph-control-bg)] px-1 py-0.5 shrink-0">
          {riskScore.toFixed(1)}
        </span>
      </div>

      <div className="text-[8px] font-mono text-[var(--graph-text-muted)] mt-0.5">
        fan_in: {fanIn} · bugs: {bugDensity.toFixed(2)}
      </div>

      {isChokePoint && (
        <span className="absolute -top-1.5 -right-1.5 rounded-full bg-amber-500/20 border border-amber-500/30 px-1 py-0.5 text-[7px] font-mono text-amber-400">
          choke
        </span>
      )}

      {isHotspot && !isChokePoint && (
        <span className="absolute -top-1.5 -right-1.5 rounded-full bg-red-500/20 border border-red-500/30 px-1 py-0.5 text-[7px] font-mono text-red-400">
          hot
        </span>
      )}

      <Handle
        type="source"
        position={Position.Bottom}
        className="!bg-transparent !w-3 !h-1.5 !rounded-full !border-0 !-bottom-[3px]"
      />
    </div>
  );
}

export default memo(FileNode);
