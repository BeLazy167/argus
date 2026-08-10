import { Network } from "lucide-react";
import type { ArchFile } from "@/lib/queries/architecture";
import { Section } from "./Section";
import { EmptyState } from "./primitives";

type DepsSectionProps = {
  archFile: ArchFile;
  expanded: boolean;
  onToggle: () => void;
};

export function DepsSection({ archFile, expanded, onToggle }: DepsSectionProps) {
  return (
    <Section
      id="deps"
      icon={<Network className="h-3 w-3 text-[var(--graph-text-muted)]" />}
      title="Dependencies"
      count={archFile.coupling.length}
      expanded={expanded}
      onToggle={onToggle}
    >
      {archFile.coupling.length > 0 ? (
        <div className="space-y-1.5">
          <p className="text-[9px] font-mono text-[var(--graph-text-muted)] uppercase tracking-wider">
            Coupled files (co-change)
          </p>
          {archFile.coupling.map((c) => (
            <div
              key={c.path}
              className="flex items-center justify-between text-[10px] font-mono rounded border border-[var(--graph-border)] bg-[var(--graph-bg)]/30 px-2 py-1.5"
            >
              <span className="text-[var(--graph-text-dim)] truncate" title={c.path}>
                {c.path.split("/").pop()}
              </span>
              <span className="text-[var(--graph-text-muted)] ml-2 shrink-0">
                {(c.score * 100).toFixed(0)}%
              </span>
            </div>
          ))}
        </div>
      ) : (
        <EmptyState text="No strong couplings detected." />
      )}

      {archFile.symbols.length > 0 && (
        <div className="mt-3 space-y-1">
          <p className="text-[9px] font-mono text-[var(--graph-text-muted)] uppercase tracking-wider">
            Symbols ({archFile.symbols.length})
          </p>
          <div className="flex flex-wrap gap-1">
            {archFile.symbols.slice(0, 10).map((s) => (
              <span
                key={s}
                className="text-[9px] font-mono text-[var(--graph-text-muted)] bg-[var(--graph-control-bg)] rounded px-1.5 py-0.5"
              >
                {s}
              </span>
            ))}
            {archFile.symbols.length > 10 && (
              <span className="text-[9px] font-mono text-[var(--graph-text-muted)]">
                +{archFile.symbols.length - 10} more
              </span>
            )}
          </div>
        </div>
      )}
    </Section>
  );
}
