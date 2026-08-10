"use client";

export function AdoptionBar({ label, value }: { label: string; value: number }) {
  return (
    <div>
      <div className="flex items-center justify-between mb-2">
        <span className="text-[11px] font-mono text-foreground">{label}</span>
        <span className="text-[11px] font-mono text-primary font-bold">{value.toFixed(1)}%</span>
      </div>
      <div className="h-2 bg-border/30 overflow-hidden" style={{ borderRadius: 4 }}>
        <div
          className="h-full bg-primary/60 transition-all duration-700"
          style={{ width: `${Math.min(value, 100)}%`, borderRadius: 4 }}
        />
      </div>
    </div>
  );
}
