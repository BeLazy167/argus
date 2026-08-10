"use client";

import { Loader2, Info } from "lucide-react";

export function Tip({ text }: { text: string }) {
  return (
    <span className="group relative inline-flex ml-1 cursor-help">
      <Info className="h-3 w-3 text-muted-foreground/50 group-hover:text-muted-foreground transition-colors" />
      <span className="pointer-events-none absolute bottom-full left-1/2 -translate-x-1/2 mb-1.5 w-52 bg-popover border border-border px-2.5 py-1.5 text-[10px] font-mono text-popover-foreground opacity-0 group-hover:opacity-100 transition-opacity z-50 shadow-lg">
        {text}
      </span>
    </span>
  );
}

export function StatCard({
  label,
  value,
  icon: Icon,
  tip,
  sub,
  valueColor,
  iconColor,
}: {
  label: string;
  value: string;
  icon: React.ComponentType<{ className?: string }>;
  tip?: string;
  sub?: string;
  valueColor?: string;
  iconColor?: string;
}) {
  return (
    <div className="border border-border bg-card p-5 flex flex-col gap-2 hover:border-primary/30 transition-colors">
      <div className="flex items-center gap-1.5">
        <Icon className={`h-3.5 w-3.5 ${iconColor ?? "text-muted-foreground"}`} />
        <span
          className="text-[10px] font-mono uppercase text-muted-foreground"
          style={{ letterSpacing: "1.2px" }}
        >
          {label}
        </span>
        {tip && <Tip text={tip} />}
      </div>
      <span
        className={`text-[32px] leading-none font-mono font-bold tracking-tight ${valueColor ?? "text-foreground"}`}
      >
        {value}
      </span>
      {sub && <span className="text-[10px] font-mono text-muted-foreground">{sub}</span>}
    </div>
  );
}

export function SectionHeader({ title, tip }: { title: string; tip?: string }) {
  return (
    <div className="flex items-center gap-1.5 mb-4">
      <h2
        className="text-xs font-mono font-bold text-foreground uppercase"
        style={{ letterSpacing: "1.5px" }}
      >
        {title}
      </h2>
      {tip && <Tip text={tip} />}
    </div>
  );
}

export function Loading() {
  return (
    <div className="flex items-center justify-center py-12">
      <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
    </div>
  );
}

export function Err({ label }: { label: string }) {
  return (
    <div className="flex items-center justify-center py-12 text-xs font-mono text-destructive">
      Failed to load {label}
    </div>
  );
}
