import type { ReviewComment } from "@/lib/types";

/* ── Severity Maps ───────────────────────────── */

export const severityStyles: Record<string, string> = {
  critical: "bg-red-400/10 text-red-400 border-red-400/30",
  warning: "bg-amber/10 text-amber border-amber/30",
  suggestion: "bg-blue-400/10 text-blue-400 border-blue-400/30",
  praise: "bg-green-400/10 text-green-400 border-green-400/30",
};

export const severityDot: Record<string, string> = {
  critical: "bg-red-400",
  warning: "bg-amber",
  suggestion: "bg-blue-400",
  praise: "bg-green-400",
};

export function lineRef(c: ReviewComment): string {
  const { start_line, end_line } = c;
  if (start_line != null && end_line != null && start_line !== end_line)
    return `L${start_line}–${end_line}`;
  const line = end_line ?? start_line;
  return line != null ? `L${line}` : "";
}
