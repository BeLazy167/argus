import { ChevronDown } from "lucide-react";

export type SectionKey = "metrics" | "deps" | "risk" | "patterns" | "findings" | "traces";

type SectionProps = {
  id: SectionKey;
  icon: React.ReactNode;
  title: string;
  count?: number;
  expanded: boolean;
  onToggle: () => void;
  children: React.ReactNode;
};

export function Section({ icon, title, count, expanded, onToggle, children }: SectionProps) {
  return (
    <div className="border-b border-[var(--graph-border)] last:border-0">
      <button
        onClick={onToggle}
        className="flex items-center gap-2 w-full px-4 py-3 hover:bg-[var(--graph-control-bg)] transition-colors"
      >
        {icon}
        <span className="text-[10px] font-mono uppercase tracking-[0.12em] text-[var(--graph-text)]">
          {title}
        </span>
        {count !== undefined && (
          <span className="text-[9px] font-mono text-[var(--graph-text-muted)] ml-1">
            ({count})
          </span>
        )}
        <ChevronDown
          className={`h-3 w-3 text-[var(--graph-text-muted)] ml-auto transition-transform duration-200 ${
            expanded ? "rotate-0" : "-rotate-90"
          }`}
        />
      </button>
      <div
        className={`overflow-hidden transition-all duration-200 ${
          expanded ? "max-h-[2000px] opacity-100" : "max-h-0 opacity-0"
        }`}
      >
        <div className="px-4 pb-3">{children}</div>
      </div>
    </div>
  );
}
