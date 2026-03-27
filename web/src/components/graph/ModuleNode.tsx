"use client";
import { memo } from "react";
import { Handle, Position, type NodeProps } from "@xyflow/react";

const LANG_COLORS: Record<string, { border: string; bg: string; dot: string }> = {
  typescript: { border: "border-blue-500/40", bg: "bg-blue-950/40", dot: "bg-blue-400" },
  javascript: { border: "border-yellow-500/40", bg: "bg-yellow-950/40", dot: "bg-yellow-400" },
  go: { border: "border-cyan-500/40", bg: "bg-cyan-950/40", dot: "bg-cyan-400" },
  python: { border: "border-green-500/40", bg: "bg-green-950/40", dot: "bg-green-400" },
  rust: { border: "border-orange-500/40", bg: "bg-orange-950/40", dot: "bg-orange-400" },
};

const DEFAULT_LANG = { border: "border-iron", bg: "bg-iron/10", dot: "bg-slate-400" };

function ModuleNode({ data }: NodeProps) {
  const lang = LANG_COLORS[data.language as string] || DEFAULT_LANG;
  const kind = data.kind as string;
  const isInterface = kind === "interface" || (data.label as string).startsWith("I") && kind === "class";

  return (
    <div
      className={`rounded-lg border ${lang.border} ${lang.bg} px-3 py-2 shadow-md backdrop-blur-sm transition-all hover:shadow-lg hover:brightness-110 cursor-pointer ${
        isInterface ? "opacity-70 min-w-[120px]" : "min-w-[150px]"
      }`}
      onClick={() => {
        if (data.githubUrl) window.open(data.githubUrl as string, "_blank");
      }}
    >
      <Handle type="target" position={Position.Top} className="!bg-slate-600 !w-1.5 !h-1.5 !border-0" />
      <div className="flex items-center gap-1.5">
        <span className={`w-2 h-2 rounded-full ${lang.dot} shrink-0`} />
        <p className={`font-semibold text-foreground truncate ${isInterface ? "text-[10px]" : "text-xs"}`}>
          {data.label as string}
        </p>
      </div>
      <Handle type="source" position={Position.Bottom} className="!bg-slate-600 !w-1.5 !h-1.5 !border-0" />
    </div>
  );
}

export default memo(ModuleNode);
