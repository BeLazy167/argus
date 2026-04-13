"use client";

const LENSES = [
  { key: "risk", label: "Risk", hint: "Composite risk score", color: "bg-red-400" },
  { key: "choke", label: "Choke Points", hint: "Files many others depend on", color: "bg-amber-400" },
  { key: "hotspot", label: "Hotspots", hint: "Files with high bug density", color: "bg-orange-400" },
  { key: "coupling", label: "Coupling", hint: "Files that change together", color: "bg-purple-400" },
] as const;

interface LensBarProps {
  active: string;
  onChange: (lens: string) => void;
  fileCounts?: Record<string, number>;
}

export default function LensBar({ active, onChange, fileCounts }: LensBarProps) {
  return (
    <div
      className="flex items-center gap-1 bg-[var(--graph-surface)]/80 backdrop-blur-sm border border-[var(--graph-border)] p-1"
      role="tablist"
      aria-label="Architecture view lens"
    >
      {LENSES.map(({ key, label, hint, color }) => {
        const isActive = active === key;
        return (
          <button
            key={key}
            onClick={() => onChange(key)}
            role="tab"
            aria-selected={isActive}
            aria-label={`${label}: ${hint}`}
            title={hint}
            className={`flex items-center gap-1.5 px-3 py-2.5 text-[11px] font-mono transition-colors duration-150 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber ${
              isActive
                ? "bg-[var(--graph-control-bg)] text-[var(--graph-text)]"
                : "text-[var(--graph-text-dim)] hover:text-[var(--graph-text)] hover:bg-[var(--graph-control-bg)]"
            }`}
          >
            <span className={`w-2 h-2 rounded-full ${color} shrink-0 ${isActive ? "" : "opacity-60"}`} />
            {label}
            {fileCounts?.[key] !== undefined && (
              <span className="text-[9px] text-[var(--graph-text-muted)] tabular-nums">{fileCounts[key]}</span>
            )}
          </button>
        );
      })}
    </div>
  );
}
