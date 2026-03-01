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
} from "lucide-react";

/* ── Section data ── */

const SECTIONS = [
  { id: "getting-started", label: "Getting Started" },
  { id: "how-it-works", label: "How It Works" },
  { id: "severities", label: "Severities" },
  { id: "categories", label: "Categories" },
  { id: "rules", label: "Review Rules" },
  { id: "models", label: "Model Config" },
  { id: "api-keys", label: "API Keys (BYOK)" },
  { id: "personas", label: "Review Personas" },
  { id: "suggestions", label: "Suggestion Blocks" },
  { id: "incremental", label: "Incremental Reviews" },
  { id: "triggers", label: "Manual Triggers" },
  { id: "commands", label: "Bot Commands" },
  { id: "scoring", label: "Scoring" },
  { id: "memory", label: "Memory & Context" },
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
    description: "Injection vulnerabilities, leaked credentials, unsafe deserialization, SSRF, path traversal.",
  },
  {
    name: "bug",
    icon: Bug,
    description: "Off-by-one errors, nil dereferences, broken invariants, incorrect boolean logic, missing edge cases.",
  },
  {
    name: "performance",
    icon: Gauge,
    description: "N+1 queries, unnecessary allocations, missing caching, O(n\u00B2) where O(n) is possible.",
  },
  {
    name: "error_handling",
    icon: Zap,
    description: "Swallowed errors, empty catch blocks, missing error propagation, silent fallbacks.",
  },
  {
    name: "readability",
    icon: Eye,
    description: "Unclear naming, complex nesting, missing comments on non-obvious logic, dead code.",
  },
  {
    name: "style",
    icon: Paintbrush,
    description: "Formatting inconsistencies, convention violations, import ordering, naming patterns.",
  },
  {
    name: "type_design",
    icon: Code2,
    description: "Weak type invariants, stringly-typed APIs, missing generics, poor encapsulation.",
  },
  {
    name: "testing",
    icon: FlaskConical,
    description: "Missing edge case tests, brittle assertions, untested error paths, test-only code in production.",
  },
];

const PIPELINE_STAGES = [
  {
    step: "01",
    label: "Triage",
    icon: FileSearch,
    description: "Fast LLM pass classifies every changed file as skip, skim, or deep review. Generated files, lockfiles, and vendored deps are skipped automatically.",
    model: "gpt-4o-mini",
  },
  {
    step: "02",
    label: "Review",
    icon: MessageSquare,
    description: "Per-file parallel review with persona-tuned prompts. Deep files get full analysis with suggestion blocks; skimmed files get a truncated pass. Each file reviewed independently.",
    model: "claude-sonnet-4",
  },
  {
    step: "03",
    label: "Synthesis",
    icon: Layers,
    description: "All file comments are aggregated into a unified summary. A quality score (1\u201310) is calculated based on severity distribution.",
    model: "same as review",
  },
  {
    step: "04",
    label: "Post",
    icon: Send,
    description: "Review posted as inline GitHub PR comments with severity tags and one-click suggestion fixes. Comments indexed in memory for future context.",
    model: "\u2014",
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
        From first install to one-click fixes. Everything you need to ship
        cleaner code, faster.
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
          {/* Getting Started */}
          <div>
            <SectionHeader id="getting-started" title="Getting Started" />
            <div className="space-y-5">
              {[
                {
                  step: "1",
                  title: "Install the GitHub App",
                  desc: "One click at github.com/apps/argus-eye. Works with orgs and personal accounts.",
                },
                {
                  step: "2",
                  title: "Select repositories",
                  desc: "Pick which repos get watched. They appear in your dashboard instantly.",
                },
                {
                  step: "3",
                  title: "Add your API key",
                  desc: "Bring your own key — OpenAI, Anthropic, or any OpenRouter provider. You own the costs, we never see your data.",
                },
                {
                  step: "4",
                  title: "Open a pull request",
                  desc: "Every PR gets reviewed automatically. Argus leaves inline comments with one-click suggestion fixes you can commit from GitHub.",
                },
                {
                  step: "5",
                  title: "Customize",
                  desc: "Choose a review persona (security hawk, mentor, architect...), add custom rules, or teach Argus your team's patterns.",
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

          {/* How It Works */}
          <div>
            <SectionHeader id="how-it-works" title="How It Works" />
            <p className="text-xs font-mono text-slate-text mb-6 leading-relaxed">
              Every PR triggers a 4-stage pipeline that triages, reviews,
              synthesizes, and posts &mdash; all in under 60 seconds. Each
              stage runs a different model, configurable per-repo.
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
                        <span className="ml-auto text-[10px] font-mono text-iron">
                          {stage.model}
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

          {/* Severities */}
          <div>
            <SectionHeader id="severities" title="Severities" />
            <p className="text-xs font-mono text-slate-text mb-6 leading-relaxed">
              Every review comment is tagged with one of four severity levels.
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

          {/* Categories */}
          <div>
            <SectionHeader id="categories" title="Categories" />
            <p className="text-xs font-mono text-slate-text mb-6 leading-relaxed">
              Comments are also tagged with a category indicating the type of
              issue found.
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

          {/* Review Rules */}
          <div>
            <SectionHeader id="rules" title="Review Rules" />
            <p className="text-xs font-mono text-slate-text mb-6 leading-relaxed">

              Tell Argus what matters to your team. Rules are injected into the
              LLM system prompt before each review, so every comment reflects
              your standards.
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
              Add a <code className="text-amber bg-iron/40 rounded px-1.5 py-0.5">.argus/rules.md</code>{" "}
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

          {/* Model Configuration */}
          <div>
            <SectionHeader id="models" title="Model Configuration" />
            <p className="text-xs font-mono text-slate-text mb-6 leading-relaxed">
              Override the LLM model for each pipeline stage per-repo from the{" "}
              <span className="text-amber">Settings</span> page.
            </p>

            <div className="rounded-lg border border-iron bg-charcoal overflow-hidden">
              <div className="grid grid-cols-4 text-[10px] font-mono uppercase tracking-wider text-slate-text border-b border-iron">
                <div className="px-4 py-2.5">Stage</div>
                <div className="px-4 py-2.5">Default Model</div>
                <div className="px-4 py-2.5">Max Tokens</div>
                <div className="px-4 py-2.5">Temperature</div>
              </div>
              {[
                { stage: "triage", model: "gpt-4o-mini", tokens: "2,048", temp: "0.2" },
                { stage: "review", model: "claude-sonnet-4", tokens: "4,096", temp: "0.2" },
                { stage: "synthesis", model: "same as review", tokens: "4,096", temp: "0.2" },
                { stage: "embedding", model: "text-embedding-3-small", tokens: "\u2014", temp: "\u2014" },
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
              Set provider to &quot;openai&quot;, &quot;anthropic&quot;, etc. Any
              OpenRouter-compatible provider works.
            </p>
          </div>

          {/* API Keys (BYOK) */}
          <div>
            <SectionHeader id="api-keys" title="API Keys (BYOK)" />
            <p className="text-xs font-mono text-slate-text mb-4 leading-relaxed">
              Your keys, your models, your bill. Argus never stores prompts or
              code on our servers &mdash; API calls go straight from our backend
              to your chosen provider. No hidden costs, no surprises.
            </p>
            <div className="rounded-lg border border-iron bg-charcoal p-4">
              <div className="flex items-center gap-3 mb-3">
                <Key className="h-4 w-4 text-amber" />
                <span className="text-xs font-mono font-bold text-foreground">
                  Setup
                </span>
              </div>
              <ol className="list-decimal list-inside space-y-1.5 text-xs font-mono text-slate-text leading-relaxed">
                <li>Go to <span className="text-amber">Settings</span> in the dashboard</li>
                <li>Select a repo and choose a provider (OpenAI, Anthropic, etc.)</li>
                <li>Enter your API key &mdash; it&apos;s encrypted at rest</li>
                <li>Pick a model for each pipeline stage (triage, review)</li>
              </ol>
            </div>
            <p className="text-[11px] font-mono text-iron mt-3">

              Keys are encrypted at rest and scoped per-installation. Without a
              key configured, Argus posts a friendly onboarding comment on your
              first PR linking to Settings.
            </p>
          </div>

          {/* Review Personas */}
          <div>
            <SectionHeader id="personas" title="Review Personas" />
            <p className="text-xs font-mono text-slate-text mb-4 leading-relaxed">
              Not every PR needs the same reviewer. Personas tune Argus&apos;s
              tone, focus, and severity threshold &mdash; from a gentle mentor
              to a zero-mercy strict auditor. Set a default per-repo or
              override per-PR.
            </p>
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
              {[
                { name: "default", desc: "Balanced across all categories. The standard Argus experience most teams start with." },
                { name: "security_auditor", desc: "Treats every PR like a pen test. Injection risks, auth flaws, data exposure, SSRF." },
                { name: "performance_engineer", desc: "Hunts N+1 queries, memory leaks, O(n\u00B2) loops, and missing cache invalidation." },
                { name: "mentor", desc: "Explains the why behind every comment. Suggests learning resources. Built for growing teams." },
                { name: "architect", desc: "Thinks in boundaries. API contracts, separation of concerns, dependency direction." },
                { name: "strict", desc: "No free passes. Comments on everything. Maximum coverage, minimum mercy." },
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
                Comment on any PR:
              </p>
              <CodeBlock>{`@argus-eye review --persona security_auditor`}</CodeBlock>
            </div>
          </div>

          {/* Suggestion Blocks */}
          <div>
            <SectionHeader id="suggestions" title="Suggestion Blocks" />
            <p className="text-xs font-mono text-slate-text mb-4 leading-relaxed">
              Argus doesn&apos;t just tell you what&apos;s wrong &mdash; it
              writes the fix. Every actionable comment includes a GitHub
              suggestion block with exact replacement code. One click to
              commit.
            </p>
            <div className="rounded-lg border border-iron bg-charcoal p-4">
              <div className="flex items-center gap-3 mb-3">
                <Sparkles className="h-4 w-4 text-amber" />
                <span className="text-xs font-mono font-bold text-foreground">
                  How it works
                </span>
              </div>
              <ol className="list-decimal list-inside space-y-1.5 text-xs font-mono text-slate-text leading-relaxed">
                <li>During review, Argus fetches the full file at HEAD</li>
                <li>The LLM generates exact replacement code for the flagged line range</li>
                <li>The suggestion is embedded as a <code className="text-amber bg-iron/40 rounded px-1 py-0.5">```suggestion</code> block</li>
                <li>GitHub renders a &quot;Commit suggestion&quot; button on the comment</li>
              </ol>
            </div>
            <p className="text-[11px] font-mono text-iron mt-3">

              Suggestions are omitted for praise or when no concrete fix
              applies. Malformed suggestions are automatically discarded
              before posting.
            </p>
          </div>

          {/* Incremental Reviews */}
          <div>
            <SectionHeader id="incremental" title="Incremental Reviews" />
            <p className="text-xs font-mono text-slate-text mb-4 leading-relaxed">
              When you push new commits to an open PR, Argus doesn&apos;t
              re-review the entire diff. It fetches only the changes between
              your previous HEAD and the new HEAD, reviewing just the
              incremental delta.
            </p>
            <div className="rounded-lg border border-iron bg-charcoal p-4">
              <div className="flex items-center gap-3 mb-2">
                <RefreshCw className="h-4 w-4 text-amber" />
                <span className="text-xs font-mono font-bold text-foreground">
                  How it works
                </span>
              </div>
              <ol className="list-decimal list-inside space-y-1.5 text-xs font-mono text-slate-text leading-relaxed">
                <li>New push triggers <code className="text-amber bg-iron/40 rounded px-1 py-0.5">synchronize</code> webhook</li>
                <li>Argus finds the last completed review for this PR</li>
                <li>Fetches inter-diff between previous HEAD and new HEAD</li>
                <li>Reviews only new/changed files</li>
                <li>Posts as a new review marked &quot;Incremental&quot;</li>
              </ol>
            </div>
            <p className="text-[11px] font-mono text-iron mt-3">
              Falls back to full diff if inter-diff fetch fails.
            </p>
          </div>

          {/* Manual Triggers */}
          <div>
            <SectionHeader id="triggers" title="Manual Triggers" />
            <p className="text-xs font-mono text-slate-text mb-4 leading-relaxed">
              Trigger a review on any PR from the dashboard, even if it was
              opened before Argus was installed.
            </p>
            <div className="rounded-lg border border-iron bg-charcoal p-4">
              <div className="flex items-center gap-3 mb-2">
                <Play className="h-4 w-4 text-amber" />
                <span className="text-xs font-mono font-bold text-foreground">
                  From the Repos page
                </span>
              </div>
              <ol className="list-decimal list-inside space-y-1.5 text-xs font-mono text-slate-text leading-relaxed">
                <li>Click &quot;Trigger review&quot; on any repo card</li>
                <li>Enter the PR number</li>
                <li>Click Run &mdash; Argus fetches the diff and reviews it</li>
              </ol>
            </div>
            <p className="text-[11px] font-mono text-iron mt-3">
              Failed reviews can be retried from the review detail page.
            </p>
          </div>

          {/* Bot Commands */}
          <div>
            <SectionHeader id="commands" title="Bot Commands" />
            <p className="text-xs font-mono text-slate-text mb-6 leading-relaxed">

              Talk to Argus directly from any PR. Mention{" "}
              <code className="text-amber bg-iron/40 rounded px-1.5 py-0.5">@argus-eye</code>{" "}
              followed by a command and it responds in seconds.
            </p>
            <div className="space-y-3">
              {[
                {
                  cmd: "@argus-eye review",
                  desc: "Trigger a full review right now. Add --persona to switch review style for this PR only.",
                  example: "@argus-eye review --persona mentor",
                },
                {
                  cmd: "@argus-eye remember <pattern>",
                  desc: "Teach Argus something new. Saves a pattern to memory so future reviews catch it. Add --org to apply across all repos.",
                  example: "@argus-eye remember --org always check for SQL injection in raw queries",
                },
                {
                  cmd: "@argus-eye resolve",
                  desc: "Clean up after yourself. Scans all bot comments and auto-minimizes ones where the referenced file has been updated.",
                  example: "@argus-eye resolve",
                },
                {
                  cmd: "@argus-eye fix",
                  desc: "The magic command. Applies every suggestion block from the review as a single atomic commit pushed straight to your PR branch.",
                  example: "@argus-eye fix",
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

          {/* Scoring */}
          <div>
            <SectionHeader id="scoring" title="Scoring" />
            <p className="text-xs font-mono text-slate-text mb-4 leading-relaxed">
              Each review gets a quality score from 1 to 10, calculated after
              synthesis based on the severity distribution of comments.
            </p>
            <div className="rounded-lg border border-iron bg-charcoal p-4">
              <div className="flex items-center gap-3 mb-2">
                <SlidersHorizontal className="h-4 w-4 text-amber" />
                <span className="text-xs font-mono font-bold text-foreground">
                  Score formula
                </span>
              </div>
              <p className="text-xs font-mono text-slate-text leading-relaxed mb-2">
                Starts at 10 and deducts points per finding:
              </p>
              <div className="space-y-1 text-xs font-mono">
                <div className="flex items-center gap-2">
                  <span className="h-2 w-2 rounded-full bg-red-400" />
                  <span className="text-red-400">critical</span>
                  <span className="text-iron">&mdash;</span>
                  <span className="text-slate-text">&minus;2 points each</span>
                </div>
                <div className="flex items-center gap-2">
                  <span className="h-2 w-2 rounded-full bg-amber" />
                  <span className="text-amber">warning</span>
                  <span className="text-iron">&mdash;</span>
                  <span className="text-slate-text">&minus;1 point each</span>
                </div>
                <div className="flex items-center gap-2">
                  <span className="h-2 w-2 rounded-full bg-blue-400" />
                  <span className="text-blue-400">suggestion</span>
                  <span className="text-iron">&mdash;</span>
                  <span className="text-slate-text">&minus;0.5 points each</span>
                </div>
                <div className="flex items-center gap-2">
                  <span className="h-2 w-2 rounded-full bg-green-400" />
                  <span className="text-green-400">praise</span>
                  <span className="text-iron">&mdash;</span>
                  <span className="text-slate-text">no deduction</span>
                </div>
              </div>
              <p className="text-[11px] font-mono text-iron mt-3">
                Score is clamped to a minimum of 1.
              </p>
            </div>
          </div>

          {/* Memory & Context */}
          <div>
            <SectionHeader id="memory" title="Memory & Context" />
            <p className="text-xs font-mono text-slate-text mb-4 leading-relaxed">

              Argus gets smarter with every review. Past patterns and feedback
              are indexed for RAG, so future reviews build on what it&apos;s
              already learned. Powered by Supermemory.
            </p>
            <div className="rounded-lg border border-iron bg-charcoal p-4">
              <div className="flex items-center gap-3 mb-3">
                <Brain className="h-4 w-4 text-amber" />
                <span className="text-xs font-mono font-bold text-foreground">
                  Memory containers
                </span>
              </div>
              <div className="space-y-2 text-xs font-mono text-slate-text leading-relaxed">
                <div className="flex items-start gap-2">
                  <span className="text-amber shrink-0">&bull;</span>
                  <span>
                    <code className="text-foreground/80 bg-iron/40 rounded px-1 py-0.5">org-patterns</code>{" "}
                    &mdash; Learned patterns across all repos
                  </span>
                </div>
                <div className="flex items-start gap-2">
                  <span className="text-amber shrink-0">&bull;</span>
                  <span>
                    <code className="text-foreground/80 bg-iron/40 rounded px-1 py-0.5">repo-patterns</code>{" "}
                    &mdash; Repo-specific patterns and conventions
                  </span>
                </div>
                <div className="flex items-start gap-2">
                  <span className="text-amber shrink-0">&bull;</span>
                  <span>
                    <code className="text-foreground/80 bg-iron/40 rounded px-1 py-0.5">repo-reviews</code>{" "}
                    &mdash; Past review comments indexed for RAG
                  </span>
                </div>
              </div>
              <p className="text-[11px] font-mono text-iron mt-3">
                During review, the LLM can search memory to find similar past
                issues and apply consistent feedback.
              </p>
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}
