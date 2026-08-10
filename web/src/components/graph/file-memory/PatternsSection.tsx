import { AlertTriangle } from "lucide-react";
import { Section } from "./Section";
import { EmptyState } from "./primitives";
import { SOURCE_BADGE_STYLES } from "./constants";

type Pattern = {
  content: string;
  source: string;
};

type PatternsSectionProps = {
  patterns: Pattern[] | null | undefined;
  expanded: boolean;
  onToggle: () => void;
};

function sourceLabel(source: string): string {
  if (source === "auto_learn") return "AI-Learned";
  if (source === "convention") return "Convention";
  return "Manual";
}

export function PatternsSection({ patterns, expanded, onToggle }: PatternsSectionProps) {
  return (
    <Section
      id="patterns"
      icon={<AlertTriangle className="h-3 w-3 text-[var(--graph-text-muted)]" />}
      title="Patterns"
      count={patterns?.length ?? 0}
      expanded={expanded}
      onToggle={onToggle}
    >
      {patterns && patterns.length > 0 ? (
        <div className="space-y-2">
          {patterns.map((p, i) => (
            <div
              key={`${p.source}-${p.content.slice(0, 32)}-${i}`}
              className="rounded border border-[var(--graph-border)] bg-[var(--graph-bg)]/30 px-3 py-2"
            >
              <p className="text-[10px] font-mono text-[var(--graph-text)] leading-relaxed">
                {p.content}
              </p>
              <span
                className={`inline-block mt-1.5 rounded border px-1.5 py-0.5 text-[9px] font-mono ${
                  SOURCE_BADGE_STYLES[p.source] ?? SOURCE_BADGE_STYLES.manual
                }`}
              >
                {sourceLabel(p.source)}
              </span>
            </div>
          ))}
        </div>
      ) : (
        <EmptyState text="No patterns yet." />
      )}
    </Section>
  );
}
