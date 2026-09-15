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
  Workflow,
  ScanSearch,
  Terminal,
  Target,
  Globe,
  KeyRound,
  Users,
  GitMerge,
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
    title: "Postgres (RAG)",
    body: "{repo} + _shared containers · vector + full-text hybrid",
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
      caption="Every review, reaction, fix, and dismissal is written to memory (RAG). Confirmed patterns are reinforced; dismissed ones are suppressed semantically — security findings are never silenced; scenarios go stale when their files change. Each review retrieves what's relevant and builds on the last."
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

/* ── Review Contract — signals → contract → gates ── */

const CONTRACT_SIGNALS = [
  "draft flag",
  "labels",
  "branch prefix",
  "path globs",
  "size",
] as const;

const CONTRACT_GATES = [
  { name: "reviewer routing", desc: "squad vs single reviewer" },
  { name: "evidence floor", desc: "never relaxes for security/migrations" },
  { name: "pass-2 eligibility", desc: "scripts/docs/generated skip" },
  { name: "judge thresholds", desc: "class-aware posting bar" },
  { name: "glass box footer", desc: "contract printed on the review" },
] as const;

export function ContractDiagram() {
  return (
    <DiagramFrame
      label="Review contract · computed before review"
      caption="Deterministic signals set the contract first — an LLM fills the change class only when metadata is silent. The contract then gates routing, floors, Pass 2, and judge thresholds, and is printed in the Glass Box footer."
    >
      <div className="flex flex-col items-center gap-4">
        <div className="flex flex-wrap justify-center gap-2">
          {CONTRACT_SIGNALS.map((s) => (
            <span
              key={s}
              className="border border-iron bg-void/60 px-2.5 py-1 font-mono text-[10px] uppercase tracking-[0.14em] text-slate-text"
            >
              {s}
            </span>
          ))}
        </div>
        <span
          aria-hidden
          className="font-mono text-[10px] uppercase tracking-[0.2em] text-amber-glow/70"
        >
          {"↓ deterministic first · LLM fills class only when silent ↓"}
        </span>
        <div className="border border-amber/40 bg-amber/[0.06] px-4 py-3 text-center">
          <div className="font-mono text-[11px] font-bold uppercase tracking-wider text-amber">
            ReviewContract
          </div>
          <div className="mt-1 font-mono text-[10px] text-slate-text">
            {"{ change_class · evidence_bar · depth · signals }"}
          </div>
        </div>
        <span aria-hidden className="font-mono text-sm text-amber/50">
          {"↓"}
        </span>
        <div className="grid w-full grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-5">
          {CONTRACT_GATES.map((g) => (
            <div
              key={g.name}
              className="border border-iron bg-void/60 px-2.5 py-2"
            >
              <div className="font-mono text-[10px] font-bold uppercase tracking-wide text-foreground">
                {g.name}
              </div>
              <div className="font-mono text-[9.5px] leading-snug text-slate-text">
                {g.desc}
              </div>
            </div>
          ))}
        </div>
      </div>
    </DiagramFrame>
  );
}

/* ── What Argus Sees — context fan-in ── */

const CONTEXT_SOURCES = [
  {
    icon: ScanSearch,
    name: "Call graph",
    desc: "callers, shared types, blast radius to depth 2",
  },
  {
    icon: Database,
    name: "Memory",
    desc: "repo patterns, past reviews, file synthesis",
  },
  {
    icon: BookOpen,
    name: "Org rules",
    desc: "custom rules your team wrote in plain language",
  },
  {
    icon: History,
    name: "Scenarios",
    desc: "known failure modes attached to these files",
  },
] as const;

export function ContextDiagram() {
  return (
    <DiagramFrame
      label="What the reviewer sees · context fan-in"
      caption="A finding is only as good as its context. Before a reviewer reads the diff, Argus assembles callers, blast radius, memory, rules, and scenarios — so a change is judged against the system it lands in."
    >
      <div className="flex flex-col items-center gap-4 lg:flex-row lg:items-stretch">
        <div className="flex shrink-0 items-center">
          <Node icon={GitPullRequest} label="PR diff" sub="the changed lines" />
        </div>
        <span aria-hidden className="font-mono text-sm text-amber/50">
          {"→"}
        </span>
        <div className="flex flex-1 flex-col gap-2">
          {CONTEXT_SOURCES.map((s) => {
            const Icon = s.icon;
            return (
              <div
                key={s.name}
                className="flex items-center gap-3 border border-iron bg-void/60 px-3 py-2"
              >
                <span className="flex h-6 w-6 shrink-0 items-center justify-center border border-amber/40 bg-amber/[0.07]">
                  <Icon className="h-3 w-3 text-amber" />
                </span>
                <span className="font-mono text-[10.5px] font-bold text-foreground">
                  {s.name}
                </span>
                <span className="font-mono text-[10px] text-slate-text">
                  {s.desc}
                </span>
              </div>
            );
          })}
        </div>
        <span aria-hidden className="font-mono text-sm text-amber/50">
          {"→"}
        </span>
        <div className="flex shrink-0 items-center">
          <Node
            icon={MessageSquare}
            label="Reviewer context"
            sub="injected into every prompt"
          />
        </div>
      </div>
    </DiagramFrame>
  );
}

/* ── Bot commands — mention → dispatch → effect ── */

const COMMAND_EFFECTS = [
  { cmd: "review", effect: "run a review · --force · --persona" },
  { cmd: "remember", effect: "store a pattern · --org for org-wide" },
  { cmd: "resolve", effect: "close open threads · maintainer-only" },
  { cmd: "fix", effect: "commit suggestion blocks to the branch" },
  { cmd: "test", effect: "test plan · --code drafts runnable tests" },
  { cmd: "help", effect: "post the command table" },
] as const;

export function CommandsDiagram() {
  return (
    <DiagramFrame
      label="Bot commands · mention → dispatch → effect"
      caption="One mention, six verbs. Dispatch parses @argus-eye <command>, checks permission where it matters (resolve is maintainer-only), and answers in seconds."
    >
      <div className="flex flex-col items-center gap-4">
        <div className="flex flex-wrap items-center justify-center gap-3">
          <Node icon={Terminal} label="@argus-eye <command>" sub="PR comment" />
          <span aria-hidden className="font-mono text-sm text-amber/50">
            {"→"}
          </span>
          <span className="border border-amber/40 bg-amber/[0.07] px-2.5 py-1.5 font-mono text-[10px] uppercase tracking-[0.16em] text-amber">
            dispatch
          </span>
          <span aria-hidden className="font-mono text-sm text-amber/50">
            {"→"}
          </span>
        </div>
        <div className="grid w-full grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3">
          {COMMAND_EFFECTS.map((c) => (
            <div
              key={c.cmd}
              className="flex items-center gap-3 border border-iron bg-void/60 px-3 py-2"
            >
              <span className="border border-amber/40 bg-amber/[0.07] px-1.5 py-0.5 font-mono text-[10px] font-bold text-amber">
                {c.cmd}
              </span>
              <span className="font-mono text-[10px] text-slate-text">
                {c.effect}
              </span>
            </div>
          ))}
        </div>
      </div>
    </DiagramFrame>
  );
}

/* ── Gauge — did the comments change the code ── */

const GAUGE_OUTCOMES = [
  {
    name: "addressed_human",
    desc: "a human commit touched the flagged lines",
    weight: "full weight",
    tone: "text-emerald-400/90",
  },
  {
    name: "addressed_agent",
    desc: "a bot-pattern author touched the flagged lines",
    weight: "×0.5",
    tone: "text-emerald-400/90",
  },
  {
    name: "ignored",
    desc: "merged with the flagged lines untouched",
    weight: "zero",
    tone: "text-amber",
  },
  {
    name: "deferred",
    desc: "PR closed without merging",
    weight: "out of scope",
    tone: "text-slate-text",
  },
] as const;

export function GaugeDiagram() {
  return (
    <DiagramFrame
      label="Gauge · did the comments change the code"
      caption="On PR close, the gauge diffs the commits pushed after each comment (±3 lines) and records one outcome per finding — the ground truth behind suppression and calibration."
    >
      <div className="flex flex-col items-center gap-4">
        <div className="flex flex-wrap items-center justify-center gap-3">
          <Node icon={GitMerge} label="PR closes" sub="merged or not" />
          <span aria-hidden className="font-mono text-sm text-amber/50">
            {"→"}
          </span>
          <span className="border border-iron bg-void/60 px-2.5 py-1.5 font-mono text-[10px] uppercase tracking-[0.14em] text-slate-text">
            diff commits after each comment · ±3 lines
          </span>
          <span aria-hidden className="font-mono text-sm text-amber/50">
            {"→"}
          </span>
        </div>
        <div className="grid w-full grid-cols-1 gap-2 sm:grid-cols-2">
          {GAUGE_OUTCOMES.map((o) => (
            <div
              key={o.name}
              className="flex items-center gap-3 border border-iron bg-void/60 px-3 py-2"
            >
              <Target className="h-3.5 w-3.5 shrink-0 text-amber" />
              <span className={`font-mono text-[10.5px] font-bold ${o.tone}`}>
                {o.name}
              </span>
              <span className="font-mono text-[10px] text-slate-text">
                {o.desc}
              </span>
              <span className="ml-auto shrink-0 font-mono text-[9px] uppercase tracking-wider text-amber-glow/70">
                {o.weight}
              </span>
            </div>
          ))}
        </div>
        <span aria-hidden className="font-mono text-sm text-amber/50">
          {"↓"}
        </span>
        <Node
          icon={Gauge}
          label="vw_review_gauge"
          sub="address rate per category × change class"
        />
      </div>
    </DiagramFrame>
  );
}

/* ── MCP — connect your own agent ── */

const MCP_STEPS = [
  {
    icon: Globe,
    name: "discover",
    desc: "GET /.well-known/oauth-protected-resource",
  },
  {
    icon: Users,
    name: "authorize",
    desc: "Clerk OAuth · pick ONE organization",
  },
  {
    icon: KeyRound,
    name: "call",
    desc: "POST /mcp · bearer verified (iss · aud · scopes)",
  },
] as const;

const MCP_READ_TOOLS = [
  "list_repos",
  "search_memory",
  "get_memory_briefing",
  "list_reviews",
  "get_review_status",
  "get_review",
] as const;

const MCP_WRITE_TOOLS = [
  "create_memory",
  "delete_memory",
  "retire_memory",
] as const;

export function MCPDiagram() {
  return (
    <DiagramFrame
      label="MCP server · your agent ↔ Argus memory and reviews"
      caption="Any MCP client that supports remote servers with OAuth connects to https://api.argus.reviews/mcp. One browser login, one organization per connection — the token only ever sees that org's repos and memory."
    >
      <div className="flex flex-col items-center gap-4">
        <div className="flex flex-col items-center gap-3 lg:flex-row">
          <Node icon={Workflow} label="MCP client" sub="Claude Code · Cursor" />
          {MCP_STEPS.map((s) => {
            const Icon = s.icon;
            return (
              <div key={s.name} className="flex items-center gap-3">
                <span aria-hidden className="font-mono text-sm text-amber/50">
                  {"→"}
                </span>
                <div className="flex items-center gap-2.5 border border-iron bg-void/60 px-3 py-2">
                  <span className="flex h-6 w-6 shrink-0 items-center justify-center border border-amber/40 bg-amber/[0.07]">
                    <Icon className="h-3 w-3 text-amber" />
                  </span>
                  <span>
                    <span className="block font-mono text-[10.5px] font-bold uppercase tracking-wide text-foreground">
                      {s.name}
                    </span>
                    <span className="block font-mono text-[9.5px] text-slate-text">
                      {s.desc}
                    </span>
                  </span>
                </div>
              </div>
            );
          })}
        </div>

        <div className="grid w-full grid-cols-1 gap-3 lg:grid-cols-2">
          <div className="border border-iron bg-charcoal/60 p-3">
            <div className="mb-2 font-mono text-[9px] uppercase tracking-[0.2em] text-amber-glow/60">
              argus:read
            </div>
            <div className="flex flex-wrap gap-1.5">
              {MCP_READ_TOOLS.map((t) => (
                <span
                  key={t}
                  className="border border-iron bg-void/60 px-1.5 py-0.5 font-mono text-[9.5px] text-slate-text"
                >
                  {t}
                </span>
              ))}
            </div>
          </div>
          <div className="border border-amber/30 bg-amber/[0.04] p-3">
            <div className="mb-2 font-mono text-[9px] uppercase tracking-[0.2em] text-amber-glow/80">
              argus:memory:write
            </div>
            <div className="flex flex-wrap gap-1.5">
              {MCP_WRITE_TOOLS.map((t) => (
                <span
                  key={t}
                  className="border border-amber/40 bg-void/60 px-1.5 py-0.5 font-mono text-[9.5px] text-amber"
                >
                  {t}
                </span>
              ))}
            </div>
          </div>
        </div>

        <div className="flex flex-wrap justify-center gap-2">
          {[
            "start with list_repos → repo_id / installation_id",
            "retire_memory stops influence · delete_memory removes one record",
            "confirm_pipeline_learned guards learned memory",
          ].map((s) => (
            <span
              key={s}
              className="border border-iron bg-void/60 px-2 py-1 font-mono text-[9.5px] text-slate-text"
            >
              {s}
            </span>
          ))}
        </div>
      </div>
    </DiagramFrame>
  );
}
