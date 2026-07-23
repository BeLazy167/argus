import {
  FileSearch,
  BookOpen,
  MessageSquare,
  Layers,
  Sparkles,
  Send,
  GitPullRequest,
  Bug,
  Shield,
  Network,
  History,
  Gauge,
  RefreshCw,
  Check,
  Plus,
  Database,
  type LucideIcon,
} from "lucide-react";

/**
 * On-brand documentation diagrams — void/charcoal surfaces, amber nodes, mono
 * labels, and amber corner-brackets that echo the surveillance-viewfinder motif
 * used across the marketing site. Deterministic flex/grid layout so they render
 * predictably at every breakpoint (no absolute positioning, no measured layout).
 */

/** Bordered figure with amber viewfinder corner ticks and a mono caption. */
function DiagramFrame({
  label,
  children,
  caption,
}: {
  label: string;
  children: React.ReactNode;
  caption?: string;
}) {
  return (
    <figure className="relative my-8 border border-iron bg-charcoal/40 px-4 py-6 sm:px-6">
      <span aria-hidden className="pointer-events-none absolute -left-px -top-px h-3 w-3 border-l border-t border-amber/60" />
      <span aria-hidden className="pointer-events-none absolute -right-px -top-px h-3 w-3 border-r border-t border-amber/60" />
      <span aria-hidden className="pointer-events-none absolute -bottom-px -left-px h-3 w-3 border-b border-l border-amber/60" />
      <span aria-hidden className="pointer-events-none absolute -bottom-px -right-px h-3 w-3 border-b border-r border-amber/60" />
      <figcaption className="mb-6 font-mono text-[10px] uppercase tracking-[0.2em] text-amber-glow/80">
        {label}
      </figcaption>
      {children}
      {caption ? (
        <p className="mt-5 border-t border-iron/50 pt-3 font-mono text-[10px] leading-relaxed text-slate-text">
          {caption}
        </p>
      ) : null}
    </figure>
  );
}

/** A boxed node with an amber icon tile and a mono label. */
function Node({
  icon: Icon,
  label,
  sub,
}: {
  icon: LucideIcon;
  label: string;
  sub?: string;
}) {
  return (
    <div className="inline-flex items-center gap-3 border border-iron bg-void/60 px-3.5 py-2.5">
      <span className="flex h-8 w-8 shrink-0 items-center justify-center border border-amber/40 bg-amber/[0.07]">
        <Icon className="h-4 w-4 text-amber" />
      </span>
      <span className="min-w-0">
        <span className="block font-mono text-[12px] font-bold uppercase tracking-wider text-foreground">
          {label}
        </span>
        {sub ? (
          <span className="block font-mono text-[10px] text-slate-text">{sub}</span>
        ) : null}
      </span>
    </div>
  );
}

/* ── The Review Pipeline ── */

const PIPELINE = [
  { n: "01", label: "Triage", icon: FileSearch, note: "classify + contract" },
  { n: "02", label: "Context", icon: BookOpen, note: "memory + deps" },
  { n: "03", label: "Review", icon: MessageSquare, note: "specialists / pass" },
  { n: "04", label: "Refine", icon: Layers, note: "pass-2 + judge" },
  { n: "05", label: "Synthesize", icon: Sparkles, note: "dedupe + score" },
  { n: "06", label: "Post & Learn", icon: Send, note: "comment + memory" },
] as const;

export function PipelineDiagram() {
  return (
    <DiagramFrame
      label="Review pipeline · one pass per PR"
      caption="Each stage runs on its own per-repo configurable model — cheap models for triage, stronger ones for deep review. The sequence typically completes in a couple of minutes."
    >
      <div className="flex items-start gap-0 overflow-x-auto pb-1">
        {PIPELINE.map((s, i) => {
          const Icon = s.icon;
          return (
            <div key={s.n} className="flex items-start">
              <div className="flex w-[104px] shrink-0 flex-col items-center gap-2 text-center">
                <span className="flex h-11 w-11 items-center justify-center border border-amber/40 bg-amber/[0.07]">
                  <Icon className="h-5 w-5 text-amber" />
                </span>
                <span className="font-mono text-[9px] tracking-[0.24em] text-amber-glow/70">
                  {s.n}
                </span>
                <span className="font-mono text-[11px] font-bold uppercase tracking-wide text-foreground">
                  {s.label}
                </span>
                <span className="font-mono text-[9.5px] leading-tight text-slate-text">
                  {s.note}
                </span>
              </div>
              {i < PIPELINE.length - 1 ? (
                <span
                  aria-hidden
                  className="mt-4 shrink-0 select-none px-0.5 font-mono text-base text-amber/50"
                >
                  {"→"}
                </span>
              ) : null}
            </div>
          );
        })}
      </div>
    </DiagramFrame>
  );
}

/* ── Deep Review — four specialists in parallel ── */

const SPECIALISTS = [
  { icon: Bug, name: "bug_hunter", desc: "logic errors, nil derefs, broken invariants" },
  { icon: Shield, name: "security", desc: "injection, auth bypass, leaked secrets" },
  { icon: Network, name: "architecture", desc: "dependency direction, blast radius" },
  { icon: History, name: "regression", desc: "bugs that came back from past reviews" },
] as const;

export function DeepReviewDiagram() {
  return (
    <DiagramFrame
      label="Deep review · Pro"
      caption="Instead of one pass, four domain specialists review every changed file at once. Their findings are merged, deduped, and judge-scored before anything is posted."
    >
      <div className="flex flex-col items-center gap-4">
        <Node icon={GitPullRequest} label="Changed files" />
        <span aria-hidden className="font-mono text-sm text-amber/50">
          {"↓"}
        </span>
        <div className="grid w-full grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
          {SPECIALISTS.map((s) => {
            const Icon = s.icon;
            return (
              <div
                key={s.name}
                className="flex flex-col gap-2 border border-iron bg-void/60 p-3"
              >
                <span className="flex items-center gap-2">
                  <span className="flex h-7 w-7 shrink-0 items-center justify-center border border-amber/40 bg-amber/[0.07]">
                    <Icon className="h-3.5 w-3.5 text-amber" />
                  </span>
                  <span className="font-mono text-[11px] font-bold text-foreground">
                    {s.name}
                  </span>
                </span>
                <span className="font-mono text-[10px] leading-snug text-slate-text">
                  {s.desc}
                </span>
              </div>
            );
          })}
        </div>
        <span
          aria-hidden
          className="font-mono text-[10px] uppercase tracking-[0.2em] text-amber-glow/70"
        >
          {"↓ merged · deduped · scored ↓"}
        </span>
        <Node icon={Gauge} label="Ranked findings" sub="hard comment cap per tier" />
      </div>
    </DiagramFrame>
  );
}

/* ── Incremental re-review — comment lifecycle ── */

const OUTCOMES = [
  {
    icon: RefreshCw,
    when: "Flagged code unchanged",
    result: "Comment carried forward",
    tone: "neutral" as const,
  },
  {
    icon: Check,
    when: "Flagged lines fixed by the push",
    result: "Resolved by ‹sha›",
    tone: "good" as const,
  },
  {
    icon: Plus,
    when: "New issue introduced",
    result: "New comment posted",
    tone: "accent" as const,
  },
];

export function LifecycleDiagram() {
  return (
    <DiagramFrame
      label="Incremental re-review · comment lifecycle on push"
      caption="On every push, Argus re-reviews only the delta and reconciles its prior comments instead of re-posting them — typically 30–70% fewer tokens than a full re-review."
    >
      <div className="flex flex-col gap-4 lg:flex-row lg:items-center">
        <div className="flex shrink-0 items-center gap-3">
          <Node icon={MessageSquare} label="Prior comment" />
          <span aria-hidden className="hidden font-mono text-sm text-amber/50 lg:inline">
            {"→"}
          </span>
        </div>
        <div className="flex shrink-0 items-center gap-2 self-start lg:self-center">
          <span className="border border-amber/40 bg-amber/[0.07] px-2 py-1 font-mono text-[10px] uppercase tracking-[0.16em] text-amber">
            git push
          </span>
          <span aria-hidden className="font-mono text-sm text-amber/50">
            {"→"}
          </span>
        </div>
        <div className="flex flex-1 flex-col gap-2">
          {OUTCOMES.map((o) => {
            const Icon = o.icon;
            const resultColor =
              o.tone === "good"
                ? "text-emerald-400/90"
                : o.tone === "accent"
                  ? "text-amber"
                  : "text-slate-text";
            return (
              <div
                key={o.result}
                className="flex items-center gap-3 border border-iron bg-void/60 px-3 py-2"
              >
                <Icon className="h-3.5 w-3.5 shrink-0 text-slate-text" />
                <span className="font-mono text-[10.5px] text-slate-text">
                  {o.when}
                </span>
                <span aria-hidden className="font-mono text-amber/40">
                  {"→"}
                </span>
                <span
                  className={`font-mono text-[10.5px] font-bold ${resultColor}`}
                >
                  {o.result}
                </span>
              </div>
            );
          })}
        </div>
      </div>
    </DiagramFrame>
  );
}

/* ── Memory architecture — store, update, retrieve ── */

const MEMORY_LOOP = [
  {
    icon: MessageSquare,
    title: "Each review emits",
    body: "findings · 👍 / 👎 reactions · replies · fixes · dismissals",
    accent: false,
  },
  {
    icon: Database,
    title: "Supermemory (RAG)",
    body: "{repo} + _shared containers · your key or ours (BYOT)",
    accent: true,
  },
  {
    icon: Sparkles,
    title: "Next review retrieves",
    body: "ranked, relevant memory. Sharper each time.",
    accent: false,
  },
] as const;

const MEMORY_TYPES = [
  { name: "Patterns", desc: "auto-learned code conventions" },
  { name: "Scenarios", desc: "failure cases from reviews + issues" },
  { name: "Decision traces", desc: "comments, replies, approvals, dismissals" },
  { name: "Context graph", desc: "the codebase's living event clock" },
] as const;

const MEMORY_RULES = [
  { glyph: "↑", tone: "text-emerald-400/90", text: "Confirmed → reinforced" },
  { glyph: "↓", tone: "text-amber", text: "Dismissed → suppressed" },
  { glyph: "↻", tone: "text-slate-text", text: "Files change → scenario outdated" },
] as const;

export function MemoryDiagram() {
  return (
    <DiagramFrame
      label="Memory architecture · store · update · retrieve"
      caption="Every review, reaction, fix, and dismissal is written to Supermemory (RAG). Confirmed patterns are reinforced; dismissed ones are suppressed semantically — security findings are never silenced; scenarios go stale when their files change. Each review retrieves what's relevant and builds on the last."
    >
      <div className="flex flex-col gap-2 lg:flex-row lg:items-stretch">
        {MEMORY_LOOP.map((n, i) => {
          const Icon = n.icon;
          return (
            <div key={n.title} className="flex flex-1 items-stretch gap-2">
              <div
                className={`flex-1 border p-3 ${
                  n.accent ? "border-amber/30 bg-amber/[0.05]" : "border-iron bg-void/60"
                }`}
              >
                <div className="flex items-center gap-2">
                  <span className="flex h-7 w-7 shrink-0 items-center justify-center border border-amber/40 bg-amber/[0.07]">
                    <Icon className="h-3.5 w-3.5 text-amber" />
                  </span>
                  <span className="font-mono text-[11px] font-bold uppercase tracking-wide text-foreground">
                    {n.title}
                  </span>
                </div>
                <p className="mt-2 font-mono text-[10px] leading-snug text-slate-text">
                  {n.body}
                </p>
              </div>
              {i < MEMORY_LOOP.length - 1 ? (
                <span
                  aria-hidden
                  className="hidden shrink-0 self-center font-mono text-sm text-amber/50 lg:inline"
                >
                  {"→"}
                </span>
              ) : null}
            </div>
          );
        })}
      </div>

      <div className="mt-4">
        <div className="mb-2 font-mono text-[9px] uppercase tracking-[0.2em] text-amber-glow/60">
          stored as
        </div>
        <div className="grid grid-cols-2 gap-2 lg:grid-cols-4">
          {MEMORY_TYPES.map((m) => (
            <div key={m.name} className="border border-iron bg-charcoal/60 px-2.5 py-2">
              <div className="font-mono text-[10.5px] font-bold text-foreground">
                {m.name}
              </div>
              <div className="font-mono text-[9.5px] leading-snug text-slate-text">
                {m.desc}
              </div>
            </div>
          ))}
        </div>
      </div>

      <div className="mt-3 flex flex-wrap gap-2">
        {MEMORY_RULES.map((r) => (
          <span
            key={r.text}
            className="inline-flex items-center gap-1.5 border border-iron bg-void/60 px-2 py-1 font-mono text-[10px] text-slate-text"
          >
            <span aria-hidden className={`font-bold ${r.tone}`}>
              {r.glyph}
            </span>
            {r.text}
          </span>
        ))}
      </div>

      <div className="mt-4 border-t border-iron/50 pt-3 text-center font-mono text-[10px] uppercase tracking-[0.2em] text-amber-glow/70">
        {"↺ memory compounds — every review builds on the last"}
      </div>
    </DiagramFrame>
  );
}
