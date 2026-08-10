"use client";

import { ResponsiveContainer } from "recharts";

export function ChartCard({
  title,
  value,
  color,
  children,
}: {
  title: string;
  value?: string;
  color: string;
  children: React.ReactNode;
}) {
  return (
    <div className="border border-border bg-card p-5 overflow-hidden">
      <div className="flex items-center justify-between">
        <span
          className="text-[10px] font-mono text-muted-foreground uppercase"
          style={{ letterSpacing: "1.2px" }}
        >
          {title}
        </span>
        {value && (
          <span className="text-sm font-mono font-bold" style={{ color }}>
            {value}
          </span>
        )}
      </div>
      <div className="h-48 mt-3">
        <ResponsiveContainer width="100%" height="100%">
          {children as React.ReactElement}
        </ResponsiveContainer>
      </div>
    </div>
  );
}
