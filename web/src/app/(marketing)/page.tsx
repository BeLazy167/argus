"use client";

import Link from "next/link";
import { useEffect, useRef, useState } from "react";
import { EyeSymbol } from "@/components/marketing/eye-symbol";
import { AnimatedReview } from "@/components/marketing/animated-review";
import { GitHubReviewMock } from "@/components/marketing/github-review-mock";

/* ── Pipeline Stage ── */
function PipelineStage({
  step,
  label,
  desc,
  isActive,
  isComplete,
}: {
  step: string;
  label: string;
  desc: string;
  isActive: boolean;
  isComplete: boolean;
}) {
  return (
    <div className="relative flex items-start gap-5 py-5">
      {/* Dot + connecting line */}
      <div className="flex flex-col items-center shrink-0">
        <div
          className={`relative h-3 w-3 rounded-full border-2 transition-all duration-700 ${
            isActive
              ? "border-amber bg-amber shadow-[0_0_12px_oklch(0.77_0.15_75/0.6)]"
              : isComplete
                ? "border-amber bg-amber/40"
                : "border-iron bg-void"
          }`}
        >
          {isActive && (
            <div className="absolute inset-0 rounded-full bg-amber animate-[pipelinePing_2s_ease-out_infinite]" />
          )}
        </div>
      </div>

      {/* Content */}
      <div
        className={`transition-all duration-500 ${
          isActive || isComplete ? "opacity-100" : "opacity-30"
        }`}
      >
        <div className="flex items-center gap-3 mb-1">
          <span className="font-mono text-[10px] text-amber tracking-wider">{step}</span>
          <span className="text-[11px] font-mono uppercase tracking-[0.1em] text-foreground font-medium">
            {label}
          </span>
        </div>
        <p className="text-xs text-slate-text leading-relaxed">{desc}</p>
      </div>
    </div>
  );
}

/* ── Comparison Row ── */
function ComparisonRow({
  label,
  traditional,
  argus,
}: {
  label: string;
  traditional: string;
  argus: string;
}) {
  return (
    <div className="grid grid-cols-[1fr_1fr_1fr] gap-4 py-4 border-b border-iron/50 last:border-0">
      <div className="text-xs font-mono text-foreground font-medium">{label}</div>
      <div className="text-xs font-mono text-slate-text/60 line-through decoration-iron/60">{traditional}</div>
      <div className="text-xs font-mono text-amber">{argus}</div>
    </div>
  );
}

/* ── Main Page ── */
export default function LandingPage() {
  const [activeStage, setActiveStage] = useState(0);
  const pipelineRef = useRef<HTMLDivElement>(null);
  const [pipelineStarted, setPipelineStarted] = useState(false);

  /* Pipeline animation — cycle through stages */
  useEffect(() => {
    const observer = new IntersectionObserver(
      (entries) => {
        const entry = entries[0];
        if (entry && entry.isIntersecting && !pipelineStarted) {
          setPipelineStarted(true);
        }
      },
      { threshold: 0.3 }
    );

    const el = pipelineRef.current;
    if (el) observer.observe(el);
    return () => {
      if (el) observer.unobserve(el);
    };
  }, [pipelineStarted]);

  useEffect(() => {
    if (!pipelineStarted) return;

    const interval = setInterval(() => {
      setActiveStage((prev) => {
        if (prev >= 4) {
          clearInterval(interval);
          return 4;
        }
        return prev + 1;
      });
    }, 1200);

    return () => clearInterval(interval);
  }, [pipelineStarted]);

  const PIPELINE_STAGES = [
    {
      step: "01",
      label: "PR PUSH",
      desc: "You push code. Argus wakes up. Every changed file, every touched function, catalogued in milliseconds.",
    },
    {
      step: "02",
      label: "CONTEXT GATHERING",
      desc: "Traces callers, imports, tests, shared types. Pulls incident history, past reviews, and org rules from memory.",
    },
    {
      step: "03",
      label: "DEEP REVIEW",
      desc: "Cross-file analysis. Bugs, security holes, blast radius. Four specialist reviewers run in parallel.",
    },
    {
      step: "04",
      label: "SIMULATION",
      desc: "Executes scenario paths against your diff. Predicts what breaks. Assigns confidence scores.",
    },
    {
      step: "05",
      label: "POST",
      desc: "Conversational inline comments with What/Why sections. Risk score. Blast radius map. Done.",
    },
  ];

  const FEATURES = [
    {
      icon: (
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" className="h-5 w-5">
          <path strokeLinecap="round" strokeLinejoin="round" d="M7.5 21L3 16.5m0 0L7.5 12M3 16.5h13.5m0-13.5L21 7.5m0 0L16.5 12M21 7.5H7.5" />
        </svg>
      ),
      title: "Cross-file context",
      description:
        "No file is an island. Argus traces callers, imports, tests, and shared types to understand how changes ripple through your entire codebase.",
    },
    {
      icon: (
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" className="h-5 w-5">
          <path strokeLinecap="round" strokeLinejoin="round" d="M3.75 3.75v4.5m0-4.5h4.5m-4.5 0L9 9M3.75 20.25v-4.5m0 4.5h4.5m-4.5 0L9 15M20.25 3.75h-4.5m4.5 0v4.5m0-4.5L15 9m5.25 11.25h-4.5m4.5 0v-4.5m0 4.5L15 15" />
        </svg>
      ),
      title: "Blast radius analysis",
      description:
        "Persistent dependency graph maps every function, class, and import. When you push, Argus shows which downstream code is affected.",
    },
    {
      icon: (
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" className="h-5 w-5">
          <path strokeLinecap="round" strokeLinejoin="round" d="M9.813 15.904L9 18.75l-.813-2.846a4.5 4.5 0 00-3.09-3.09L2.25 12l2.846-.813a4.5 4.5 0 003.09-3.09L9 5.25l.813 2.846a4.5 4.5 0 003.09 3.09L15.75 12l-2.846.813a4.5 4.5 0 00-3.09 3.09zM18.259 8.715L18 9.75l-.259-1.035a3.375 3.375 0 00-2.455-2.456L14.25 6l1.036-.259a3.375 3.375 0 002.455-2.456L18 2.25l.259 1.035a3.375 3.375 0 002.455 2.456L21.75 6l-1.036.259a3.375 3.375 0 00-2.455 2.456z" />
        </svg>
      ),
      title: "Scenario memory",
      description:
        "Institutional knowledge that persists. Past bugs, incidents, and edge cases are remembered. \"Last time this module changed, EU FX rounding broke.\"",
    },
    {
      icon: (
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" className="h-5 w-5">
          <path strokeLinecap="round" strokeLinejoin="round" d="M3.75 6A2.25 2.25 0 016 3.75h2.25A2.25 2.25 0 0110.5 6v2.25a2.25 2.25 0 01-2.25 2.25H6a2.25 2.25 0 01-2.25-2.25V6zM3.75 15.75A2.25 2.25 0 016 13.5h2.25a2.25 2.25 0 012.25 2.25V18a2.25 2.25 0 01-2.25 2.25H6A2.25 2.25 0 013.75 18v-2.25zM13.5 6a2.25 2.25 0 012.25-2.25H18A2.25 2.25 0 0120.25 6v2.25A2.25 2.25 0 0118 10.5h-2.25a2.25 2.25 0 01-2.25-2.25V6zM13.5 15.75a2.25 2.25 0 012.25-2.25H18a2.25 2.25 0 012.25 2.25V18A2.25 2.25 0 0118 20.25h-2.25A2.25 2.25 0 0113.5 18v-2.25z" />
        </svg>
      ),
      title: "Decision traces",
      description:
        "Every review, every developer reply, every fix builds a living knowledge graph. Argus gets smarter with every PR your team merges.",
    },
    {
      icon: (
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" className="h-5 w-5">
          <path strokeLinecap="round" strokeLinejoin="round" d="M3.75 13.5l10.5-11.25L12 10.5h8.25L9.75 21.75 12 13.5H3.75z" />
        </svg>
      ),
      title: "Code simulation",
      description:
        "Given a scenario and your diff, Argus simulates execution paths and predicts what breaks before you merge. With confidence scores.",
    },
    {
      icon: (
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" className="h-5 w-5">
          <path strokeLinecap="round" strokeLinejoin="round" d="M20.25 8.511c.884.284 1.5 1.128 1.5 2.097v4.286c0 1.136-.847 2.1-1.98 2.193-.34.027-.68.052-1.02.072v3.091l-3-3c-1.354 0-2.694-.055-4.02-.163a2.115 2.115 0 01-.825-.242m9.345-8.334a2.126 2.126 0 00-.476-.095 48.64 48.64 0 00-8.048 0c-1.131.094-1.976 1.057-1.976 2.192v4.286c0 .837.46 1.58 1.155 1.951m9.345-8.334V6.637c0-1.621-1.152-3.026-2.76-3.235A48.455 48.455 0 0011.25 3c-2.115 0-4.198.137-6.24.402-1.608.209-2.76 1.614-2.76 3.235v6.226c0 1.621 1.152 3.026 2.76 3.235.577.075 1.157.14 1.74.194V21l4.155-4.155" />
        </svg>
      ),
      title: "Conversational reviews",
      description:
        "No robotic issue lists. Argus writes like a senior dev giving feedback &mdash; structured What/Why sections on every inline comment.",
    },
  ];

  return (
    <>
      {/* ── HERO ── */}
      <section className="relative flex min-h-[100vh] flex-col items-center justify-center overflow-hidden bg-noise">
        {/* Ambient glow */}
        <div
          className="pointer-events-none absolute top-1/3 left-1/2 -translate-x-1/2 -translate-y-1/2 h-[800px] w-[800px] rounded-full opacity-15"
          style={{
            background:
              "radial-gradient(circle, oklch(0.77 0.15 75 / 0.35) 0%, transparent 65%)",
          }}
        />

        {/* Scan lines */}
        <div
          className="pointer-events-none absolute inset-0 opacity-[0.015]"
          style={{
            backgroundImage:
              "repeating-linear-gradient(0deg, transparent, transparent 2px, rgba(245,166,35,0.1) 2px, rgba(245,166,35,0.1) 4px)",
          }}
        />

        <div className="relative z-10 flex flex-col items-center text-center px-6 pt-20">
          <EyeSymbol className="mb-6 h-20 w-auto text-amber" trackMouse />

          <h1 className="wordmark text-5xl md:text-7xl lg:text-8xl text-foreground mb-4 tracking-[0.15em]">
            ARGUS
          </h1>

          <div className="inline-flex items-center gap-2 rounded-full border border-amber/30 bg-amber/5 px-4 py-1.5 mb-5">
            <span className="h-1.5 w-1.5 rounded-full bg-amber animate-pulse" />
            <span className="text-[11px] font-mono text-amber tracking-wider">EARLY ACCESS &mdash; FREE DURING BETA</span>
          </div>

          <p className="font-display text-lg md:text-2xl text-amber mb-3 font-normal italic">
            Code that understands itself.
          </p>

          <p className="max-w-xl text-sm md:text-base leading-relaxed text-ash/80 mb-10">
            Other tools review files. Argus comprehends your codebase &mdash; tracing
            dependencies, remembering incidents, simulating failures before they ship.
          </p>

          <div className="flex flex-col sm:flex-row gap-4 mb-16">
            <Link
              href="/sign-up"
              className="group inline-flex h-11 items-center rounded-md bg-amber px-8 text-sm font-mono font-medium text-void transition-all hover:brightness-110 hover:shadow-[0_0_24px_-4px_oklch(0.77_0.15_75/0.5)]"
            >
              Install in 60 seconds
              <svg
                className="ml-2 h-3.5 w-3.5 transition-transform group-hover:translate-x-0.5"
                fill="none"
                viewBox="0 0 24 24"
                stroke="currentColor"
                strokeWidth={2}
              >
                <path strokeLinecap="round" strokeLinejoin="round" d="M13 7l5 5m0 0l-5 5m5-5H6" />
              </svg>
            </Link>
            <Link
              href="/docs"
              className="inline-flex h-11 items-center rounded-md border border-iron px-8 text-sm font-mono text-ash transition-colors hover:border-slate-text hover:text-foreground"
            >
              Read the docs
            </Link>
          </div>

          {/* Live review animation */}
          <AnimatedReview />
        </div>

        {/* Scroll indicator */}
        <div className="absolute bottom-8 flex flex-col items-center gap-2 text-slate-text">
          <span className="text-[10px] font-mono uppercase tracking-widest">
            Scroll
          </span>
          <div className="h-8 w-px bg-gradient-to-b from-slate-text/50 to-transparent animate-[scrollPulse_2s_ease-in-out_infinite]" />
        </div>
      </section>

      {/* ── LOSS AVERSION ── */}
      <section className="border-t border-iron bg-noise">
        <div className="mx-auto max-w-4xl px-6 py-24 text-center">
          <p className="mb-4 text-[11px] font-mono uppercase tracking-[0.15em] text-red-400/80">
            The comprehension gap
          </p>
          <h2 className="font-display text-2xl md:text-4xl font-bold text-foreground mb-6 leading-tight">
            Your review tools read diffs.<br />
            <span className="text-red-400/80">They don&apos;t understand your codebase.</span>
          </h2>
          <p className="max-w-2xl mx-auto text-sm text-ash/70 leading-relaxed mb-8">
            File-by-file reviews miss the big picture. They can&apos;t trace a renamed
            function to its 14 callers, or remember that this exact pattern caused a
            P0 three months ago. The gaps compound with every PR.
          </p>
          <div className="grid grid-cols-1 md:grid-cols-3 gap-6 max-w-2xl mx-auto">
            <div className="rounded-lg border border-iron bg-charcoal/50 p-5">
              <div className="font-display text-2xl font-bold text-red-400/80 mb-1">72%</div>
              <p className="text-[11px] font-mono text-slate-text">
                of production bugs originate in code review gaps
              </p>
            </div>
            <div className="rounded-lg border border-iron bg-charcoal/50 p-5">
              <div className="font-display text-2xl font-bold text-red-400/80 mb-1">3.2 hrs</div>
              <p className="text-[11px] font-mono text-slate-text">
                average time spent waiting for human review
              </p>
            </div>
            <div className="rounded-lg border border-iron bg-charcoal/50 p-5">
              <div className="font-display text-2xl font-bold text-red-400/80 mb-1">$4.7M</div>
              <p className="text-[11px] font-mono text-slate-text">
                average cost of a single data breach incident
              </p>
            </div>
          </div>
        </div>
      </section>

      {/* ── SEE IT IN ACTION ── */}
      <section className="border-t border-iron bg-charcoal/30 bg-noise">
        <div className="mx-auto max-w-5xl px-6 py-28">
          <div className="mb-16 text-center">
            <p className="mb-3 text-[11px] font-mono uppercase tracking-[0.15em] text-amber">
              See it in action
            </p>
            <h2 className="font-display text-3xl md:text-4xl font-bold text-foreground mb-4">
              Reviews that actually catch bugs.
            </h2>
            <p className="max-w-lg mx-auto text-sm text-ash/70">
              Unlike other review bots that drown you in style nits and linting
              warnings &mdash; Argus finds real vulnerabilities, logic errors,
              and footguns your team would have shipped.
            </p>
          </div>

          <GitHubReviewMock />
        </div>
      </section>

      {/* ── HOW IT WORKS — PIPELINE ── */}
      <section className="border-t border-iron bg-charcoal/50 bg-noise">
        <div className="mx-auto max-w-4xl px-6 py-28">
          <div className="mb-16 text-center">
            <p className="mb-3 text-[11px] font-mono uppercase tracking-[0.15em] text-amber">
              How it works
            </p>
            <h2 className="font-display text-3xl font-bold text-foreground mb-3">
              Push &rarr; Comprehend &rarr; Review &rarr; Simulate &rarr; Ship
            </h2>
            <p className="max-w-lg mx-auto text-sm text-ash/70">
              Five stages. Full codebase awareness. Every PR.
            </p>
          </div>

          <div ref={pipelineRef} className="relative ml-1.5">
            {/* Connecting vertical line */}
            <div className="absolute left-[5px] top-5 bottom-5 w-px bg-iron">
              <div
                className="w-full bg-gradient-to-b from-amber to-amber/20 transition-all duration-1000 ease-out"
                style={{
                  height: `${Math.min(((activeStage + 1) / PIPELINE_STAGES.length) * 100, 100)}%`,
                }}
              />
            </div>

            {PIPELINE_STAGES.map((stage, i) => (
              <PipelineStage
                key={stage.step}
                {...stage}
                isActive={i === activeStage}
                isComplete={i < activeStage}
              />
            ))}
          </div>
        </div>
      </section>

      {/* ── FEATURES / CAPABILITIES ── */}
      <section className="border-t border-iron bg-noise">
        <div className="mx-auto max-w-5xl px-6 py-28">
          <div className="mb-16 text-center">
            <p className="mb-3 text-[11px] font-mono uppercase tracking-[0.15em] text-amber">
              Capabilities
            </p>
            <h2 className="font-display text-3xl font-bold text-foreground mb-3">
              Your codebase has a brain now.
            </h2>
            <p className="max-w-lg mx-auto text-sm text-ash/70">
              Not just a reviewer. A comprehension engine that builds a living model of your code.
            </p>
          </div>

          <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
            {FEATURES.map((feature) => (
              <div
                key={feature.title}
                className="group relative rounded-lg border border-iron bg-charcoal p-6 transition-all hover:border-amber/30 hover:shadow-[0_0_24px_-8px_oklch(0.77_0.15_75/0.2)]"
              >
                <div className="mb-4 text-amber">{feature.icon}</div>
                <h3 className="mb-2 text-sm font-bold text-foreground">
                  {feature.title}
                </h3>
                <p className="text-xs leading-relaxed text-slate-text">
                  {feature.description}
                </p>
              </div>
            ))}
          </div>
        </div>
      </section>

      {/* ── COMPARISON ── */}
      <section className="border-t border-iron bg-charcoal/30 bg-noise">
        <div className="mx-auto max-w-4xl px-6 py-28">
          <div className="mb-16 text-center">
            <p className="mb-3 text-[11px] font-mono uppercase tracking-[0.15em] text-amber">
              The difference
            </p>
            <h2 className="font-display text-3xl font-bold text-foreground mb-3">
              Traditional review tools vs. Argus
            </h2>
            <p className="max-w-lg mx-auto text-sm text-ash/70">
              Most tools look at what changed. Argus understands why it matters.
            </p>
          </div>

          <div className="rounded-lg border border-iron bg-charcoal overflow-hidden">
            {/* Header */}
            <div className="grid grid-cols-[1fr_1fr_1fr] gap-4 px-6 py-4 border-b border-iron bg-charcoal/80">
              <div className="text-[11px] font-mono uppercase tracking-wider text-slate-text"></div>
              <div className="text-[11px] font-mono uppercase tracking-wider text-slate-text/60">Traditional</div>
              <div className="text-[11px] font-mono uppercase tracking-wider text-amber">Argus</div>
            </div>
            {/* Rows */}
            <div className="px-6">
              <ComparisonRow
                label="Scope"
                traditional="Single-file diff"
                argus="Cross-file dependency graph"
              />
              <ComparisonRow
                label="Memory"
                traditional="Stateless per run"
                argus="Remembers every review, bug, incident"
              />
              <ComparisonRow
                label="Context"
                traditional="Just the changed lines"
                argus="Callers, imports, tests, shared types"
              />
              <ComparisonRow
                label="Predictions"
                traditional="None"
                argus="Simulates execution paths with confidence"
              />
              <ComparisonRow
                label="Feedback style"
                traditional="Robotic issue list"
                argus="Conversational What/Why sections"
              />
              <ComparisonRow
                label="Impact analysis"
                traditional="Not available"
                argus="Blast radius map of affected code"
              />
            </div>
          </div>
        </div>
      </section>

      {/* ── CODE SIMULATION SPOTLIGHT ── */}
      <section className="border-t border-iron bg-noise">
        <div className="mx-auto max-w-5xl px-6 py-28">
          <div className="mb-16 text-center">
            <p className="mb-3 text-[11px] font-mono uppercase tracking-[0.15em] text-amber">
              The big differentiator
            </p>
            <h2 className="font-display text-3xl md:text-4xl font-bold text-foreground mb-3">
              See failures before they happen.
            </h2>
            <p className="max-w-lg mx-auto text-sm text-ash/70">
              Argus doesn&apos;t just flag problems &mdash; it simulates what happens when
              your code runs. Confidence scores tell you how certain it is.
            </p>
          </div>

          {/* Simulation output mock */}
          <div className="max-w-3xl mx-auto">
            <div className="flex items-center gap-2 rounded-t-lg border border-iron bg-charcoal px-4 py-2.5">
              <div className="flex gap-1.5">
                <div className="h-2.5 w-2.5 rounded-full bg-iron" />
                <div className="h-2.5 w-2.5 rounded-full bg-iron" />
                <div className="h-2.5 w-2.5 rounded-full bg-iron" />
              </div>
              <span className="ml-2 text-[11px] font-mono text-amber">
                argus &mdash; simulation output
              </span>
            </div>
            <div className="border-x border-b border-iron rounded-b-lg bg-void p-5 space-y-4">
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
                    <span className="text-slate-text">What:</span> Two concurrent cancellation requests for the same subscription reach <code className="text-amber/80">billing.cancel()</code>. First succeeds at Stripe, second throws <code className="text-amber/80">StripeInvalidRequestError</code>. DB update runs for both.
                  </p>
                  <p className="text-[11px] font-mono text-ash/70 leading-relaxed mt-1">
                    <span className="text-slate-text">Why:</span> No mutex or idempotency key on the cancellation path. The Stripe call and DB write are not wrapped in a transaction. Race window is ~200ms under load.
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
                    Scenario: Cache key collision under user ID reuse
                  </span>
                  <span className="ml-auto text-[10px] font-mono text-yellow-400">
                    confidence 78%
                  </span>
                </div>
                <div className="pl-4 border-l-2 border-yellow-500/30">
                  <p className="text-[11px] font-mono text-ash/70 leading-relaxed">
                    <span className="text-slate-text">What:</span> Deleted user IDs are recycled. Cache key <code className="text-amber/80">{"`user_${id}`"}</code> with TTL=0 serves stale data from the previous account holder.
                  </p>
                  <p className="text-[11px] font-mono text-ash/70 leading-relaxed mt-1">
                    <span className="text-slate-text">Why:</span> Infinite TTL + recycled IDs = data leakage. Add a generation counter or namespace the key with a creation timestamp.
                  </p>
                </div>
              </div>

              <div className="border-t border-iron/50" />

              {/* Scenario 3 */}
              <div className="space-y-2">
                <div className="flex items-center gap-2">
                  <span className="text-[9px] font-mono uppercase tracking-wider px-1.5 py-0.5 rounded border bg-emerald-500/20 text-emerald-400 border-emerald-500/30">
                    passes
                  </span>
                  <span className="text-[11px] font-mono text-foreground">
                    Scenario: Token refresh during active session
                  </span>
                  <span className="ml-auto text-[10px] font-mono text-emerald-400">
                    confidence 91%
                  </span>
                </div>
                <div className="pl-4 border-l-2 border-emerald-500/30">
                  <p className="text-[11px] font-mono text-ash/70 leading-relaxed">
                    <span className="text-slate-text">What:</span> Bearer token split correctly handles refresh flow. New authorization header parsed without prefix contamination.
                  </p>
                  <p className="text-[11px] font-mono text-ash/70 leading-relaxed mt-1">
                    <span className="text-slate-text">Why:</span> The <code className="text-amber/80">?.split(&quot; &quot;)[1]</code> fix correctly isolates the token. Verified against JWT verify contract.
                  </p>
                </div>
              </div>
            </div>
          </div>
        </div>
      </section>

      {/* ── HONESTY / PRATFALL ── */}
      <section className="border-t border-iron bg-charcoal/30 bg-noise">
        <div className="mx-auto max-w-3xl px-6 py-24 text-center">
          <p className="mb-3 text-[11px] font-mono uppercase tracking-[0.15em] text-amber">
            Honest take
          </p>
          <h2 className="font-display text-3xl md:text-4xl font-bold text-foreground mb-6">
            Not perfect. Gets better every review.
          </h2>
          <p className="max-w-xl mx-auto text-sm text-ash/70 leading-relaxed mb-8">
            Argus is AI. It will sometimes flag things that don&apos;t need flagging. But
            it learns your codebase, your patterns, your rules. The false positives
            shrink. The catches that matter stay sharp.
          </p>
          <div className="grid grid-cols-1 md:grid-cols-3 gap-6">
            <div className="rounded-lg border border-iron bg-charcoal/50 p-5">
              <div className="font-display text-sm font-bold text-foreground mb-2">
                Learns your patterns
              </div>
              <p className="text-[11px] font-mono text-slate-text">
                Custom rules, past reviews, and incident history shape every review.
              </p>
            </div>
            <div className="rounded-lg border border-iron bg-charcoal/50 p-5">
              <div className="font-display text-sm font-bold text-foreground mb-2">
                Never forgets
              </div>
              <p className="text-[11px] font-mono text-slate-text">
                That edge case from 6 months ago? Still in memory. Still checked.
              </p>
            </div>
            <div className="rounded-lg border border-iron bg-charcoal/50 p-5">
              <div className="font-display text-sm font-bold text-foreground mb-2">
                Transparent reasoning
              </div>
              <p className="text-[11px] font-mono text-slate-text">
                Every comment explains why. No black box. Disagree and dismiss.
              </p>
            </div>
          </div>
        </div>
      </section>

      {/* ── PRICING ── */}
      <section className="border-t border-iron bg-noise">
        <div className="mx-auto max-w-4xl px-6 py-24">
          <div className="text-center mb-12">
            <p className="mb-3 text-[11px] font-mono uppercase tracking-[0.15em] text-amber">
              Pricing
            </p>
            <h2 className="font-display text-2xl md:text-3xl font-bold text-foreground mb-4">
              Free during early access. Pro when you&apos;re ready.
            </h2>
            <p className="max-w-lg mx-auto text-sm text-ash/70 leading-relaxed">
              Start free. Upgrade when your team needs more. Early adopters lock in
              a permanent discount.
            </p>
          </div>

          <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
            <div className="rounded-lg border border-iron bg-charcoal/50 p-5 text-center">
              <div className="font-display text-2xl font-bold text-amber mb-1">Free</div>
              <p className="text-[11px] font-mono text-slate-text">
                All features during beta &mdash; no credit card
              </p>
            </div>
            <div className="rounded-lg border border-iron bg-charcoal/50 p-5 text-center">
              <div className="font-display text-2xl font-bold text-amber mb-1">Direct access</div>
              <p className="text-[11px] font-mono text-slate-text">
                Shape the product &mdash; your feedback builds the roadmap
              </p>
            </div>
            <div className="rounded-lg border border-iron bg-charcoal/50 p-5 text-center">
              <div className="font-display text-2xl font-bold text-amber mb-1">Lock in pricing</div>
              <p className="text-[11px] font-mono text-slate-text">
                Early adopters keep a permanent discount at launch
              </p>
            </div>
          </div>

          <div className="mt-8 text-center">
            <Link
              href="/pricing"
              className="inline-flex items-center gap-2 text-sm font-mono text-amber hover:text-amber/80 transition-colors"
            >
              View full pricing
              <svg className="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M13 7l5 5m0 0l-5 5m5-5H6" />
              </svg>
            </Link>
          </div>
        </div>
      </section>

      {/* ── CTA ── */}
      <section className="border-t border-iron bg-charcoal/30 bg-noise relative overflow-hidden">
        {/* Ambient glow */}
        <div
          className="pointer-events-none absolute top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 h-[500px] w-[500px] rounded-full opacity-10"
          style={{
            background:
              "radial-gradient(circle, oklch(0.77 0.15 75 / 0.4) 0%, transparent 70%)",
          }}
        />
        <div className="relative z-10 mx-auto max-w-3xl px-6 py-28 text-center">
          <EyeSymbol className="mx-auto mb-6 h-12 w-auto text-amber/60" />
          <h2 className="font-display text-3xl md:text-4xl font-bold text-foreground mb-4">
            The guardian your main branch deserves.
          </h2>
          <p className="text-sm text-ash/70 mb-3 max-w-md mx-auto">
            Install the GitHub App. Connect your repos. First review in under
            two minutes.
          </p>
          <p className="text-xs text-slate-text mb-8 max-w-sm mx-auto font-mono">
            Free during early access. No credit card required.
          </p>
          <Link
            href="/sign-up"
            className="group inline-flex h-12 items-center rounded-md bg-amber px-10 text-sm font-mono font-medium text-void transition-all hover:brightness-110 hover:shadow-[0_0_30px_-4px_oklch(0.77_0.15_75/0.5)]"
          >
            Get started free
            <svg
              className="ml-2 h-4 w-4 transition-transform group-hover:translate-x-0.5"
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
              strokeWidth={2}
            >
              <path strokeLinecap="round" strokeLinejoin="round" d="M13 7l5 5m0 0l-5 5m5-5H6" />
            </svg>
          </Link>
        </div>
      </section>

      {/* ── FOOTER ── */}
      <footer className="border-t border-iron py-10 px-6">
        <div className="mx-auto max-w-6xl">
          <div className="flex items-center justify-between mb-8">
            <span className="wordmark text-xs text-slate-text tracking-[0.15em]">
              ARGUS
            </span>
            <div className="flex gap-6">
              <Link
                href="/docs"
                className="text-[11px] font-mono text-slate-text hover:text-ash transition-colors"
              >
                Docs
              </Link>
              <Link
                href="/pricing"
                className="text-[11px] font-mono text-slate-text hover:text-ash transition-colors"
              >
                Pricing
              </Link>
              <Link
                href="/blog"
                className="text-[11px] font-mono text-slate-text hover:text-ash transition-colors"
              >
                Blog
              </Link>
              <a
                href="https://github.com/BeLazy167/argus"
                target="_blank"
                rel="noopener noreferrer"
                className="text-[11px] font-mono text-slate-text hover:text-ash transition-colors"
              >
                GitHub
              </a>
            </div>
            <span className="text-[11px] font-mono text-iron">
              Nothing merges unseen.
            </span>
          </div>

          {/* Newsletter signup */}
          <div className="flex items-center justify-center gap-3 pt-6 border-t border-iron/50">
            <span className="text-[11px] font-mono text-slate-text">
              Get the changelog &mdash;
            </span>
            <form
              action="https://buttondown.com/api/emails/embed-subscribe/argus"
              method="post"
              target="_blank"
              className="flex gap-2"
            >
              <input
                type="email"
                name="email"
                placeholder="you@company.com"
                required
                className="w-52 rounded-md border border-iron bg-charcoal px-3 py-1.5 text-xs font-mono text-foreground placeholder:text-iron focus:border-amber focus:outline-none"
              />
              <button
                type="submit"
                className="rounded-md bg-amber/10 border border-amber/30 px-4 py-1.5 text-[11px] font-mono text-amber hover:bg-amber/20 transition-colors"
              >
                Subscribe
              </button>
            </form>
          </div>
        </div>
      </footer>
    </>
  );
}
