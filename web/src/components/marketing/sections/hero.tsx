import Link from "next/link";
import { FadeIn } from "@/components/marketing/fade-in";

/**
 * Hero — v3 landing (polish + bolder pass, v2)
 *
 * Composition:
 *  - Left-heavy, asymmetric grid. Headline dominates; right column is a
 *    flush-right "review brief" metadata block (repo, PR, signal count).
 *  - Italic amber-glow phrases star; non-italic copy recedes to slate-text.
 *  - Status strip is a ps1-style terminal prompt, not a pill tag.
 *  - Mockup is framed by sharp amber corner brackets, not a soft glow.
 *  - Chrome reads like a real terminal header, not a browser mockup.
 *  - A single amber scan sweep crosses the italic phrases on mount,
 *    triggered by mounting the headline; transform + opacity only, 260ms.
 *
 * Tokens: amber, amber-glow, ember, charcoal, iron, void/background,
 * foreground, slate-text, ash. No teal, no purple, no new CSS vars.
 */
export function Hero() {
  return (
    <section
      id="hero"
      aria-labelledby="hero-title"
      className="relative overflow-hidden bg-background pb-24 pt-14 sm:pt-20 lg:pb-32 lg:pt-28"
    >
      <HeroBackdrop />

      <div className="relative mx-auto w-full max-w-[1280px] px-6 sm:px-8">
        <HeroHeadline />
        <HeroMockup />
      </div>
    </section>
  );
}

/* ─────────────────────────────────────────────────────────────── */
/* Backdrop — single off-axis glow + thin amber horizon rule       */
/* ─────────────────────────────────────────────────────────────── */

function HeroBackdrop() {
  return (
    <div aria-hidden="true" className="pointer-events-none absolute inset-0">
      {/* One anchored amber glow, off-axis — not centered */}
      <div
        className="absolute left-[12%] top-[-4%] h-[560px] w-[560px] opacity-[0.24] blur-[140px]"
        style={{
          background:
            "radial-gradient(circle at center, color-mix(in oklch, var(--color-amber-glow) 75%, transparent) 0%, transparent 68%)",
        }}
      />
      {/* Far-right cooler ember hint, very subtle */}
      <div
        className="absolute right-[-6%] top-[340px] h-[360px] w-[360px] opacity-[0.10] blur-[160px]"
        style={{
          background:
            "radial-gradient(circle at center, color-mix(in oklch, var(--color-amber-glow) 60%, transparent) 0%, transparent 70%)",
        }}
      />
      {/* Thin amber horizon rule — crosses the composition at headline baseline */}
      <div
        className="absolute left-0 right-0 top-[420px] h-px"
        style={{
          background:
            "linear-gradient(90deg, transparent 0%, color-mix(in oklch, var(--color-amber-glow) 28%, transparent) 18%, color-mix(in oklch, var(--color-amber-glow) 40%, transparent) 50%, color-mix(in oklch, var(--color-amber-glow) 28%, transparent) 82%, transparent 100%)",
        }}
      />
      {/* Dot matrix — sparser than before, fades off the headline */}
      <div
        className="absolute inset-0 opacity-[0.28]"
        style={{
          backgroundImage:
            "radial-gradient(circle at 1px 1px, color-mix(in oklch, var(--color-iron) 55%, transparent) 1px, transparent 0)",
          backgroundSize: "36px 36px",
          maskImage:
            "linear-gradient(180deg, transparent 0%, black 18%, black 80%, transparent 100%)",
          WebkitMaskImage:
            "linear-gradient(180deg, transparent 0%, black 18%, black 80%, transparent 100%)",
        }}
      />
    </div>
  );
}

/* ─────────────────────────────────────────────────────────────── */
/* Headline — left-aligned, asymmetric, italic phrases star        */
/* ─────────────────────────────────────────────────────────────── */

function HeroHeadline() {
  return (
    <div className="relative grid grid-cols-1 gap-y-10 lg:grid-cols-[minmax(0,1fr)_auto] lg:items-end lg:gap-x-10">
      {/* Left column — the actual statement */}
      <div className="relative flex flex-col items-start text-left">
        {/* Terminal ps1 strip — replaces the pill tag */}
        <FadeIn>
          <div className="mb-10 inline-flex items-center gap-2.5 font-mono text-[11px] tracking-[0.06em] text-slate-text">
            <span className="text-amber">~</span>
            <span className="text-ash/70">argus</span>
            <span aria-hidden="true" className="text-iron">§</span>
            <span className="uppercase tracking-[0.18em] text-slate-text/80">
              beta · built in the open
            </span>
            <span
              className="ml-1 inline-block h-[10px] w-[6px] bg-amber hero-status-pulse"
              aria-hidden="true"
            />
          </div>
        </FadeIn>

        {/* Headline. Non-italic = slate (recedes). Italic amber phrases star.
            A thin amber sweep crosses the italic phrases on mount. */}
        <FadeIn delay={80}>
          <h1
            id="hero-title"
            className="relative max-w-[1080px] font-mono font-bold tracking-[-0.025em]"
            style={{
              fontSize: "clamp(1.875rem, 6vw, 5.25rem)",
              lineHeight: 1.05,
            }}
          >
            <span className="block text-slate-text/85">
              Argus remembers what{" "}
              <ItalicAmber>broke last time.</ItalicAmber>
            </span>
            <span className="mt-2 block text-slate-text/85">
              It simulates what could{" "}
              <ItalicAmber>break next.</ItalicAmber>
            </span>
          </h1>
        </FadeIn>

        {/* Sub-headline — tight 52ch measure, flush-left */}
        <FadeIn delay={180}>
          <p className="mt-8 max-w-[52ch] font-mono text-[14px] leading-[1.7] text-slate-text sm:text-[15px]">
            <span className="text-foreground">Learns</span> from your team&rsquo;s review feedback.{" "}
            <span className="text-foreground">Simulates</span> real failure scenarios against your diff.{" "}
            <span className="text-foreground">Runs on your own keys</span> — zero hidden costs.
          </p>
        </FadeIn>

        {/* CTA row — primary button + text link (asymmetric on purpose) */}
        <FadeIn delay={260}>
          <div className="mt-10 flex flex-col items-start gap-5 sm:flex-row sm:items-center sm:gap-6">
            <Link
              href="/sign-up"
              className="group relative inline-flex h-12 items-center gap-2.5 bg-amber px-6 font-mono text-[13px] font-semibold uppercase tracking-[0.12em] text-primary-foreground transition-[transform,background-color] duration-150 ease-[cubic-bezier(0.23,1,0.32,1)] hover:bg-amber-glow"
            >
              <GithubMark />
              Install GitHub App
              <span className="mx-1 h-3 w-px bg-primary-foreground/40" aria-hidden="true" />
              <span className="text-[11px] tracking-[0.14em] text-primary-foreground/80">free</span>
              <CaretRight />
            </Link>
            <Link
              href="/compare"
              className="group inline-flex items-center gap-2 font-mono text-[13px] text-slate-text transition-colors duration-150 hover:text-amber"
            >
              <span className="underline decoration-iron decoration-dotted underline-offset-[6px] transition-colors group-hover:decoration-amber">
                vs. CodeRabbit, Greptile &amp; Cubic
              </span>
              <ArrowRight />
            </Link>
          </div>
        </FadeIn>

        {/* Trust row — GitHub + beta scarcity + build ethos. No fabricated
            numbers; link to real repo, real beta program. */}
        <FadeIn delay={360}>
          <div className="mt-10 flex flex-wrap items-center gap-x-5 gap-y-3 font-mono text-[10.5px] uppercase tracking-[0.18em] text-slate-text/80">
            <a
              href="https://github.com/BeLazy167/argus"
              target="_blank"
              rel="noreferrer"
              className="group inline-flex items-center gap-2 transition-colors hover:text-amber"
            >
              <svg viewBox="0 0 16 16" width="12" height="12" fill="currentColor" aria-hidden>
                <path d="M8 0a8 8 0 0 0-2.53 15.59c.4.08.55-.17.55-.38v-1.33c-2.23.48-2.7-1.07-2.7-1.07-.36-.93-.89-1.18-.89-1.18-.72-.5.06-.49.06-.49.8.06 1.23.83 1.23.83.72 1.23 1.88.87 2.34.66.07-.52.28-.87.5-1.07-1.78-.2-3.65-.89-3.65-3.96 0-.88.31-1.59.83-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82a7.62 7.62 0 0 1 4 0c1.53-1.03 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.52.56.83 1.27.83 2.15 0 3.08-1.87 3.76-3.65 3.95.29.25.54.74.54 1.48v2.2c0 .21.15.47.55.38A8 8 0 0 0 8 0Z" />
              </svg>
              <span>Open on GitHub</span>
            </a>
            <span aria-hidden className="hidden h-[10px] w-px bg-iron sm:inline-block" />
            <span className="inline-flex items-center gap-2">
              <span aria-hidden className="h-1.5 w-1.5 rounded-full bg-amber hero-status-pulse" />
              <span>
                Beta · first 100 teams get{" "}
                <span className="text-amber">50%</span> off launch
              </span>
            </span>
            <span aria-hidden className="hidden h-[10px] w-px bg-iron sm:inline-block" />
            <span>Built in the open</span>
          </div>
        </FadeIn>
      </div>

      {/* Right column — "review brief" metadata, flush-right on desktop.
          Hidden under lg to avoid eating the headline at narrow widths. */}
      <FadeIn delay={320}>
        <aside
          aria-label="Live review sample"
          className="hidden w-[220px] shrink-0 border-l border-iron/60 pl-5 font-mono text-[10px] uppercase tracking-[0.18em] text-slate-text/70 lg:block"
        >
          <div className="flex items-center gap-2">
            <span
              className="h-1.5 w-1.5 rounded-full bg-amber hero-status-pulse"
              aria-hidden="true"
            />
            <span className="text-amber">live review</span>
          </div>
          <dl className="mt-5 space-y-3">
            <div>
              <dt className="text-[9px] tracking-[0.22em] text-iron">repo</dt>
              <dd className="mt-1 normal-case tracking-normal text-ash/90">
                acme/billing-service
              </dd>
            </div>
            <div>
              <dt className="text-[9px] tracking-[0.22em] text-iron">pr</dt>
              <dd className="mt-1 normal-case tracking-normal text-foreground">
                #1047 — invoice rounding
              </dd>
            </div>
            <div>
              <dt className="text-[9px] tracking-[0.22em] text-iron">signal</dt>
              <dd className="mt-1 normal-case tracking-normal text-amber">
                3 regressions · 1 bug
              </dd>
            </div>
          </dl>
        </aside>
      </FadeIn>
    </div>
  );
}

/**
 * ItalicAmber — the starring phrase component.
 *
 * Wraps its children in amber-glow italic type with a single mount-triggered
 * amber sweep. The sweep is a ::before element animated with `clip-path`
 * (transform-origin: left) to reveal a lighter band left-to-right.
 *
 * Animation stays under 300ms, uses transform + opacity only via clip-path,
 * and respects `prefers-reduced-motion` via the existing global override.
 */
function ItalicAmber({ children }: { children: React.ReactNode }) {
  return (
    <span
      className="relative inline-block italic hero-sweep-target"
      style={{
        color: "var(--color-amber-glow)",
        fontFamily: "var(--font-sans)",
        fontWeight: 600,
        fontStyle: "italic",
      }}
    >
      {children}
    </span>
  );
}

/* ─────────────────────────────────────────────────────────────── */
/* Mockup — framed by amber corner brackets, terminal-style header */
/* ─────────────────────────────────────────────────────────────── */

function HeroMockup() {
  return (
    <FadeIn delay={400}>
      <div className="relative mx-auto mt-24 w-full max-w-[1180px] sm:mt-28">
        {/* Four amber corner brackets — replace the soft outer glow */}
        <CornerBracket position="tl" />
        <CornerBracket position="tr" />
        <CornerBracket position="bl" />
        <CornerBracket position="br" />

        {/* Subtle lift glow only (no halo) */}
        <div
          aria-hidden="true"
          className="pointer-events-none absolute inset-x-16 -bottom-8 h-24 opacity-70 blur-[60px]"
          style={{
            background:
              "radial-gradient(50% 50% at 50% 50%, color-mix(in oklch, var(--color-amber-glow) 14%, transparent) 0%, transparent 70%)",
          }}
        />

        <div
          className="relative overflow-hidden border border-iron/80 bg-charcoal shadow-[0_28px_88px_-28px_rgba(0,0,0,0.85)]"
          role="img"
          aria-label="Preview of an Argus review on a pull request"
        >
          <TerminalHeader />
          <ScanCommandLine />

          <div className="flex min-h-[500px] w-full bg-[color-mix(in_oklch,var(--color-void)_92%,black_8%)]">
            <FileTree />
            <ReviewPane />
          </div>

          <StatsFooter />
        </div>
      </div>
    </FadeIn>
  );
}

/* ── Corner bracket — crisp amber tick marks at each corner ─── */

function CornerBracket({ position }: { position: "tl" | "tr" | "bl" | "br" }) {
  const placement = {
    tl: "-left-2 -top-2 border-l-2 border-t-2",
    tr: "-right-2 -top-2 border-r-2 border-t-2",
    bl: "-left-2 -bottom-2 border-l-2 border-b-2",
    br: "-right-2 -bottom-2 border-r-2 border-b-2",
  }[position];
  return (
    <span
      aria-hidden="true"
      className={`pointer-events-none absolute z-10 h-5 w-5 border-amber ${placement}`}
      style={{
        boxShadow:
          "0 0 12px color-mix(in oklch, var(--color-amber-glow) 45%, transparent)",
      }}
    />
  );
}

/* ── Terminal header — replaces macOS chrome + URL bar ──────── */

function TerminalHeader() {
  return (
    <div className="flex h-[44px] items-center gap-3 border-b border-iron/60 bg-[color-mix(in_oklch,var(--color-charcoal)_88%,black_12%)] px-5">
      {/* Argus sigil — micro eye, amber */}
      <span
        className="inline-flex h-5 w-5 items-center justify-center border border-amber/40 bg-amber/[0.08]"
        aria-hidden="true"
      >
        <ArgusEye />
      </span>
      <span className="font-mono text-[11px] font-semibold tracking-[0.08em] text-foreground">
        argus
      </span>
      <span aria-hidden="true" className="text-iron/80">/</span>
      <span className="font-mono text-[11px] text-slate-text">
        acme/billing-service
      </span>
      <span aria-hidden="true" className="text-iron/80">·</span>
      <span className="font-mono text-[11px] text-amber">#1047</span>

      <div className="ml-auto flex items-center gap-3">
        <span className="hidden items-center gap-1.5 font-mono text-[10px] uppercase tracking-[0.16em] text-amber sm:inline-flex">
          <span
            className="h-1.5 w-1.5 rounded-full bg-amber hero-status-pulse"
            aria-hidden="true"
          />
          reviewing
        </span>
        <span className="hidden font-mono text-[10px] text-slate-text/70 md:inline">
          4.3s
        </span>
      </div>
    </div>
  );
}

/* ── Scan command line — replaces the tabs row ──────────────── */

function ScanCommandLine() {
  return (
    <div className="flex items-center gap-2.5 border-b border-iron/60 bg-[color-mix(in_oklch,var(--color-charcoal)_70%,black_30%)] px-5 py-2.5 font-mono text-[11px] leading-none">
      <span className="text-amber">$</span>
      <span className="text-slate-text">argus scan</span>
      <span className="text-ash/70">--pr=#1047</span>
      <span aria-hidden="true" className="mx-1 text-iron/70">→</span>
      <span className="text-foreground">
        matched <span className="text-amber">Feb 14 rounding regression</span>
      </span>
      <span aria-hidden="true" className="text-iron/60">·</span>
      <span className="text-slate-text">3 scenarios reproduced</span>
      <span
        className="ml-1 inline-block h-[10px] w-[6px] bg-amber hero-status-pulse"
        aria-hidden="true"
      />
    </div>
  );
}

/* ── File tree ──────────────────────────────────────────────── */

function FileTree() {
  return (
    <aside className="hidden w-[260px] shrink-0 border-r border-iron/60 bg-[color-mix(in_oklch,var(--color-charcoal)_60%,var(--color-void)_40%)] p-5 lg:block">
      <div className="font-mono text-[13px] leading-snug text-foreground">
        Fix invoice rounding in EU
        <br />
        checkout flow
      </div>

      <div className="mt-5 flex items-center gap-3">
        <span className="inline-flex items-center gap-1.5 border border-amber/40 bg-amber/[0.08] px-2 py-0.5 font-mono text-[10px] uppercase tracking-[0.12em] text-amber">
          <span className="h-1 w-1 rounded-full bg-amber" aria-hidden="true" />
          open
        </span>
        <span className="font-mono text-[11px] text-slate-text">@bela.k</span>
      </div>

      <div className="mt-8 font-mono text-[10px] uppercase tracking-[0.18em] text-slate-text/70">
        agents
      </div>

      <div className="mt-3 flex flex-col gap-1.5">
        <AgentChip label="triage" />
        <AgentChip label="security" active />
        <AgentChip label="memory" />
      </div>

      {/* Memory echo — a distinctive touch: what argus recalls */}
      <div className="mt-10 border-l border-amber/40 pl-3">
        <div className="font-mono text-[9px] uppercase tracking-[0.2em] text-amber/80">
          memory
        </div>
        <p className="mt-2 font-mono text-[11px] leading-[1.6] text-slate-text">
          2 months ago —{" "}
          <span className="text-foreground">@jordan</span> shipped an
          almost-identical rounding change.{" "}
          <span className="text-amber">0.01 drift × 2,113 invoices.</span>
        </p>
      </div>
    </aside>
  );
}

function AgentChip({ label, active }: { label: string; active?: boolean }) {
  return (
    <div
      className={`flex w-fit items-center gap-2 border px-2.5 py-1 font-mono text-[11px] tracking-wide transition-colors duration-150 ${
        active
          ? "border-amber/60 bg-amber/[0.1] text-amber"
          : "border-iron/70 bg-charcoal/40 text-slate-text"
      }`}
    >
      <span
        className={`h-1.5 w-1.5 rounded-full ${
          active ? "bg-amber" : "bg-iron"
        }`}
        aria-hidden="true"
        style={
          active
            ? { boxShadow: "0 0 5px color-mix(in oklch, var(--color-amber-glow) 70%, transparent)" }
            : undefined
        }
      />
      {label}
    </div>
  );
}

/* ── Review pane ────────────────────────────────────────────── */

function ReviewPane() {
  return (
    <div className="flex min-w-0 flex-1 flex-col">
      <div className="flex items-center gap-2 border-b border-iron/60 bg-[color-mix(in_oklch,var(--color-void)_88%,black_12%)] px-5 py-2.5 font-mono text-[11px]">
        <span className="text-slate-text">services/billing/</span>
        <span className="text-amber">invoice.ts</span>
        <span className="mx-1 text-iron/70" aria-hidden="true">·</span>
        <span className="text-slate-text">L42–L58</span>
        <div className="ml-auto flex items-center gap-3 text-[10px] text-slate-text/70">
          <span className="hidden sm:inline">+7</span>
          <span className="hidden text-emerald-400/60 sm:inline">−3</span>
        </div>
      </div>

      <CodeDiff />
      <ArgusReviewCard />
    </div>
  );
}

function CodeDiff() {
  const lines: Array<{ no: number; code: string; kind?: "add" | "del" }> = [
    { no: 41, code: "  const total = cart.items.reduce((s, it) => s + it.price, 0);" },
    { no: 42, code: "  const tax = total * region.vatRate;", kind: "del" },
    { no: 43, code: "  const tax = round(total * region.vatRate, 2);", kind: "add" },
    { no: 44, code: "  return { total, tax, grand: total + tax };" },
  ];
  return (
    <div className="bg-[color-mix(in_oklch,var(--color-void)_95%,black_5%)] font-mono text-[12px] leading-[1.75]">
      {lines.map((l) => {
        const bg =
          l.kind === "add"
            ? "bg-emerald-500/[0.07]"
            : l.kind === "del"
              ? "bg-red-500/[0.07]"
              : "";
        const marker =
          l.kind === "add"
            ? "text-emerald-400/80"
            : l.kind === "del"
              ? "text-red-400/80"
              : "text-transparent";
        return (
          <div key={l.no} className={`flex ${bg}`}>
            <span className="w-11 shrink-0 select-none border-r border-iron/40 px-3 py-0.5 text-right text-[10px] text-slate-text/60">
              {l.no}
            </span>
            <span className={`w-5 shrink-0 select-none text-center ${marker}`}>
              {l.kind === "add" ? "+" : l.kind === "del" ? "−" : ""}
            </span>
            <code className="min-w-0 whitespace-pre px-2 py-0.5 text-ash/90">
              {l.code}
            </code>
          </div>
        );
      })}
    </div>
  );
}

function ArgusReviewCard() {
  return (
    <div className="border-t border-iron/60 bg-charcoal/40 px-5 py-4">
      <div className="flex items-center gap-2.5">
        <div className="flex h-5 w-5 items-center justify-center bg-amber/15 ring-1 ring-amber/30">
          <ArgusEye />
        </div>
        <span className="font-mono text-[12px] font-semibold text-foreground">
          Argus
        </span>
        <span className="border border-amber/40 bg-amber/[0.1] px-1.5 py-0.5 font-mono text-[9px] uppercase tracking-[0.16em] text-amber">
          bug
        </span>
        <span className="ml-auto font-mono text-[10px] text-slate-text/70">
          seen in PR #927 — rounding bug
        </span>
      </div>

      <p className="mt-3 max-w-[720px] font-mono text-[12px] leading-[1.7] text-ash/85">
        <span className="text-foreground">mixedCurrency</span> cases banker&rsquo;s
        rounding for EUR — our tests still expect half-up. Last time (Feb 14),
        <span className="text-foreground"> @jordan</span> merged a near-identical
        change and it caused{" "}
        <span className="text-amber">0.01 drift on 2,113 invoices</span>.
      </p>

      <FailureSimStrip />

      <div className="mt-4 flex flex-wrap items-center gap-2">
        <button
          type="button"
          className="inline-flex h-8 items-center gap-2 bg-amber px-3 font-mono text-[11px] font-semibold uppercase tracking-[0.1em] text-primary-foreground transition-colors duration-150 hover:bg-amber-glow"
        >
          <CheckMark />
          apply
        </button>
        <button
          type="button"
          className="inline-flex h-8 items-center border border-iron/70 bg-transparent px-3 font-mono text-[11px] text-slate-text transition-colors duration-150 hover:border-iron hover:text-foreground"
        >
          flash fixes
        </button>
        <button
          type="button"
          className="inline-flex h-8 items-center border border-iron/70 bg-transparent px-3 font-mono text-[11px] text-slate-text transition-colors duration-150 hover:border-iron hover:text-foreground"
        >
          dismiss
        </button>
        <div className="ml-auto flex items-center gap-3 font-mono text-[10px] text-slate-text/70">
          <span>
            <span className="text-foreground">2,147 tokens</span>
          </span>
          <span aria-hidden="true" className="text-iron/70">·</span>
          <span>
            <span className="text-foreground">$0.0042</span>{" "}
            <span className="text-slate-text/60">(your key)</span>
          </span>
        </div>
      </div>
    </div>
  );
}

function FailureSimStrip() {
  const cases = [
    { label: "EUR · €19.99 × 3", status: "fail" as const },
    { label: "CHF · 49.00 (half-round)", status: "fail" as const },
    { label: "USD · tax bucket", status: "pass" as const },
    { label: "GBP · mixed-currency cart", status: "fail" as const },
  ];
  return (
    <div className="mt-4 border border-iron/60 bg-[color-mix(in_oklch,var(--color-void)_90%,black_10%)] px-3 py-3">
      <div className="flex items-center gap-2 font-mono text-[10px] uppercase tracking-[0.16em] text-slate-text/80">
        <span
          className="h-1 w-1 rounded-full bg-amber"
          aria-hidden="true"
          style={{ boxShadow: "0 0 4px var(--color-amber-glow)" }}
        />
        failure simulation
        <span className="text-iron/70" aria-hidden="true">·</span>
        <span>4 scenarios</span>
        <span className="ml-auto flex items-center gap-1.5 text-[9px] text-amber/85">
          <span
            className="inline-block h-1 w-1 rounded-full bg-amber"
            aria-hidden="true"
          />
          3 failed
        </span>
      </div>
      <ul className="mt-2.5 space-y-1.5">
        {cases.map((c) => (
          <li
            key={c.label}
            className="flex items-center gap-3 font-mono text-[11px]"
          >
            <span
              className={`inline-flex h-[14px] w-[14px] shrink-0 items-center justify-center border ${
                c.status === "fail"
                  ? "border-amber/50 bg-amber/[0.1] text-amber"
                  : "border-iron/60 bg-transparent text-slate-text"
              }`}
              aria-label={c.status}
            >
              {c.status === "fail" ? (
                <svg viewBox="0 0 10 10" className="h-2 w-2" fill="none">
                  <path
                    d="M2 2 L8 8 M8 2 L2 8"
                    stroke="currentColor"
                    strokeWidth="1.5"
                    strokeLinecap="square"
                  />
                </svg>
              ) : (
                <svg viewBox="0 0 10 10" className="h-2 w-2" fill="none">
                  <path
                    d="M2 5 L4.5 7.5 L8 3"
                    stroke="currentColor"
                    strokeWidth="1.5"
                    strokeLinecap="square"
                    strokeLinejoin="miter"
                  />
                </svg>
              )}
            </span>
            <span
              className={
                c.status === "fail" ? "text-foreground" : "text-slate-text/80"
              }
            >
              {c.label}
            </span>
            <span className="ml-auto text-[10px] text-slate-text/60">
              {c.status === "fail" ? "reproduced" : "ok"}
            </span>
          </li>
        ))}
      </ul>
    </div>
  );
}

/* ── Stats footer ───────────────────────────────────────────── */

function StatsFooter() {
  const items = [
    { icon: <KeyIcon />, text: "BYOK — your key, your model" },
    { icon: <ClockIcon />, text: "Reviewed in 4.3s" },
    { icon: <HeartIcon />, text: "12 reactions indexed" },
  ];
  return (
    <div className="flex flex-wrap items-center gap-x-6 gap-y-2 border-t border-iron/60 bg-[color-mix(in_oklch,var(--color-charcoal)_85%,black_15%)] px-5 py-3">
      <div className="flex flex-wrap items-center gap-x-5 gap-y-2 font-mono text-[11px] text-slate-text">
        {items.map((item, i) => (
          <span key={i} className="flex items-center gap-2">
            <span className="text-amber/80" aria-hidden="true">
              {item.icon}
            </span>
            {item.text}
          </span>
        ))}
      </div>
      <Link
        href="/compare"
        className="ml-auto inline-flex items-center gap-1.5 font-mono text-[11px] text-amber transition-colors duration-150 hover:text-amber-glow"
      >
        view full review
        <ArrowRight />
      </Link>
    </div>
  );
}

/* ─────────────────────────────────────────────────────────────── */
/* Icons                                                           */
/* ─────────────────────────────────────────────────────────────── */

function GithubMark() {
  return (
    <svg viewBox="0 0 16 16" className="h-4 w-4" fill="currentColor" aria-hidden="true">
      <path d="M8 .2a8 8 0 0 0-2.53 15.59c.4.08.55-.17.55-.38v-1.33c-2.23.48-2.7-1.07-2.7-1.07-.36-.92-.88-1.17-.88-1.17-.72-.5.05-.48.05-.48.8.06 1.22.82 1.22.82.71 1.22 1.87.87 2.33.67.07-.52.28-.87.5-1.07-1.78-.2-3.64-.89-3.64-3.96 0-.87.31-1.58.82-2.14-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82a7.6 7.6 0 0 1 4 0c1.53-1.03 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.14 0 3.08-1.87 3.76-3.65 3.96.29.25.55.73.55 1.48v2.2c0 .21.15.47.55.38A8 8 0 0 0 8 .2Z" />
    </svg>
  );
}

function ArrowRight() {
  return (
    <svg
      viewBox="0 0 16 16"
      className="h-3.5 w-3.5 transition-transform duration-150 ease-[cubic-bezier(0.23,1,0.32,1)] group-hover:translate-x-0.5"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.75"
      strokeLinecap="square"
      aria-hidden="true"
    >
      <path d="M3 8 H13 M9 4 L13 8 L9 12" />
    </svg>
  );
}

function CaretRight() {
  return (
    <svg
      viewBox="0 0 12 12"
      className="h-3 w-3 transition-transform duration-150 ease-[cubic-bezier(0.23,1,0.32,1)] group-hover:translate-x-0.5"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="square"
      aria-hidden="true"
    >
      <path d="M4 2 L8 6 L4 10" />
    </svg>
  );
}

function CheckMark() {
  return (
    <svg viewBox="0 0 12 12" className="h-3 w-3" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden="true">
      <path d="M2.5 6 L5 8.5 L9.5 3.5" strokeLinecap="square" />
    </svg>
  );
}

function KeyIcon() {
  return (
    <svg viewBox="0 0 14 14" className="h-3 w-3" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden="true">
      <circle cx="5" cy="7" r="2.5" />
      <path d="M7.5 7 H12 M10 7 V9 M12 7 V9" strokeLinecap="square" />
    </svg>
  );
}

function ClockIcon() {
  return (
    <svg viewBox="0 0 14 14" className="h-3 w-3" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden="true">
      <circle cx="7" cy="7" r="5" />
      <path d="M7 4 V7 L9 8.5" strokeLinecap="square" />
    </svg>
  );
}

function HeartIcon() {
  return (
    <svg viewBox="0 0 14 14" className="h-3 w-3" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden="true">
      <path
        d="M7 11.5 C3 9 1.5 6.5 1.5 4.75 A2.75 2.75 0 0 1 7 4 a2.75 2.75 0 0 1 5.5 0.75 C12.5 6.5 11 9 7 11.5 Z"
        strokeLinejoin="miter"
      />
    </svg>
  );
}

function ArgusEye() {
  return (
    <svg viewBox="0 0 120 60" className="h-3 w-3 text-amber" fill="none" aria-hidden="true">
      <path
        d="M10 30 C10 30 30 8 60 8 C90 8 110 30 110 30 C110 30 90 52 60 52 C30 52 10 30 10 30 Z"
        stroke="currentColor"
        strokeWidth="8"
        fill="none"
      />
      <circle cx="60" cy="30" r="10" fill="currentColor" />
    </svg>
  );
}
