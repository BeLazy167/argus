"use client";

import { useState } from "react";

/**
 * Interactive BYOK cost estimator. Pick the model Argus routes deep analysis to
 * and drag the monthly review volume; the estimated bill updates live. It makes
 * the section's promise — "your LLM, your cost, full transparency" — tangible:
 * the number shown is what your own provider would charge, with no Argus markup.
 */

type Model = {
  id: string;
  name: string;
  /** USD per 1M input tokens */
  input: number;
  /** USD per 1M output tokens */
  output: number;
  hint: string;
};

const MODELS: Model[] = [
  { id: "haiku", name: "Haiku 4.5", input: 0.8, output: 4, hint: "fast · cheap" },
  { id: "sonnet", name: "Sonnet 4.5", input: 3, output: 15, hint: "balanced" },
  { id: "opus", name: "Opus 4.1", input: 15, output: 75, hint: "deep analysis" },
];

// Representative per-review token profile. Code review is input-heavy: a lot of
// diff + context goes in, concise findings come out.
const INPUT_TOKENS = 12_000;
const OUTPUT_TOKENS = 2_000;

function costPerReview(m: Model): number {
  return (INPUT_TOKENS * m.input + OUTPUT_TOKENS * m.output) / 1_000_000;
}

export function ByokEstimator() {
  const [modelId, setModelId] = useState<string>("sonnet");
  const [reviews, setReviews] = useState<number>(200);

  const model = MODELS.find((m) => m.id === modelId) ?? MODELS[1]!;
  const perReview = costPerReview(model);
  const monthly = perReview * reviews;

  return (
    <div className="relative flex flex-col gap-6 border border-iron/70 bg-charcoal/40 p-5 sm:p-6">
      {/* corner ticks — matches the section's viewfinder motif */}
      <span aria-hidden className="pointer-events-none absolute -left-px -top-px h-2.5 w-2.5 border-l border-t border-amber-glow/60" />
      <span aria-hidden className="pointer-events-none absolute -right-px -top-px h-2.5 w-2.5 border-r border-t border-amber-glow/60" />
      <span aria-hidden className="pointer-events-none absolute -bottom-px -left-px h-2.5 w-2.5 border-b border-l border-amber-glow/60" />
      <span aria-hidden className="pointer-events-none absolute -bottom-px -right-px h-2.5 w-2.5 border-b border-r border-amber-glow/60" />

      <div className="flex items-center justify-between gap-3">
        <span className="font-mono text-[11px] uppercase tracking-[0.22em] text-amber-glow/80">
          Estimate your bill
        </span>
        <span className="font-mono text-[10px] uppercase tracking-[0.18em] text-slate-text/70">
          your key &middot; no markup
        </span>
      </div>

      {/* Model picker */}
      <div>
        <div className="mb-2 font-mono text-[10px] uppercase tracking-[0.2em] text-slate-text/70">
          Deep-analysis model
        </div>
        <div className="grid grid-cols-3 gap-2">
          {MODELS.map((m) => {
            const active = m.id === modelId;
            return (
              <button
                key={m.id}
                type="button"
                onClick={() => setModelId(m.id)}
                aria-pressed={active}
                className={`flex flex-col items-start gap-0.5 border px-2.5 py-2 text-left transition-colors duration-150 ${
                  active
                    ? "border-amber bg-amber/10"
                    : "border-iron bg-void/40 hover:border-amber/40"
                }`}
              >
                <span
                  className={`font-mono text-[11px] font-bold ${active ? "text-amber" : "text-foreground"}`}
                >
                  {m.name}
                </span>
                <span className="font-mono text-[9px] text-slate-text">{m.hint}</span>
              </button>
            );
          })}
        </div>
      </div>

      {/* Volume slider */}
      <div>
        <div className="mb-2 flex items-baseline justify-between font-mono">
          <span className="text-[10px] uppercase tracking-[0.2em] text-slate-text/70">
            Reviews / month
          </span>
          <span className="text-[13px] font-bold tabular-nums text-foreground">
            {reviews.toLocaleString()}
          </span>
        </div>
        <input
          type="range"
          min={10}
          max={1000}
          step={10}
          value={reviews}
          onChange={(e) => setReviews(Number(e.target.value))}
          aria-label="Reviews per month"
          className="h-1 w-full cursor-pointer appearance-none rounded-full bg-iron accent-amber"
        />
      </div>

      {/* Live readout */}
      <div className="flex items-end justify-between border-t border-iron/60 pt-4">
        <div>
          <div className="font-mono text-[10px] uppercase tracking-[0.18em] text-slate-text/70">
            LLM cost, on your key
          </div>
          <div className="font-mono text-[28px] font-bold leading-none tabular-nums text-amber">
            ${monthly.toFixed(2)}
            <span className="ml-1 text-[12px] font-normal text-slate-text">/mo</span>
          </div>
        </div>
        <div className="text-right font-mono text-[10px] tabular-nums leading-relaxed text-slate-text">
          <div>~${perReview.toFixed(3)} / review</div>
          <div className="text-slate-text/60">Argus: $0&ndash;$19/mo flat</div>
        </div>
      </div>

      <p className="font-mono text-[9.5px] leading-relaxed text-slate-text/70">
        Estimate at ~14k tokens/review (mostly input). Route Haiku for triage and
        Opus for deep analysis to tune this &mdash; you pay your provider directly, and
        Argus never marks it up.
      </p>
    </div>
  );
}
