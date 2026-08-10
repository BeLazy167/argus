import type { Period } from "@/lib/queries/org-stats";

export const PERIODS: { value: Period; label: string }[] = [
  { value: "7d", label: "7d" },
  { value: "30d", label: "30d" },
  { value: "90d", label: "90d" },
];

export const PIE_COLORS = [
  "#f59e0b",
  "#3b82f6",
  "#10b981",
  "#ef4444",
  "#8b5cf6",
  "#ec4899",
  "#06b6d4",
  "#84cc16",
];

export const SEV_COLORS: Record<string, string> = {
  critical: "#ef4444",
  warning: "#f59e0b",
  suggestion: "#3b82f6",
  praise: "#10b981",
};

/** Format a dollar amount: < $1 shows 3 decimals, otherwise 2. */
export function fmt$(n: number) {
  return n < 1 ? `$${n.toFixed(3)}` : `$${n.toFixed(2)}`;
}

/** Format a token count as abbreviated string (M / k / raw). */
export function fmtTok(n: number) {
  return n >= 1e6
    ? `${(n / 1e6).toFixed(1)}M`
    : n >= 1e3
      ? `${(n / 1e3).toFixed(1)}k`
      : String(n);
}

/** Format seconds into Xm Ys or Xs. */
export function fmtSecs(s: number) {
  return s >= 60 ? `${Math.floor(s / 60)}m ${s % 60}s` : `${s}s`;
}

/** Tailwind class for a review score (1-10 scale). Red <=4, green >=7, amber otherwise. */
export function scoreColor(n: number): string {
  if (n <= 4) return "text-destructive";
  if (n >= 7) return "text-green-500";
  return "text-primary";
}

export const tooltipStyle = {
  background: "hsl(var(--card))",
  border: "1px solid hsl(var(--border))",
  fontSize: 11,
  fontFamily: "monospace",
  color: "hsl(var(--foreground))",
};
