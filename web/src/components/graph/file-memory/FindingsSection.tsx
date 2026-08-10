import { Bug } from "lucide-react";
import { Section } from "./Section";
import { EmptyState } from "./primitives";
import { SEVERITY_STYLES } from "./constants";

type Comment = {
  severity: string;
  category: string;
  body: string;
  created_at: string;
};

type FindingsSectionProps = {
  comments: Comment[] | null | undefined;
  expanded: boolean;
  onToggle: () => void;
};

export function FindingsSection({ comments, expanded, onToggle }: FindingsSectionProps) {
  return (
    <Section
      id="findings"
      icon={<Bug className="h-3 w-3 text-[var(--graph-text-muted)]" />}
      title="Findings"
      count={comments?.length ?? 0}
      expanded={expanded}
      onToggle={onToggle}
    >
      {comments && comments.length > 0 ? (
        <div className="space-y-2">
          {comments.slice(0, 5).map((c, i) => (
            <div
              key={`${c.severity}-${c.body.slice(0, 32)}-${i}`}
              className="rounded border border-[var(--graph-border)] bg-[var(--graph-bg)]/30 px-3 py-2"
            >
              <div className="flex items-center gap-2 mb-1">
                <span
                  className={`inline-block rounded border px-1.5 py-0.5 text-[9px] font-mono ${
                    SEVERITY_STYLES[c.severity] ?? SEVERITY_STYLES.suggestion
                  }`}
                >
                  {c.severity}
                </span>
                {c.category && (
                  <span className="text-[9px] font-mono text-[var(--graph-text-muted)]">{c.category}</span>
                )}
              </div>
              <p className="text-[10px] font-mono text-[var(--graph-text-dim)] leading-relaxed line-clamp-3">
                {c.body}
              </p>
            </div>
          ))}
        </div>
      ) : (
        <EmptyState text="No findings yet." />
      )}
    </Section>
  );
}
