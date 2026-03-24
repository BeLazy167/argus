"use client";

import { useEffect, useState } from "react";
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
} from "lucide-react";

/* ── Section data ── */

const SECTIONS = [
  { id: "getting-started", label: "Getting Started" },
  { id: "pipeline", label: "The Review Pipeline" },
  { id: "what-argus-sees", label: "What Argus Sees" },
  { id: "code-simulation", label: "Code Simulation" },
  { id: "conversational-review", label: "Conversational Review" },
  { id: "severities", label: "Severities" },
  { id: "categories", label: "Categories" },
  { id: "rules", label: "Review Rules" },
  { id: "models", label: "Model Config" },
  { id: "api-keys", label: "API Keys (BYOK)" },
  { id: "personas", label: "Review Personas" },
  { id: "commands", label: "Bot Commands" },
  { id: "test-generation", label: "Test Generation" },
  { id: "memory", label: "Memory & Learning" },
  { id: "insights", label: "Insights & Risk" },
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
      "Well-written code, good patterns, clever solutions, or thorough test coverage worth highlighting.",
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
      "Formatting inconsistencies, convention violations, import ordering, naming patterns.",
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
      "Classifies every changed file as skip, skim, or deep review. Generated files, lockfiles, and vendored dependencies are discarded before a single token is spent.",
  },
  {
    step: "02",
    label: "Context Gathering",
    icon: Network,
    description:
      "Cross-file analysis, dependency tracing, scenario matching, and decision trace lookup. Argus builds a complete picture of the change before reviewing a single line.",
  },
  {
    step: "03",
    label: "Deep Review",
    icon: MessageSquare,
    description:
      "Per-file parallel review with four specialists — bug hunter, security auditor, architecture critic, regression analyst — each armed with full codebase awareness.",
  },
  {
    step: "04",
    label: "Scoring & Validation",
    icon: SlidersHorizontal,
    description:
      "A separate model scores each finding independently. Low-confidence noise is dropped. Duplicate findings are merged. What survives is signal.",
  },
  {
    step: "05",
    label: "Synthesis",
    icon: Layers,
    description:
      "A senior-dev persona reads every surviving finding and writes a conversational summary. Not a list of issues — a colleague's honest take on the PR.",
  },
  {
    step: "06",
    label: "Post & Learn",
    icon: Send,
    description:
      "Review posted as inline GitHub comments. Developer reactions are collected. Approvals reinforce patterns, dismissals suppress future false positives. The system learns.",
  },
];

/* ── Components ── */

function SidebarLink({
  id,
  label,
  active,
}: {
  id: string;
  label: string;
  active: boolean;
}) {
  return (
    <a
      href={`#${id}`}
      className={`block py-1.5 text-xs font-mono transition-colors border-l-2 pl-3 ${
        active
          ? "border-amber text-amber"
          : "border-transparent text-slate-text hover:text-foreground hover:border-iron"
      }`}
    >
      {label}
    </a>
  );
}

function SectionHeader({ id, title }: { id: string; title: string }) {
  return (
    <div id={id} className="scroll-mt-24">
      <h2 className="font-display text-xl font-bold text-foreground mb-1">
        {title}
      </h2>
      <div className="h-px bg-iron mb-6" />
    </div>
  );
}

function CodeBlock({ children }: { children: string }) {
  return (
    <pre className="rounded-lg border border-iron bg-void/80 p-4 overflow-x-auto">
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
      <div className="flex items-center gap-2 rounded-t-lg border border-iron bg-charcoal px-4 py-2.5">
        <div className="flex gap-1.5">
          <div className="h-2.5 w-2.5 rounded-full bg-iron" />
          <div className="h-2.5 w-2.5 rounded-full bg-iron" />
          <div className="h-2.5 w-2.5 rounded-full bg-iron" />
        </div>
        <span className="ml-2 text-[11px] font-mono text-amber">{title}</span>
      </div>
      <div className="border-x border-b border-iron rounded-b-lg bg-void p-5 space-y-4">
        {children}
      </div>
    </div>
  );
}

/* ── Page ── */

export default function DocsPage() {
  const [activeSection, setActiveSection] = useState<string>(SECTIONS[0].id);

  useEffect(() => {
    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting) {
            setActiveSection(entry.target.id);
          }
        }
      },
      { rootMargin: "-20% 0px -60% 0px" },
    );

    for (const s of SECTIONS) {
      const el = document.getElementById(s.id);
      if (el) observer.observe(el);
    }

    return () => observer.disconnect();
  }, []);

  return (
    <section className="mx-auto max-w-5xl px-6 py-28">
      {/* Header */}
      <p className="mb-3 text-[11px] font-mono uppercase tracking-[0.15em] text-amber">
        Documentation
      </p>
      <h1 className="font-display text-4xl font-bold text-foreground mb-3">
        Argus AI Reference
      </h1>
      <p className="text-sm font-mono text-slate-text mb-16 max-w-xl">
        What Argus sees, what it remembers, and what it does with that
        knowledge. Everything from first install to institutional memory.
      </p>

      <div className="flex gap-12">
        {/* Sidebar */}
        <nav className="hidden lg:block w-48 shrink-0">
          <div className="sticky top-24 space-y-0.5">
            <p className="text-[10px] font-mono uppercase tracking-[0.15em] text-iron mb-3">
              On this page
            </p>
            {SECTIONS.map((s) => (
              <SidebarLink
                key={s.id}
                id={s.id}
                label={s.label}
                active={activeSection === s.id}
              />
            ))}
          </div>
        </nav>

        {/* Content */}
        <div className="flex-1 min-w-0 space-y-16">
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
                  <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-md bg-amber/10 text-xs font-mono font-medium text-amber">
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
              Every PR triggers a six-stage pipeline. Each stage runs a
              different model, configurable per-repo. The entire sequence
              completes in under 60 seconds.
            </p>
            <div className="space-y-1">
              {PIPELINE_STAGES.map((stage, i) => {
                const Icon = stage.icon;
                return (
                  <div
                    key={stage.step}
                    className="rounded-lg border border-iron bg-charcoal p-4 flex gap-4"
                  >
                    <div className="flex flex-col items-center shrink-0 pt-0.5">
                      <div className="h-8 w-8 rounded-md bg-amber/10 flex items-center justify-center">
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
                    className="rounded-lg border border-iron bg-charcoal p-5"
                  >
                    <div className="flex items-center gap-3 mb-3">
                      <div className="h-8 w-8 rounded-md bg-amber/10 flex items-center justify-center">
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

          {/* ── Code Simulation ── */}
          <div>
            <SectionHeader id="code-simulation" title="Code Simulation" />
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
                  <p className="text-[11px] font-mono text-foreground/90 leading-relaxed">
                    Solid refactor overall — the extraction of{" "}
                    <code className="text-amber/80">PaymentService</code> into
                    its own module is clean and the tests cover the happy path
                    well. Two things I&apos;d block on: the cancellation
                    handler has a race condition under concurrent requests (see
                    inline), and the new cache key scheme will collide if user
                    IDs get recycled. The rest is style nits.
                  </p>
                  <div className="flex items-center gap-4 mt-3 pt-3 border-t border-iron/50">
                    <span className="text-[10px] font-mono text-slate-text">
                      Quality score:{" "}
                      <span className="text-amber font-bold">7/10</span>
                    </span>
                    <span className="text-[10px] font-mono text-slate-text">
                      1 critical &middot; 1 warning &middot; 3 suggestions
                      &middot; 2 praise
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
                  <div className="rounded-lg border border-iron bg-charcoal p-4">
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
                  <div className="rounded-lg border border-iron bg-charcoal p-4">
                    <div className="flex items-center gap-2 mb-2">
                      <ThumbsDown className="h-3.5 w-3.5 text-red-400" />
                      <span className="text-xs font-mono font-bold text-foreground">
                        Dismiss
                      </span>
                    </div>
                    <p className="text-[11px] font-mono text-slate-text leading-relaxed">
                      Suppresses the pattern. Argus stores a &ldquo;dismissed&rdquo;
                      signal and avoids similar false positives going forward.
                    </p>
                  </div>
                </div>
              </div>
            </div>
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
                  className="flex items-start gap-3 rounded-lg border border-iron bg-charcoal p-4"
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
                    className="rounded-lg border border-iron bg-charcoal p-4"
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

## style
- Enforce camelCase for variables, PascalCase for types
- Require JSDoc on exported functions`}</CodeBlock>
          </div>

          {/* ── Model Configuration ── */}
          <div>
            <SectionHeader id="models" title="Model Configuration" />
            <p className="text-xs font-mono text-slate-text mb-6 leading-relaxed">
              Override the LLM model for each pipeline stage per-repo from the{" "}
              <span className="text-amber">Settings</span> page. The
              synthesis stage defaults to the same model as review but can be
              overridden independently for teams that want a different voice.
            </p>

            <div className="rounded-lg border border-iron bg-charcoal overflow-hidden">
              <div className="grid grid-cols-4 text-[10px] font-mono uppercase tracking-wider text-slate-text border-b border-iron">
                <div className="px-4 py-2.5">Stage</div>
                <div className="px-4 py-2.5">Default Model</div>
                <div className="px-4 py-2.5">Max Tokens</div>
                <div className="px-4 py-2.5">Temperature</div>
              </div>
              {[
                {
                  stage: "triage",
                  model: "gpt-4o-mini",
                  tokens: "2,048",
                  temp: "0.2",
                },
                {
                  stage: "review",
                  model: "claude-sonnet-4",
                  tokens: "4,096",
                  temp: "0.2",
                },
                {
                  stage: "scoring",
                  model: "configurable",
                  tokens: "4,096",
                  temp: "0.2",
                },
                {
                  stage: "synthesis",
                  model: "same as review",
                  tokens: "4,096",
                  temp: "0.2",
                },
              ].map((row, i, arr) => (
                <div
                  key={row.stage}
                  className={`grid grid-cols-4 text-xs font-mono ${
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
            <div className="rounded-lg border border-iron bg-charcoal p-4">
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
            <div className="rounded-lg border border-amber/20 bg-amber/5 p-4 mt-4">
              <span className="text-xs font-mono font-bold text-amber">Security</span>
              <ul className="mt-2 space-y-1.5 text-xs font-mono text-slate-text leading-relaxed">
                <li><span className="text-foreground">AES-256-GCM</span> &mdash; bank-grade encryption at rest. Plaintext never persists.</li>
                <li><span className="text-foreground">Unique nonce</span> &mdash; every key produces different ciphertext, even if identical.</li>
                <li><span className="text-foreground">In-memory only</span> &mdash; decrypted for API calls, then discarded. Never logged or cached.</li>
                <li><span className="text-foreground">Scoped</span> &mdash; isolated per installation. No other workspace can access your keys.</li>
                <li><span className="text-foreground">Masked</span> &mdash; dashboard shows <code className="text-amber">sk-...****</code> only. Full key never sent to frontend.</li>
              </ul>
            </div>
            <p className="text-[11px] font-mono text-iron mt-3">
              We never see your code. We never see your keys. Without
              a key configured, Argus posts a friendly onboarding comment on
              your first PR linking to Settings.
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
                  desc: "No free passes. Comments on everything. Maximum coverage, minimum mercy.",
                },
                {
                  name: "custom",
                  desc: "Define your own persona with a freeform system prompt. Full control over tone, focus, and severity.",
                },
              ].map((p) => (
                <div
                  key={p.name}
                  className="rounded-lg border border-iron bg-charcoal p-4"
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
            <div className="mt-4 rounded-lg border border-iron bg-charcoal p-4">
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
                  desc: "Scans all unresolved review threads and resolves ones where the referenced file has been updated in the latest push.",
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
                  className="rounded-lg border border-iron bg-charcoal p-4"
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
              <div className="rounded-lg border border-iron bg-charcoal p-4">
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

              <div className="rounded-lg border border-iron bg-charcoal p-4">
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
            <SectionHeader id="memory" title="Memory & Learning" />
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
                    className="rounded-lg border border-iron bg-charcoal p-4"
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

            <div className="mt-6 rounded-lg border border-amber/20 bg-amber/5 p-4">
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
                    className="rounded-lg border border-iron bg-charcoal p-4"
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
                  label: "Cross-file context",
                  desc: "Enables dependency tracing and caller analysis across your codebase during review.",
                  status: "on by default",
                },
                {
                  label: "Blast radius",
                  desc: "Maps downstream impact of every change using the persistent dependency graph.",
                  status: "on by default",
                },
                {
                  label: "Scenario memory",
                  desc: "Matches PR changes against known failure scenarios from your review history.",
                  status: "on by default",
                },
                {
                  label: "Code simulation",
                  desc: "Simulates execution paths against known scenarios. Reports confidence, root cause, and impact.",
                  status: "experimental",
                },
                {
                  label: "Auto-learn patterns",
                  desc: "Automatically extracts code conventions and patterns from reviewed diffs.",
                  status: "on by default",
                },
                {
                  label: "Incremental reviews",
                  desc: "On new pushes, reviews only the delta since last review instead of the full diff.",
                  status: "on by default",
                },
              ].map((toggle) => (
                <div
                  key={toggle.label}
                  className="rounded-lg border border-iron bg-charcoal p-4 flex items-start gap-4"
                >
                  <ToggleRight className="h-4 w-4 text-amber mt-0.5 shrink-0" />
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-3 mb-1">
                      <span className="text-xs font-mono font-bold text-foreground">
                        {toggle.label}
                      </span>
                      <span
                        className={`ml-auto text-[9px] font-mono uppercase tracking-wider px-1.5 py-0.5 rounded border ${
                          toggle.status === "experimental"
                            ? "bg-yellow-500/20 text-yellow-400 border-yellow-500/30"
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
