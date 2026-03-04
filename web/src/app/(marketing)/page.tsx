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

/* ── Stat Counter ── */
function StatCounter({ value, label }: { value: string; label: string }) {
  return (
    <div className="text-center">
      <div className="font-display text-3xl md:text-4xl font-bold text-amber mb-1">
        {value}
      </div>
      <div className="text-[11px] font-mono text-slate-text uppercase tracking-wider">
        {label}
      </div>
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
      label: "TRIAGE",
      desc: "Classifies files by risk. Skips vendored code, configs, lockfiles.",
    },
    {
      step: "02",
      label: "CONTEXT",
      desc: "Retrieves past reviews, rules, and incident history for each file.",
    },
    {
      step: "03",
      label: "REVIEW",
      desc: "Deep analysis per file — bugs, security, error handling, types, tests.",
    },
    {
      step: "04",
      label: "SYNTHESIZE",
      desc: "Aggregates findings. Calculates risk score. Builds the verdict.",
    },
    {
      step: "05",
      label: "POST",
      desc: "Inline comments on the PR. Summary with risk score. Done.",
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

        {/* Scan lines overlay */}
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
            Every line. Every commit. Every time.
          </p>

          <p className="max-w-xl text-sm md:text-base leading-relaxed text-ash/80 mb-10">
            The AI code reviewer that catches what humans miss and remembers what
            humans forget. Ship with zero anxiety.
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
            Without Argus
          </p>
          <h2 className="font-display text-2xl md:text-4xl font-bold text-foreground mb-6 leading-tight">
            That one PR on Friday at 5pm?<br />
            <span className="text-red-400/80">It shipped a SQL injection.</span>
          </h2>
          <p className="max-w-2xl mx-auto text-sm text-ash/70 leading-relaxed mb-8">
            Human reviewers get tired. They skim diffs. They miss the subtle bugs
            hiding in refactors. They forget that this exact pattern caused an
            incident three months ago. Argus doesn&apos;t.
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

      {/* ── FEATURES / CAPABILITIES ── */}
      <section className="border-t border-iron bg-noise">
        <div className="mx-auto max-w-5xl px-6 py-28">
          <div className="mb-16 text-center">
            <p className="mb-3 text-[11px] font-mono uppercase tracking-[0.15em] text-amber">
              Capabilities
            </p>
            <h2 className="font-display text-3xl font-bold text-foreground">
              Your codebase has a memory now.
            </h2>
          </div>

          <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
            {[
              {
                icon: (
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" className="h-5 w-5">
                    <path strokeLinecap="round" strokeLinejoin="round" d="M2.036 12.322a1.012 1.012 0 010-.639C3.423 7.51 7.36 4.5 12 4.5c4.638 0 8.573 3.007 9.963 7.178.07.207.07.431 0 .639C20.577 16.49 16.64 19.5 12 19.5c-4.638 0-8.573-3.007-9.963-7.178z" />
                    <path strokeLinecap="round" strokeLinejoin="round" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
                  </svg>
                ),
                title: "Catch bugs before your users do",
                description:
                  "Every PR analyzed for bugs, security holes, error handling gaps, and logic errors. Not style nits — real issues that break production.",
              },
              {
                icon: (
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" className="h-5 w-5">
                    <path strokeLinecap="round" strokeLinejoin="round" d="M9.813 15.904L9 18.75l-.813-2.846a4.5 4.5 0 00-3.09-3.09L2.25 12l2.846-.813a4.5 4.5 0 003.09-3.09L9 5.25l.813 2.846a4.5 4.5 0 003.09 3.09L15.75 12l-2.846.813a4.5 4.5 0 00-3.09 3.09zM18.259 8.715L18 9.75l-.259-1.035a3.375 3.375 0 00-2.455-2.456L14.25 6l1.036-.259a3.375 3.375 0 002.455-2.456L18 2.25l.259 1.035a3.375 3.375 0 002.455 2.456L21.75 6l-1.036.259a3.375 3.375 0 00-2.455 2.456z" />
                  </svg>
                ),
                title: "Never repeat the same mistake",
                description:
                  "Remembers past reviews, incidents, and patterns. Flags when you touch code that caused problems before.",
              },
              {
                icon: (
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" className="h-5 w-5">
                    <path strokeLinecap="round" strokeLinejoin="round" d="M7.5 21L3 16.5m0 0L7.5 12M3 16.5h13.5m0-13.5L21 7.5m0 0L16.5 12M21 7.5H7.5" />
                  </svg>
                ),
                title: "Push again, only new code reviewed",
                description:
                  "On new pushes, only reviews the delta. No duplicate noise. Knows what it already said.",
              },
              {
                icon: (
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" className="h-5 w-5">
                    <path strokeLinecap="round" strokeLinejoin="round" d="M9 12.75L11.25 15 15 9.75m-3-7.036A11.959 11.959 0 013.598 6 11.99 11.99 0 003 9.749c0 5.592 3.824 10.29 9 11.623 5.176-1.332 9-6.03 9-11.622 0-1.31-.21-2.571-.598-3.751h-.152c-3.196 0-6.1-1.248-8.25-3.285z" />
                  </svg>
                ),
                title: "Your standards, enforced every time",
                description:
                  "Org-wide rules from your dashboard. Per-repo overrides via .argus/rules.md. Consistent reviews across the team.",
              },
              {
                icon: (
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" className="h-5 w-5">
                    <path strokeLinecap="round" strokeLinejoin="round" d="M3.75 13.5l10.5-11.25L12 10.5h8.25L9.75 21.75 12 13.5H3.75z" />
                  </svg>
                ),
                title: "Bring your own model",
                description:
                  "OpenRouter, Claude, GPT, Grok, Qwen — any OpenAI-compatible endpoint. You pick the brain for each pipeline stage.",
              },
              {
                icon: (
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" className="h-5 w-5">
                    <path strokeLinecap="round" strokeLinejoin="round" d="M3 13.125C3 12.504 3.504 12 4.125 12h2.25c.621 0 1.125.504 1.125 1.125v6.75C7.5 20.496 6.996 21 6.375 21h-2.25A1.125 1.125 0 013 19.875v-6.75zM9.75 8.625c0-.621.504-1.125 1.125-1.125h2.25c.621 0 1.125.504 1.125 1.125v11.25c0 .621-.504 1.125-1.125 1.125h-2.25a1.125 1.125 0 01-1.125-1.125V8.625zM16.5 4.125c0-.621.504-1.125 1.125-1.125h2.25C20.496 3 21 3.504 21 4.125v15.75c0 .621-.504 1.125-1.125 1.125h-2.25a1.125 1.125 0 01-1.125-1.125V4.125z" />
                  </svg>
                ),
                title: "Know if a PR is safe to ship",
                description:
                  "Every PR gets a 1-10 risk score. Critical issues tank it. Green score = merge with confidence.",
              },
            ].map((feature) => (
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

      {/* ── PIPELINE ── */}
      <section className="border-t border-iron bg-charcoal/50 bg-noise">
        <div className="mx-auto max-w-4xl px-6 py-28">
          <div className="mb-16 text-center">
            <p className="mb-3 text-[11px] font-mono uppercase tracking-[0.15em] text-amber">
              Pipeline
            </p>
            <h2 className="font-display text-3xl font-bold text-foreground">
              PR opens. Argus reviews. You ship.
            </h2>
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

      {/* ── HONESTY / PRATFALL ── */}
      <section className="border-t border-iron bg-noise">
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

      {/* ── EARLY ACCESS ── */}
      <section className="border-t border-iron bg-charcoal/30 bg-noise">
        <div className="mx-auto max-w-4xl px-6 py-24">
          <div className="text-center mb-12">
            <p className="mb-3 text-[11px] font-mono uppercase tracking-[0.15em] text-amber">
              Early access
            </p>
            <h2 className="font-display text-2xl md:text-3xl font-bold text-foreground mb-4">
              We just launched. You&apos;re early.
            </h2>
            <p className="max-w-lg mx-auto text-sm text-ash/70 leading-relaxed">
              Argus is in active development. Early adopters get free access to
              all features, direct input on the roadmap, and a permanent
              discount when we launch paid tiers.
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
        </div>
      </section>

      {/* ── CTA ── */}
      <section className="border-t border-iron bg-noise relative overflow-hidden">
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

      {/* Keyframe animations */}
      <style>{`
        @keyframes draw {
          to { stroke-dashoffset: 0; }
        }
        @keyframes fadeIn {
          to { opacity: 1; }
        }
        @keyframes scrollPulse {
          0%, 100% { opacity: 0.5; }
          50% { opacity: 1; }
        }
        @keyframes pipelinePing {
          0% {
            transform: scale(1);
            opacity: 0.8;
          }
          100% {
            transform: scale(3);
            opacity: 0;
          }
        }
      `}</style>
    </>
  );
}
