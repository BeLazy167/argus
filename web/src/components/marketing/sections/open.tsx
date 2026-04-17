import Link from "next/link";
import { FadeIn } from "@/components/marketing/fade-in";
import {
  argusFeatures,
  competitors,
  type Competitor,
} from "@/lib/pseo/competitors";

type FeatureValue = "yes" | "no" | "partial";

/**
 * Map the raw competitor feature schema onto the five comparison columns the
 * design calls for. The marketing surface shows a distilled matrix — not every
 * feature flag — so this layer resolves the display state per row.
 */
function getRowValues(features: Competitor["features"]): {
  memory: FeatureValue;
  failureSim: FeatureValue;
  byok: FeatureValue;
  transparency: FeatureValue;
} {
  return {
    memory: features.memory ? "yes" : "no",
    failureSim: features.codeSimulation ? "yes" : "no",
    byok: features.byok ? "yes" : features.selfHosted ? "partial" : "no",
    transparency: features.selfHosted
      ? "yes"
      : features.byok
        ? "partial"
        : "no",
  };
}

const ARGUS_PRICE = "$19";
const ARGUS_PRICE_UNIT = "/mo";

const COMPETITOR_PRICES: Record<string, { value: string; unit: string }> = {
  coderabbit: { value: "$24", unit: "/dev" },
  greptile: { value: "$30", unit: "/dev" },
  cubic: { value: "$30", unit: "/dev" },
  sourcery: { value: "$24", unit: "/dev" },
  qodo: { value: "$38", unit: "/dev" },
  semgrep: { value: "$40", unit: "/dev" },
  codacy: { value: "$25", unit: "/dev" },
  sonarqube: { value: "$32", unit: "/dev" },
  "github-copilot": { value: "$19", unit: "/dev" },
};

const DISPLAY_ORDER = [
  "coderabbit",
  "greptile",
  "qodo",
  "cubic",
  "semgrep",
  "codacy",
  "sourcery",
  "sonarqube",
  "github-copilot",
];

const COMPETITOR_MARKS: Record<string, string> = {
  coderabbit: "CR",
  greptile: "GR",
  cubic: "CU",
  sourcery: "SC",
  qodo: "QD",
  semgrep: "SG",
  codacy: "CD",
  sonarqube: "SQ",
  "github-copilot": "GC",
};

const COMPETITOR_DISPLAY_NAMES: Record<string, string> = {
  coderabbit: "CodeRabbit",
  greptile: "Greptile",
  cubic: "Cubic",
  sourcery: "Sourcery",
  qodo: "Qodo",
  semgrep: "Semgrep",
  codacy: "Codacy",
  sonarqube: "SonarQube",
  "github-copilot": "GitHub Copilot",
};

/**
 * Render the comparison-cell state. Amber check for present, iron dash for
 * missing, amber-at-opacity for partial — no secondary accent tokens.
 */
function Cell({ value }: { value: FeatureValue }) {
  if (value === "yes") {
    return (
      <span className="inline-flex h-4 w-4 items-center justify-center">
        <svg
          viewBox="0 0 16 16"
          className="h-[14px] w-[14px] text-amber"
          fill="none"
          stroke="currentColor"
          strokeWidth="2.5"
          strokeLinecap="square"
        >
          <path d="M3 8.5l3 3 7-7" />
        </svg>
      </span>
    );
  }
  if (value === "partial") {
    return (
      <span className="inline-flex items-center justify-center border border-amber/30 bg-amber/[0.06] px-1.5 py-px font-mono text-[9px] uppercase tracking-[0.14em] text-amber/80">
        partial
      </span>
    );
  }
  return (
    <span className="inline-flex h-4 w-4 items-center justify-center">
      <span className="block h-px w-2.5 bg-slate-text/35" />
    </span>
  );
}

const DIFF_LINES: {
  kind: "ctx" | "del" | "add";
  oldLine: number | null;
  newLine: number | null;
  text: string;
}[] = [
  { kind: "ctx", oldLine: 118, newLine: 118, text: "async function handleWebhook(event: Stripe.Event) {" },
  { kind: "ctx", oldLine: 119, newLine: 119, text: "  const handler = handlers[event.type];" },
  { kind: "del", oldLine: 120, newLine: null, text: "  await handler(event);" },
  { kind: "add", oldLine: null, newLine: 120, text: "  await retryWithBackoff(() => handler(event), {" },
  { kind: "add", oldLine: null, newLine: 121, text: "    retries: 8, factor: 2, minTimeout: 500," },
  { kind: "add", oldLine: null, newLine: 122, text: "  });" },
  { kind: "ctx", oldLine: 121, newLine: 123, text: "}" },
];

const SUGGESTIONS = [
  "Cap retries at 3 (match Stripe's native envelope)",
  "Failover to dead-letter queue",
  "Suppression window on repeat `evt_*` ids",
];

export function Open() {
  const highlighted = DISPLAY_ORDER.map((slug) =>
    competitors.find((c) => c.slug === slug),
  ).filter((c): c is Competitor => Boolean(c));

  const argusRowValues = {
    memory: argusFeatures.memory ? "yes" : "no",
    failureSim: argusFeatures.codeSimulation ? "yes" : "no",
    byok: argusFeatures.byok ? "yes" : "no",
    transparency: "yes",
  } as const;

  return (
    <section id="open" className="relative border-t border-iron/60 bg-background">
      <div className="mx-auto max-w-[1200px] px-6 py-28 sm:px-8">
        {/* Eyebrow + headline */}
        <FadeIn>
          <div className="mx-auto flex max-w-3xl flex-col items-center text-center">
            <div className="inline-flex items-center gap-2 border border-amber/25 bg-amber/[0.04] px-2.5 py-1">
              <span className="h-1 w-1 rounded-full bg-amber" />
              <span className="font-mono text-[10px] uppercase tracking-[0.2em] text-amber">
                Argus Opened
              </span>
            </div>
            <h2 className="mt-6 font-display text-4xl font-medium leading-[1.08] tracking-tight text-foreground sm:text-[44px]">
              Every reviewer makes trade-offs.
              <br />
              <span className="text-foreground">Here are ours in the open.</span>
            </h2>
            <p className="mt-5 max-w-xl font-mono text-[13px] leading-relaxed text-slate-text">
              We built Argus because the bots shipping today forget what broke
              yesterday. This isn&apos;t a benchmark — it&apos;s a feature matrix
              you can audit.
            </p>
          </div>
        </FadeIn>

        {/* PR mock */}
        <FadeIn delay={80} className="mt-16">
          <div className="mx-auto max-w-5xl">
            <div className="border border-iron bg-charcoal/60 shadow-[0_1px_0_rgba(255,255,255,0.02)_inset,0_40px_80px_-40px_rgba(0,0,0,0.6)]">
              {/* Browser chrome */}
              <div className="flex items-center gap-3 border-b border-iron bg-void/60 px-4 py-3">
                <div className="flex gap-1.5">
                  <span className="h-2.5 w-2.5 rounded-full bg-iron" />
                  <span className="h-2.5 w-2.5 rounded-full bg-iron" />
                  <span className="h-2.5 w-2.5 rounded-full bg-iron" />
                </div>
                <div className="flex flex-1 items-center justify-center gap-1.5 font-mono text-[11px] text-slate-text/70">
                  <svg viewBox="0 0 16 16" className="h-3 w-3 text-slate-text/50" fill="none" stroke="currentColor" strokeWidth="1.5">
                    <path d="M5.5 10.5a3 3 0 014.24 0L11 11.76a3 3 0 01-4.24 4.24l-.38-.38M10.5 5.5a3 3 0 00-4.24 0L5 6.76a3 3 0 004.24 4.24l.38-.38" strokeLinecap="square" />
                  </svg>
                  <span>github.com/northwest-co/app/pull/2347</span>
                </div>
                <div className="flex items-center gap-1 border border-iron/70 px-2 py-0.5 font-mono text-[10px] uppercase tracking-wider text-slate-text/80">
                  <span className="h-1 w-1 rounded-full bg-amber" />
                  Argus
                </div>
              </div>

              {/* PR title row */}
              <div className="border-b border-iron/70 bg-void/40 px-6 py-5">
                <div className="flex items-start gap-3">
                  <span className="mt-0.5 inline-flex items-center gap-1.5 border border-amber/30 bg-amber/[0.06] px-2 py-0.5 font-mono text-[10px] uppercase tracking-wider text-amber">
                    <span className="h-1 w-1 rounded-full bg-amber" />
                    Open
                  </span>
                  <div className="min-w-0 flex-1">
                    <h3 className="font-mono text-[15px] leading-snug text-foreground">
                      feat(checkout): retry failed Stripe webhooks with backoff
                    </h3>
                    <div className="mt-1.5 flex flex-wrap items-center gap-x-2 gap-y-1 font-mono text-[11px] text-slate-text/80">
                      <span className="text-ash">jkblanc</span>
                      <span className="text-iron">·</span>
                      <span>wants to merge 2 commits into</span>
                      <span className="text-ash/90">main</span>
                      <span className="text-iron">·</span>
                      <span>17m ago</span>
                      <span className="text-iron">·</span>
                      <span className="text-amber/80">unresolved</span>
                    </div>
                  </div>
                </div>
              </div>

              {/* File header */}
              <div className="flex items-center justify-between border-b border-iron/70 bg-charcoal/40 px-6 py-2">
                <div className="flex items-center gap-2.5 font-mono text-[11px]">
                  <svg viewBox="0 0 16 16" className="h-3.5 w-3.5 text-slate-text/60" fill="currentColor">
                    <path d="M2 1.75C2 .784 2.784 0 3.75 0h6.586c.464 0 .909.184 1.237.513l2.914 2.914c.329.328.513.773.513 1.237v9.586A1.75 1.75 0 0113.25 16h-9.5A1.75 1.75 0 012 14.25V1.75z" />
                  </svg>
                  <span className="text-ash">src/checkout/webhooks.ts</span>
                  <span className="inline-flex items-center gap-1.5 border border-iron/70 bg-void/60 px-1.5 py-0.5">
                    <span className="text-emerald-400/90">+4</span>
                    <span className="text-slate-text/30">·</span>
                    <span className="text-amber/90">−1</span>
                  </span>
                </div>
                <div className="flex items-center gap-3 font-mono text-[10px] uppercase tracking-wider text-slate-text/50">
                  <span>unified</span>
                  <span className="text-iron">·</span>
                  <span>viewed</span>
                </div>
              </div>

              {/* Hunk header — aligns with diff line-number gutters */}
              <div className="border-b border-iron/50 bg-void/40 py-1 font-mono text-[11px]">
                <div className="flex">
                  <span className="w-[88px] shrink-0 select-none border-r border-iron/40 px-2 text-right text-slate-text/30">
                    @@
                  </span>
                  <span className="px-3 text-slate-text/60">
                    <span className="text-amber/70">@@ -118,4 +118,7 @@</span>
                    <span className="pl-2 text-slate-text/50">async function handleWebhook</span>
                  </span>
                </div>
              </div>

              {/* Diff */}
              <div className="border-b border-iron/70 bg-void/70">
                {DIFF_LINES.map((l, i) => (
                  <div
                    key={`${l.kind}-${l.oldLine ?? l.newLine ?? i}`}
                    className={`flex font-mono text-[12px] leading-[1.75] ${
                      l.kind === "add"
                        ? "bg-emerald-500/[0.06]"
                        : l.kind === "del"
                          ? "bg-amber/[0.08]"
                          : ""
                    }`}
                  >
                    <span className="w-11 shrink-0 select-none px-2 text-right text-[10px] tabular-nums text-slate-text/35">
                      {l.oldLine ?? ""}
                    </span>
                    <span className="w-11 shrink-0 select-none border-r border-iron/40 px-2 text-right text-[10px] tabular-nums text-slate-text/35">
                      {l.newLine ?? ""}
                    </span>
                    <span
                      className={`w-5 shrink-0 select-none text-center text-[11px] ${
                        l.kind === "add"
                          ? "text-emerald-400/80"
                          : l.kind === "del"
                            ? "text-amber/80"
                            : "text-transparent"
                      }`}
                    >
                      {l.kind === "add" ? "+" : l.kind === "del" ? "−" : " "}
                    </span>
                    <code className="whitespace-pre px-2 text-ash/85">
                      {l.text}
                    </code>
                  </div>
                ))}
              </div>

              {/* Human reviewer comment — GitHub-style two-column */}
              <div className="border-b border-iron/70 bg-void/30 px-6 py-5">
                <div className="flex gap-3">
                  <span
                    aria-hidden
                    className="mt-0.5 inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-iron text-[10px] font-mono text-ash"
                  >
                    P
                  </span>
                  <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                      <span className="font-mono text-[12.5px] font-medium text-foreground">
                        pauluhn
                      </span>
                      <span className="font-mono text-[11px] text-slate-text/60">
                        reviewed
                      </span>
                      <span className="font-mono text-[11px] text-slate-text/60">
                        1 day ago
                      </span>
                      <span className="ml-auto inline-flex items-center gap-1.5 border border-iron/70 bg-charcoal/60 px-2 py-0.5 font-mono text-[10px] uppercase tracking-wider text-slate-text/70">
                        <svg viewBox="0 0 16 16" className="h-3 w-3 text-slate-text/70" fill="none" stroke="currentColor" strokeWidth="1.5">
                          <circle cx="8" cy="8" r="3.5" />
                        </svg>
                        requested changes
                      </span>
                    </div>
                    <p className="mt-2.5 font-mono text-[12.5px] leading-relaxed text-ash/85">
                      Backoff retries can saturate Stripe&apos;s webhook quota.
                      Combined with Stripe auto-retries, a single failing event
                      triggers up to 56 calls — one incident in this repo
                      (PR #1597). Gate the 503s upstream and prevent{" "}
                      <code className="bg-iron/40 px-1 text-ash">payment_intent.succeeded</code>{" "}
                      from re-entering linked subscriptions.
                    </p>
                  </div>
                </div>
              </div>

              {/* Argus bubble — distinct amber left rail + metadata chip */}
              <div className="relative bg-charcoal/40 px-6 py-5">
                <div className="absolute inset-y-0 left-0 w-[2px] bg-amber" />
                <div className="flex gap-3">
                  <span
                    aria-hidden
                    className="mt-0.5 inline-flex h-7 w-7 shrink-0 items-center justify-center border border-amber/40 bg-amber/[0.08]"
                  >
                    <svg viewBox="0 0 120 60" className="h-3 w-3 text-amber" fill="none">
                      <path
                        d="M10 30C10 30 30 8 60 8C90 8 110 30 110 30C110 30 90 52 60 52C30 52 10 30 10 30Z"
                        stroke="currentColor"
                        strokeWidth="10"
                      />
                      <circle cx="60" cy="30" r="10" fill="currentColor" />
                    </svg>
                  </span>
                  <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                      <span className="font-mono text-[12.5px] font-medium text-amber">
                        argus
                      </span>
                      <span className="inline-flex items-center gap-1 border border-amber/30 bg-amber/[0.06] px-1.5 py-px font-mono text-[10px] text-amber/90">
                        <svg viewBox="0 0 16 16" className="h-2.5 w-2.5" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="square">
                          <path d="M3 8.5l3 3 7-7" />
                        </svg>
                        pattern match · webhooks
                      </span>
                      <span className="font-mono text-[11px] text-slate-text/60">
                        commented
                      </span>
                      <span className="font-mono text-[11px] text-slate-text/60">
                        just now
                      </span>
                      <span className="ml-auto inline-flex items-center gap-1.5 border border-amber/40 bg-amber/[0.08] px-2 py-0.5 font-mono text-[10px] uppercase tracking-wider text-amber">
                        <span className="h-1 w-1 rounded-full bg-amber" />
                        blocker · memory-hit
                      </span>
                    </div>
                    <p className="mt-2.5 font-mono text-[12.5px] leading-relaxed text-ash/90">
                      Pattern matches a prior webhook-quota fix in this repo.
                      Suggesting the same guardrail to avoid re-shipping the
                      class of bug.
                    </p>

                    {/* Code snippet */}
                    <div className="mt-4 border border-iron/70 bg-void/70">
                      <div className="flex items-center justify-between border-b border-iron/60 px-3 py-1.5 font-mono text-[10px] uppercase tracking-wider text-slate-text/60">
                        <div className="flex items-center gap-1.5">
                          <span className="text-amber/70">suggested guardrail</span>
                          <span className="text-iron">·</span>
                          <span className="normal-case tracking-normal text-slate-text/70">
                            src/checkout/webhooks.ts
                          </span>
                        </div>
                        <div className="flex items-center gap-3 text-slate-text/50">
                          <span>apply</span>
                          <span>copy</span>
                          <span>diff</span>
                        </div>
                      </div>
                      <pre className="overflow-x-auto px-4 py-3 font-mono text-[11.5px] leading-[1.75] text-ash/80">
{`if (await seenRecently(event.id, "15m")) return 200;
await retryWithBackoff(() => handler(event), {
  retries: 3,
  factor: 2,
  onFail: (e) => deadLetter.push(event, e),
});`}
                      </pre>
                    </div>

                    {/* Suggestions */}
                    <ul className="mt-4 divide-y divide-iron/50 border border-iron/60">
                      {SUGGESTIONS.map((s, i) => (
                        <li
                          key={s}
                          className="flex items-center gap-3 bg-void/30 px-3 py-2 font-mono text-[11.5px] text-ash/80"
                        >
                          <span className="font-mono text-[10px] tabular-nums text-amber/80">
                            {String(i + 1).padStart(2, "0")}
                          </span>
                          <span className="flex-1">{s}</span>
                          <span className="font-mono text-[10px] uppercase tracking-wider text-slate-text/50">
                            accept
                          </span>
                        </li>
                      ))}
                    </ul>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </FadeIn>

        {/* Comparison table */}
        <FadeIn delay={120} className="mt-24">
          <div className="mx-auto max-w-5xl">
            <div className="flex items-end justify-between border-b border-iron pb-4">
              <div>
                <div className="inline-flex items-center gap-2">
                  <span className="h-1 w-1 rounded-full bg-amber" />
                  <span className="font-mono text-[10px] uppercase tracking-[0.2em] text-amber">
                    Comparison
                  </span>
                </div>
                <h3 className="mt-3 font-display text-3xl font-medium tracking-tight text-foreground">
                  Argus vs 9 alternatives
                </h3>
              </div>
              <Link
                href="/compare"
                className="group inline-flex items-center gap-2 font-mono text-[12px] text-slate-text hover:text-amber"
              >
                <span>See all 9 alternatives</span>
                <svg viewBox="0 0 16 16" className="h-3 w-3 transition-transform group-hover:translate-x-0.5" fill="none" stroke="currentColor" strokeWidth="1.5">
                  <path d="M3 8h10M9 4l4 4-4 4" strokeLinecap="square" />
                </svg>
              </Link>
            </div>

            <div className="mt-0 overflow-x-auto border-x border-b border-iron">
              <div className="min-w-[720px]">
              {/* Column header — Tool widest, feature columns narrow, price right-aligned */}
              <div className="grid grid-cols-[minmax(240px,2.4fr)_80px_96px_80px_120px_minmax(120px,1fr)] border-b border-iron bg-charcoal/50 font-mono text-[10px] uppercase tracking-[0.18em] text-slate-text/70">
                <div className="px-4 py-3">Tool</div>
                <div className="px-3 py-3 text-center">Memory</div>
                <div className="px-3 py-3 text-center">Failure sim</div>
                <div className="px-3 py-3 text-center">BYOK</div>
                <div className="px-3 py-3 text-center">Transparency</div>
                <div className="px-4 py-3 text-right">
                  <span>Price</span>
                  <span className="text-slate-text/40"> · </span>
                  <span className="normal-case tracking-normal text-slate-text/50">
                    20-dev team
                  </span>
                </div>
              </div>

              {/* Argus row (highlighted) */}
              <div className="relative grid grid-cols-[minmax(240px,2.4fr)_80px_96px_80px_120px_minmax(120px,1fr)] items-center border-b border-iron bg-amber/[0.05] font-mono text-[12.5px]">
                <div className="absolute inset-y-0 left-0 w-[3px] bg-amber" />
                <div className="flex items-center gap-3 px-4 py-4">
                  <span className="inline-flex h-7 w-7 shrink-0 items-center justify-center border border-amber/40 bg-amber/[0.08] font-mono text-[10px] text-amber">
                    A
                  </span>
                  <div className="flex min-w-0 flex-col">
                    <div className="flex items-center gap-2">
                      <span className="font-medium text-foreground">Argus</span>
                      <span className="inline-flex items-center border border-amber/40 bg-amber/[0.08] px-1.5 py-px text-[9px] uppercase tracking-[0.14em] text-amber">
                        You are here
                      </span>
                    </div>
                    <span className="truncate text-[11px] text-slate-text/70">
                      AI code reviewer with memory
                    </span>
                  </div>
                </div>
                <div className="px-3 py-4 text-center"><Cell value={argusRowValues.memory} /></div>
                <div className="px-3 py-4 text-center"><Cell value={argusRowValues.failureSim} /></div>
                <div className="px-3 py-4 text-center"><Cell value={argusRowValues.byok} /></div>
                <div className="px-3 py-4 text-center"><Cell value={argusRowValues.transparency} /></div>
                <div className="px-4 py-4 text-right tabular-nums">
                  <span className="font-medium text-amber">{ARGUS_PRICE}</span>
                  <span className="text-amber/60">{ARGUS_PRICE_UNIT}</span>
                </div>
              </div>

              {/* Competitor rows */}
              {highlighted.map((c, i) => {
                const v = getRowValues(c.features);
                const isLast = i === highlighted.length - 1;
                const price = COMPETITOR_PRICES[c.slug];
                return (
                  <div
                    key={c.slug}
                    className={`grid grid-cols-[minmax(240px,2.4fr)_80px_96px_80px_120px_minmax(120px,1fr)] items-center font-mono text-[12.5px] ${
                      isLast ? "" : "border-b border-iron/60"
                    } hover:bg-charcoal/40`}
                  >
                    <div className="flex items-center gap-3 px-4 py-4">
                      <span className="inline-flex h-7 w-7 shrink-0 items-center justify-center border border-iron bg-void/60 font-mono text-[10px] text-slate-text/70">
                        {COMPETITOR_MARKS[c.slug]}
                      </span>
                      <span className="truncate text-ash/90">
                        {COMPETITOR_DISPLAY_NAMES[c.slug] ?? c.name}
                      </span>
                    </div>
                    <div className="px-3 py-4 text-center"><Cell value={v.memory} /></div>
                    <div className="px-3 py-4 text-center"><Cell value={v.failureSim} /></div>
                    <div className="px-3 py-4 text-center"><Cell value={v.byok} /></div>
                    <div className="px-3 py-4 text-center"><Cell value={v.transparency} /></div>
                    <div className="px-4 py-4 text-right tabular-nums text-slate-text/80">
                      {price ? (
                        <>
                          <span className="text-ash/90">{price.value}</span>
                          <span className="text-slate-text/50">{price.unit}</span>
                        </>
                      ) : (
                        c.pricing
                      )}
                    </div>
                  </div>
                );
              })}
              </div>
            </div>

            {/* Legend + audit line */}
            <div className="mt-4 flex flex-wrap items-center gap-x-5 gap-y-2 border-t border-iron/40 pt-3 font-mono text-[10px] uppercase tracking-[0.14em] text-slate-text/55">
              <span className="flex items-center gap-1.5">
                <Cell value="yes" />
                <span>included</span>
              </span>
              <span className="flex items-center gap-1.5">
                <span className="inline-flex h-4 w-4 items-center justify-center">
                  <span className="block h-px w-2.5 bg-slate-text/35" />
                </span>
                <span>missing</span>
              </span>
              <span className="flex items-center gap-1.5">
                <Cell value="partial" />
                <span>self-host only</span>
              </span>
              <span className="ml-auto flex items-center gap-2 normal-case tracking-normal text-slate-text/50">
                <span className="h-1 w-1 rounded-full bg-slate-text/30" />
                <span>
                  Last audited{" "}
                  <span className="tabular-nums text-slate-text/70">
                    2026-04-16
                  </span>
                </span>
                <span className="text-iron">·</span>
                <a
                  href="/compare"
                  className="underline-offset-4 hover:text-amber hover:underline"
                >
                  sources
                </a>
              </span>
            </div>
          </div>
        </FadeIn>
      </div>
    </section>
  );
}
