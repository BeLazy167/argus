"use client";
import { memo } from "react";
import { Handle, Position, type NodeProps } from "@xyflow/react";

const LANG_COLORS: Record<string, { dot: string; text: string }> = {
  typescript: { dot: "bg-blue-400", text: "text-blue-300" },
  javascript: { dot: "bg-amber-400", text: "text-amber-300" },
  go: { dot: "bg-cyan-400", text: "text-cyan-300" },
  python: { dot: "bg-emerald-400", text: "text-emerald-300" },
  rust: { dot: "bg-orange-400", text: "text-orange-300" },
};

const DEFAULT_LANG = { dot: "bg-slate-400", text: "text-slate-300" };

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
  language: string;
  riskScore: number;
  fanIn: number;
  bugDensity: number;
  changeFrequency: number;
  insight?: string;
  isChokePoint: boolean;
  isHotspot: boolean;
  lens: string;
  [key: string]: unknown;
}

function FileNode({ data }: NodeProps) {
  const {
    label,
    language,
    riskScore,
    fanIn,
    bugDensity,
    changeFrequency,
    isChokePoint,
    isHotspot,
  } = data as FileNodeData;

  const lang = LANG_COLORS[language] || DEFAULT_LANG;
  // Pulse when file is in the top tier of churn — 10+ PRs is the high-churn threshold.
  const shouldPulse = changeFrequency >= 10;

  return (
    <div
      className={`group relative rounded-md bg-[#12121a]/90 backdrop-blur-md
        shadow-lg ${densityGlow(bugDensity)} ${densityBorderColor(bugDensity)}
        transition-[box-shadow,filter,border-color] duration-200
        [@media(hover:hover)]:hover:shadow-xl [@media(hover:hover)]:hover:brightness-125
        px-3 py-2 min-w-[120px] cursor-pointer
        ${shouldPulse ? "animate-pulse-subtle" : ""}`}
      style={{ borderWidth: fanInBorder(fanIn), borderStyle: "solid" }}
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
        <span className="ml-auto font-mono text-[9px] font-semibold text-slate-400 bg-slate-800/60 rounded px-1 py-0.5 shrink-0">
          {riskScore.toFixed(1)}
        </span>
      </div>

      <div className="text-[8px] font-mono text-slate-600 mt-0.5">
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
