"use client";

import { useEffect, useRef, useState } from "react";

import { FadeIn } from "@/components/marketing/fade-in";

/**
 * Simulation section — "failure scenarios against your actual diff".
 *
 * Layout: badge + headline + subhead, then a 66/33 split
 *   Left:  terminal chrome with realistic Go diff + scenario output
 *   Right: "Edge surface" telemetry gauge (concentric rings, radial core, tick marks)
 * Bottom: 3 editorial numbered how-it-works columns with vertical rules
 *         and deliberately uneven typographic weight per step.
 *
 * All teal accents from the design source are reinterpreted as amber at
 * varied opacity or as neutral iron/slate tones — per brand.
 *
 * Motion:
 *   - Cards stagger in at 80ms offsets with a strong custom ease.
 *   - Gauge/LIVE pulses only when in viewport (IntersectionObserver + data-attr).
 *   - All animations are transform/opacity only and respect reduced-motion.
 */
export function Simulation() {
  return (
    <section
      id="features"
      className="relative border-t border-iron/60 bg-background"
    >
      {/* ambient amber wash — faint, top-center */}
      <div
        aria-hidden
        className="pointer-events-none absolute inset-x-0 top-0 h-[420px]"
        style={{
          background:
            "radial-gradient(60% 80% at 50% 0%, color-mix(in oklch, var(--color-amber-glow) 6%, transparent) 0%, transparent 70%)",
        }}
      />

      <div className="relative mx-auto max-w-[1312px] px-8 py-28 sm:py-32">
        {/* ── Header ────────────────────────────────────────────── */}
        <FadeIn>
          <div className="flex items-center gap-3 font-mono text-[11px] uppercase tracking-[0.22em] text-amber-glow">
            <span className="inline-block h-1.5 w-1.5 rounded-full bg-amber-glow shadow-[0_0_8px_currentColor]" />
            <span>02 &middot; Failure Simulation</span>
          </div>
        </FadeIn>

        <FadeIn delay={80}>
          <h2 className="mt-7 max-w-[20ch] font-mono text-[44px] font-bold leading-[1.05] tracking-[-0.02em] text-foreground sm:text-[56px]">
            Simulates failure scenarios against your actual diff
          </h2>
        </FadeIn>

        <FadeIn delay={160}>
          <p className="mt-6 max-w-[68ch] font-sans text-[15px] leading-[1.7] text-slate-text sm:text-base">
            Most reviewers pattern-match. Argus stress-tests: null inputs, PVC
            adoption edge cases, race conditions. It tells you exactly where
            prod could break &mdash; before merge.
          </p>
        </FadeIn>

        {/* ── 66/33 split: code viewer + edge-surface gauge ─────── */}
        <div className="mt-16 grid grid-cols-1 gap-6 lg:grid-cols-[1.9fr_1fr]">
          <FadeIn delay={200}>
            <TerminalDiff />
          </FadeIn>
          <FadeIn delay={260}>
            <EdgeSurfaceGauge />
          </FadeIn>
        </div>

        {/* ── Bottom: editorial how-it-works cards ──────────────── */}
        <FadeIn delay={320}>
          <HowItWorks />
        </FadeIn>
      </div>

      {/* Keyframes kept local to this section to avoid polluting globals.css */}
      <style>{`
        @keyframes simLivePulse {
          0%, 100% { transform: scale(1); opacity: 0.55; }
          50%      { transform: scale(2.4); opacity: 0; }
        }
        @keyframes simGaugeOrbit {
          from { transform: rotate(0deg); }
          to   { transform: rotate(360deg); }
        }
        @keyframes simGaugeGlow {
          0%, 100% { opacity: 0.55; }
          50%      { opacity: 1; }
        }
        @keyframes simScan {
          0%   { opacity: 0; transform: translateY(-100%); }
          10%  { opacity: 0.9; }
          90%  { opacity: 0.9; }
          100% { opacity: 0; transform: translateY(100%); }
        }
        @keyframes simCardIn {
          from { opacity: 0; transform: translateY(12px); }
          to   { opacity: 1; transform: translateY(0); }
        }
        [data-in-view="false"] .sim-gauge-orbit,
        [data-in-view="false"] .sim-gauge-glow,
        [data-in-view="false"] .sim-scan,
        [data-in-view="false"] .sim-live-ping {
          animation-play-state: paused !important;
        }
        [data-in-view="true"] .sim-card {
          animation: simCardIn 520ms cubic-bezier(0.23, 1, 0.32, 1) both;
        }
        [data-in-view="true"] .sim-card[data-step="01"] { animation-delay: 0ms; }
        [data-in-view="true"] .sim-card[data-step="02"] { animation-delay: 80ms; }
        [data-in-view="true"] .sim-card[data-step="03"] { animation-delay: 160ms; }
        @media (prefers-reduced-motion: reduce) {
          .sim-gauge-orbit, .sim-gauge-glow, .sim-scan, .sim-live-ping {
            animation: none !important;
          }
          .sim-card { animation: none !important; opacity: 1 !important; transform: none !important; }
        }
      `}</style>
    </section>
  );
}

/* ─────────────────────────────────────────────────────────────── */
/* useInView — shared IntersectionObserver hook                    */
/* ─────────────────────────────────────────────────────────────── */

/**
 * Lightweight intersection hook. Sets a `data-in-view` attribute on the
 * returned ref's element once it enters the viewport so descendant
 * animations can pause/resume via CSS selectors.
 */
function useInView<T extends HTMLElement>(rootMargin = "0px") {
  const ref = useRef<T>(null);
  const [inView, setInView] = useState(false);

  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const obs = new IntersectionObserver(
      ([entry]) => {
        setInView(!!entry?.isIntersecting);
      },
      { threshold: 0.15, rootMargin }
    );
    obs.observe(el);
    return () => obs.disconnect();
  }, [rootMargin]);

  return { ref, inView };
}

/* ─────────────────────────────────────────────────────────────── */
/* Terminal diff + scenario output                                 */
/* ─────────────────────────────────────────────────────────────── */

type DiffLine = {
  no: number;
  text: string;
  kind: "context" | "add" | "remove";
};

/**
 * Realistic-looking Go snippet: a PVC reconciler claim path. The line
 * numbers on the left gutter are right-aligned tabular-nums so three-digit
 * numbers line up cleanly with two-digit numbers. `+` is rendered in amber
 * and `-` in iron so adds read as intent and removes read as context.
 */
const DIFF_LINES: DiffLine[] = [
  { no: 136, text: "func (r *Reconciler) Claim(ctx context.Context) error {", kind: "context" },
  { no: 137, text: "\tr.mu.Lock()", kind: "context" },
  { no: 138, text: "\tdefer r.mu.Unlock()", kind: "context" },
  { no: 139, text: "\tif r.closed { return ErrClosed }", kind: "remove" },
  { no: 140, text: "\tif atomic.LoadInt64(&r.inFlight) >= maxClaims {", kind: "add" },
  { no: 141, text: "\t\treturn fmt.Errorf(\"claim: %w\", ErrBusy)", kind: "add" },
  { no: 142, text: "\t}", kind: "add" },
  { no: 143, text: "\tatomic.AddInt64(&r.inFlight, 1)", kind: "add" },
  { no: 144, text: "\tdefer atomic.AddInt64(&r.inFlight, -1)", kind: "context" },
  { no: 145, text: "\treturn r.reconcile(ctx)", kind: "context" },
  { no: 146, text: "}", kind: "context" },
];

type Scenario = {
  label: string;
  status: "queued" | "passed" | "flagged";
  severity?: "critical";
  hint?: string;
};

const SCENARIOS: Scenario[] = [
  { label: "Null storage class on claim creation", status: "passed" },
  { label: "PVC adoption w/ orphaned finalizer", status: "passed" },
  {
    label: "Race condition when consumed-PVC count > 200",
    status: "flagged",
    severity: "critical",
    hint: "could yield ~250× concurrent claims. Add bounded ch on line 143.",
  },
  { label: "Context cancellation mid-reconcile", status: "passed" },
  { label: "Graceful shutdown with pending ops", status: "queued" },
  { label: "Hot quorum loss during write", status: "queued" },
];

function TerminalDiff() {
  const { ref, inView } = useInView<HTMLDivElement>();
  return (
    <div
      ref={ref}
      data-in-view={inView ? "true" : "false"}
      className="relative overflow-hidden border border-iron/80 bg-charcoal/70 shadow-[0_1px_0_0_rgba(255,255,255,0.03)_inset,0_30px_80px_-40px_rgba(0,0,0,0.8)]"
      style={{
        backgroundImage:
          "linear-gradient(180deg, color-mix(in oklch, var(--color-charcoal) 88%, black) 0%, var(--color-charcoal) 100%)",
      }}
    >
      {/* chrome */}
      <div className="flex items-center justify-between border-b border-iron/60 px-5 py-3">
        <div className="flex items-center gap-4 font-mono text-[11px] tracking-[0.18em] text-slate-text">
          <span className="flex items-center gap-2 text-amber-glow">
            <LivePulse />
            <span className="uppercase">Live simulation</span>
          </span>
          <span className="h-3 w-px bg-iron" />
          <span className="lowercase tracking-[0.08em] text-slate-text/80">
            diff.patch &rarr; argus.sim
          </span>
        </div>
        <div className="flex items-center gap-1.5">
          <span className="h-2 w-2 rounded-full bg-iron" />
          <span className="h-2 w-2 rounded-full bg-iron" />
          <span className="h-2 w-2 rounded-full bg-iron" />
        </div>
      </div>

      {/* body */}
      <div className="grid grid-cols-1 md:grid-cols-[1.1fr_1fr]">
        {/* left: diff */}
        <div className="relative border-b border-iron/60 md:border-b-0 md:border-r">
          <div className="flex items-center gap-3 px-5 pb-2 pt-4 font-mono text-[10.5px] uppercase tracking-[0.22em] text-slate-text/70">
            <span>pkg/storage/pvc_reconciler.go</span>
            <span className="ml-auto text-slate-text/50">+target HEAD</span>
          </div>
          <div className="relative overflow-x-auto px-2 pb-5 font-mono text-[12.5px] leading-[1.7] whitespace-pre">
            {/* subtle vertical gutter separator */}
            <span
              aria-hidden
              className="pointer-events-none absolute inset-y-4 left-[48px] w-px bg-iron/40"
            />
            {DIFF_LINES.map((ln) => (
              <DiffRow key={ln.no} line={ln} />
            ))}
          </div>
        </div>

        {/* right: scenarios */}
        <div className="relative">
          {/* scan line — only paints while section is in view */}
          <span
            aria-hidden
            className="sim-scan pointer-events-none absolute inset-x-0 top-0 h-[2px] bg-[linear-gradient(90deg,transparent,color-mix(in_oklch,var(--color-amber-glow)_45%,transparent),transparent)]"
            style={{ animation: "simScan 6s cubic-bezier(0.77,0,0.175,1) infinite" }}
          />
          <div className="flex items-center gap-3 px-5 pb-2 pt-4 font-mono text-[10.5px] uppercase tracking-[0.22em] text-slate-text/70">
            <span>scenarios</span>
            <span className="ml-auto tabular-nums text-slate-text/50">
              6 total &middot; 1 flagged
            </span>
          </div>
          <ul className="flex flex-col gap-px px-3 pb-4">
            {SCENARIOS.map((s, i) => (
              <ScenarioRow key={i} scenario={s} />
            ))}
          </ul>
        </div>
      </div>
    </div>
  );
}

function DiffRow({ line }: { line: DiffLine }) {
  const isAdd = line.kind === "add";
  const isRemove = line.kind === "remove";
  const bg = isAdd
    ? "bg-[color-mix(in_oklch,var(--color-amber-glow)_10%,transparent)]"
    : isRemove
      ? "bg-[color-mix(in_oklch,var(--color-iron)_35%,transparent)]"
      : "bg-transparent";
  const gutter = isAdd
    ? "text-amber-glow/90"
    : isRemove
      ? "text-iron"
      : "text-slate-text/40";
  const text = isAdd
    ? "text-foreground"
    : isRemove
      ? "text-slate-text/55 line-through decoration-iron/70"
      : "text-slate-text/80";
  const marker = isAdd ? "+" : isRemove ? "-" : " ";

  return (
    <div className={`relative flex gap-3 px-3 py-0.5 ${bg}`}>
      {isAdd && (
        <span
          aria-hidden
          className="absolute inset-y-0 left-0 w-[2px] bg-amber-glow/80"
        />
      )}
      <span className={`w-9 select-none text-right tabular-nums ${gutter}`}>
        {line.no}
      </span>
      <span className={`w-3 select-none font-bold ${gutter}`}>{marker}</span>
      <span className={`flex-1 whitespace-pre ${text}`}>{line.text}</span>
    </div>
  );
}

function ScenarioRow({ scenario }: { scenario: Scenario }) {
  const isCrit = scenario.severity === "critical";
  return (
    <li
      className={
        "group relative flex flex-col gap-1.5 px-4 py-2.5 font-mono text-[12.5px] " +
        (isCrit
          ? "bg-[color-mix(in_oklch,var(--color-amber-glow)_9%,transparent)] shadow-[inset_0_0_0_1px_color-mix(in_oklch,var(--color-amber-glow)_28%,transparent),0_8px_36px_-20px_color-mix(in_oklch,var(--color-amber-glow)_55%,transparent)]"
          : "hover:bg-charcoal/40")
      }
    >
      {/* Critical left rail */}
      {isCrit && (
        <span
          aria-hidden
          className="absolute inset-y-0 left-0 w-[3px] bg-amber-glow shadow-[0_0_12px_currentColor]"
        />
      )}
      <div className="flex items-start gap-3">
        <StatusGlyph status={scenario.status} />
        <span
          className={
            "flex-1 leading-[1.45] " +
            (isCrit
              ? "font-semibold text-foreground"
              : scenario.status === "queued"
                ? "text-slate-text/55"
                : "text-slate-text/90")
          }
        >
          {scenario.label}
        </span>
        {isCrit && (
          <span className="shrink-0 border border-amber-glow/70 bg-amber-glow/15 px-1.5 py-0.5 text-[9px] font-semibold uppercase tracking-[0.22em] text-amber-glow">
            Critical
          </span>
        )}
        {!isCrit && (
          <span
            className={
              "shrink-0 font-mono text-[9px] uppercase tracking-[0.22em] " +
              (scenario.status === "passed"
                ? "text-slate-text/55"
                : "text-slate-text/35")
            }
          >
            {scenario.status === "passed" ? "pass" : "pending"}
          </span>
        )}
      </div>
      {isCrit && scenario.hint && (
        <p className="pl-[26px] text-[11.5px] leading-[1.55] text-amber-glow/75">
          <span className="text-amber-glow">&gt;</span>{" "}
          <span className="text-slate-text/80">{scenario.hint}</span>
        </p>
      )}
    </li>
  );
}

function StatusGlyph({ status }: { status: Scenario["status"] }) {
  if (status === "flagged") {
    return (
      <span className="mt-0.5 inline-flex h-3.5 w-3.5 items-center justify-center text-amber-glow">
        <svg
          viewBox="0 0 12 12"
          className="h-3 w-3"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.6"
        >
          <path d="M6 1.5 L10.5 10.5 L1.5 10.5 Z" />
          <path d="M6 5v2.4" strokeLinecap="round" />
          <circle cx="6" cy="9" r="0.4" fill="currentColor" />
        </svg>
      </span>
    );
  }
  if (status === "passed") {
    return (
      <span className="mt-0.5 inline-flex h-3.5 w-3.5 items-center justify-center text-amber-glow/80">
        <svg
          viewBox="0 0 12 12"
          className="h-3 w-3"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.8"
          strokeLinecap="round"
          strokeLinejoin="round"
        >
          <path d="M2 6.5 L5 9 L10 3" />
        </svg>
      </span>
    );
  }
  return (
    <span className="mt-0.5 inline-flex h-3.5 w-3.5 items-center justify-center text-slate-text/35">
      <span className="h-1.5 w-1.5 rounded-full border border-current" />
    </span>
  );
}

function LivePulse() {
  return (
    <span className="relative inline-flex h-1.5 w-1.5">
      <span
        className="sim-live-ping absolute inset-0 rounded-full bg-amber-glow/60"
        style={{ animation: "simLivePulse 2s cubic-bezier(0.23,1,0.32,1) infinite" }}
      />
      <span className="relative inline-block h-1.5 w-1.5 rounded-full bg-amber-glow shadow-[0_0_8px_currentColor]" />
    </span>
  );
}

/* ─────────────────────────────────────────────────────────────── */
/* Edge-surface gauge — telemetry readout                          */
/* ─────────────────────────────────────────────────────────────── */

/**
 * A concentric-ring gauge that reads like a telemetry instrument rather
 * than a loading spinner. Built out of (outer → inner):
 *   1. a dashed tick crown (24 ticks, every 6th emphasized),
 *   2. a soft outer halo ring,
 *   3. the primary progress arc (amber, 87% coverage, rounded cap),
 *   4. a slowly rotating orbit marker riding that arc,
 *   5. an inner etched ring for instrument depth,
 *   6. a radial-gradient core that holds the "LIVE" readout.
 * The orbit + glow pulses pause when the section leaves the viewport.
 */
function EdgeSurfaceGauge() {
  const { ref, inView } = useInView<HTMLDivElement>();

  const size = 200;
  const stroke = 8;
  const r = (size - stroke) / 2 - 6;
  const circumference = 2 * Math.PI * r;
  const coverage = 0.87;
  const dash = circumference * coverage;
  const gap = circumference - dash;

  // orbit marker sits at the leading edge of the arc
  const orbitAngleDeg = -90 + coverage * 360;
  const orbitRad = (orbitAngleDeg * Math.PI) / 180;
  const orbitX = size / 2 + r * Math.cos(orbitRad);
  const orbitY = size / 2 + r * Math.sin(orbitRad);

  // 24 tick crown sitting just outside the main arc
  const ticks = Array.from({ length: 24 }, (_, i) => i);
  const tickR1 = r + 10;
  const tickR2 = r + 14;
  const tickR2Major = r + 17;

  return (
    <div
      ref={ref}
      data-in-view={inView ? "true" : "false"}
      className="relative flex h-full flex-col overflow-hidden border border-iron/80 bg-charcoal/70 p-7"
      style={{
        backgroundImage:
          "radial-gradient(120% 80% at 50% 0%, color-mix(in oklch, var(--color-amber-glow) 5%, transparent) 0%, transparent 60%), linear-gradient(180deg, color-mix(in oklch, var(--color-charcoal) 90%, black) 0%, var(--color-charcoal) 100%)",
      }}
    >
      <div className="flex items-start justify-between">
        <div>
          <div className="font-mono text-[10.5px] uppercase tracking-[0.22em] text-amber-glow/80">
            Scenario coverage
          </div>
          <h3 className="mt-2 font-mono text-[22px] font-bold tracking-[-0.01em] text-foreground">
            Edge surface
          </h3>
        </div>
        <button
          type="button"
          aria-label="Expand edge surface"
          className="flex h-8 w-8 items-center justify-center border border-iron/70 text-amber-glow/80 transition-colors hover:border-amber-glow/50 hover:text-amber-glow"
        >
          <svg viewBox="0 0 14 14" className="h-3.5 w-3.5" fill="none" stroke="currentColor" strokeWidth="1.4">
            <path d="M4 10 L10 4 M6 4 H10 V8" strokeLinecap="square" />
          </svg>
        </button>
      </div>

      {/* gauge */}
      <div className="relative mx-auto my-5 flex items-center justify-center">
        <svg
          width={size}
          height={size}
          viewBox={`0 0 ${size} ${size}`}
          className="overflow-visible"
          aria-hidden
        >
          <defs>
            <radialGradient id="sim-core" cx="50%" cy="50%" r="50%">
              <stop
                offset="0%"
                stopColor="var(--color-amber-glow)"
                stopOpacity="0.28"
              />
              <stop
                offset="55%"
                stopColor="var(--color-amber-glow)"
                stopOpacity="0.06"
              />
              <stop offset="100%" stopColor="transparent" stopOpacity="0" />
            </radialGradient>
            <filter id="sim-soft-glow" x="-50%" y="-50%" width="200%" height="200%">
              <feGaussianBlur stdDeviation="2.2" />
            </filter>
          </defs>

          {/* 1. tick crown */}
          <g>
            {ticks.map((i) => {
              const angle = (i / ticks.length) * 2 * Math.PI - Math.PI / 2;
              const major = i % 6 === 0;
              const outer = major ? tickR2Major : tickR2;
              const x1 = size / 2 + tickR1 * Math.cos(angle);
              const y1 = size / 2 + tickR1 * Math.sin(angle);
              const x2 = size / 2 + outer * Math.cos(angle);
              const y2 = size / 2 + outer * Math.sin(angle);
              return (
                <line
                  key={i}
                  x1={x1}
                  y1={y1}
                  x2={x2}
                  y2={y2}
                  stroke={
                    major
                      ? "color-mix(in oklch, var(--color-amber-glow) 75%, transparent)"
                      : "color-mix(in oklch, var(--color-iron) 80%, transparent)"
                  }
                  strokeWidth={major ? 1.2 : 0.8}
                  strokeLinecap="square"
                />
              );
            })}
          </g>

          {/* 2. radial core */}
          <circle cx={size / 2} cy={size / 2} r={r - 6} fill="url(#sim-core)" />

          {/* 3. base track */}
          <circle
            cx={size / 2}
            cy={size / 2}
            r={r}
            fill="none"
            stroke="color-mix(in oklch, var(--color-iron) 55%, transparent)"
            strokeWidth={stroke}
          />

          {/* 4. amber progress arc */}
          <circle
            cx={size / 2}
            cy={size / 2}
            r={r}
            fill="none"
            stroke="var(--color-amber-glow)"
            strokeWidth={stroke}
            strokeLinecap="round"
            strokeDasharray={`${dash} ${gap}`}
            transform={`rotate(-90 ${size / 2} ${size / 2})`}
            style={{
              filter:
                "drop-shadow(0 0 10px color-mix(in oklch, var(--color-amber-glow) 55%, transparent))",
            }}
          />

          {/* 5. soft echo ring — pulses with gauge glow */}
          <circle
            className="sim-gauge-glow"
            cx={size / 2}
            cy={size / 2}
            r={r}
            fill="none"
            stroke="color-mix(in oklch, var(--color-amber-glow) 32%, transparent)"
            strokeWidth={1}
            strokeDasharray={`${dash} ${gap}`}
            transform={`rotate(-90 ${size / 2} ${size / 2})`}
            filter="url(#sim-soft-glow)"
            style={{
              animation:
                "simGaugeGlow 2s cubic-bezier(0.23,1,0.32,1) infinite",
            }}
          />

          {/* 6. inner etched ring */}
          <circle
            cx={size / 2}
            cy={size / 2}
            r={r - 12}
            fill="none"
            stroke="color-mix(in oklch, var(--color-iron) 40%, transparent)"
            strokeWidth={0.75}
          />

          {/* 7. orbit marker — a tiny dot rotating around the ring center */}
          <g
            className="sim-gauge-orbit"
            style={{
              transformOrigin: `${size / 2}px ${size / 2}px`,
              animation:
                "simGaugeOrbit 9s cubic-bezier(0.77,0,0.175,1) infinite",
            }}
          >
            <circle
              cx={orbitX}
              cy={orbitY}
              r={3}
              fill="var(--color-amber-glow)"
              style={{
                filter:
                  "drop-shadow(0 0 6px color-mix(in oklch, var(--color-amber-glow) 90%, transparent))",
              }}
            />
          </g>

          {/* 8. coverage readout — tiny number next to the arc head */}
          <text
            x={size / 2}
            y={stroke + 6}
            textAnchor="middle"
            className="font-mono"
            fontSize="9"
            letterSpacing="2"
            fill="color-mix(in oklch, var(--color-amber-glow) 80%, transparent)"
          >
            87%
          </text>
        </svg>

        {/* center label — telemetry readout */}
        <div className="pointer-events-none absolute inset-0 flex flex-col items-center justify-center">
          <span className="font-mono text-[10px] uppercase tracking-[0.32em] text-amber-glow/70">
            T+0.42s
          </span>
          <span className="mt-0.5 font-mono text-[26px] font-bold tracking-[0.22em] text-foreground">
            LIVE
          </span>
          <span className="mt-0.5 font-mono text-[9px] uppercase tracking-[0.28em] text-slate-text/55">
            sim &middot; 87/100
          </span>
        </div>
      </div>

      {/* metrics */}
      <div className="mt-auto grid grid-cols-3 gap-3 border-t border-iron/60 pt-4">
        <Metric label="runs" value="87" hint="edge cases" />
        <Metric label="flagged" value="2" hint="push diff" accent />
        <Metric label="line" value="143" hint="suggested fix" />
      </div>
    </div>
  );
}

function Metric({
  label,
  value,
  hint,
  accent,
}: {
  label: string;
  value: string;
  hint: string;
  accent?: boolean;
}) {
  return (
    <div className="flex flex-col gap-1">
      <span
        className={
          "font-mono text-[13px] font-semibold tabular-nums " +
          (accent ? "text-amber-glow" : "text-foreground")
        }
      >
        {value}{" "}
        <span className="font-normal text-slate-text/70">{label}</span>
      </span>
      <span className="font-mono text-[10px] uppercase tracking-[0.22em] text-slate-text/45">
        {hint}
      </span>
    </div>
  );
}

/* ─────────────────────────────────────────────────────────────── */
/* How it works — editorial numbered columns                       */
/* ─────────────────────────────────────────────────────────────── */

type Step = {
  n: "01" | "02" | "03";
  kicker: string;
  title: string;
  body: string;
  meta: string;
};

const STEPS: Step[] = [
  {
    n: "01",
    kicker: "Parse",
    title: "Read the diff",
    body: "AST-level analysis of every changed line, its callers, and its blast radius across the repo.",
    meta: "tree-sitter · ~120ms",
  },
  {
    n: "02",
    kicker: "Simulate",
    title: "Generate scenarios",
    body: "Stress tests synthesized from your diff — null inputs, race conditions, adoption edge cases.",
    meta: "≤ 40 scenarios / diff",
  },
  {
    n: "03",
    kicker: "Post",
    title: "Report to the PR",
    body: "One structured review comment: noisy paths, memory matches, and the exact lines to re-check.",
    meta: "GitHub · GitLab",
  },
];

function HowItWorks() {
  const { ref, inView } = useInView<HTMLDivElement>();
  return (
    <div
      ref={ref}
      data-in-view={inView ? "true" : "false"}
      className="mt-10 border border-iron/70 bg-charcoal/40 px-8 py-9"
    >
      <div className="flex flex-col gap-2 border-b border-iron/60 pb-7 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <div className="font-mono text-[10.5px] uppercase tracking-[0.22em] text-amber-glow/80">
            How it works
          </div>
          <h3 className="mt-2 font-mono text-[22px] font-bold tracking-[-0.01em] text-foreground">
            From diff to verdict in seconds
          </h3>
        </div>
        <span className="inline-flex items-center gap-2 self-start border border-iron/70 px-3 py-1 font-mono text-[10px] uppercase tracking-[0.22em] text-slate-text/70 sm:self-end">
          <span className="inline-block h-1.5 w-1.5 rounded-full bg-amber-glow/80" />
          Real-time simulation
        </span>
      </div>

      <ol className="grid grid-cols-1 md:grid-cols-3">
        {STEPS.map((s, i) => (
          <StepColumn key={s.n} step={s} index={i} last={i === STEPS.length - 1} />
        ))}
      </ol>
    </div>
  );
}

/**
 * Editorial step column: giant mono numeral, tiny kicker, asymmetric title
 * weight per index (step 02 reads heaviest), vertical iron rule divider
 * between columns. Each card animates in with an 80ms stagger when the
 * surrounding HowItWorks enters the viewport.
 */
function StepColumn({
  step,
  index,
  last,
}: {
  step: Step;
  index: number;
  last: boolean;
}) {
  // Deliberate rhythm: 02 is the heaviest. Breaks "three identical tiles".
  const titleSize =
    index === 1
      ? "text-[22px] leading-[1.15] tracking-[-0.01em]"
      : "text-[18px] leading-[1.2] tracking-[-0.005em]";
  const titleWeight = index === 1 ? "font-bold" : "font-semibold";
  const numeralColor =
    index === 1 ? "text-amber-glow" : "text-amber-glow/55";
  const numeralSize =
    index === 1
      ? "text-[64px] leading-[0.85]"
      : "text-[56px] leading-[0.9]";

  return (
    <li
      data-step={step.n}
      className={
        "sim-card relative flex flex-col gap-5 py-8 md:py-7 " +
        (index === 0 ? "md:pl-0 md:pr-8" : "") +
        (index === 1 ? "md:px-8" : "") +
        (index === 2 ? "md:pl-8 md:pr-0" : "") +
        (!last
          ? " border-b border-iron/45 md:border-b-0 md:border-r md:border-r-iron/45"
          : "")
      }
    >
      {/* kicker + rule */}
      <div className="flex items-center gap-3">
        <span className="font-mono text-[10px] uppercase tracking-[0.3em] text-amber-glow/80">
          {step.kicker}
        </span>
        <span className="h-px flex-1 bg-iron/50" />
        <span className="font-mono text-[10px] uppercase tracking-[0.22em] text-slate-text/45">
          step {step.n}
        </span>
      </div>

      {/* numeral + title share a baseline grid */}
      <div className="flex items-start gap-5">
        <span
          className={`font-mono font-bold tabular-nums ${numeralSize} ${numeralColor}`}
          style={{
            textShadow:
              index === 1
                ? "0 0 32px color-mix(in oklch, var(--color-amber-glow) 35%, transparent)"
                : undefined,
          }}
        >
          {step.n}
        </span>
        <div className="flex min-w-0 flex-1 flex-col gap-2 pt-2">
          <h4
            className={`font-mono ${titleWeight} ${titleSize} text-foreground`}
          >
            {step.title}
          </h4>
          <p className="max-w-[32ch] font-sans text-[13.5px] leading-[1.6] text-slate-text/85">
            {step.body}
          </p>
        </div>
      </div>

      {/* footer meta */}
      <div className="mt-auto flex items-center gap-2 pt-3 font-mono text-[10px] uppercase tracking-[0.22em] text-slate-text/50">
        <span
          aria-hidden
          className="inline-block h-[1px] w-5 bg-amber-glow/50"
        />
        <span>{step.meta}</span>
      </div>
    </li>
  );
}
