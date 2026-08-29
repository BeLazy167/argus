import { Brain } from "lucide-react";
import { formatDistanceToNow } from "@/lib/time";
import type { LearnedMemory, LearnedMemoryCount } from "@/lib/types";

/** Total rows across every bucket. The counts are uncapped, unlike the list. */
function totalWritten(counts: LearnedMemoryCount[]): number {
  return counts.reduce((sum, c) => sum + c.count, 0);
}

/**
 * What Argus learned from this review.
 *
 * Memory writes used to be entirely invisible. Indexing failures are non-fatal
 * and only log server-side, so an installation whose memory had silently
 * stopped working looked exactly like a healthy one — which made "is memory
 * working?" unanswerable for the person it is supposed to help. This panel is
 * the answer.
 *
 * Rendered only for a finished review. A running review has not reached its
 * memory sinks yet, so an empty panel there would report a failure that has not
 * happened.
 *
 * Every type noun is rendered from the server's `label`, never derived here.
 * The posted PR comment names the same counts from the same table in
 * `store.LearnedMemoryLabel`, so the two surfaces cannot describe one review
 * differently. A local table plus a naive `+ "s"` printed "2 PR summarys" on
 * the page while the comment said "2 PR summaries".
 */
export function LearnedMemories({
  memories,
  counts,
  status,
}: {
  memories: LearnedMemory[];
  counts: LearnedMemoryCount[];
  status: string;
}) {
  if (status !== "completed") return null;

  const total = totalWritten(counts);

  return (
    <section
      className="border border-iron bg-charcoal/80 p-5 mb-8"
      aria-label="What Argus learned from this review"
    >
      <div className="flex items-center gap-2 mb-4 flex-wrap">
        <Brain className="h-3.5 w-3.5 text-slate-text" />
        <span className="text-[11px] font-mono uppercase tracking-wider text-slate-text">
          What Argus learned
        </span>
        {total > 0 && (
          <span className="text-[11px] font-mono text-slate-text">
            · {total} {total === 1 ? "memory" : "memories"}
          </span>
        )}
      </div>

      {total === 0 ? (
        <p className="text-xs font-mono text-slate-text">
          This review wrote nothing to memory. That is expected for a trivial change, and
          also what a broken memory configuration looks like — check the memory provider in
          settings if you see it on every review.
        </p>
      ) : (
        <>
          <div className="flex flex-wrap gap-2 mb-4">
            {counts.map((c) => (
              <span
                key={c.type}
                className="border border-iron px-2 py-0.5 text-[11px] font-mono text-foreground"
              >
                {c.count} {c.label}
              </span>
            ))}
          </div>

          <ol className="space-y-3">
            {memories.map((m, i) => (
              <li
                key={`${m.type}-${m.written_at}-${i}`}
                className="border-l border-iron pl-3 min-w-0"
              >
                <div className="flex items-center gap-2 flex-wrap text-[11px] font-mono text-slate-text mb-1">
                  <span className="text-foreground">{m.label}</span>
                  <span>·</span>
                  <span>{m.container_tag === "_shared" ? "org-wide" : m.container_tag}</span>
                  <span>·</span>
                  <span>{formatDistanceToNow(m.written_at)}</span>
                </div>
                <p className="text-xs font-mono text-slate-text whitespace-pre-wrap break-words">
                  {m.excerpt}
                </p>
              </li>
            ))}
          </ol>

          {memories.length < total && (
            <p className="mt-3 text-[11px] font-mono text-slate-text">
              Showing the {memories.length} most recent of {total}.
            </p>
          )}
        </>
      )}
    </section>
  );
}
