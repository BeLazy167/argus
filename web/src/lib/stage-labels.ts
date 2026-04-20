/**
 * Canonical stage labels, order, and color palette.
 *
 * Mirrors backend/internal/pipeline/labels.go. Keep the two files in sync —
 * a new pipeline stage needs an entry in both. The Go side has a test that
 * catches struct-field drift; there is no automated TS parity check today,
 * so review both files together when adding or renaming a stage. Unmapped
 * keys degrade to the raw string — visible, just unstyled.
 */

/** Pipeline-execution order. Used for stable rendering in the per-review
 *  detail page when iterating a TokenUsage object. Stats page sorts by cost
 *  desc and ignores this order. */
export const STAGE_ORDER = [
  "intent",
  "triage",
  "enrichment",
  "conventions",
  "patterns",
  "lead_agent",
  "graph",
  "file_synthesis",
  "review",
  "acceptance",
  "cross_pr",
  "simulation",
  "scoring",
  "synthesis",
  "reply",
] as const;

/** Canonical render order for review specialists. Matches Go
 *  SpecialistOrder. The "review" bucket is the skim-fallback for entries
 *  with no Specialist field. */
export const SPECIALIST_ORDER = [
  "correctness",
  "bug_hunter",
  "security",
  "architecture",
  "regression",
  "review",
] as const;

const STAGE_LABELS: Record<string, string> = {
  intent: "Intent",
  triage: "Triage",
  enrichment: "Enrichment",
  conventions: "Conventions",
  patterns: "Patterns",
  lead_agent: "Lead agent",
  graph: "Graph",
  file_synthesis: "File synthesis",
  review: "Review",
  acceptance: "Acceptance",
  cross_pr: "Cross-PR",
  simulation: "Simulation",
  scoring: "Scoring",
  synthesis: "Synthesis",
  reply: "Reply",
};

/**
 * Return a human-readable label for a stage key. Composite keys like
 * `review.bug_hunter` render as `Review · bug_hunter`. The `review.review`
 * skim-fallback is collapsed to plain `Review` — rendering "Review · review"
 * is redundant.
 *
 * Unknown base keys pass through as-is so a new Go stage shipped without a
 * matching label shows up (degraded, not missing).
 */
export function stageLabel(key: string): string {
  const dot = key.indexOf(".");
  if (dot < 0) return STAGE_LABELS[key] ?? key;
  const base = key.slice(0, dot);
  const sub = key.slice(dot + 1);
  const baseLabel = STAGE_LABELS[base] ?? base;
  if (base === "review" && sub === "review") return baseLabel;
  return `${baseLabel} · ${sub}`;
}

/** Base palette for plain stage keys. Kept in hex; stageColor() derives
 *  composite-key shades via HSL hue-shift. */
const BASE_COLORS: Record<string, string> = {
  intent: "#fde047",
  triage: "#10b981",
  enrichment: "#22d3ee",
  conventions: "#14b8a6",
  patterns: "#a855f7",
  lead_agent: "#eab308",
  graph: "#f97316",
  file_synthesis: "#0ea5e9",
  review: "#3b82f6",
  acceptance: "#2dd4bf",
  cross_pr: "#d946ef",
  simulation: "#6366f1",
  scoring: "#f59e0b",
  synthesis: "#8b5cf6",
  reply: "#f472b6",
};

/** djb2-ish string hash; deterministic so `bug_hunter` always picks the
 *  same shade within a render and across reloads. */
function hashStr(s: string): number {
  let h = 5381;
  for (let i = 0; i < s.length; i++) h = ((h << 5) + h + s.charCodeAt(i)) | 0;
  return Math.abs(h);
}

/** Parse #rrggbb into [h(0-360), s(0-100), l(0-100)]. Assumes the caller
 *  validates — BASE_COLORS is the only source, all entries are well-formed. */
function hexToHsl(hex: string): [number, number, number] {
  const n = parseInt(hex.slice(1), 16);
  const r = ((n >> 16) & 0xff) / 255;
  const g = ((n >> 8) & 0xff) / 255;
  const b = (n & 0xff) / 255;
  const max = Math.max(r, g, b);
  const min = Math.min(r, g, b);
  const l = (max + min) / 2;
  if (max === min) return [0, 0, Math.round(l * 100)];
  const d = max - min;
  const s = l > 0.5 ? d / (2 - max - min) : d / (max + min);
  let h: number;
  if (max === r) h = (g - b) / d + (g < b ? 6 : 0);
  else if (max === g) h = (b - r) / d + 2;
  else h = (r - g) / d + 4;
  return [Math.round(h * 60), Math.round(s * 100), Math.round(l * 100)];
}

/**
 * Return a CSS color for a stage key. Plain keys use the base palette;
 * composite keys derive a deterministic hue-shifted shade (±20°) with
 * slightly reduced saturation so the specialist variants read as "same
 * family, distinct row." Unknown base keys fall back to the theme primary.
 */
export function stageColor(key: string): string {
  const dot = key.indexOf(".");
  const base = dot < 0 ? key : key.slice(0, dot);
  const hex = BASE_COLORS[base];
  if (!hex) return "hsl(var(--primary))";
  if (dot < 0) return hex;
  const sub = key.slice(dot + 1);
  const [h, s, l] = hexToHsl(hex);
  const hueShift = (hashStr(sub) % 40) - 20;
  const dH = (h + hueShift + 360) % 360;
  const dS = Math.max(25, s - 12);
  const dL = Math.max(30, Math.min(70, l - 4));
  return `hsl(${dH} ${dS}% ${dL}%)`;
}
