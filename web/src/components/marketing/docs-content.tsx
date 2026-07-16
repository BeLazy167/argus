"use client";

import { useEffect, useRef, useState } from "react";
import {
  GitPullRequest,
  Zap,
  FileSearch,
  MessageSquare,
  Send,
  BookOpen,
  Shield,
  Bug,
  Gauge,
  Paintbrush,
  Eye,
  Code2,
  FlaskConical,
  Layers,
  RefreshCw,
  Play,
  SlidersHorizontal,
  Brain,
  Key,
  UserCog,
  Sparkles,
  Terminal,
  Network,
  Activity,
  Radio,
  Target,
  ThumbsUp,
  ThumbsDown,
  ToggleRight,
  BarChart3,
  Search,
  History,
  TestTube2,
  AlertTriangle,
  Sun,
  GitCompare,
  Workflow,
  Database,
  Focus,
  Compass,
  Scan,
  Flag,
  Info,
} from "lucide-react";

/* ── Section data ── */

const SECTIONS = [
  { id: "getting-started", label: "Getting Started" },
  { id: "pipeline", label: "The Review Pipeline" },
  { id: "review-contract", label: "The Review Contract" },
  { id: "review-laws", label: "Review Laws" },
  { id: "deep-review", label: "Deep Review", pro: true },
  { id: "incremental-reviews", label: "Incremental Reviews" },
  { id: "what-argus-sees", label: "What Argus Sees" },
  { id: "architecture-viz", label: "Architecture Visualization", pro: true },
  { id: "code-simulation", label: "Code Simulation", pro: true },
  { id: "pr-enrichment", label: "PR Enrichment & Diagrams", pro: true },
  { id: "conversational-review", label: "Conversational Review" },
  { id: "live-timeline", label: "Live Activity Timeline" },
  { id: "severities", label: "Severities" },
  { id: "categories", label: "Categories" },
  { id: "rules", label: "Review Rules" },
  { id: "models", label: "Model Config" },
  { id: "api-keys", label: "API Keys (BYOK)" },
  { id: "supermemory", label: "BYOT Supermemory" },
  { id: "personas", label: "Review Personas" },
  { id: "auto-review", label: "Auto-review & Triggers" },
  { id: "commands", label: "Bot Commands" },
  { id: "test-generation", label: "Test Generation" },
  { id: "memory", label: "Memory & Learning", pro: true },
  { id: "glass-box", label: "Glass Box & Gauge" },
  { id: "insights", label: "Insights & Risk" },
  { id: "token-tracking", label: "Token & Cost Tracking" },
  { id: "light-mode", label: "Light Mode" },
  { id: "feature-flags", label: "Feature Flags" },
  { id: "settings", label: "Settings & Controls" },
] as const;

const SEVERITIES = [
  {
    name: "critical",
    dot: "bg-red-400",
    text: "text-red-400",
    description:
      "Bugs, security vulnerabilities, data loss risks, or logic errors that will cause failures in production.",
  },
  {
    name: "warning",
    dot: "bg-amber",
    text: "text-amber",
    description:
      "Performance issues, error handling gaps, race conditions, or code that works but is fragile.",
  },
  {
    name: "suggestion",
    dot: "bg-blue-400",
    text: "text-blue-400",
    description:
      "Readability improvements, style consistency, better naming, or minor refactors.",
  },
  {
    name: "praise",
    dot: "bg-green-400",
    text: "text-green-400",
    description:
      "Genuinely notable code — at most one sentence in the review summary. Never posted as inline filler comments.",
  },
];

const CATEGORIES = [
  {
    name: "security",
    icon: Shield,
    description:
      "Injection vulnerabilities, leaked credentials, unsafe deserialization, SSRF, path traversal.",
  },
  {
    name: "bug",
    icon: Bug,
    description:
      "Off-by-one errors, nil dereferences, broken invariants, incorrect boolean logic, missing edge cases.",
  },
  {
    name: "performance",
    icon: Gauge,
    description:
      "N+1 queries, unnecessary allocations, missing caching, O(n\u00B2) where O(n) is possible.",
  },
  {
    name: "error_handling",
    icon: Zap,
    description:
      "Swallowed errors, empty catch blocks, missing error propagation, silent fallbacks.",
  },
  {
    name: "readability",
    icon: Eye,
    description:
      "Unclear naming, complex nesting, missing comments on non-obvious logic, dead code.",
  },
  {
    name: "style",
    icon: Paintbrush,
    description:
      "Formatting inconsistencies, convention violations, import ordering. Under the Review Laws these are never posted — that's the linter's job.",
  },
  {
    name: "type_design",
    icon: Code2,
    description:
      "Weak type invariants, stringly-typed APIs, missing generics, poor encapsulation.",
  },
  {
    name: "testing",
    icon: FlaskConical,
    description:
      "Missing edge case tests, brittle assertions, untested error paths, test-only code in production.",
  },
];

const PIPELINE_STAGES = [
  {
    step: "01",
    label: "Triage",
    icon: FileSearch,
    description:
      "Computes the review contract and classifies each changed file by risk before any tokens are spent. Generated files, lockfiles, and vendored dependencies are skipped.",
  },
  {
    step: "02",
    label: "Context",
    icon: BookOpen,
    description:
      "Gathers cross-file context, blast radius, and relevant memory — so the review understands how your change affects the rest of the system.",
  },
  {
    step: "03",
    label: "Review",
    icon: MessageSquare,
    description:
      "Performs focused analysis across multiple angles in parallel — correctness, security, architecture, and regression risk.",
  },
  {
    step: "04",
    label: "Refine",
    icon: Layers,
    description:
      "Deduplicates findings, then an LLM judge scores each one against class-aware thresholds — on every review, every plan. Low-signal comments are dropped or folded into a collapsed Minor notes section.",
  },
  {
    step: "05",
    label: "Synthesize",
    icon: Sparkles,
    description:
      "Produces a scannable verdict with fix ordering, severity tiers, and diagrams — actionable, not a wall of text.",
  },
  {
    step: "06",
    label: "Post & Learn",
    icon: Send,
    description:
      "Posts inline comments to GitHub and updates memory from your feedback — so future reviews get sharper.",
  },
];

/* ── Components ── */

function SidebarLink({
  id,
  label,
  active,
  pro,
}: {
  id: string;
  label: string;
  active: boolean;
  /** Renders a compact "Pro" dot after the label — small enough not to
   * compete with the link itself but visible enough to communicate the
   * plan gate before the user clicks through. */
  pro?: boolean;
}) {
  return (
    <a
      href={`#${id}`}
      className={`flex items-center justify-between gap-2 py-1.5 text-xs font-mono transition-[color,border-color] duration-150 pl-3 ${
        active
          ? "text-amber border-l-2 border-amber"
          : "text-slate-text hover:text-foreground border-l-2 border-transparent hover:border-iron"
      }`}
    >
      <span className="truncate">{label}</span>
      {pro ? (
        <span
          className="shrink-0 text-[8px] font-mono font-semibold uppercase tracking-[0.14em] text-amber/80"
          aria-label="Pro plan only"
          title="Pro plan only"
        >
          PRO
        </span>
      ) : null}
    </a>
  );
}

function SectionHeader({
  id,
  title,
  pro,
}: {
  id: string;
  title: string;
  /** When true, inline a PRO tag next to the title so readers on Free see
   * upfront that the section describes a paid-plan feature. */
  pro?: boolean;
}) {
  return (
    <div id={id} className="scroll-mt-24">
      <h2 className="font-mono text-xl font-bold text-foreground mb-1 inline-flex items-baseline gap-2.5 flex-wrap">
        <span>{title}</span>
        {pro ? <ProTag /> : null}
      </h2>
      <div className="h-px bg-iron mb-6" />
    </div>
  );
}

/** Inline PRO tag. Shares aesthetic with the one in docs/features/memory-tuning:
 * amber-on-dark, mono, uppercase, tight tracking. Marked as aria-label so
 * screen readers say "Pro plan only" instead of just reading "Pro". */
function ProTag() {
  return (
    <span
      className="relative -top-0.5 inline-flex items-center border border-amber/50 bg-amber/10 px-1.5 py-0.5 text-[9px] font-mono font-semibold uppercase tracking-[0.16em] text-amber"
      aria-label="Pro plan only"
    >
      Pro
    </span>
  );
}

function CodeBlock({ children }: { children: string }) {
  return (
    <pre className="border border-iron bg-void/80 p-4 overflow-x-auto">
      <code className="text-xs font-mono text-foreground/80 leading-relaxed">
        {children}
      </code>
    </pre>
  );
}

function TerminalBlock({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <div>
      <div className="flex items-center gap-2 border border-iron bg-charcoal px-4 py-2.5">
        <div className="flex gap-1.5">
          <div className="h-2.5 w-2.5 rounded-full bg-iron" />
          <div className="h-2.5 w-2.5 rounded-full bg-iron" />
          <div className="h-2.5 w-2.5 rounded-full bg-iron" />
        </div>
        <span className="ml-2 text-[11px] font-mono text-amber">{title}</span>
      </div>
      <div className="border-x border-b border-iron bg-void p-5 space-y-4">
        {children}
      </div>
    </div>
  );
}

/* ── Page ── */

export function DocsContent() {
  const [activeSection, setActiveSection] = useState<string>(SECTIONS[0].id);
  const sidebarScrollRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    // Scroll-spy: flag a section as active once its top crosses 20% into
    // the viewport, and until its bottom passes the 40% mark. The middle
    // band is wide enough that fast scrolling still registers every
    // section — prior version used -60% on the bottom which created
    // dead zones on short sections.
    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting) {
            setActiveSection(entry.target.id);
          }
        }
      },
      { rootMargin: "-20% 0px -40% 0px" },
    );

    for (const s of SECTIONS) {
      const el = document.getElementById(s.id);
      if (el) observer.observe(el);
    }

    return () => observer.disconnect();
  }, []);

  // Keep the active link visible inside the sidebar's own scroll region
  // WITHOUT touching the page/window scroll. Element.scrollIntoView()
  // scrolls every ancestor scroll container including the window — a
  // prior version used it here and caused the page to auto-scroll back
  // up whenever the user scrolled down enough to change the active
  // section. We now compute the offset manually and mutate only
  // container.scrollTop, which the browser guarantees doesn't affect
  // any other scroll context.
  useEffect(() => {
    const container = sidebarScrollRef.current;
    if (!container) return;
    const link = container.querySelector<HTMLElement>(
      `a[href="#${activeSection}"]`,
    );
    if (!link) return;
    const cRect = container.getBoundingClientRect();
    const lRect = link.getBoundingClientRect();
    const margin = 12;
    if (lRect.top < cRect.top + margin) {
      container.scrollTop += lRect.top - cRect.top - margin;
    } else if (lRect.bottom > cRect.bottom - margin) {
      container.scrollTop += lRect.bottom - cRect.bottom + margin;
    }
  }, [activeSection]);

  return (
    <section className="mx-auto max-w-5xl px-6 py-28">
      {/* Header */}
      <p className="mb-3 text-[11px] font-mono uppercase tracking-[0.15em] text-amber">
        Documentation
      </p>
      <h1 className="font-display text-4xl font-bold text-foreground mb-3">
        Argus Documentation
      </h1>
      <p className="mb-3 text-[11px] font-mono uppercase tracking-[0.18em] text-slate-text/70">
        Last updated{" "}
        <time dateTime="2026-07-11" className="text-foreground">
          July 11, 2026
        </time>
      </p>
      <p className="text-sm font-mono text-slate-text mb-16 max-w-xl">
        Find the bugs your team missed. Setup, pipeline, memory, commands
        &mdash; everything you need to get started.
      </p>

      <div className="flex gap-12">
        {/* Sidebar */}
        <nav aria-label="Documentation navigation" className="hidden lg:block w-48 shrink-0">
          <div
            ref={sidebarScrollRef}
            className="sticky top-20 max-h-[calc(100vh-6rem)] overflow-y-auto space-y-0.5 pb-8"
          >
            <p className="text-[10px] font-mono uppercase tracking-[0.15em] text-iron mb-3">
              On this page
            </p>
            {SECTIONS.map((s) => (
              <SidebarLink
                key={s.id}
                id={s.id}
                label={s.label}
                active={activeSection === s.id}
                pro={"pro" in s ? s.pro : false}
              />
            ))}
          </div>
        </nav>

        {/* Content */}
        <div className="flex-1 min-w-0 max-w-full space-y-16 overflow-hidden" style={{ overflowWrap: "break-word" }}>
          {/* ── Getting Started ── */}
          <div>
            <SectionHeader id="getting-started" title="Getting Started" />
            <p className="text-xs font-mono text-slate-text mb-6 leading-relaxed">
              Three minutes from zero to your first automated review.
            </p>
            <div className="space-y-5">
              {[
                {
                  step: "1",
                  title: "Install the GitHub App",
                  desc: "One click at github.com/apps/argus-eye. Works with orgs and personal accounts. Your repos appear in the dashboard immediately.",
                },
                {
                  step: "2",
                  title: "Select repositories",
                  desc: "Choose which repos Argus watches. Enable all or pick specific ones. You can change this any time.",
                },
                {
                  step: "3",
                  title: "Add your API key",
                  desc: "Bring your own key — OpenAI, Anthropic, or any OpenRouter provider. Your key, your costs, your data stays yours.",
                },
                {
                  step: "4",
                  title: "Open a pull request",
                  desc: "Every PR triggers Argus automatically. Inline comments appear with one-click suggestion fixes you can commit straight from GitHub.",
                },
                {
                  step: "5",
                  title: "Teach it your standards",
                  desc: "Choose a review persona, add custom rules, or let Argus learn your team's patterns over time. It gets sharper with every review.",
                },
              ].map((item) => (
                <div key={item.step} className="flex gap-4">
                  <span className="flex h-7 w-7 shrink-0 items-center justify-center bg-amber/10 text-xs font-mono font-medium text-amber">
                    {item.step}
                  </span>
                  <div>
                    <h3 className="text-sm font-bold text-foreground mb-0.5">
                      {item.title}
                    </h3>
                    <p className="text-xs font-mono text-slate-text leading-relaxed">
                      {item.desc}
                    </p>
                  </div>
                </div>
              ))}
            </div>
          </div>

          {/* ── The Review Pipeline ── */}
          <div>
            <SectionHeader id="pipeline" title="The Review Pipeline" />
            <p className="text-xs font-mono text-slate-text mb-6 leading-relaxed">
              Every PR runs through a multi-stage pipeline. Each stage can
              use a different model, configurable per-repo. The sequence
              typically completes in a couple of minutes.
            </p>
            <div className="space-y-1">
              {PIPELINE_STAGES.map((stage, i) => {
                const Icon = stage.icon;
                return (
                  <div
                    key={stage.step}
                    className="border border-iron bg-charcoal p-4 flex gap-4"
                  >
                    <div className="flex flex-col items-center shrink-0 pt-0.5">
                      <div className="h-8 w-8 bg-amber/10 flex items-center justify-center">
                        <Icon className="h-4 w-4 text-amber" />
                      </div>
                      {i < PIPELINE_STAGES.length - 1 && (
                        <div className="w-px h-full bg-iron mt-1" />
                      )}
                    </div>
                    <div className="flex-1 min-w-0">
                      <div className="flex items-center gap-3 mb-1">
                        <span className="text-[10px] font-mono text-amber tracking-wider">
                          {stage.step}
                        </span>
                        <span className="text-xs font-mono font-bold text-foreground uppercase tracking-wider">
                          {stage.label}
                        </span>
                      </div>
                      <p className="text-xs font-mono text-slate-text leading-relaxed">
                        {stage.description}
                      </p>
                    </div>
                  </div>
                );
              })}
            </div>
          </div>

          {/* ── The Review Contract ── */}
          <div>
            <SectionHeader id="review-contract" title="The Review Contract" />
            <p className="text-xs font-mono text-slate-text mb-3 leading-relaxed">
              Not every PR deserves the same review.
            </p>
            <p className="text-xs font-mono text-slate-text mb-8 leading-relaxed">
              Before reviewing, Argus computes a contract for the PR: what
              kind of change it is, and how deeply it should be reviewed.
              Classification is deterministic-first &mdash; labels, branch
              prefixes, and path patterns decide the class, while draft
              status, title framing, and size adjust depth and evidence
              bar. The LLM fills in intent only when the metadata is
              silent. The contract is visible on every review.
            </p>

            <h3 className="text-sm font-bold text-foreground mb-3">
              Change classes
            </h3>
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-3 mb-8">
              {[
                {
                  name: "production",
                  desc: "The default. Full pipeline, full depth — exactly the review Argus has always done.",
                },
                {
                  name: "migration",
                  desc: "Schema and data migrations. The safety floor is raised and never relaxes — destructive SQL gets maximum scrutiny.",
                },
                {
                  name: "one_time_script",
                  desc: "Backfills, one-off jobs. Reviewed by a single balanced reviewer focused on correctness and data safety — not the full specialist squad.",
                },
                {
                  name: "test",
                  desc: "Test-only changes. Full-depth review — the class travels with every finding so the judge reads them in context.",
                },
                {
                  name: "config",
                  desc: "Config and infra changes. Full-depth review with the class on record for the judge and the footer.",
                },
                {
                  name: "docs",
                  desc: "Documentation. Raised posting bar for nitpicks; skips the second pass entirely.",
                },
                {
                  name: "generated",
                  desc: "Lockfiles, codegen output. Raised posting bar for nitpicks; skips the second pass entirely.",
                },
                {
                  name: "revert",
                  desc: "Reverts, detected from revert/ branch prefixes (other revert forms are classified from intent). Full-depth review with the class on record.",
                },
              ].map((cls) => (
                <div key={cls.name} className="border border-iron bg-charcoal p-4">
                  <span className="text-xs font-mono font-bold text-amber">
                    {cls.name}
                  </span>
                  <p className="text-[11px] font-mono text-slate-text leading-relaxed mt-1">
                    {cls.desc}
                  </p>
                </div>
              ))}
            </div>

            <h3 className="text-sm font-bold text-foreground mb-3">
              Depth follows the contract
            </h3>
            <div className="space-y-3">
              {[
                {
                  icon: SlidersHorizontal,
                  title: "Routing",
                  desc: "One-off scripts get a single balanced reviewer (correctness + data safety) instead of the full specialist squad. Docs and generated changes skip the second pass. Production PRs get full depth.",
                },
                {
                  icon: Shield,
                  title: "Floors never relax",
                  desc: "Security-relevant and migration changes keep maximum scrutiny regardless of class, labels, or past dismissals. No signal can lower this floor.",
                },
                {
                  icon: AlertTriangle,
                  title: "Oversized PRs",
                  desc: "Beyond reviewable size (~1,500 changed lines or 60 files), Argus still reviews — but posts an honest reduced-confidence note and recommends splitting the PR.",
                },
              ].map((item) => {
                const Icon = item.icon;
                return (
                  <div key={item.title} className="border border-iron bg-charcoal p-4">
                    <div className="flex items-center gap-3 mb-2">
                      <Icon className="h-4 w-4 text-amber" />
                      <span className="text-xs font-mono font-bold text-foreground">
                        {item.title}
                      </span>
                    </div>
                    <p className="text-[11px] font-mono text-slate-text leading-relaxed">
                      {item.desc}
                    </p>
                  </div>
                );
              })}
            </div>

            <p className="text-[11px] font-mono text-iron mt-4">
              The contract appears in the Glass Box footer of every posted
              review, e.g.{" "}
              <code className="text-amber bg-iron/40 rounded px-1.5 py-0.5">
                Contract: production/full
              </code>
              . See{" "}
              <a href="#glass-box" className="text-amber hover:text-foreground transition-colors">
                Glass Box &amp; Gauge
              </a>
              .
            </p>
          </div>

          {/* ── Review Laws ── */}
          <div>
            <SectionHeader id="review-laws" title="Review Laws" />
            <p className="text-xs font-mono text-slate-text mb-3 leading-relaxed">
              The rules every Argus review follows. Non-negotiable, on every
              plan.
            </p>
            <p className="text-xs font-mono text-slate-text mb-8 leading-relaxed">
              Findings are earned, never guaranteed. There is no
              minimum-comment behavior &mdash; a clean PR gets a short
              approval, not manufactured nitpicks.
            </p>

            <div className="space-y-3 mb-8">
              {[
                {
                  title: "One severity rubric",
                  desc: "The same critical/warning/suggestion bar applies everywhere — every reviewer, every persona, every change class.",
                },
                {
                  title: "Silence is a valid review",
                  desc: "Zero findings is a complete review. Argus never manufactures comments to look thorough.",
                },
                {
                  title: "No praise filler",
                  desc: "At most one genuine sentence in the summary. Never inline praise comments.",
                },
                {
                  title: "Style is the linter's job",
                  desc: "Formatting, import ordering, naming conventions — never flagged. If a machine can auto-fix it, Argus doesn't comment on it.",
                },
                {
                  title: "Every finding carries proof",
                  desc: "A concrete failure scenario, file:line evidence, and a suggested fix. No vague “consider improving” comments.",
                },
                {
                  title: "Permanent safety checks",
                  desc: "Never suppressed by class, persona, or memory: destructive SQL with a missing WHERE, secrets or PII entering logs, unit-ambiguous numeric constants, refactors that silently change behavior, unchecked errors.",
                },
              ].map((law, i) => (
                <div
                  key={law.title}
                  className="border border-iron bg-charcoal p-4 flex gap-4"
                >
                  <span className="text-[10px] font-mono text-amber tracking-wider pt-0.5 shrink-0">
                    {String(i + 1).padStart(2, "0")}
                  </span>
                  <div className="flex-1 min-w-0">
                    <span className="text-xs font-mono font-bold text-foreground">
                      {law.title}
                    </span>
                    <p className="text-[11px] font-mono text-slate-text leading-relaxed mt-1">
                      {law.desc}
                    </p>
                  </div>
                </div>
              ))}
            </div>

            <h3 className="text-sm font-bold text-foreground mb-3">
              Judge scoring — every review, every plan
            </h3>
            <p className="text-xs font-mono text-slate-text mb-4 leading-relaxed">
              An LLM judge scores every finding against thresholds
              conditioned on the change class. Score filtering is not a paid
              feature &mdash; Pro adds depth (the specialist squad and Pass
              2), not filtering.
            </p>
            <div className="space-y-3">
              {[
                {
                  icon: SlidersHorizontal,
                  title: "Class-aware thresholds",
                  desc: "A suggestion on a one-off script needs a higher score to post than a critical on a migration. Thresholds are caps, not floors — nothing is resurrected to fill space.",
                },
                {
                  icon: MessageSquare,
                  title: "Hard cap: 10 inline comments",
                  desc: "At most 10 inline comments per review. Near-threshold findings fold into a collapsed “Minor notes” section in the summary instead of cluttering the diff.",
                },
              ].map((item) => {
                const Icon = item.icon;
                return (
                  <div key={item.title} className="border border-iron bg-charcoal p-4">
                    <div className="flex items-center gap-3 mb-2">
                      <Icon className="h-4 w-4 text-amber" />
                      <span className="text-xs font-mono font-bold text-foreground">
                        {item.title}
                      </span>
                    </div>
                    <p className="text-[11px] font-mono text-slate-text leading-relaxed">
                      {item.desc}
                    </p>
                  </div>
                );
              })}
            </div>

            <p className="text-[11px] font-mono text-iron mt-4">
              Verdicts use &ldquo;needs work&rdquo; language — Argus reviews,
              it doesn&apos;t gatekeep. Merging remains your call.
            </p>
          </div>

          {/* ── Deep Review ── */}
          <div>
            <SectionHeader id="deep-review" title="Deep Review" pro />
            <p className="text-xs font-mono text-slate-text mb-3 leading-relaxed">
              Four specialist agents review every file in parallel.
            </p>
            <p className="text-xs font-mono text-slate-text mb-8 leading-relaxed">
              Instead of one pass, Argus deploys four domain specialists
              per file. Each brings a different lens &mdash; and they run
              concurrently, so it doesn&apos;t slow you down.
            </p>

            <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
              {[
                {
                  icon: Bug,
                  name: "bug_hunter",
                  desc: "Logic errors, off-by-ones, nil dereferences, broken invariants, incorrect boolean chains. The specialist that catches what compiles but doesn\u2019t work.",
                },
                {
                  icon: Shield,
                  name: "security",
                  desc: "Injection, auth bypass, SSRF, path traversal, leaked credentials, insecure deserialization. Reviews with a pen-tester\u2019s eye.",
                },
                {
                  icon: Network,
                  name: "architecture",
                  desc: "Dependency direction, API contracts, separation of concerns, blast radius. Flags when a change violates the system\u2019s structural intent.",
                },
                {
                  icon: History,
                  name: "regression",
                  desc: "Uses scenario memory and past review history to detect changes that re-introduce previously fixed bugs or break known invariants.",
                },
              ].map((specialist) => {
                const Icon = specialist.icon;
                return (
                  <div
                    key={specialist.name}
                    className="border border-iron bg-charcoal p-5"
                  >
                    <div className="flex items-center gap-3 mb-3">
                      <div className="h-8 w-8 bg-amber/10 flex items-center justify-center">
                        <Icon className="h-4 w-4 text-amber" />
                      </div>
                      <span className="text-xs font-mono font-bold text-amber">
                        {specialist.name}
                      </span>
                    </div>
                    <p className="text-[11px] font-mono text-slate-text leading-relaxed">
                      {specialist.desc}
                    </p>
                  </div>
                );
              })}
            </div>

            <p className="text-[11px] font-mono text-iron mt-4">
              Depth follows the review contract: one-off scripts get a single
              balanced reviewer (correctness + data safety) instead of the
              full squad, and docs/generated changes skip the second pass.
              Enable Deep Review globally in{" "}
              <span className="text-amber">Settings &rarr; Features</span>.
              Findings from all four specialists are deduplicated before
              scoring.
            </p>
          </div>

          {/* ── Incremental Reviews ── */}
          <div>
            <SectionHeader id="incremental-reviews" title="Incremental Reviews" />
            <p className="text-xs font-mono text-slate-text mb-3 leading-relaxed">
              Push again? Argus only reviews what changed.
            </p>
            <p className="text-xs font-mono text-slate-text mb-8 leading-relaxed">
              When you push new commits to an already-reviewed PR, Argus
              computes the diff since the last review and only analyzes the
              delta. Previous findings that are still relevant are preserved.
              Resolved findings are dropped.
            </p>

            <div className="space-y-3">
              {[
                {
                  icon: GitCompare,
                  title: "Delta detection",
                  desc: "Compares HEAD against the last-reviewed SHA. Only new or modified hunks enter the pipeline. Unchanged files are skipped entirely.",
                },
                {
                  icon: RefreshCw,
                  title: "Finding lifecycle",
                  desc: "Findings from the previous review are carried forward if the relevant code is unchanged. When a push fixes flagged lines, Argus resolves its own comment with a “Resolved by <sha>” reply — and only posts what's new.",
                },
                {
                  icon: Gauge,
                  title: "Cost reduction",
                  desc: "Incremental reviews typically use 30\u201370% fewer tokens than a full re-review, depending on how much changed between pushes.",
                },
              ].map((item) => {
                const Icon = item.icon;
                return (
                  <div
                    key={item.title}
                    className="border border-iron bg-charcoal p-4"
                  >
                    <div className="flex items-center gap-3 mb-2">
                      <Icon className="h-4 w-4 text-amber" />
                      <span className="text-xs font-mono font-bold text-foreground">
                        {item.title}
                      </span>
                    </div>
                    <p className="text-[11px] font-mono text-slate-text leading-relaxed">
                      {item.desc}
                    </p>
                  </div>
                );
              })}
            </div>

            <p className="text-[11px] font-mono text-iron mt-4">
              Force a full re-review with{" "}
              <code className="text-amber bg-iron/40 rounded px-1.5 py-0.5">
                @argus-eye review --force
              </code>
              .
            </p>
          </div>

          {/* ── What Argus Sees ── */}
          <div>
            <SectionHeader id="what-argus-sees" title="What Argus Sees" />
            <p className="text-xs font-mono text-slate-text mb-3 leading-relaxed">
              Most review tools see the diff. Argus sees the system.
            </p>
            <p className="text-xs font-mono text-slate-text mb-8 leading-relaxed">
              Before reviewing a single line of code, Argus builds a living
              model of your codebase that evolves with every review. This is
              what separates a linter from an engineer.
            </p>

            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
              {[
                {
                  icon: Network,
                  title: "Cross-file context",
                  desc: "Argus traces callers, imports, tests, and shared types. When you change a function, Argus already knows who calls it — and what breaks if the contract shifts.",
                },
                {
                  icon: Target,
                  title: "Blast radius",
                  desc: "A persistent dependency graph maps every function and class. On each PR, Argus surfaces what downstream code is affected. No more \"I didn't realize that module depended on this.\"",
                },
                {
                  icon: History,
                  title: "Scenario memory",
                  desc: "Past bugs, incidents, and edge cases are remembered across team turnover. \"The last time this module changed, EU billing broke.\" Argus remembers so your team doesn't have to.",
                },
                {
                  icon: Brain,
                  title: "Decision traces",
                  desc: "Every review, every developer reply, every fix builds a living knowledge graph. Patterns that were dismissed stop recurring. Patterns that were confirmed get reinforced.",
                },
              ].map((item) => {
                const Icon = item.icon;
                return (
                  <div
                    key={item.title}
                    className="border border-iron bg-charcoal p-5"
                  >
                    <div className="flex items-center gap-3 mb-3">
                      <div className="h-8 w-8 bg-amber/10 flex items-center justify-center">
                        <Icon className="h-4 w-4 text-amber" />
                      </div>
                      <span className="text-xs font-mono font-bold text-foreground">
                        {item.title}
                      </span>
                    </div>
                    <p className="text-[11px] font-mono text-slate-text leading-relaxed">
                      {item.desc}
                    </p>
                  </div>
                );
              })}
            </div>

            <p className="text-[11px] font-mono text-iron mt-4">
              Argus maintains a world model of your codebase. The more it
              reviews, the more it understands. Context is not a feature — it
              is the architecture.
            </p>
          </div>

          {/* ── Architecture Visualization ── */}
          <div>
            <SectionHeader id="architecture-viz" title="Architecture Visualization" pro />
            <p className="text-xs font-mono text-slate-text mb-3 leading-relaxed">
              See your codebase as a dependency graph.
            </p>
            <p className="text-xs font-mono text-slate-text mb-8 leading-relaxed">
              The Architecture page renders an interactive dependency graph
              built from every review. Nodes are files, edges are
              import/call/type relationships. Four analytical lenses let you
              see different dimensions of the system.
            </p>

            <div className="grid grid-cols-1 sm:grid-cols-2 gap-3 mb-6">
              {[
                {
                  icon: AlertTriangle,
                  name: "Risk",
                  desc: "Colors nodes by cumulative risk score. High-severity findings, frequent changes, and unresolved comments push a file\u2019s risk up.",
                },
                {
                  icon: Focus,
                  name: "Choke Points",
                  desc: "Highlights files with high fan-in \u2014 the modules everything depends on. Breaking these breaks everything.",
                },
                {
                  icon: Activity,
                  name: "Hotspots",
                  desc: "Surfaces files with the most review activity. Frequent changes + frequent findings = code that needs attention.",
                },
                {
                  icon: Layers,
                  name: "Coupling",
                  desc: "Shows tightly coupled file clusters. Files that always change together likely share hidden dependencies.",
                },
              ].map((lens) => {
                const Icon = lens.icon;
                return (
                  <div
                    key={lens.name}
                    className="border border-iron bg-charcoal p-4"
                  >
                    <div className="flex items-center gap-3 mb-2">
                      <Icon className="h-4 w-4 text-amber" />
                      <span className="text-xs font-mono font-bold text-foreground">
                        {lens.name}
                      </span>
                    </div>
                    <p className="text-[11px] font-mono text-slate-text leading-relaxed">
                      {lens.desc}
                    </p>
                  </div>
                );
              })}
            </div>

            <h3 className="text-sm font-bold text-foreground mb-3">
              Navigation
            </h3>
            <div className="space-y-3">
              {[
                {
                  icon: Search,
                  title: "File search",
                  desc: "Fuzzy search across all nodes. Select a result to center and highlight it in the graph.",
                },
                {
                  icon: Compass,
                  title: "Smart zoom",
                  desc: "On first load, the graph auto-zooms to the highest-risk cluster so you see what matters immediately.",
                },
                {
                  icon: Scan,
                  title: "Node hover",
                  desc: "Hover any node for a metrics tooltip: risk score, review count, finding breakdown, and last review date.",
                },
              ].map((item) => {
                const Icon = item.icon;
                return (
                  <div
                    key={item.title}
                    className="border border-iron bg-charcoal p-4"
                  >
                    <div className="flex items-center gap-3 mb-2">
                      <Icon className="h-4 w-4 text-amber" />
                      <span className="text-xs font-mono font-bold text-foreground">
                        {item.title}
                      </span>
                    </div>
                    <p className="text-[11px] font-mono text-slate-text leading-relaxed">
                      {item.desc}
                    </p>
                  </div>
                );
              })}
            </div>

            <p className="text-[11px] font-mono text-iron mt-4">
              The graph builds incrementally with each review. An onboarding
              guide walks new users through the interface on first visit.
            </p>
          </div>

          {/* ── Code Simulation ── */}
          <div>
            <SectionHeader id="code-simulation" title="Code Simulation" pro />
            <p className="text-xs font-mono text-slate-text mb-3 leading-relaxed">
              Before you merge, Argus imagines what happens.
            </p>
            <p className="text-xs font-mono text-slate-text mb-8 leading-relaxed">
              Given a PR and known scenarios from your codebase history, Argus
              simulates execution paths and reports what it finds. Confidence
              scores tell you how certain the system is.
            </p>

            <TerminalBlock title="argus — simulation output">
              {/* Scenario 1 */}
              <div className="space-y-2">
                <div className="flex items-center gap-2">
                  <span className="text-[9px] font-mono uppercase tracking-wider px-1.5 py-0.5 rounded border bg-red-500/20 text-red-400 border-red-500/30">
                    fails
                  </span>
                  <span className="text-[11px] font-mono text-foreground">
                    Scenario: Concurrent subscription cancellation
                  </span>
                  <span className="ml-auto text-[10px] font-mono text-red-400">
                    confidence 94%
                  </span>
                </div>
                <div className="pl-4 border-l-2 border-red-500/30">
                  <p className="text-[11px] font-mono text-ash/70 leading-relaxed">
                    <span className="text-slate-text">Root cause:</span> No
                    idempotency key on the cancellation path. Two concurrent
                    requests reach the payment provider — first succeeds, second
                    throws. DB update runs for both.
                  </p>
                  <p className="text-[11px] font-mono text-ash/70 leading-relaxed mt-1">
                    <span className="text-slate-text">Impact:</span> Double
                    refund issued. Revenue loss proportional to cancellation
                    volume.
                  </p>
                  <p className="text-[11px] font-mono text-amber/60 leading-relaxed mt-1">
                    <span className="text-slate-text">Fix:</span> Add mutex or
                    idempotency key. Wrap call + DB write in a transaction.
                  </p>
                </div>
              </div>

              <div className="border-t border-iron/50" />

              {/* Scenario 2 */}
              <div className="space-y-2">
                <div className="flex items-center gap-2">
                  <span className="text-[9px] font-mono uppercase tracking-wider px-1.5 py-0.5 rounded border bg-yellow-500/20 text-yellow-400 border-yellow-500/30">
                    degrades
                  </span>
                  <span className="text-[11px] font-mono text-foreground">
                    Scenario: Cache key collision under ID reuse
                  </span>
                  <span className="ml-auto text-[10px] font-mono text-yellow-400">
                    confidence 78%
                  </span>
                </div>
                <div className="pl-4 border-l-2 border-yellow-500/30">
                  <p className="text-[11px] font-mono text-ash/70 leading-relaxed">
                    <span className="text-slate-text">Root cause:</span>{" "}
                    Deleted user IDs are recycled. Infinite TTL cache serves
                    stale data from the previous account holder.
                  </p>
                  <p className="text-[11px] font-mono text-ash/70 leading-relaxed mt-1">
                    <span className="text-slate-text">Impact:</span> Data
                    leakage between accounts. Severity scales with user churn.
                  </p>
                </div>
              </div>

              <div className="border-t border-iron/50" />

              {/* Scenario 3 */}
              <div className="space-y-2">
                <div className="flex items-center gap-2">
                  <span className="text-[9px] font-mono uppercase tracking-wider px-1.5 py-0.5 rounded border bg-green-500/20 text-green-400 border-green-500/30">
                    passes
                  </span>
                  <span className="text-[11px] font-mono text-foreground">
                    Scenario: Webhook retry under network partition
                  </span>
                  <span className="ml-auto text-[10px] font-mono text-green-400">
                    confidence 91%
                  </span>
                </div>
                <div className="pl-4 border-l-2 border-green-500/30">
                  <p className="text-[11px] font-mono text-ash/70 leading-relaxed">
                    <span className="text-slate-text">Result:</span>{" "}
                    Idempotency key already present on this path. Retry is
                    safe. No state corruption detected.
                  </p>
                </div>
              </div>
            </TerminalBlock>

            <p className="text-[11px] font-mono text-iron mt-4">
              Simulation is powered by scenario memory — the richer your
              review history, the more scenarios Argus can test against.
              Currently in experimental rollout.
            </p>
          </div>

          {/* ── PR Enrichment & Diagrams ── */}
          <div>
            <SectionHeader id="pr-enrichment" title="PR Enrichment & Mermaid Diagrams" pro />
            <p className="text-xs font-mono text-slate-text mb-3 leading-relaxed">
              Argus writes the context your PR description forgot.
            </p>
            <p className="text-xs font-mono text-slate-text mb-8 leading-relaxed">
              After reviewing, Argus appends auto-generated Mermaid diagrams
              and missing context directly to the PR description. Reviewers
              see the system impact before reading a single line of diff.
            </p>

            <div className="space-y-3">
              {[
                {
                  icon: Workflow,
                  title: "Sequence diagrams",
                  desc: "Generated from call paths affected by the PR. Shows the request flow through services, middleware, and handlers.",
                },
                {
                  icon: Activity,
                  title: "Data flow diagrams",
                  desc: "Maps how data transforms as it moves through the changed code. Input \u2192 validation \u2192 processing \u2192 output, with types annotated.",
                },
                {
                  icon: Network,
                  title: "Dependency diagrams",
                  desc: "Shows which modules the PR touches and their upstream/downstream relationships. Highlights the blast radius visually.",
                },
              ].map((item) => {
                const Icon = item.icon;
                return (
                  <div
                    key={item.title}
                    className="border border-iron bg-charcoal p-4"
                  >
                    <div className="flex items-center gap-3 mb-2">
                      <Icon className="h-4 w-4 text-amber" />
                      <span className="text-xs font-mono font-bold text-foreground">
                        {item.title}
                      </span>
                    </div>
                    <p className="text-[11px] font-mono text-slate-text leading-relaxed">
                      {item.desc}
                    </p>
                  </div>
                );
              })}
            </div>

            <p className="text-[11px] font-mono text-iron mt-4">
              Diagrams render natively on GitHub. Toggle in{" "}
              <span className="text-amber">Settings &rarr; Features &rarr; PR Enrichment</span>.
            </p>
          </div>

          {/* ── The Conversational Review ── */}
          <div>
            <SectionHeader
              id="conversational-review"
              title="The Conversational Review"
            />
            <p className="text-xs font-mono text-slate-text mb-3 leading-relaxed">
              Argus doesn&apos;t post a list of findings. It writes you a
              review the way a senior engineer would — conversational,
              opinionated, and to the point.
            </p>
            <p className="text-xs font-mono text-slate-text mb-8 leading-relaxed">
              Every review has three layers: the summary, the inline comments,
              and the feedback loop.
            </p>

            <div className="space-y-6">
              {/* Summary mock */}
              <div>
                <h3 className="text-sm font-bold text-foreground mb-3">
                  The summary
                </h3>
                <TerminalBlock title="argus — review summary">
                  <div className="space-y-3">
                    <p className="text-[11px] font-mono text-foreground/90 leading-relaxed">
                      <span className="font-bold text-foreground">Verdict:</span>{" "}
                      Adds 20 utility modules but has critical security and
                      correctness issues that must be fixed before merging.
                    </p>
                    <div>
                      <p className="text-[11px] font-mono font-bold text-red-400 mb-1">
                        Critical issues:
                      </p>
                      <ul className="space-y-1 text-[11px] font-mono text-foreground/90 leading-relaxed">
                        <li>
                          <code className="text-amber/80">src/lib/convert/units.ts:L15</code>{" "}
                          &mdash; Hour multiplier is 360,000ms instead of 3,600,000ms
                        </li>
                        <li>
                          <code className="text-amber/80">src/lib/filter/predicate.ts:L42</code>{" "}
                          &mdash; User input passed directly to RegExp without escaping
                        </li>
                      </ul>
                    </div>
                    <div>
                      <p className="text-[11px] font-mono font-bold text-yellow-400 mb-1">
                        Warnings:
                      </p>
                      <ul className="space-y-1 text-[11px] font-mono text-foreground/90 leading-relaxed">
                        <li>
                          <code className="text-amber/80">src/lib/color/grade.ts:L10</code>{" "}
                          &mdash; No NaN check before clamping
                        </li>
                        <li>
                          <code className="text-amber/80">src/lib/counter/rolling.ts:L28</code>{" "}
                          &mdash; Unbounded bucket array (+4 more)
                        </li>
                      </ul>
                    </div>
                  </div>
                  <div className="flex items-center gap-4 mt-3 pt-3 border-t border-iron/50">
                    <span className="text-[10px] font-mono text-slate-text">
                      2 critical &middot; 2 warnings &middot; 4 suggestions
                    </span>
                  </div>
                </TerminalBlock>
              </div>

              {/* Inline comment format */}
              <div>
                <h3 className="text-sm font-bold text-foreground mb-3">
                  Inline comments
                </h3>
                <p className="text-xs font-mono text-slate-text mb-4 leading-relaxed">
                  Every inline comment follows a structured format: what the
                  issue is, why it matters, and a one-click suggestion fix when
                  applicable.
                </p>
                <TerminalBlock title="argus — inline comment">
                  <div className="space-y-2">
                    <div className="flex items-center gap-2">
                      <span className="text-[9px] font-mono uppercase tracking-wider px-1.5 py-0.5 rounded border bg-red-500/20 text-red-400 border-red-500/30">
                        critical
                      </span>
                      <span className="text-[9px] font-mono uppercase tracking-wider px-1.5 py-0.5 rounded border bg-amber/20 text-amber border-amber/30">
                        bug
                      </span>
                    </div>
                    <p className="text-[11px] font-mono text-foreground/90 leading-relaxed">
                      <span className="text-slate-text font-bold">
                        What:
                      </span>{" "}
                      Two concurrent cancellation requests can both pass the{" "}
                      <code className="text-amber/80">
                        status === &quot;active&quot;
                      </code>{" "}
                      check. First succeeds at the payment provider, second
                      throws — but the DB update runs for both.
                    </p>
                    <p className="text-[11px] font-mono text-foreground/90 leading-relaxed">
                      <span className="text-slate-text font-bold">Why:</span>{" "}
                      No lock or idempotency key on this path. The check-then-act
                      window is ~200ms under load. This will cause double
                      refunds in production.
                    </p>
                  </div>
                </TerminalBlock>
              </div>

              {/* Feedback loop */}
              <div>
                <h3 className="text-sm font-bold text-foreground mb-3">
                  The feedback loop
                </h3>
                <p className="text-xs font-mono text-slate-text mb-4 leading-relaxed">
                  Every Argus comment has approval reactions. Your feedback
                  directly shapes future reviews.
                </p>
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                  <div className="border border-iron bg-charcoal p-4">
                    <div className="flex items-center gap-2 mb-2">
                      <ThumbsUp className="h-3.5 w-3.5 text-green-400" />
                      <span className="text-xs font-mono font-bold text-foreground">
                        Approve
                      </span>
                    </div>
                    <p className="text-[11px] font-mono text-slate-text leading-relaxed">
                      Reinforces the pattern. Argus will catch similar issues
                      with higher confidence in future reviews.
                    </p>
                  </div>
                  <div className="border border-iron bg-charcoal p-4">
                    <div className="flex items-center gap-2 mb-2">
                      <ThumbsDown className="h-3.5 w-3.5 text-red-400" />
                      <span className="text-xs font-mono font-bold text-foreground">
                        Dismiss
                      </span>
                    </div>
                    <p className="text-[11px] font-mono text-slate-text leading-relaxed">
                      Becomes a semantic memory with the reason and the change
                      kind. Repeated dismissed patterns are auto-suppressed
                      &mdash; security findings never are, and dismissals on
                      one-off scripts don&apos;t silence production reviews.
                    </p>
                  </div>
                </div>
              </div>
            </div>
          </div>

          {/* ── Live Activity Timeline ── */}
          <div>
            <SectionHeader id="live-timeline" title="Live Activity Timeline" />
            <p className="text-xs font-mono text-slate-text mb-3 leading-relaxed">
              Watch reviews happen in real time.
            </p>
            <p className="text-xs font-mono text-slate-text mb-8 leading-relaxed">
              When a review is in progress, the review detail page streams live
              activity via WebSocket. You see exactly what Argus is doing as it
              happens.
            </p>

            <div className="space-y-3">
              {[
                {
                  icon: Radio,
                  title: "Live streaming",
                  desc: "WebSocket-powered real-time updates. See which file is being reviewed, which specialist is assigned, and comments as they arrive.",
                },
                {
                  icon: SlidersHorizontal,
                  title: "Scoring results",
                  desc: "Watch findings get scored in real time. Low-confidence findings drop out as scoring completes.",
                },
                {
                  icon: BarChart3,
                  title: "Token & cost counter",
                  desc: "Live token usage and cost counter updates as each pipeline stage completes.",
                },
                {
                  icon: Activity,
                  title: "Elapsed timer",
                  desc: "Running timer shows total review duration. Auto-scrolls when you're at the bottom, stops auto-scroll when you scroll up to read.",
                },
              ].map((item) => {
                const Icon = item.icon;
                return (
                  <div
                    key={item.title}
                    className="border border-iron bg-charcoal p-4"
                  >
                    <div className="flex items-center gap-3 mb-2">
                      <Icon className="h-4 w-4 text-amber" />
                      <span className="text-xs font-mono font-bold text-foreground">
                        {item.title}
                      </span>
                    </div>
                    <p className="text-[11px] font-mono text-slate-text leading-relaxed">
                      {item.desc}
                    </p>
                  </div>
                );
              })}
            </div>

            <p className="text-[11px] font-mono text-iron mt-4">
              The timeline is collapsible for long reviews. All activity
              persists in the review detail page after completion.
            </p>
          </div>

          {/* ── Severities ── */}
          <div>
            <SectionHeader id="severities" title="Severities" />
            <p className="text-xs font-mono text-slate-text mb-6 leading-relaxed">
              Every finding is tagged with one of four severity levels. These
              drive the quality score and determine what gets posted.
            </p>
            <div className="space-y-3">
              {SEVERITIES.map((sev) => (
                <div
                  key={sev.name}
                  className="flex items-start gap-3 border border-iron bg-charcoal p-4"
                >
                  <div
                    className={`h-2.5 w-2.5 rounded-full ${sev.dot} mt-1 shrink-0`}
                  />
                  <div>
                    <span
                      className={`text-xs font-mono font-bold uppercase tracking-wider ${sev.text}`}
                    >
                      {sev.name}
                    </span>
                    <p className="text-xs font-mono text-slate-text leading-relaxed mt-0.5">
                      {sev.description}
                    </p>
                  </div>
                </div>
              ))}
            </div>
          </div>

          {/* ── Categories ── */}
          <div>
            <SectionHeader id="categories" title="Categories" />
            <p className="text-xs font-mono text-slate-text mb-6 leading-relaxed">
              Every finding is also tagged with a category — the type of issue
              detected.
            </p>
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
              {CATEGORIES.map((cat) => {
                const Icon = cat.icon;
                return (
                  <div
                    key={cat.name}
                    className="border border-iron bg-charcoal p-4"
                  >
                    <div className="flex items-center gap-2 mb-2">
                      <Icon className="h-3.5 w-3.5 text-amber" />
                      <span className="text-xs font-mono font-bold text-foreground">
                        {cat.name}
                      </span>
                    </div>
                    <p className="text-[11px] font-mono text-slate-text leading-relaxed">
                      {cat.description}
                    </p>
                  </div>
                );
              })}
            </div>
          </div>

          {/* ── Review Rules ── */}
          <div>
            <SectionHeader id="rules" title="Review Rules" />
            <p className="text-xs font-mono text-slate-text mb-6 leading-relaxed">
              Tell Argus what matters to your team. Rules are injected into
              every review, so every comment reflects your standards — not
              generic best practices.
            </p>

            <h3 className="text-sm font-bold text-foreground mb-3">
              Org-level rules
            </h3>
            <p className="text-xs font-mono text-slate-text mb-4 leading-relaxed">
              Create rules in the dashboard under{" "}
              <span className="text-amber">Rules</span>. Each rule has a
              category, content, priority, and enabled flag. These apply to
              all repos in your org.
            </p>

            <h3 className="text-sm font-bold text-foreground mb-3">
              Repo-level rules
            </h3>
            <p className="text-xs font-mono text-slate-text mb-4 leading-relaxed">
              Add a{" "}
              <code className="text-amber bg-iron/40 rounded px-1.5 py-0.5">
                .argus/rules.md
              </code>{" "}
              file to your repo. Repo rules override org rules in the same
              category.
            </p>

            <CodeBlock>{`## security
- Always flag hardcoded API keys or secrets
- Check for SQL injection in raw query strings

## performance
- Flag N+1 queries in ORM code
- Warn about unbounded list fetches without pagination

## testing
- Require tests for new exported functions
- Flag test-only helpers imported from production code`}</CodeBlock>
          </div>

          {/* ── Model Configuration ── */}
          <div>
            <SectionHeader id="models" title="Model Configuration" />
            <p className="text-xs font-mono text-slate-text mb-6 leading-relaxed">
              All 4 pipeline stages are independently configurable per-repo from
              the <span className="text-amber">Settings</span> page. Default
              model depends on your OpenRouter key. Temperature and MaxTokens
              are adjustable per stage via sliders.
            </p>

            <div className="border border-iron bg-charcoal overflow-x-auto">
              <div className="grid grid-cols-2 sm:grid-cols-4 text-[10px] font-mono uppercase tracking-wider text-slate-text border-b border-iron min-w-[480px]">
                <div className="px-4 py-2.5">Stage</div>
                <div className="px-4 py-2.5">Default Model</div>
                <div className="px-4 py-2.5">Max Tokens</div>
                <div className="px-4 py-2.5">Temperature</div>
              </div>
              {[
                {
                  stage: "triage",
                  model: "configurable",
                  tokens: "configurable",
                  temp: "configurable",
                },
                {
                  stage: "review",
                  model: "configurable",
                  tokens: "configurable",
                  temp: "configurable",
                },
                {
                  stage: "scoring",
                  model: "configurable",
                  tokens: "configurable",
                  temp: "configurable",
                },
                {
                  stage: "synthesis",
                  model: "configurable",
                  tokens: "configurable",
                  temp: "configurable",
                },
              ].map((row, i, arr) => (
                <div
                  key={row.stage}
                  className={`grid grid-cols-2 sm:grid-cols-4 text-xs font-mono min-w-[480px] ${
                    i < arr.length - 1 ? "border-b border-iron/50" : ""
                  }`}
                >
                  <div className="px-4 py-3 text-amber">{row.stage}</div>
                  <div className="px-4 py-3 text-foreground">{row.model}</div>
                  <div className="px-4 py-3 text-slate-text">{row.tokens}</div>
                  <div className="px-4 py-3 text-slate-text">{row.temp}</div>
                </div>
              ))}
            </div>

            <p className="text-[11px] font-mono text-iron mt-3">
              Supported providers: OpenRouter, OpenAI, Anthropic, Azure OpenAI,
              GCP Vertex AI, AWS Bedrock, and Zhipu AI. Custom model names are
              supported &mdash; enter any model identifier your provider accepts.
            </p>
          </div>

          {/* ── API Keys (BYOK) ── */}
          <div>
            <SectionHeader id="api-keys" title="API Keys (BYOK)" />
            <p className="text-xs font-mono text-slate-text mb-4 leading-relaxed">
              Your keys, your models, your bill. Argus never stores prompts or
              code on our servers &mdash; API calls go straight from our
              backend to your chosen provider. No hidden costs, no surprises.
            </p>
            <div className="border border-iron bg-charcoal p-4">
              <div className="flex items-center gap-3 mb-3">
                <Key className="h-4 w-4 text-amber" />
                <span className="text-xs font-mono font-bold text-foreground">
                  Setup
                </span>
              </div>
              <ol className="list-decimal list-inside space-y-1.5 text-xs font-mono text-slate-text leading-relaxed">
                <li>
                  Go to <span className="text-amber">Settings</span> in the
                  dashboard
                </li>
                <li>
                  Select a repo and choose a provider (OpenAI, Anthropic,
                  etc.)
                </li>
                <li>
                  Enter your API key &mdash; it&apos;s encrypted at rest
                </li>
                <li>
                  Pick a model for each pipeline stage (triage, review,
                  scoring, synthesis)
                </li>
              </ol>
            </div>
            <div className="border border-amber/20 bg-amber/5 p-4 mt-4">
              <span className="text-xs font-mono font-bold text-amber">Security</span>
              <ul className="mt-2 space-y-1.5 text-xs font-mono text-slate-text leading-relaxed">
                <li><span className="text-foreground">Strong encryption at rest</span> &mdash; keys never persist in plaintext.</li>
                <li><span className="text-foreground">In-memory only</span> &mdash; decrypted for API calls, then discarded. Never logged or cached.</li>
                <li><span className="text-foreground">Workspace-isolated</span> &mdash; no other workspace can access your keys.</li>
                <li><span className="text-foreground">Masked</span> &mdash; dashboard shows <code className="text-amber">sk-...****</code> only. Full key never sent to the frontend.</li>
              </ul>
            </div>
            <p className="text-[11px] font-mono text-iron mt-3">
              We never see your code. We never see your keys. Without
              a key configured, Argus posts a friendly onboarding comment on
              your first PR linking to Settings.
            </p>
          </div>

          {/* ── BYOT Supermemory ── */}
          <div>
            <SectionHeader id="supermemory" title="BYOT Supermemory" />
            <p className="text-xs font-mono text-slate-text mb-3 leading-relaxed">
              Bring Your Own Token for Supermemory.
            </p>
            <p className="text-xs font-mono text-slate-text mb-8 leading-relaxed">
              Argus uses{" "}
              <a
                href="https://supermemory.com"
                target="_blank"
                rel="noopener noreferrer"
                className="text-amber hover:text-foreground transition-colors"
              >
                Supermemory
              </a>{" "}
              for RAG-powered memory &mdash; storing review patterns,
              codebase conventions, and scenario history. You can bring your
              own Supermemory API key for full control over your data.
            </p>

            <div className="border border-iron bg-charcoal p-4 mb-4">
              <div className="flex items-center gap-3 mb-3">
                <Database className="h-4 w-4 text-amber" />
                <span className="text-xs font-mono font-bold text-foreground">
                  Setup
                </span>
              </div>
              <ol className="list-decimal list-inside space-y-1.5 text-xs font-mono text-slate-text leading-relaxed">
                <li>
                  Go to{" "}
                  <span className="text-amber">
                    Integrations
                  </span>{" "}
                  in the dashboard
                </li>
                <li>
                  Enter your Supermemory API key under the Supermemory section
                </li>
                <li>
                  Key is scoped per-org &mdash; all repos in the org share
                  the same memory backend
                </li>
              </ol>
            </div>

            <p className="text-[11px] font-mono text-iron">
              Without a custom key, Argus uses its shared Supermemory
              instance. Your data is isolated per-installation regardless.
            </p>
          </div>

          {/* ── Review Personas ── */}
          <div>
            <SectionHeader id="personas" title="Review Personas" />
            <p className="text-xs font-mono text-slate-text mb-4 leading-relaxed">
              Not every PR needs the same reviewer. Personas tune the tone,
              focus, and severity threshold &mdash; from a gentle mentor to a
              zero-mercy auditor. Set a default per-repo or override per-PR.
            </p>
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
              {[
                {
                  name: "default",
                  desc: "Balanced across all categories. The standard Argus experience most teams start with.",
                },
                {
                  name: "security_auditor",
                  desc: "Treats every PR like a pen test. Injection risks, auth flaws, data exposure, SSRF.",
                },
                {
                  name: "performance_engineer",
                  desc: "Hunts N+1 queries, memory leaks, O(n\u00B2) loops, and missing cache invalidation.",
                },
                {
                  name: "mentor",
                  desc: "Explains the why behind every comment. Suggests learning resources. Built for growing teams.",
                },
                {
                  name: "architect",
                  desc: "Thinks in boundaries. API contracts, separation of concerns, dependency direction.",
                },
                {
                  name: "strict",
                  desc: "Exhaustive analysis depth — traces every path and error branch. The severity bar never changes; it doesn't manufacture comments.",
                },
                {
                  name: "custom",
                  desc: "Define your own persona with a freeform system prompt. Full control over tone, focus, and severity.",
                },
              ].map((p) => (
                <div
                  key={p.name}
                  className="border border-iron bg-charcoal p-4"
                >
                  <span className="text-xs font-mono font-bold text-amber">
                    {p.name}
                  </span>
                  <p className="text-[11px] font-mono text-slate-text leading-relaxed mt-1">
                    {p.desc}
                  </p>
                </div>
              ))}
            </div>
            <div className="mt-4 border border-iron bg-charcoal p-4">
              <div className="flex items-center gap-3 mb-2">
                <UserCog className="h-4 w-4 text-amber" />
                <span className="text-xs font-mono font-bold text-foreground">
                  Per-PR override
                </span>
              </div>
              <p className="text-xs font-mono text-slate-text leading-relaxed">
                Override per-PR with{" "}
                <code className="text-amber bg-iron/40 rounded px-1.5 py-0.5">
                  @argus-eye review --persona strict
                </code>
              </p>
              <CodeBlock>{`@argus-eye review --persona security_auditor`}</CodeBlock>
            </div>
          </div>

          {/* ── Auto-review & Triggers ── */}
          <div>
            <SectionHeader id="auto-review" title="Auto-review & Triggers" />
            <p className="text-xs font-mono text-slate-text mb-3 leading-relaxed">
              Argus supports two trigger modes. Pick per-org, override per-repo.
            </p>
            <p className="text-xs font-mono text-slate-text mb-6 leading-relaxed">
              <span className="text-foreground">Auto-review on (default).</span>{" "}
              Every PR opened, pushed, or reopened is reviewed automatically —
              no checkbox, no preview. A push to an open PR re-reviews the new
              commits.
              <br />
              <br />
              <span className="text-foreground">Auto-review off.</span>{" "}
              When a PR opens — or is pushed while off — Argus posts a{" "}
              <span className="text-amber">Trigger Argus review</span>{" "}
              checkbox comment (once per PR) with an estimated token + cost
              preview. Reviewers tick the box to run a review on demand.
            </p>

            <div className="grid gap-3 md:grid-cols-2">
              <div className="border border-iron bg-charcoal p-4">
                <div className="flex items-center gap-2 mb-2">
                  <ToggleRight className="h-4 w-4 text-amber" />
                  <span className="text-xs font-mono font-bold text-foreground">
                    Precedence
                  </span>
                </div>
                <p className="text-[11px] font-mono text-slate-text leading-relaxed">
                  Repo override beats org default. If the repo setting is
                  unset, the org default applies. If both are unset, auto-run
                  is on.
                </p>
              </div>

              <div className="border border-iron bg-charcoal p-4">
                <div className="flex items-center gap-2 mb-2">
                  <Gauge className="h-4 w-4 text-amber" />
                  <span className="text-xs font-mono font-bold text-foreground">
                    Rate limits
                  </span>
                </div>
                <p className="text-[11px] font-mono text-slate-text leading-relaxed">
                  Every review draws from a 10/hour per-repo bucket and a
                  50/day per-org bucket. Checkbox clicks and{" "}
                  <code className="text-amber bg-iron/40 rounded px-1 py-0.5">
                    --force
                  </code>{" "}
                  additionally draw from a tighter 3/hour per-repo force bucket
                  — effectively capping on-demand triggers at 3/hour.
                </p>
              </div>

              <div className="border border-iron bg-charcoal p-4">
                <div className="flex items-center gap-2 mb-2">
                  <Sparkles className="h-4 w-4 text-amber" />
                  <span className="text-xs font-mono font-bold text-foreground">
                    Cost preview
                  </span>
                </div>
                <p className="text-[11px] font-mono text-slate-text leading-relaxed">
                  The trigger comment shows changed-file count, diff lines, and
                  a historical average of tokens + USD cost across your last
                  20 reviews for this repo. USD is omitted when pricing data
                  is unavailable (token-only fallback).
                </p>
              </div>

              <div className="border border-iron bg-charcoal p-4">
                <div className="flex items-center gap-2 mb-2">
                  <MessageSquare className="h-4 w-4 text-amber" />
                  <span className="text-xs font-mono font-bold text-foreground">
                    Fallback command
                  </span>
                </div>
                <p className="text-[11px] font-mono text-slate-text leading-relaxed">
                  You can always trigger a review by commenting{" "}
                  <code className="text-amber bg-iron/40 rounded px-1 py-0.5">
                    @argus-eye review
                  </code>
                  , regardless of the auto-run setting. Useful if the checkbox
                  comment is missing (webhook redelivery, PR opened before
                  Argus install).
                </p>
              </div>
            </div>

            <h3 className="font-mono text-sm font-semibold text-foreground mt-8 mb-3">
              How the checkbox works
            </h3>
            <ol className="space-y-2 text-xs font-mono text-slate-text leading-relaxed list-decimal pl-5 mb-6">
              <li>
                PR opens → Argus posts a single comment with cost preview and{" "}
                <code className="text-amber bg-iron/40 rounded px-0.5">
                  - [ ] Trigger Argus review
                </code>
                .
              </li>
              <li>
                A user with triage-level access ticks the box. GitHub fires an{" "}
                <code className="text-amber bg-iron/40 rounded px-1 py-0.5">
                  issue_comment.edited
                </code>{" "}
                webhook.
              </li>
              <li>
                Argus verifies the comment author is{" "}
                <code className="text-amber bg-iron/40 rounded px-1 py-0.5">
                  argus-eye[bot]
                </code>{" "}
                (anti-hijack), rate-limits the click, swaps the checkbox for{" "}
                <code className="text-amber bg-iron/40 rounded px-1 py-0.5">
                  Running Argus review…
                </code>
                , and dispatches the review.
              </li>
              <li>
                If the pipeline errors, the checkbox is restored with a retry
                hint. Tick again to run.
              </li>
            </ol>

            <h3 className="font-mono text-sm font-semibold text-foreground mb-3">
              Where to toggle
            </h3>
            <p className="text-xs font-mono text-slate-text mb-3 leading-relaxed">
              Dashboard →{" "}
              <span className="text-amber">Settings</span>:
            </p>
            <ul className="space-y-1.5 text-xs font-mono text-slate-text leading-relaxed list-disc pl-5 mb-6">
              <li>
                <span className="text-foreground">Org Defaults</span> tab →{" "}
                <span className="text-amber">Auto-review</span> card for the
                org-wide default.
              </li>
              <li>
                <span className="text-foreground">Repo Overrides</span> tab →{" "}
                <span className="text-amber">Auto-review</span> card for a
                per-repo override.
              </li>
            </ul>

            <div className="border border-iron bg-charcoal p-4">
              <div className="flex items-center gap-2 mb-2">
                <Info className="h-3.5 w-3.5 text-amber" />
                <span className="text-[11px] font-mono font-bold text-foreground uppercase tracking-wider">
                  Gotchas
                </span>
              </div>
              <ul className="space-y-1.5 text-[11px] font-mono text-slate-text leading-relaxed list-disc pl-4">
                <li>
                  Trigger comments are posted only on <code className="text-amber">opened</code>. Pushes to an open PR (<code className="text-amber">synchronize</code>) do not repost — use the existing checkbox or{" "}
                  <code className="text-amber">@argus-eye review</code>.
                </li>
                <li>
                  Ticking the box on <em>anyone else&apos;s</em> comment that
                  mimics our format is ignored — only comments authored by
                  Argus trigger reviews.
                </li>
                <li>
                  Only the{" "}
                  <code className="text-amber">[ ]→[x]</code>{" "}
                  transition triggers a review. Unticking (<code className="text-amber">[x]→[ ]</code>)
                  does nothing, and a running review cannot be cancelled from
                  the checkbox.
                </li>
              </ul>
            </div>
          </div>

          {/* ── Bot Commands ── */}
          <div>
            <SectionHeader id="commands" title="Bot Commands" />
            <p className="text-xs font-mono text-slate-text mb-6 leading-relaxed">
              Talk to Argus directly from any PR. Mention{" "}
              <code className="text-amber bg-iron/40 rounded px-1.5 py-0.5">
                @argus-eye
              </code>{" "}
              followed by a command and it responds in seconds.
            </p>
            <div className="space-y-3">
              {[
                {
                  cmd: "@argus-eye review",
                  desc: "Trigger a full review. Add --force to re-review at the same SHA. Add --persona to switch style for this PR only.",
                  example: "@argus-eye review --force --persona mentor",
                },
                {
                  cmd: "@argus-eye remember <pattern>",
                  desc: "Teach Argus something new. Saves a pattern to memory for future reviews. Add --org to apply across all repos.",
                  example:
                    "@argus-eye remember --org always check for SQL injection in raw queries",
                },
                {
                  cmd: "@argus-eye resolve",
                  desc: "Resolves every open Argus review thread on the PR and marks each finding resolved. Maintainer-only (owner, member, or collaborator).",
                  example: "@argus-eye resolve",
                },
                {
                  cmd: "@argus-eye fix",
                  desc: "Applies every suggestion block from the review as a single atomic commit pushed straight to your PR branch.",
                  example: "@argus-eye fix",
                },
                {
                  cmd: "@argus-eye test",
                  desc: "Generate a test plan from review findings. Covers unit, edge case, integration, and regression tests.",
                  example: "@argus-eye test",
                },
                {
                  cmd: "@argus-eye test --code",
                  desc: "Draft executable test code for findings, matching your project's framework and conventions.",
                  example: "@argus-eye test --code",
                },
                {
                  cmd: "@argus-eye review --persona <name>",
                  desc: "Review with a specific persona for this PR only. Overrides the repo default.",
                  example: "@argus-eye review --persona strict",
                },
                {
                  cmd: "@argus-eye help",
                  desc: "Lists all available commands and their usage right in the PR.",
                  example: "@argus-eye help",
                },
              ].map((c) => (
                <div
                  key={c.cmd}
                  className="border border-iron bg-charcoal p-4"
                >
                  <div className="flex items-center gap-3 mb-2">
                    <Terminal className="h-3.5 w-3.5 text-amber" />
                    <code className="text-xs font-mono font-bold text-foreground">
                      {c.cmd}
                    </code>
                  </div>
                  <p className="text-[11px] font-mono text-slate-text leading-relaxed mb-2">
                    {c.desc}
                  </p>
                  <div className="rounded bg-void/80 border border-iron/50 px-3 py-2">
                    <code className="text-[11px] font-mono text-foreground/70">
                      {c.example}
                    </code>
                  </div>
                </div>
              ))}
            </div>
          </div>

          {/* ── Test Generation ── */}
          <div>
            <SectionHeader id="test-generation" title="Test Generation" />
            <p className="text-xs font-mono text-slate-text mb-3 leading-relaxed">
              Turn review findings into tests before you merge.
            </p>
            <p className="text-xs font-mono text-slate-text mb-8 leading-relaxed">
              Argus analyzes its own findings and generates targeted test plans
              or executable test code. No more &ldquo;I&apos;ll add a test
              later.&rdquo;
            </p>

            <div className="space-y-4">
              <div className="border border-iron bg-charcoal p-4">
                <div className="flex items-center gap-3 mb-2">
                  <TestTube2 className="h-4 w-4 text-amber" />
                  <span className="text-xs font-mono font-bold text-foreground">
                    Test plan
                  </span>
                </div>
                <p className="text-[11px] font-mono text-slate-text leading-relaxed mb-3">
                  <code className="text-amber bg-iron/40 rounded px-1.5 py-0.5">
                    @argus-eye test
                  </code>{" "}
                  generates a structured test plan covering unit tests, edge
                  cases, integration tests, and regression tests &mdash; all
                  derived from the review findings on the current PR.
                </p>
              </div>

              <div className="border border-iron bg-charcoal p-4">
                <div className="flex items-center gap-3 mb-2">
                  <Code2 className="h-4 w-4 text-amber" />
                  <span className="text-xs font-mono font-bold text-foreground">
                    Executable test code
                  </span>
                </div>
                <p className="text-[11px] font-mono text-slate-text leading-relaxed mb-3">
                  <code className="text-amber bg-iron/40 rounded px-1.5 py-0.5">
                    @argus-eye test --code
                  </code>{" "}
                  drafts ready-to-run test code that matches your project&apos;s
                  testing framework and conventions. Copy, paste, run.
                </p>
              </div>
            </div>

            <p className="text-[11px] font-mono text-iron mt-4">
              Test generation uses the same review context and memory that
              powers the review pipeline. The richer the review, the better
              the tests.
            </p>
          </div>

          {/* ── Memory & Learning ── */}
          <div>
            <SectionHeader id="memory" title="Memory & Learning" pro />
            <p className="text-xs font-mono text-slate-text mb-3 leading-relaxed">
              Most tools forget between PRs. Argus remembers everything.
            </p>
            <p className="text-xs font-mono text-slate-text mb-8 leading-relaxed">
              Every review, every developer reaction, every fix and dismissal
              feeds a growing knowledge base. The system doesn&apos;t just
              review code — it accumulates institutional memory that survives
              team turnover.
            </p>

            <div className="space-y-4">
              {[
                {
                  icon: Paintbrush,
                  title: "Patterns",
                  desc: "Code conventions auto-learned from your codebase. Error handling styles, naming patterns, architecture decisions — extracted from what your team actually writes, not what a style guide says.",
                },
                {
                  icon: Activity,
                  title: "Scenarios",
                  desc: "Three sources: auto-extracted from reviews, auto-imported from GitHub Issues labeled argus or bug, and manual via bot command. Each scenario includes steps, initial state, and expected outcome. Scenarios are marked outdated when referenced files change. React \uD83D\uDC4E to dismiss.",
                },
                {
                  icon: History,
                  title: "Decision traces",
                  desc: "Every review comment, every developer reply, every approval and dismissal. This is review history as institutional memory. Why was this pattern introduced? Who approved it? What broke last time?",
                },
                {
                  icon: Network,
                  title: "Context graph",
                  desc: "The \"event clock\" of your codebase. A living record of why things are the way they are — connecting reviews, patterns, scenarios, and code changes into a navigable knowledge graph.",
                },
              ].map((item) => {
                const Icon = item.icon;
                return (
                  <div
                    key={item.title}
                    className="border border-iron bg-charcoal p-4"
                  >
                    <div className="flex items-center gap-3 mb-2">
                      <Icon className="h-4 w-4 text-amber" />
                      <span className="text-xs font-mono font-bold text-foreground">
                        {item.title}
                      </span>
                    </div>
                    <p className="text-[11px] font-mono text-slate-text leading-relaxed">
                      {item.desc}
                    </p>
                  </div>
                );
              })}
            </div>

            <div className="mt-6 border border-amber/20 bg-amber/5 p-4">
              <div className="flex items-center gap-3 mb-2">
                <RefreshCw className="h-4 w-4 text-amber" />
                <span className="text-xs font-mono font-bold text-amber">
                  The flywheel
                </span>
              </div>
              <p className="text-[11px] font-mono text-slate-text leading-relaxed">
                Every review makes the system smarter. Patterns that get
                approved are reinforced. Patterns that get dismissed are
                suppressed. Scenarios that match real bugs get higher
                confidence. Over time, Argus converges on your team&apos;s
                actual standards — not generic rules, but the hard-won
                knowledge that usually lives only in senior engineers&apos;
                heads.
              </p>
            </div>
          </div>

          {/* ── Glass Box & Gauge ── */}
          <div>
            <SectionHeader id="glass-box" title="Glass Box & Gauge" />
            <p className="text-xs font-mono text-slate-text mb-3 leading-relaxed">
              Every review shows its work.
            </p>
            <p className="text-xs font-mono text-slate-text mb-8 leading-relaxed">
              The Glass Box footer on every posted review states what
              contract the review ran under, which reviewers checked the
              code, how many findings team feedback suppressed, and how long
              it took. No silent filtering — if something was suppressed,
              the count says so.
            </p>

            <TerminalBlock title="argus — glass box footer">
              <p className="text-[11px] font-mono text-slate-text leading-relaxed">
                Contract: production/full &middot; checked: bug_hunter,
                security, architecture, regression &middot; 2 suppressed by
                team feedback &middot; review took 1m42s
              </p>
            </TerminalBlock>

            <h3 className="text-sm font-bold text-foreground mt-8 mb-3">
              Gauge — address-rate telemetry
            </h3>
            <p className="text-xs font-mono text-slate-text mb-4 leading-relaxed">
              Comment volume is a vanity metric. Gauge tracks whether Argus
              comments actually led to code changes.
            </p>
            <div className="space-y-3">
              {[
                {
                  icon: Target,
                  title: "Address rate",
                  desc: "For each comment, Gauge records whether the flagged code changed before merge. Findings fixed by a human commit weigh more than ones Argus auto-fixed.",
                },
                {
                  icon: BarChart3,
                  title: "Per category, per change class",
                  desc: "Address rate is broken down by finding category and contract class — so you can see, e.g., that security findings on migrations get fixed and readability nits on scripts get ignored.",
                },
              ].map((item) => {
                const Icon = item.icon;
                return (
                  <div key={item.title} className="border border-iron bg-charcoal p-4">
                    <div className="flex items-center gap-3 mb-2">
                      <Icon className="h-4 w-4 text-amber" />
                      <span className="text-xs font-mono font-bold text-foreground">
                        {item.title}
                      </span>
                    </div>
                    <p className="text-[11px] font-mono text-slate-text leading-relaxed">
                      {item.desc}
                    </p>
                  </div>
                );
              })}
            </div>
          </div>

          {/* ── Insights & Risk ── */}
          <div>
            <SectionHeader id="insights" title="Insights & Risk" />
            <p className="text-xs font-mono text-slate-text mb-3 leading-relaxed">
              Your codebase has a health score now.
            </p>
            <p className="text-xs font-mono text-slate-text mb-8 leading-relaxed">
              The Insights dashboard aggregates everything Argus learns into
              an operational view of your codebase. Not vanity metrics —
              actionable risk signals drawn from real review data.
            </p>

            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
              {[
                {
                  icon: BarChart3,
                  title: "Hot files",
                  desc: "Files most frequently flagged across reviews. These are the parts of your codebase that keep breaking — the modules that need a rewrite or better test coverage.",
                },
                {
                  icon: Shield,
                  title: "Risk scores",
                  desc: "Per-file and per-module risk scores based on severity history, change frequency, and unresolved findings. Higher risk = higher attention from Argus.",
                },
                {
                  icon: History,
                  title: "Decision trace timeline",
                  desc: "A chronological view of every review, reaction, and pattern learned. See how your codebase quality trends over time — and which decisions shaped it.",
                },
                {
                  icon: Activity,
                  title: "Quality trends",
                  desc: "Track quality scores across PRs, repos, and teams. Spot regressions before they compound. Know when a refactor is paying off.",
                },
              ].map((item) => {
                const Icon = item.icon;
                return (
                  <div
                    key={item.title}
                    className="border border-iron bg-charcoal p-4"
                  >
                    <div className="flex items-center gap-3 mb-2">
                      <Icon className="h-4 w-4 text-amber" />
                      <span className="text-xs font-mono font-bold text-foreground">
                        {item.title}
                      </span>
                    </div>
                    <p className="text-[11px] font-mono text-slate-text leading-relaxed">
                      {item.desc}
                    </p>
                  </div>
                );
              })}
            </div>
          </div>

          {/* ── Token & Cost Tracking ── */}
          <div>
            <SectionHeader id="token-tracking" title="Token & Cost Tracking" />
            <p className="text-xs font-mono text-slate-text mb-3 leading-relaxed">
              Know exactly what every review costs.
            </p>
            <p className="text-xs font-mono text-slate-text mb-8 leading-relaxed">
              Argus records per-stage token usage and cost for every review.
              Model and provider are tracked independently for each stage.
              Token data persists even on failed reviews.
            </p>

            <div className="space-y-3">
              <div className="border border-iron bg-charcoal p-4">
                <div className="flex items-center gap-3 mb-2">
                  <Gauge className="h-4 w-4 text-amber" />
                  <span className="text-xs font-mono font-bold text-foreground">
                    Per-stage breakdown
                  </span>
                </div>
                <p className="text-[11px] font-mono text-slate-text leading-relaxed">
                  Token usage tracked for: triage, review, scoring, synthesis,
                  enrichment, conventions, patterns, file_synthesis, and graph.
                  Each stage records input tokens, output tokens, model, and
                  cost.
                </p>
              </div>

              <div className="border border-iron bg-charcoal p-4">
                <div className="flex items-center gap-3 mb-2">
                  <Eye className="h-4 w-4 text-amber" />
                  <span className="text-xs font-mono font-bold text-foreground">
                    TokenPill
                  </span>
                </div>
                <p className="text-[11px] font-mono text-slate-text leading-relaxed">
                  Hover any TokenPill in the review detail page to see the full
                  cost breakdown per stage, including model name and provider.
                </p>
              </div>
            </div>
          </div>

          {/* ── Light Mode ── */}
          <div>
            <SectionHeader id="light-mode" title="Light Mode" />
            <p className="text-xs font-mono text-slate-text mb-3 leading-relaxed">
              Dark isn&apos;t the only option anymore.
            </p>
            <p className="text-xs font-mono text-slate-text mb-8 leading-relaxed">
              Toggle between dark and light themes using the Sun/Moon icon
              in the sidebar footer. Your preference persists via
              localStorage and is applied instantly without a page reload.
            </p>

            <div className="space-y-3">
              {[
                {
                  icon: Sun,
                  title: "Toggle",
                  desc: "Click the Sun/Moon icon in the sidebar footer. Dark \u2192 Light \u2192 Dark. No page refresh required.",
                },
                {
                  icon: Eye,
                  title: "System preference",
                  desc: "On first visit, Argus respects your OS prefers-color-scheme setting. After manual toggle, your choice takes precedence.",
                },
                {
                  icon: Paintbrush,
                  title: "Full coverage",
                  desc: "All dashboard pages, the architecture graph, code diffs, and marketing pages support both themes. Graph tokens use a warm cream palette in light mode.",
                },
              ].map((item) => {
                const Icon = item.icon;
                return (
                  <div
                    key={item.title}
                    className="border border-iron bg-charcoal p-4"
                  >
                    <div className="flex items-center gap-3 mb-2">
                      <Icon className="h-4 w-4 text-amber" />
                      <span className="text-xs font-mono font-bold text-foreground">
                        {item.title}
                      </span>
                    </div>
                    <p className="text-[11px] font-mono text-slate-text leading-relaxed">
                      {item.desc}
                    </p>
                  </div>
                );
              })}
            </div>
          </div>

          {/* ── Feature Flags ── */}
          <div>
            <SectionHeader id="feature-flags" title="Feature Flags" />
            <p className="text-xs font-mono text-slate-text mb-3 leading-relaxed">
              Toggle capabilities per-org from the dashboard.
            </p>
            <p className="text-xs font-mono text-slate-text mb-8 leading-relaxed">
              Feature flags let you enable or disable advanced capabilities
              without code changes. All flags are scoped per-org and take
              effect on the next review.
            </p>

            <div className="space-y-3">
              {[
                {
                  label: "Cross-PR Checks",
                  desc: "Detect linked PRs across repos and run compatibility verification. Adds one extra LLM call per linked PR.",
                  status: "off",
                },
                {
                  label: "Issue Acceptance",
                  desc: "Verify that PR diffs address linked issue acceptance criteria. Works with GitHub\u2019s native issue-linking keywords.",
                  status: "on by default",
                },
                {
                  label: "Deep Review",
                  desc: "4-specialist parallel review per file. Higher coverage, higher token cost.",
                  status: "off",
                },
                {
                  label: "PR Enrichment",
                  desc: "Append Mermaid diagrams and missing context to PR descriptions after review.",
                  status: "on by default",
                },
                {
                  label: "Pattern Learning",
                  desc: "Auto-extract reusable code patterns from high-confidence findings.",
                  status: "on by default",
                },
                {
                  label: "Convention Learning",
                  desc: "Extract naming, error handling, and architecture conventions from diffs.",
                  status: "on by default",
                },
                {
                  label: "Architecture Graph",
                  desc: "Build and maintain a persistent dependency graph from code changes.",
                  status: "on by default",
                },
              ].map((toggle) => (
                <div
                  key={toggle.label}
                  className="border border-iron bg-charcoal p-4 flex items-start gap-4"
                >
                  <Flag className="h-4 w-4 text-amber mt-0.5 shrink-0" />
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-3 mb-1">
                      <span className="text-xs font-mono font-bold text-foreground">
                        {toggle.label}
                      </span>
                      <span
                        className={`ml-auto text-[9px] font-mono uppercase tracking-wider px-1.5 py-0.5 rounded border ${
                          toggle.status === "off"
                            ? "bg-iron/20 text-slate-text border-iron"
                            : "bg-green-500/20 text-green-400 border-green-500/30"
                        }`}
                      >
                        {toggle.status}
                      </span>
                    </div>
                    <p className="text-[11px] font-mono text-slate-text leading-relaxed">
                      {toggle.desc}
                    </p>
                  </div>
                </div>
              ))}
            </div>

            <p className="text-[11px] font-mono text-iron mt-4">
              Manage flags in{" "}
              <span className="text-amber">Settings &rarr; Features</span>.
              Changes apply to the next review triggered on any repo in the
              org.
            </p>
          </div>

          {/* ── Settings & Controls ── */}
          <div>
            <SectionHeader id="settings" title="Settings & Controls" />
            <p className="text-xs font-mono text-slate-text mb-6 leading-relaxed">
              Every advanced capability can be toggled independently per-repo.
              Start with the defaults and enable features as your team is
              ready.
            </p>

            <div className="space-y-3">
              {[
                {
                  label: "Auto-review",
                  desc: "Review every PR automatically — opened, pushed, or reopened; a push re-reviews the new commits. When off, Argus posts a Trigger checkbox (once per PR) with a token/cost preview — reviewers tick to run on demand.",
                  status: "on by default",
                },
                {
                  label: "Deep Review",
                  desc: "Enables the 4-specialist parallel review (bug_hunter, security, architecture, regression) per file.",
                  status: "off",
                },
                {
                  label: "Cross-File Context",
                  desc: "Enables dependency tracing and caller analysis across your codebase during review.",
                  status: "on by default",
                },
                {
                  label: "Blast Radius Analysis",
                  desc: "Maps downstream impact of every change using the persistent dependency graph.",
                  status: "on by default",
                },
                {
                  label: "Simulation & Scenarios",
                  desc: "Simulates execution paths against known scenarios. Reports confidence, root cause, and impact.",
                  status: "off",
                },
                {
                  label: "PR Enrichment",
                  desc: "Auto-enriches PR descriptions with missing context and mermaid diagrams.",
                  status: "on by default",
                },
                {
                  label: "Pattern Learning",
                  desc: "Learns reusable patterns from high-confidence findings across reviews.",
                  status: "on by default",
                },
                {
                  label: "Convention Learning",
                  desc: "Extracts codebase conventions from diffs — naming, error handling, architecture patterns.",
                  status: "on by default",
                },
                {
                  label: "File Synthesis",
                  desc: "Creates per-file institutional memory — summaries of what each file does and how it has changed.",
                  status: "on by default",
                },
                {
                  label: "Architecture Graph",
                  desc: "Extracts dependency graph from code changes. Powers blast radius analysis and cross-file context.",
                  status: "on by default",
                },
              ].map((toggle) => (
                <div
                  key={toggle.label}
                  className="border border-iron bg-charcoal p-4 flex items-start gap-4"
                >
                  <ToggleRight className="h-4 w-4 text-amber mt-0.5 shrink-0" />
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-3 mb-1">
                      <span className="text-xs font-mono font-bold text-foreground">
                        {toggle.label}
                      </span>
                      <span
                        className={`ml-auto text-[9px] font-mono uppercase tracking-wider px-1.5 py-0.5 rounded border ${
                          toggle.status === "off"
                            ? "bg-iron/20 text-slate-text border-iron"
                            : "bg-green-500/20 text-green-400 border-green-500/30"
                        }`}
                      >
                        {toggle.status}
                      </span>
                    </div>
                    <p className="text-[11px] font-mono text-slate-text leading-relaxed">
                      {toggle.desc}
                    </p>
                  </div>
                </div>
              ))}
            </div>

            <p className="text-[11px] font-mono text-iron mt-4">
              All toggles are accessible from{" "}
              <span className="text-amber">Settings</span> in the dashboard.
              Changes take effect on the next review.
            </p>
          </div>
        </div>
      </div>
    </section>
  );
}
