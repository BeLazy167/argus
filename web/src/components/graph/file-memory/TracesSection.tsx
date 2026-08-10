import { GitBranch } from "lucide-react";
import { formatDistanceToNow } from "@/lib/time";
import { Section } from "./Section";
import { EmptyState } from "./primitives";
import { KIND_BADGE_STYLES } from "./constants";

type Trace = {
  trace_type: string;
  content: string;
  pr_number: number;
  created_at: string;
};

type TracesSectionProps = {
  traces: Trace[] | null | undefined;
  expanded: boolean;
  onToggle: () => void;
};

export function TracesSection({ traces, expanded, onToggle }: TracesSectionProps) {
  return (
    <Section
      id="traces"
      icon={<GitBranch className="h-3 w-3 text-[var(--graph-text-muted)]" />}
      title="Traces"
      count={traces?.length ?? 0}
      expanded={expanded}
      onToggle={onToggle}
    >
      {traces && traces.length > 0 ? (
        <div className="space-y-2">
          {traces.map((t, i) => (
            <div
              key={`${t.created_at}-${i}`}
              className="rounded border border-[var(--graph-border)] bg-[var(--graph-bg)]/30 px-3 py-2"
            >
              <div className="flex items-center gap-2 mb-1">
                <span
                  className={`inline-block rounded border px-1.5 py-0.5 text-[9px] font-mono ${
                    KIND_BADGE_STYLES[t.trace_type] ?? "border-slate-500/30 bg-slate-500/10 text-[var(--graph-text-dim)]"
                  }`}
                >
                  {t.trace_type}
                </span>
                {t.pr_number > 0 && (
                  <span className="text-[9px] font-mono text-[var(--graph-text-muted)]">PR #{t.pr_number}</span>
                )}
                <span className="text-[9px] font-mono text-[var(--graph-text-muted)] ml-auto">
                  {formatDistanceToNow(t.created_at)}
                </span>
              </div>
              <p className="text-[10px] font-mono text-[var(--graph-text-dim)] leading-relaxed line-clamp-2">
                {t.content}
              </p>
            </div>
          ))}
        </div>
      ) : (
        <EmptyState text="No traces yet." />
      )}
    </Section>
  );
}
