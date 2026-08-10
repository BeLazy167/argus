"use client";

import { useMemo, useState } from "react";
import { QueryProvider } from "@/providers/query-provider";
import {
  useOpenRouterCatalog,
  useOpenRouterEndpoints,
} from "@/lib/queries/openrouter-public";

/**
 * ByokEstimator carries its own QueryProvider.
 *
 * QueryProvider is mounted in the dashboard layout only, and this renders on
 * the marketing home page, which Next prerenders — so the query hooks inside
 * threw "No QueryClient set" during static export and failed the build.
 *
 * Scoped here rather than added to the marketing layout: a provider on every
 * marketing page would ship a QueryClient to visitors of pages that never
 * query anything, to serve one widget. A separate cache is the right shape
 * too — nothing on these pages shares state with the dashboard.
 */
export function ByokEstimator() {
  return (
    <QueryProvider>
      <ByokEstimatorInner />
    </QueryProvider>
  );
}

/**
 * Interactive BYOK cost estimator backed by live OpenRouter data. Search the
 * real model catalog, pick a model, then pick among the providers that serve it
 * (OpenRouter aggregates the same model across many providers, each with its own
 * price), and the estimated monthly bill updates live. It makes the section's
 * promise — "your LLM, your cost, full transparency" — tangible: the number is
 * what your own provider charges, with no Argus markup.
 */

type Model = { id: string; name: string; prompt: number; completion: number };
type Provider = {
  provider: string;
  prompt: number;
  completion: number;
  quantization: string | null;
};

// Representative per-review token profile. Code review is input-heavy: a lot of
// diff + context in, concise findings out.
const INPUT_TOKENS = 12_000;
const OUTPUT_TOKENS = 2_000;

// Preferred defaults, first match wins; falls back to the first model.
const DEFAULT_PREFS = [
  "anthropic/claude-sonnet",
  "openai/gpt-4o",
  "meta-llama/llama-3.3-70b",
  "google/gemini",
];

// Static seed so the estimator always renders a real estimate even before (or
// if) the live OpenRouter fetch resolves. Replaced by the live catalog on load.
// Prices are USD per token, approximate.
const FALLBACK_MODELS: Model[] = [
  { id: "anthropic/claude-sonnet-4.5", name: "Anthropic: Claude Sonnet 4.5", prompt: 0.000003, completion: 0.000015 },
  { id: "openai/gpt-4o", name: "OpenAI: GPT-4o", prompt: 0.0000025, completion: 0.00001 },
  { id: "meta-llama/llama-3.3-70b-instruct", name: "Meta: Llama 3.3 70B Instruct", prompt: 0.0000001, completion: 0.00000032 },
  { id: "google/gemini-2.5-flash", name: "Google: Gemini 2.5 Flash", prompt: 0.0000003, completion: 0.0000025 },
];

/** USD per 1M tokens, for display. */
function perMillion(x: number): string {
  return `$${(x * 1_000_000).toFixed(2)}`;
}

function reviewCost(prompt: number, completion: number): number {
  return INPUT_TOKENS * prompt + OUTPUT_TOKENS * completion;
}

function ByokEstimatorInner() {
  const [query, setQuery] = useState("");
  const [open, setOpen] = useState(false);
  const [providerIdx, setProviderIdx] = useState(0);
  const [reviews, setReviews] = useState(200);
  // Null means "no explicit choice yet", so the default follows the catalogue
  // once it loads. Storing the resolved id instead would need an effect to
  // re-sync when the live list replaces the fallback.
  const [picked, setPicked] = useState<string | null>(null);

  const catalog = useOpenRouterCatalog();
  // Keep the static fallback when the live list is empty (outage), so the
  // estimator never degrades to a blank or $0 state.
  const isLive = (catalog.data?.length ?? 0) > 0;
  const models: Model[] = isLive ? (catalog.data as Model[]) : FALLBACK_MODELS;

  const modelId =
    picked ??
    DEFAULT_PREFS.map((p) => models.find((m) => m.id.startsWith(p))).find(Boolean)?.id ??
    models[0]?.id ??
    "";

  const endpoints = useOpenRouterEndpoints({ variables: { modelId }, enabled: modelId !== "" });
  const loadingProviders = endpoints.isPending && modelId !== "";
  // Sort by the same input-weighted cost the estimate displays, so index 0
  // (badged "cheapest" and selected by default) is truly cheapest here — not
  // merely the lowest unweighted prompt+completion sum.
  //
  // The query is keyed by model, so switching models yields undefined here
  // rather than the previous model's providers: the readout falls back to the
  // new model's own default price instead of showing a stale one under it.
  const providers: Provider[] = useMemo(() => {
    const raw = (endpoints.data ?? []) as Provider[];
    return [...raw].sort(
      (a, b) => reviewCost(a.prompt, a.completion) - reviewCost(b.prompt, b.completion),
    );
  }, [endpoints.data]);

  const selectedModel = models.find((m) => m.id === modelId);
  const provider = providers[providerIdx];

  const matches = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (q.length < 2) return [];
    return models
      .filter((m) => `${m.name} ${m.id}`.toLowerCase().includes(q))
      .slice(0, 8);
  }, [query, models]);

  // Prefer the picked provider's price; fall back to the model's default price
  // while endpoints load, so the number is never blank.
  const perReview = provider
    ? reviewCost(provider.prompt, provider.completion)
    : selectedModel
      ? reviewCost(selectedModel.prompt, selectedModel.completion)
      : 0;
  const monthly = perReview * reviews;

  function choose(m: Model) {
    setPicked(m.id);
    setProviderIdx(0);
    setQuery("");
    setOpen(false);
  }

  return (
    <div className="relative flex flex-col gap-5 border border-iron/70 bg-charcoal/40 p-5 sm:p-6">
      <span aria-hidden className="pointer-events-none absolute -left-px -top-px h-2.5 w-2.5 border-l border-t border-amber-glow/60" />
      <span aria-hidden className="pointer-events-none absolute -right-px -top-px h-2.5 w-2.5 border-r border-t border-amber-glow/60" />
      <span aria-hidden className="pointer-events-none absolute -bottom-px -left-px h-2.5 w-2.5 border-b border-l border-amber-glow/60" />
      <span aria-hidden className="pointer-events-none absolute -bottom-px -right-px h-2.5 w-2.5 border-b border-r border-amber-glow/60" />

      <div className="flex items-center justify-between gap-3">
        <span className="font-mono text-[11px] uppercase tracking-[0.22em] text-amber-glow/80">
          Estimate your bill
        </span>
        <span className="inline-flex items-center gap-1.5 font-mono text-[9.5px] uppercase tracking-[0.16em] text-slate-text/70">
          {isLive ? (
            <span aria-hidden className="relative flex h-1.5 w-1.5">
              <span className="absolute inset-0 rounded-full bg-amber-glow opacity-70" />
              <span className="relative h-1.5 w-1.5 rounded-full bg-amber-glow" />
            </span>
          ) : null}
          {isLive
            ? "live · OpenRouter · no markup"
            : "OpenRouter pricing · no markup"}
        </span>
      </div>

      {/* Model search */}
      <div className="relative">
        <label
          htmlFor="byok-model-search"
          className="mb-1.5 block font-mono text-[10px] uppercase tracking-[0.2em] text-slate-text/70"
        >
          Model {isLive ? `(${models.length} live)` : `(${models.length})`}
        </label>
        <input
          id="byok-model-search"
          type="text"
          value={open ? query : (selectedModel?.name ?? query)}
          onChange={(e) => {
            setQuery(e.target.value);
            setOpen(true);
          }}
          onFocus={() => {
            setQuery("");
            setOpen(true);
          }}
          onBlur={() => window.setTimeout(() => setOpen(false), 120)}
          placeholder="Search models..."
          className="w-full border border-iron bg-void/50 px-3 py-2 font-mono text-[13px] text-foreground placeholder:text-slate-text/50 focus:border-amber/60 focus:outline-none"
        />
        {open && matches.length > 0 ? (
          <ul className="absolute left-0 right-0 top-full z-20 mt-1 max-h-64 overflow-y-auto border border-iron bg-void shadow-[0_12px_40px_-12px_rgba(0,0,0,0.8)]">
            {matches.map((m) => (
              <li key={m.id}>
                <button
                  type="button"
                  onMouseDown={(e) => {
                    e.preventDefault();
                    choose(m);
                  }}
                  className="flex w-full items-baseline justify-between gap-3 px-3 py-2 text-left font-mono hover:bg-amber/10"
                >
                  <span className="truncate text-[12px] text-foreground">{m.name}</span>
                  <span className="shrink-0 text-[10px] tabular-nums text-slate-text/70">
                    {perMillion(m.prompt)}
                    <span className="text-slate-text/40"> in</span>
                  </span>
                </button>
              </li>
            ))}
          </ul>
        ) : null}
      </div>

      {/* Provider picker */}
      <div>
        <div className="mb-1.5 flex items-baseline justify-between">
          <span className="font-mono text-[10px] uppercase tracking-[0.2em] text-slate-text/70">
            Provider
          </span>
          <span className="font-mono text-[9px] uppercase tracking-[0.16em] text-slate-text/50">
            USD / 1M tok {"·"} in {"·"} out
          </span>
        </div>
        <div className="max-h-40 overflow-y-auto border border-iron/70">
          {loadingProviders ? (
            <div className="px-3 py-3 font-mono text-[11px] text-slate-text/60">
              Loading providers...
            </div>
          ) : providers.length === 0 ? (
            <div className="px-3 py-3 font-mono text-[11px] text-slate-text/60">
              No provider pricing for this model.
            </div>
          ) : (
            providers.map((p, i) => {
              const active = i === providerIdx;
              return (
                <button
                  key={`${p.provider}-${i}`}
                  type="button"
                  onClick={() => setProviderIdx(i)}
                  aria-pressed={active}
                  className={`flex w-full items-center justify-between gap-3 border-b border-iron/40 px-3 py-2 text-left transition-colors last:border-b-0 ${
                    active ? "bg-amber/10" : "hover:bg-iron/10"
                  }`}
                >
                  <span className="flex min-w-0 items-center gap-2">
                    <span
                      aria-hidden
                      className={`h-1.5 w-1.5 shrink-0 rounded-full ${active ? "bg-amber" : "bg-iron"}`}
                    />
                    <span
                      className={`truncate font-mono text-[12px] ${active ? "text-foreground" : "text-foreground/80"}`}
                    >
                      {p.provider}
                    </span>
                    {i === 0 ? (
                      <span className="shrink-0 border border-emerald-400/40 px-1 py-px font-mono text-[8.5px] uppercase tracking-[0.12em] text-emerald-400/90">
                        cheapest
                      </span>
                    ) : null}
                  </span>
                  <span className="shrink-0 font-mono text-[11px] tabular-nums text-slate-text/80">
                    {perMillion(p.prompt)}{" "}
                    <span className="text-slate-text/40">{"·"}</span>{" "}
                    {perMillion(p.completion)}
                  </span>
                </button>
              );
            })
          )}
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
          <div>~${perReview.toFixed(4)} / review</div>
          <div className="text-slate-text/60">Argus: $0&ndash;$19/mo flat</div>
        </div>
      </div>

      <p className="font-mono text-[9.5px] leading-relaxed text-slate-text/70">
        Live pricing from OpenRouter, estimated at ~14k tokens/review (mostly
        input). Pick any of the providers serving a model to compare. You pay
        your provider directly &mdash; Argus never marks it up.
      </p>
    </div>
  );
}
