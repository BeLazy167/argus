"use client";

import Link from "next/link";
import { useEffect, useRef, useState } from "react";
import dynamic from "next/dynamic";
import { EyeSymbol } from "@/components/marketing/eye-symbol";
import { AnimatedReview } from "@/components/marketing/animated-review";
import { GitHubReviewMock } from "@/components/marketing/github-review-mock";
import { FadeIn } from "@/components/marketing/fade-in";

const ConstellationBackground = dynamic(
  () => import("@/components/marketing/constellation").then((m) => m.ConstellationBackground),
  { ssr: false }
);

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
          className={`relative h-3 w-3 rounded-full border-2 transition-[background-color,border-color,box-shadow] duration-300 ${
            isActive
              ? "border-amber bg-amber shadow-[0_0_12px_color-mix(in_oklch,var(--color-amber-glow)_60%,transparent)]"
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
        className={`transition-opacity duration-400 ${
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

/* ── Comparison Data ── */
const COMPARISON_ROWS = [
  { label: "Scope", traditional: "Single-file diff", argus: "Cross-file dependency graph" },
  { label: "Memory", traditional: "Stateless per run", argus: "Remembers every review, bug, incident" },
  { label: "Context", traditional: "Just the changed lines", argus: "Callers, imports, tests, shared types" },
  { label: "Predictions", traditional: "None", argus: "Simulates execution paths with confidence" },
  { label: "Feedback style", traditional: "Robotic issue list", argus: "Conversational What/Why sections" },
  { label: "Impact analysis", traditional: "Not available", argus: "Blast radius map of affected code" },
  { label: "Learning", traditional: "Same output every time", argus: "Learns from every 👍/👎 reaction" },
];

/* ── Main Page ── */
export function LandingContent() {
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
        // Stop on the last stage so the final frame shows that stage active.
        // Using PIPELINE_STAGES.length - 1 keeps the terminal in range if the
        // array shrinks or grows — a hardcoded ceiling here previously went
        // out of bounds when the pipeline was trimmed from 7 stages to 6.
        const last = PIPELINE_STAGES.length - 1;
        if (prev >= last) {
          clearInterval(interval);
          return last;
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
      desc: "Classifies each changed file by risk. Generated files, lock files, and vendored deps are skipped before any tokens are spent.",
    },
    {
      step: "02",
      label: "CONTEXT",
      desc: "Gathers cross-file context, blast radius, and relevant memory so the review understands how your change affects the rest of the system.",
    },
    {
      step: "03",
      label: "REVIEW",
      desc: "Focused analysis from multiple angles in parallel — correctness, security, architecture, and regression risk.",
    },
    {
      step: "04",
      label: "REFINE",
      desc: "Deduplicates and validates findings. Noise and low-signal comments are dropped so only high-confidence issues survive.",
    },
    {
      step: "05",
      label: "SYNTHESIZE",
      desc: "Produces a scannable verdict with fix ordering, severity tiers, and diagrams for complex PRs — actionable, not a wall of text.",
    },
    {
      step: "06",
      label: "POST & LEARN",
      desc: "Posts inline comments to GitHub. Learns from your \uD83D\uDC4D / \uD83D\uDC4E and replies — so future reviews get sharper.",
    },
  ];

  return (
    <>
      {/* ── HERO ── */}
      <section aria-label="Hero" className="relative flex min-h-[90vh] md:min-h-[100vh] flex-col items-center justify-center overflow-hidden bg-noise">
        {/* Subtle ambient glow */}
        <div
          className="pointer-events-none absolute top-1/3 left-1/2 -translate-x-1/2 -translate-y-1/2 h-[500px] w-[500px] rounded-full opacity-10"
          style={{
            background:
              "radial-gradient(circle, color-mix(in oklch, var(--color-amber-glow) 30%, transparent) 0%, transparent 60%)",
          }}
        />

        {/* 3D constellation background */}
        <ConstellationBackground />

        {/* Scan lines */}
        <div
          className="pointer-events-none absolute inset-0 opacity-[0.012]"
          style={{
            backgroundImage:
              "repeating-linear-gradient(0deg, transparent, transparent 2px, rgba(245,166,35,0.08) 2px, rgba(245,166,35,0.08) 4px)",
          }}
        />

        {/* Horizontal scan line animation */}
        <div className="pointer-events-none absolute inset-0 overflow-hidden">
          <div className="hero-scan-line absolute left-0 right-0 h-px bg-gradient-to-r from-transparent via-amber/20 to-transparent" />
        </div>

        <div className="relative z-10 flex flex-col items-center text-center px-6 pt-20">
          {/* Eye symbol with staggered entry */}
          <div className="hero-reveal hero-reveal-1">
            <EyeSymbol className="mb-6 h-20 w-auto text-amber hero-eye-glow" trackMouse />
          </div>

          {/* Title with cinematic reveal */}
          <div className="hero-reveal hero-reveal-2 overflow-hidden">
              <h1 className="wordmark text-5xl md:text-7xl lg:text-8xl text-foreground mb-2 tracking-[0.15em] hero-title-stencil">
              ARGUS
            </h1>
          </div>

          {/* Subtitle with typewriter feel */}
          <div className="hero-reveal hero-reveal-3">
            <p className="text-[11px] md:text-xs font-mono text-amber/60 tracking-[0.35em] uppercase mb-5">
              The All-Seeing Code Reviewer
            </p>
          </div>

          {/* Beta badge */}
          <div className="hero-reveal hero-reveal-4">
            <div className="inline-flex items-center gap-2 rounded-full border border-amber/30 bg-amber/5 px-4 py-1.5 mb-6 backdrop-blur-sm">
              <span className="h-1.5 w-1.5 rounded-full bg-amber hero-status-pulse" />
              <span className="text-[11px] font-mono text-amber tracking-wider">EARLY ACCESS &mdash; FREE DURING BETA</span>
            </div>
          </div>

          {/* Tagline */}
          <div className="hero-reveal hero-reveal-5">
            <p className="font-mono text-lg md:text-2xl text-amber mb-3 font-normal italic text-balance">
              Find the bugs your team missed.
            </p>
          </div>

          {/* Description */}
          <div className="hero-reveal hero-reveal-6">
            <p className="max-w-xl text-sm md:text-base leading-relaxed text-ash/80 mb-10 text-pretty">
              AI code review that understands your whole system &mdash; not just the diff.
              Traces dependencies, remembers past incidents, catches the bugs that ship to production.
            </p>
          </div>

          {/* CTA buttons */}
          <div className="hero-reveal hero-reveal-7 flex flex-col sm:flex-row gap-4 mb-16">
            <Link
              href="/sign-up"
              className="group relative inline-flex h-12 items-center border bg-amber px-8 text-sm font-mono font-medium text-void transition-[transform,filter,box-shadow] duration-200 ease-out hover:brightness-110 hover:shadow-[0_0_32px_-4px_color-mix(in_oklch,var(--color-amber-glow)_60%,transparent)] active:scale-[0.97] overflow-hidden"
            >
              <span className="relative z-10 flex items-center">
                Install in 60 seconds
                <svg
                  className="ml-2 h-3.5 w-3.5 transition-transform duration-300 group-hover:translate-x-1"
                  fill="none"
                  viewBox="0 0 24 24"
                  stroke="currentColor"
                  strokeWidth={2}
                >
                  <path strokeLinecap="round" strokeLinejoin="round" d="M13 7l5 5m0 0l-5 5m5-5H6" />
                </svg>
              </span>
              {/* Button shimmer */}
              <div className="absolute inset-0 -translate-x-full group-hover:translate-x-full transition-transform duration-700 bg-gradient-to-r from-transparent via-white/20 to-transparent" />
            </Link>
            <Link
              href="/docs"
              className="group inline-flex h-12 items-center border border-iron px-8 text-sm font-mono text-ash transition-[border-color,color,box-shadow] duration-200 hover:border-amber/40 hover:text-foreground hover:shadow-[0_0_16px_-6px_color-mix(in_oklch,var(--color-amber-glow)_30%,transparent)]"
            >
              Read the docs
            </Link>
          </div>

          {/* Live review animation */}
          <div className="hero-reveal hero-reveal-8">
            <AnimatedReview />
          </div>
        </div>

        {/* Scroll indicator */}
        <div className="absolute bottom-8 flex flex-col items-center gap-2 text-slate-text hero-reveal hero-reveal-9">
          <span className="text-[10px] font-mono uppercase tracking-widest">
            Scroll
          </span>
          <div className="h-8 w-px bg-gradient-to-b from-slate-text/50 to-transparent animate-[scrollPulse_2s_ease-in-out_infinite]" />
        </div>
      </section>

      {/* ── SOCIAL PROOF ── */}
      <section aria-label="Social proof" className="border-t border-iron bg-charcoal/30">
        <div className="mx-auto max-w-3xl px-6 py-10 flex flex-wrap items-center justify-center gap-8 md:gap-14">
          {[
            { value: "80%", label: "bug recall" },
            { value: "95%", label: "precision" },
            { value: "<2 min", label: "per review" },
            { value: "4", label: "specialist reviewers" },
          ].map((stat, i) => (
            <FadeIn key={stat.label} delay={i * 80} className="flex items-center gap-8 md:gap-14">
              {i > 0 && <div className="h-8 w-px bg-iron hidden md:block" />}
              <div className="text-center">
                <div className="font-mono text-2xl font-bold text-foreground">{stat.value}</div>
                <p className="text-[10px] font-mono text-slate-text mt-1">{stat.label}</p>
              </div>
            </FadeIn>
          ))}
        </div>
      </section>

      {/* ── SEE IT IN ACTION ── */}
      <section aria-label="See it in action" className="border-t border-iron bg-charcoal/30 bg-noise">
        <div className="mx-auto max-w-5xl px-6 py-28">
          <div className="mb-16 text-center">
            <p className="mb-3 text-[11px] font-mono uppercase tracking-[0.15em] text-amber">
              Real bugs, not lint warnings
            </p>
            <h2 className="font-mono text-3xl md:text-4xl font-bold text-foreground mb-4">
              This is what Argus posts on your PRs.
            </h2>
            <p className="max-w-lg mx-auto text-sm text-ash/70">
              SQL injection. Auth bypass. Race conditions. The{" "}
              <a href="https://owasp.org/www-project-top-ten/" target="_blank" rel="noopener noreferrer" className="text-amber/70 hover:text-amber transition-colors">OWASP Top 10</a>{" "}
              bugs that pass code review and break production at 2am.
            </p>
          </div>

          <FadeIn>
            <GitHubReviewMock />
          </FadeIn>
        </div>
      </section>

      {/* ── WHY ARGUS ── */}
      <section aria-label="Why Argus" className="border-t border-iron bg-noise">
        <div className="mx-auto max-w-4xl px-6 py-24">
          <div className="grid grid-cols-1 md:grid-cols-3 gap-6">
            {[
              {
                icon: <path strokeLinecap="round" strokeLinejoin="round" d="M7.5 21L3 16.5m0 0L7.5 12M3 16.5h13.5m0-13.5L21 7.5m0 0L16.5 12M21 7.5H7.5" />,
                title: "Sees across files",
                desc: "When you change a function, Argus traces who calls it, what tests cover it, and what breaks downstream. Not just the diff.",
              },
              {
                icon: <path strokeLinecap="round" strokeLinejoin="round" d="M12 6v6h4.5m4.5 0a9 9 0 11-18 0 9 9 0 0118 0z" />,
                title: "Remembers everything",
                desc: "That edge case from 6 months ago? Still in memory. Past bugs, incidents, and team decisions inform every future review.",
              },
              {
                icon: <path strokeLinecap="round" strokeLinejoin="round" d="M9 12.75L11.25 15 15 9.75m-3-7.036A11.959 11.959 0 013.598 6 11.99 11.99 0 003 9.749c0 5.592 3.824 10.29 9 11.623 5.176-1.332 9-6.03 9-11.622 0-1.31-.21-2.571-.598-3.751h-.152c-3.196 0-6.1-1.248-8.25-3.285z" />,
                title: "Gets smarter every review",
                desc: "React 👍 to confirm findings, 👎 to dismiss. Argus learns your patterns. False positives shrink. Real catches stay sharp.",
              },
            ].map((card, i) => (
              <FadeIn key={card.title} delay={i * 100}>
                <div className="border border-iron bg-charcoal/50 p-6 h-full">
                  <div className="text-amber mb-3">
                    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" className="h-6 w-6">
                      {card.icon}
                    </svg>
                  </div>
                  <h3 className="font-mono text-sm font-bold text-foreground mb-2">{card.title}</h3>
                  <p className="text-[11px] font-mono text-slate-text leading-relaxed">{card.desc}</p>
                </div>
              </FadeIn>
            ))}
          </div>
          <FadeIn className="mt-8 text-center">
            <Link href="/compare" className="text-xs font-mono text-amber hover:text-amber/80 transition-colors">
              See how Argus compares to CodeRabbit, Copilot, and 7 other tools →
            </Link>
          </FadeIn>
        </div>
      </section>

      {/* ── HOW IT WORKS — PIPELINE ── */}
      <section aria-label="How it works" className="border-t border-iron bg-charcoal/50 bg-noise">
        <div className="mx-auto max-w-4xl px-6 py-28">
          <FadeIn>
            <div className="mb-16 text-center">
              <p className="mb-3 text-[11px] font-mono uppercase tracking-[0.15em] text-amber">
                How it works
              </p>
              <h2 className="font-mono text-3xl font-bold text-foreground mb-3">
                Six stages. Under two minutes.
              </h2>
              <p className="max-w-lg mx-auto text-sm text-ash/70">
                Every PR gets the same rigorous pipeline. No shortcuts.
              </p>
            </div>
          </FadeIn>

          <div ref={pipelineRef} className="relative ml-1.5">
            {/* Connecting vertical line */}
            <div className="absolute left-[5px] top-5 bottom-5 w-px bg-iron">
              <div
                className="w-full bg-gradient-to-b from-amber to-amber/20 transition-[height] duration-700 ease-out"
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
          <FadeIn className="mt-10 text-center">
            <Link href="/docs" className="text-xs font-mono text-amber hover:text-amber/80 transition-colors">
              Read the full pipeline documentation →
            </Link>
          </FadeIn>
        </div>
      </section>

      {/* ── PRICING ── */}
      <section aria-label="Pricing" className="border-t border-iron bg-noise">
        <div className="mx-auto max-w-4xl px-6 py-24">
          <FadeIn>
            <div className="text-center mb-12">
              <p className="mb-3 text-[11px] font-mono uppercase tracking-[0.15em] text-amber">
                Pricing
              </p>
              <h2 className="font-mono text-2xl md:text-3xl font-bold text-foreground mb-4">
                Start free. Scale when you&apos;re ready.
              </h2>
              <p className="max-w-lg mx-auto text-sm text-ash/70 leading-relaxed">
                Bring your own LLM key via OpenRouter. You control the model, the cost, and the quality.
              </p>
            </div>
          </FadeIn>

          <div className="grid grid-cols-1 md:grid-cols-2 gap-6 max-w-2xl mx-auto">
            {/* Free tier */}
            <div className="border border-iron bg-charcoal/50 p-6">
              <div className="font-mono text-2xl font-bold text-foreground mb-1">Free</div>
              <p className="text-[11px] font-mono text-slate-text mb-5">No credit card required</p>
              <ul className="space-y-2.5 text-xs font-mono text-ash/80">
                <li className="flex items-center gap-2">
                  <span className="text-amber">&#10003;</span> 3 repositories
                </li>
                <li className="flex items-center gap-2">
                  <span className="text-amber">&#10003;</span> 50 reviews / month
                </li>
                <li className="flex items-center gap-2">
                  <span className="text-amber">&#10003;</span> Full 6-stage pipeline
                </li>
                <li className="flex items-center gap-2">
                  <span className="text-amber">&#10003;</span> BYOK via OpenRouter
                </li>
              </ul>
            </div>
            {/* Pro tier */}
            <div className="border border-amber/40 bg-charcoal/50 p-6 relative">
              <div className="absolute -top-2.5 right-4 text-[9px] font-mono uppercase tracking-wider px-2 py-0.5 bg-amber text-void font-bold">
                Recommended
              </div>
              <div className="font-mono text-2xl font-bold text-amber mb-1">
                $19<span className="text-base font-normal text-slate-text">/mo per workspace</span>
              </div>
              <p className="text-[11px] font-mono text-slate-text mb-5">Everything in Free, plus</p>
              <ul className="space-y-2.5 text-xs font-mono text-ash/80">
                <li className="flex items-center gap-2">
                  <span className="text-amber">&#10003;</span> Unlimited repositories
                </li>
                <li className="flex items-center gap-2">
                  <span className="text-amber">&#10003;</span> 500 reviews / month
                </li>
                <li className="flex items-center gap-2">
                  <span className="text-amber">&#10003;</span> Priority support
                </li>
                <li className="flex items-center gap-2">
                  <span className="text-amber">&#10003;</span> Early access to new features
                </li>
              </ul>
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
      <section aria-label="Get started" className="border-t border-iron bg-charcoal/30 bg-noise relative overflow-hidden">
        {/* Ambient glow */}
        <div
          className="pointer-events-none absolute top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 h-[500px] w-[500px] rounded-full opacity-10"
          style={{
            background:
              "radial-gradient(circle, color-mix(in oklch, var(--color-amber-glow) 40%, transparent) 0%, transparent 70%)",
          }}
        />
        <FadeIn className="relative z-10 mx-auto max-w-3xl px-6 py-28 text-center">
          <EyeSymbol className="mx-auto mb-6 h-12 w-auto text-amber/60" />
          <h2 className="font-mono text-3xl md:text-4xl font-bold text-foreground mb-4">
            Stop shipping bugs.
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
            className="group inline-flex h-12 items-center border bg-amber px-10 text-sm font-mono font-medium text-void transition-[transform,filter,box-shadow] duration-150 ease-out hover:brightness-110 hover:shadow-[0_0_30px_-4px_color-mix(in_oklch,var(--color-amber-glow)_50%,transparent)] active:scale-[0.97]"
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
        </FadeIn>
      </section>

      {/* ── FOOTER ── */}
      <footer className="border-t border-iron py-10 px-6">
        <div className="mx-auto max-w-6xl">
          <div className="flex flex-col md:flex-row items-center justify-between mb-8 gap-4">
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
                className="w-52 border border-iron bg-charcoal px-3 py-1.5 text-xs font-mono text-foreground placeholder:text-iron focus:border-amber focus:outline-none"
              />
              <button
                type="submit"
                className="border bg-amber/10 border-amber/30 px-4 py-1.5 text-[11px] font-mono text-amber hover:bg-amber/20 transition-[background-color] duration-150"
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
