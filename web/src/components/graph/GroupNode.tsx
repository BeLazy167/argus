"use client";
import { memo } from "react";
import type { NodeProps } from "@xyflow/react";

function GroupNode({ data }: NodeProps) {
  return (
    <div className="w-full h-full relative">
      {/* Group label */}
      <div className="absolute -top-6 left-3 flex items-center gap-1.5">
        <span className="text-[11px] font-mono uppercase tracking-[0.12em] text-slate-400 bg-[var(--graph-bg)]/80 px-1.5 py-0.5">
          {data.label as string}
        </span>
        <div className="h-px flex-1 bg-gradient-to-r from-slate-700/50 to-transparent min-w-[40px]" />
      </div>
    </div>
  );
}

export default memo(GroupNode);
