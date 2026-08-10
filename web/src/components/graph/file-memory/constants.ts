export const SEVERITY_STYLES: Record<string, string> = {
  critical: "border-red-500/30 bg-red-500/10 text-red-400",
  warning: "border-amber-500/30 bg-amber-500/10 text-amber-400",
  suggestion: "border-blue-500/30 bg-blue-500/10 text-blue-400",
  praise: "border-green-500/30 bg-green-500/10 text-green-400",
};

export const SOURCE_BADGE_STYLES: Record<string, string> = {
  manual: "border-slate-500/30 bg-slate-500/10 text-[var(--graph-text-dim)]",
  auto_learn: "border-amber-500/30 bg-amber-500/10 text-amber-400",
  convention: "border-blue-500/30 bg-blue-500/10 text-blue-400",
};

export const KIND_BADGE_STYLES: Record<string, string> = {
  review: "border-blue-500/30 bg-blue-500/10 text-blue-400",
  scoring: "border-amber-500/30 bg-amber-500/10 text-amber-400",
  triage: "border-purple-500/30 bg-purple-500/10 text-purple-400",
  synthesis: "border-green-500/30 bg-green-500/10 text-green-400",
};

export function riskColor(traceCount: number): string {
  if (traceCount >= 10) return "border-red-500/40 bg-red-500/15 text-red-400";
  if (traceCount >= 5) return "border-amber-500/40 bg-amber-500/15 text-amber-400";
  return "border-green-500/40 bg-green-500/15 text-green-400";
}

/** Risk color for the progress bar */
export function riskBarColor(score: number): string {
  if (score >= 7) return "bg-red-500";
  if (score >= 4) return "bg-amber-500";
  return "bg-emerald-500";
}

/** Format percentile rank — only show callouts for unusual values, hide noise. */
export function formatPercentile(pct: number): string {
  if (pct >= 95) return `top ${Math.max(100 - pct, 1)}%`;
  if (pct >= 90) return `top 10%`;
  if (pct >= 75) return `top 25%`;
  return ""; // suppress everything else — too noisy
}
