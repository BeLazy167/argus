"use client";
import { memo } from "react";
import { Handle, Position, type NodeProps } from "@xyflow/react";

const LANG_COLORS: Record<string, string> = {
  typescript: "border-blue-500/50 bg-blue-500/5",
  javascript: "border-yellow-500/50 bg-yellow-500/5",
  go: "border-cyan-500/50 bg-cyan-500/5",
  python: "border-green-500/50 bg-green-500/5",
  rust: "border-orange-500/50 bg-orange-500/5",
};

const KIND_BADGES: Record<string, string> = {
  module: "bg-purple-500/20 text-purple-400",
  class: "bg-blue-500/20 text-blue-400",
  function: "bg-amber-500/20 text-amber-400",
  file: "bg-slate-500/20 text-slate-400",
};

function ModuleNode({ data }: NodeProps) {
  const langClass = LANG_COLORS[data.language as string] || "border-iron bg-iron/5";
  const kindClass = KIND_BADGES[data.kind as string] || KIND_BADGES.file;

  return (
    <div
      className={`rounded-lg border-2 ${langClass} px-4 py-3 min-w-[160px] shadow-lg backdrop-blur-sm transition-all hover:shadow-xl hover:scale-[1.02] cursor-pointer`}
      onClick={() => {
        if (data.githubUrl) window.open(data.githubUrl as string, "_blank");
      }}
    >
      <Handle type="target" position={Position.Top} className="!bg-slate-500 !w-2 !h-2 !border-0" />
      <div className="flex items-center gap-2 mb-1">
        <span className={`rounded px-1.5 py-0.5 text-[9px] font-mono uppercase ${kindClass}`}>
          {data.kind as string}
        </span>
      </div>
      <p className="text-xs font-semibold text-foreground truncate">{data.label as string}</p>
      <p className="text-[10px] font-mono text-slate-text truncate mt-0.5">{data.filePath as string}</p>
      <Handle type="source" position={Position.Bottom} className="!bg-slate-500 !w-2 !h-2 !border-0" />
    </div>
  );
}

export default memo(ModuleNode);
