"use client";
import { memo } from "react";
import { Handle, Position, type NodeProps } from "@xyflow/react";

const LANG_THEME: Record<string, { glow: string; dot: string; border: string; text: string }> = {
  typescript: { glow: "shadow-blue-500/20", dot: "bg-blue-400", border: "border-blue-500/30", text: "text-blue-300" },
  javascript: { glow: "shadow-amber-500/20", dot: "bg-amber-400", border: "border-amber-500/30", text: "text-amber-300" },
  go: { glow: "shadow-cyan-500/20", dot: "bg-cyan-400", border: "border-cyan-500/30", text: "text-cyan-300" },
  python: { glow: "shadow-emerald-500/20", dot: "bg-emerald-400", border: "border-emerald-500/30", text: "text-emerald-300" },
  rust: { glow: "shadow-orange-500/20", dot: "bg-orange-400", border: "border-orange-500/30", text: "text-orange-300" },
};

const DEFAULT_THEME = { glow: "shadow-slate-500/10", dot: "bg-slate-400", border: "border-slate-500/20", text: "text-slate-300" };

function ModuleNode({ data }: NodeProps) {
  const lang = LANG_THEME[data.language as string] || DEFAULT_THEME;
  const kind = data.kind as string;
  const name = data.label as string;
  const isInterface = kind === "interface" || (name.startsWith("I") && name[1] === name[1]?.toUpperCase() && kind === "class");
  const isMerged = data.isMerged as boolean;
  const prNumber = data.prNumber as number | null;
  const isOpenPR = prNumber != null && !isMerged;

  return (
    <div
      className={`group relative border ${lang.border} bg-[#12121a]/90 backdrop-blur-md
        shadow-lg ${lang.glow} transition-[box-shadow,filter,border-color] duration-200
        [@media(hover:hover)]:hover:shadow-xl [@media(hover:hover)]:hover:brightness-125 [@media(hover:hover)]:hover:border-opacity-60 cursor-pointer
        ${isInterface ? "px-2.5 py-1.5 min-w-[100px]" : "px-3 py-2 min-w-[130px]"}
        ${isOpenPR ? "border-dashed animate-pulse-subtle" : ""}`}
      onDoubleClick={() => {
        if (data.githubUrl) window.open(data.githubUrl as string, "_blank");
      }}
    >
      <Handle type="target" position={Position.Top} className="!bg-transparent !w-3 !h-1.5 !rounded-full !border-0 !-top-[3px]" />

      {/* Language indicator line at top */}
      <div className={`absolute -top-px left-3 right-3 h-px ${lang.dot} opacity-40 rounded-full`} />

      <div className="flex items-center gap-1.5">
        <span className={`w-1.5 h-1.5 rounded-full ${lang.dot} shrink-0 ${isInterface ? "opacity-50" : ""}`} />
        <span className={`font-mono font-medium truncate leading-tight ${isInterface ? "text-[10px] opacity-60" : "text-[11px]"} ${lang.text}`}>
          {name}
        </span>
      </div>

      {/* Subtle kind indicator */}
      {!isInterface && (
        <span className="text-[8px] font-mono text-slate-600 uppercase tracking-wider mt-0.5 block">
          {kind}
        </span>
      )}

      {isOpenPR && (
        <span className="absolute -bottom-1 -right-1 rounded-full bg-amber-500/20 border border-amber-500/30 px-1 py-0.5 text-[7px] font-mono text-amber-400">
          PR #{prNumber}
        </span>
      )}

      <Handle type="source" position={Position.Bottom} className="!bg-transparent !w-3 !h-1.5 !rounded-full !border-0 !-bottom-[3px]" />
    </div>
  );
}

export default memo(ModuleNode);
