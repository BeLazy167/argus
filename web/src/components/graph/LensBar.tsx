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
      className="flex items-center gap-1 bg-[var(--graph-surface)]/80 backdrop-blur-sm border border-slate-800 p-1"
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
            className={`flex items-center gap-1.5 px-3 py-2.5 text-[11px] font-mono rounded-md transition-colors duration-150 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-slate-500 ${
              isActive
                ? "bg-slate-800 text-slate-100"
                : "text-slate-500 hover:text-slate-300 hover:bg-slate-800/40"
            }`}
          >
            <span className={`w-2 h-2 rounded-full ${color} shrink-0 ${isActive ? "" : "opacity-60"}`} />
            {label}
            {fileCounts?.[key] !== undefined && (
              <span className="text-[9px] text-slate-600 tabular-nums">{fileCounts[key]}</span>
            )}
          </button>
        );
      })}
    </div>
  );
}
