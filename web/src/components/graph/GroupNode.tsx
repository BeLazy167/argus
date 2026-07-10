"use client";
import { memo } from "react";
import type { NodeProps } from "@xyflow/react";

function GroupNode({ data }: NodeProps) {
  return (
    <div className="w-full h-full relative">
      {/* Group label — always-visible, high-contrast uppercase caption */}
      <div className="absolute -top-6 left-3 flex items-center gap-1.5">
        <span className="text-[11px] font-mono font-medium uppercase tracking-[0.14em] text-[var(--graph-text)] bg-[var(--graph-bg)]/90 px-1.5 py-0.5 rounded-sm">
          {data.label as string}
        </span>
        <div className="h-px flex-1 bg-gradient-to-r from-[var(--graph-border)] to-transparent min-w-[40px]" />
      </div>
    </div>
  );
}

export default memo(GroupNode);
