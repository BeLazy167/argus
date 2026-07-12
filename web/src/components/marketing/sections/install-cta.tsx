"use client";

import Link from "next/link";
import { FadeIn } from "@/components/marketing/fade-in";
import { focusRing, focusRingAmber } from "@/components/marketing/focus-ring";
import { InViewSection } from "@/components/marketing/in-view-section";
import { track } from "@/lib/analytics";

/* ────────────────────────────────────────────────────────────
   Chip row — engraved plaques, not pills. Mono text, amber dots.
   ──────────────────────────────────────────────────────────── */
function ChipRow() {
  const chips = ["50 FREE REVIEWS / MONTH", "2-MIN INSTALL", "BYOK — YOUR KEY, YOUR BILL"];
  return (
    <div className="flex flex-wrap items-center justify-center gap-x-0 gap-y-2 text-[10.5px] font-mono uppercase tracking-[0.2em]">
      {chips.map((c, i) => (
        <span key={c} className="flex items-center">
          <span className="flex items-center gap-2 px-4">
            <span
              aria-hidden
              className="h-[5px] w-[5px] rounded-full bg-amber-glow shadow-[0_0_6px_color-mix(in_oklch,var(--color-amber-glow)_80%,transparent)]"
            />
            <span className="text-foreground/85">{c}</span>
          </span>
          {i < chips.length - 1 && (
            <span aria-hidden className="h-3 w-px bg-iron/80" />
          )}
        </span>
      ))}
    </div>
  );
}

/* ────────────────────────────────────────────────────────────
   Inline SVG socials — square, muted, amber on hover.
   ──────────────────────────────────────────────────────────── */
function SocialIcon({
  href,
  label,
  children,
}: {
  href: string;
  label: string;
  children: React.ReactNode;
}) {
  return (
    <a
      href={href}
      aria-label={label}
      className={`flex h-11 w-11 items-center justify-center border border-iron text-slate-text transition-colors hover:border-amber hover:text-amber ${focusRing}`}
    >
      {children}
    </a>
  );
}

/* ────────────────────────────────────────────────────────────
   ARGUS cascading wordmark — "remembering" metaphor.
   Three stacked lines at decreasing opacity (past, present,
   future). Baseline tick row below reads as a build receipt.
   Fluid via text-[18vw] — scales from phone to ultrawide.
   ──────────────────────────────────────────────────────────── */
function ArgusEchoWordmark() {
  return (
    <div
      aria-label="ARGUS"
      role="img"
      className="relative w-full py-12 sm:py-16"
    >
      <div className="select-none px-4" aria-hidden>
        <div className="font-display uppercase tracking-[0.04em] text-center block text-[15.5vw] leading-[0.85] text-amber">
          ARGUS
        </div>
        <div className="font-display uppercase tracking-[0.04em] text-center -mt-[1.8vw] block text-[15.5vw] leading-[0.85] text-amber/30">
          ARGUS
        </div>
        <div className="font-display uppercase tracking-[0.04em] text-center -mt-[1.8vw] block text-[15.5vw] leading-[0.85] text-amber/10">
          ARGUS
        </div>
      </div>

      {/* Baseline receipt row — version + date + mantra */}
      <div className="mt-4 flex items-center gap-2 font-mono text-[9px] uppercase tracking-[0.22em] text-slate-text sm:mt-6 sm:text-[10px]">
        <span aria-hidden className="h-px flex-1 bg-iron" />
        <span>v0.1.0</span>
        <span
          aria-hidden
          className="h-1 w-1 rounded-full bg-amber shadow-[0_0_6px_color-mix(in_oklch,var(--color-amber-glow)_80%,transparent)]"
        />
        <span>2026.04.16</span>
        <span
          aria-hidden
          className="h-1 w-1 rounded-full bg-amber shadow-[0_0_6px_color-mix(in_oklch,var(--color-amber-glow)_80%,transparent)]"
        />
        <span className="hidden sm:inline">Built in the open</span>
        <span className="sm:hidden">BITO</span>
        <span aria-hidden className="h-px flex-1 bg-iron" />
      </div>
    </div>
  );
}

/* ────────────────────────────────────────────────────────────
   Footer primary nav — single mono row, amber dot separators.
   Only real routes. Changelog page is planned but the slot
   belongs here by design.
   ──────────────────────────────────────────────────────────── */
function FooterNav() {
  const items: { href: string; label: string }[] = [
    { href: "/pricing", label: "Pricing" },
    { href: "/compare", label: "Compare" },
    { href: "/docs", label: "Docs" },
    { href: "/blog", label: "Blog" },
    { href: "/changelog", label: "Changelog" },
  ];
  return (
    <nav
      aria-label="Footer"
      className="flex flex-wrap items-center justify-center gap-y-2 font-mono text-[11px] uppercase tracking-[0.22em] sm:justify-start"
    >
      {items.map((it, i) => (
        <span key={it.href} className="flex items-center">
          <Link
            href={it.href}
            className={`px-3 py-2 text-slate-text transition-colors hover:text-amber ${focusRing}`}
          >
            {it.label}
          </Link>
          {i < items.length - 1 && (
            <span
              aria-hidden
              className="h-1 w-1 rounded-full bg-amber/60"
            />
          )}
        </span>
      ))}
    </nav>
  );
}

export function InstallCta() {
  return (
    <InViewSection id="install" className="relative isolate overflow-hidden">
      {/* Local keyframes — SSR-safe. transform + opacity + filter only. */}
      <style>{`
        @keyframes argusBannerScan {
          0%   { transform: translateX(-100%); }
          100% { transform: translateX(100%); }
        }
        @keyframes argusGlowBreath {
          0%, 100% { opacity: 0.55; }
          50%      { opacity: 0.78; }
        }
        @keyframes argusOpsDot {
          0%, 100% { opacity: 1; transform: scale(1); }
          50%      { opacity: 0.55; transform: scale(0.85); }
        }
        .argus-banner-scan { animation: argusBannerScan 6s cubic-bezier(0.77, 0, 0.175, 1) infinite; }
        .argus-glow-breath { animation: argusGlowBreath 5s cubic-bezier(0.77, 0, 0.175, 1) infinite; }
        .argus-ops-dot { animation: argusOpsDot 2.4s cubic-bezier(0.77, 0, 0.175, 1) infinite; }
        [data-in-view="false"] .argus-banner-scan,
        [data-in-view="false"] .argus-glow-breath,
        [data-in-view="false"] .argus-ops-dot { animation-play-state: paused !important; }
        @media (prefers-reduced-motion: reduce) {
          .argus-banner-scan,
          .argus-glow-breath,
          .argus-ops-dot { animation: none !important; }
        }
      `}</style>

      {/* ── CTA hero card ─────────────────────────── */}
      <div className="mx-auto w-full max-w-[1280px] px-6 pt-24 pb-10 sm:pt-28 sm:pb-16">
        <FadeIn>
          <div className="relative">
            {/* Single deliberate radial glow behind card — breathes slowly */}
            <div
              aria-hidden
              className="argus-glow-breath pointer-events-none absolute -inset-x-32 -inset-y-20 -z-10"
              style={{
                background:
                  "radial-gradient(50% 45% at 50% 50%, color-mix(in oklch, var(--color-amber-glow) 28%, transparent) 0%, color-mix(in oklch, var(--color-amber-glow) 10%, transparent) 40%, transparent 72%)",
              }}
            />
            <div
              className="relative border border-iron bg-charcoal/60 px-6 py-14 sm:px-16 sm:py-24"
              style={{
                boxShadow:
                  "inset 0 0 0 1px color-mix(in oklch, var(--color-amber-glow) 10%, transparent), inset 0 1px 0 0 color-mix(in oklch, var(--color-amber-glow) 18%, transparent)",
              }}
            >
              {/* Asymmetric hand-drawn corner ticks */}
              <CornerTicks />

              <ChipRow />

              <h2 className="mx-auto mt-10 max-w-[980px] text-center font-mono font-bold leading-[0.98] tracking-[-0.02em] text-[40px] sm:text-[64px] md:text-[72px]">
                <span className="block text-foreground">Install Argus.</span>
                <span
                  className="block bg-clip-text text-transparent"
                  style={{
                    backgroundImage:
                      "linear-gradient(180deg, oklch(0.98 0.02 80) 0%, oklch(0.88 0.18 78) 45%, oklch(0.65 0.18 50) 100%)",
                  }}
                >
                  Your next bad merge gets caught.
                </span>
              </h2>

              <p className="mx-auto mt-6 max-w-[600px] text-center text-[13px] leading-relaxed text-slate-text sm:text-[15px]">
                One-click GitHub App. Bring your own LLM key. See every token
                spent. Free tier — 50 reviews/month on 3 repos, no credit card.
              </p>

              <div className="mt-10 flex flex-col items-center justify-center gap-3 sm:flex-row sm:gap-4">
                <Link
                  href="/sign-up"
                  onClick={() => track("onboarding.install_clicked", { source: "install_cta" })}
                  className={`group relative inline-flex items-center justify-center gap-2 overflow-hidden bg-amber px-8 py-4 font-mono text-sm font-semibold uppercase tracking-[0.1em] text-primary-foreground shadow-[0_0_48px_color-mix(in_oklch,var(--color-amber-glow)_34%,transparent)] transition-[transform,background-color] hover:bg-amber-glow active:scale-[0.98] ${focusRingAmber}`}
                >
                  <span aria-hidden className="relative">{"\u25B8"}</span>
                  <span className="relative">Install GitHub App — Free</span>
                  {/* subtle inner highlight */}
                  <span
                    aria-hidden
                    className="pointer-events-none absolute inset-x-0 top-0 h-px bg-white/30"
                  />
                </Link>
                <Link
                  href="/compare"
                  className={`inline-flex items-center justify-center gap-2 border border-iron px-8 py-4 font-mono text-sm font-semibold uppercase tracking-[0.1em] text-foreground hover:border-amber hover:text-amber ${focusRing}`}
                >
                  <span>See Argus vs CodeRabbit &amp; Greptile</span>
                  <span aria-hidden>{"\u2192"}</span>
                </Link>
              </div>

              <div className="mt-10 flex flex-wrap items-center justify-center gap-x-6 gap-y-2 text-[11px] font-mono uppercase tracking-[0.14em] text-slate-text">
                <span className="flex items-center gap-1.5">
                  <span aria-hidden className="text-amber">
                    {"\u2192"}
                  </span>
                  No credit card
                </span>
                <span aria-hidden className="hidden h-[10px] w-px bg-iron/70 sm:inline-block" />
                <span className="flex items-center gap-1.5">
                  <span aria-hidden className="text-amber">
                    {"\u2192"}
                  </span>
                  BYOK — paid keys never leave your machine
                </span>
                <span aria-hidden className="hidden h-[10px] w-px bg-iron/70 sm:inline-block" />
                <span className="flex items-center gap-1.5">
                  <span aria-hidden className="text-amber">
                    {"\u2192"}
                  </span>
                  Free tier · 50 reviews/mo
                </span>
                <span aria-hidden className="hidden h-[10px] w-px bg-iron/70 sm:inline-block" />
                <span className="flex items-center gap-1.5">
                  <span aria-hidden className="text-amber">
                    {"\u2192"}
                  </span>
                  $19/mo on Pro · less than one coffee per workday
                </span>
              </div>
            </div>
          </div>
        </FadeIn>
      </div>

      {/* ── System banner ─────────────────────────
          Amber left-rail + scanline sweep. Reads as a proper
          status banner, not a footer gimmick. */}
      <div className="mx-auto w-full max-w-[1280px] px-6">
        <FadeIn delay={120}>
          <div className="relative flex flex-col items-start justify-between gap-3 overflow-hidden border border-iron bg-void/40 pl-6 pr-6 py-4 sm:flex-row sm:items-center">
            {/* amber left rail */}
            <span
              aria-hidden
              className="pointer-events-none absolute inset-y-0 left-0 w-[3px] bg-amber"
              style={{
                boxShadow:
                  "0 0 12px color-mix(in oklch, var(--color-amber-glow) 50%, transparent)",
              }}
            />
            {/* scanline sweep */}
            <span
              aria-hidden
              className="argus-banner-scan pointer-events-none absolute inset-y-0 left-0 w-32"
              style={{
                background:
                  "linear-gradient(90deg, transparent 0%, color-mix(in oklch, var(--color-amber-glow) 14%, transparent) 50%, transparent 100%)",
              }}
            />
            <div className="relative flex flex-col gap-1 pl-3 sm:flex-row sm:items-center sm:gap-4">
              <span className="flex items-center gap-2 text-[10.5px] font-mono uppercase tracking-[0.22em] text-amber">
                <span
                  aria-hidden
                  className="h-[5px] w-[5px] bg-amber-glow shadow-[0_0_6px_color-mix(in_oklch,var(--color-amber-glow)_80%,transparent)]"
                />
                Built in the open
              </span>
              <span aria-hidden className="hidden h-3 w-px bg-iron sm:block" />
              <span className="text-[12px] font-mono text-slate-text">
                Bring your own key. Every prompt, every token, every cost — visible.
              </span>
            </div>
            <a
              href="https://github.com/BeLazy167/argus"
              target="_blank"
              rel="noreferrer"
              className="relative flex shrink-0 items-center gap-2 font-mono text-[10.5px] uppercase tracking-[0.22em] text-amber hover:text-amber-glow"
            >
              Open repo <span aria-hidden>{"\u2192"}</span>
            </a>
          </div>
        </FadeIn>
      </div>

      {/* ── Giant ARGUS echo wordmark — closing artifact ────────── */}
      <div className="relative mx-auto w-full max-w-[1560px] px-4 pt-20 sm:px-8 sm:pt-24">
        <FadeIn delay={180}>
          {/* Radial floor glow — warms the wordmark from below */}
          <div className="relative">
            <div
              aria-hidden
              className="pointer-events-none absolute inset-x-0 bottom-0 h-40 -z-10"
              style={{
                background:
                  "radial-gradient(60% 100% at 50% 100%, color-mix(in oklch, var(--color-amber-glow) 14%, transparent) 0%, transparent 75%)",
              }}
            />
            <ArgusEchoWordmark />
          </div>
        </FadeIn>
      </div>

      {/* ── Minimal footer — three rows, no columns ─────────────── */}
      <div className="mx-auto w-full max-w-[1280px] px-6 pb-10 sm:pb-14">
        <FadeIn delay={220}>
          {/* Row 1 — primary nav */}
          <div className="border-t border-iron pt-6">
            <FooterNav />
          </div>

          {/* Row 2 — brand pitch + socials */}
          <div className="mt-6 flex flex-col items-start justify-between gap-5 border-t border-iron pt-6 sm:flex-row sm:items-center">
            <div className="flex items-center gap-3">
              {/* Small eye mark */}
              <span
                aria-hidden
                className="flex h-8 w-8 items-center justify-center border border-iron"
              >
                <svg
                  viewBox="0 0 24 24"
                  width="14"
                  height="14"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="1.5"
                  className="text-amber"
                >
                  <path d="M1.5 12S5 5 12 5s10.5 7 10.5 7S19 19 12 19 1.5 12 1.5 12Z" />
                  <circle cx="12" cy="12" r="3" />
                </svg>
              </span>
              <div className="flex flex-col gap-0.5">
                <span className="wordmark text-[13px] leading-none text-foreground">
                  ARGUS
                </span>
                <span className="font-mono text-[11px] text-slate-text">
                  Remembers. Simulates. Built in the open.
                </span>
              </div>
            </div>

            <div className="flex items-center gap-2">
              <SocialIcon href="https://github.com/BeLazy167/argus" label="GitHub">
                <svg
                  viewBox="0 0 16 16"
                  width="14"
                  height="14"
                  fill="currentColor"
                  aria-hidden
                >
                  <path d="M8 0a8 8 0 0 0-2.53 15.59c.4.08.55-.17.55-.38v-1.33c-2.23.48-2.7-1.07-2.7-1.07-.36-.93-.89-1.18-.89-1.18-.72-.5.06-.49.06-.49.8.06 1.23.83 1.23.83.72 1.23 1.88.87 2.34.66.07-.52.28-.87.5-1.07-1.78-.2-3.65-.89-3.65-3.96 0-.88.31-1.59.83-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82a7.62 7.62 0 0 1 4 0c1.53-1.03 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.52.56.83 1.27.83 2.15 0 3.08-1.87 3.76-3.65 3.95.29.25.54.74.54 1.48v2.2c0 .21.15.47.55.38A8 8 0 0 0 8 0Z" />
                </svg>
              </SocialIcon>
              <SocialIcon href="https://x.com/belazyaf" label="X / Twitter">
                <svg
                  viewBox="0 0 16 16"
                  width="12"
                  height="12"
                  fill="currentColor"
                  aria-hidden
                >
                  <path d="M12.27 1.5h2.3l-5 5.72L15.5 14.5h-4.6l-3.6-4.7-4.12 4.7H.88l5.35-6.12L.5 1.5h4.72l3.25 4.3 3.8-4.3Zm-.8 11.6h1.27L4.6 2.84H3.22l8.25 10.26Z" />
                </svg>
              </SocialIcon>
            </div>
          </div>

          {/* Row 3 — copyright + status */}
          <div className="mt-6 flex flex-col items-start justify-between gap-2 border-t border-iron pt-6 font-mono text-[10.5px] uppercase tracking-[0.22em] text-slate-text sm:flex-row sm:items-center">
            <span>&copy; 2026 Argus</span>
            <span className="flex items-center gap-2">
              <span
                aria-hidden
                className="argus-ops-dot h-1.5 w-1.5 rounded-full bg-amber shadow-[0_0_6px_color-mix(in_oklch,var(--color-amber-glow)_80%,transparent)]"
              />
              <span>All systems operational</span>
            </span>
          </div>
        </FadeIn>
      </div>
    </InViewSection>
  );
}

/* Decorative corner tick marks — asymmetric, hand-drawn feel.
   Different sizes + offsets across corners so it doesn't read
   as a generic uniform frame. */
function CornerTicks() {
  return (
    <>
      {/* top-left — longest, clearest */}
      <span
        aria-hidden
        className="pointer-events-none absolute left-0 top-0 h-4 w-4 border-l border-t border-amber"
      />
      {/* top-right — shorter */}
      <span
        aria-hidden
        className="pointer-events-none absolute right-0 top-0 h-3 w-3 border-r border-t border-amber/80"
      />
      {/* bottom-left — shorter still, slight inset */}
      <span
        aria-hidden
        className="pointer-events-none absolute bottom-0 left-[2px] h-[10px] w-[10px] border-b border-l border-amber/70"
      />
      {/* bottom-right — medium */}
      <span
        aria-hidden
        className="pointer-events-none absolute bottom-0 right-0 h-[14px] w-[14px] border-b border-r border-amber/90"
      />
    </>
  );
}
