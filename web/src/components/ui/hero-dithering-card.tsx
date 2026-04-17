"use client";

import Link from "next/link";
import { Suspense, lazy, useState, type ReactNode } from "react";

// Lazy-load the WebGL shader — it only runs client-side, not part of SSR bundle.
// The `default` export wrap is required because `@paper-design/shaders-react`
// exports `Dithering` as a named export, not default.
const Dithering = lazy(() =>
  import("@paper-design/shaders-react").then((mod) => ({ default: mod.Dithering })),
);

/**
 * Hero-dithering-card — an animated-shader rounded card used as a landing-page
 * hero surface. The dithering shader runs at low opacity behind the content so
 * the motion reads as ambient texture rather than decoration.
 *
 * Design-system-faithful: uses Argus's amber / mono / void tokens — the shader's
 * `colorFront` pulls from `--color-amber-glow` so rebrands propagate. The card
 * has a soft-rounded 48px corner to contrast Argus's sharper section borders,
 * creating visual focus on the hero.
 *
 * Interactive: shader `speed` roughly doubles on hover, giving the surface a
 * sense of attention. Respects prefers-reduced-motion via the global CSS
 * override that zeroes animation durations.
 *
 * Slots: caller passes pill label + headline + subcopy + CTAs as children of
 * the shape — keeps this component free of copy decisions.
 */
export function HeroDitheringCard({
  pillIcon,
  pillLabel,
  headline,
  subcopy,
  primaryHref,
  primaryLabel,
  secondaryHref,
  secondaryLabel,
}: {
  pillIcon?: ReactNode;
  pillLabel: string;
  headline: ReactNode;
  subcopy: ReactNode;
  primaryHref: string;
  primaryLabel: ReactNode;
  secondaryHref?: string;
  secondaryLabel?: ReactNode;
}) {
  const [isHovered, setIsHovered] = useState(false);

  return (
    <section className="w-full px-4 py-12 md:px-6 md:py-16">
      <div
        className="relative mx-auto w-full max-w-[1280px]"
        onMouseEnter={() => setIsHovered(true)}
        onMouseLeave={() => setIsHovered(false)}
      >
        <div className="relative flex min-h-[600px] flex-col items-center justify-center overflow-hidden rounded-[48px] border border-iron bg-charcoal/40 shadow-[0_40px_120px_-40px_color-mix(in_oklch,var(--color-amber-glow)_18%,transparent)] backdrop-blur-sm">
          <Suspense fallback={<div className="absolute inset-0 bg-charcoal/30" />}>
            <div className="pointer-events-none absolute inset-0 z-0 opacity-[0.22] mix-blend-screen">
              <Dithering
                colorBack="#00000000"
                colorFront="var(--color-amber-glow)"
                shape="warp"
                type="4x4"
                speed={isHovered ? 0.55 : 0.18}
                className="size-full"
                minPixelRatio={1}
              />
            </div>
          </Suspense>

          <div className="relative z-10 mx-auto flex max-w-[940px] flex-col items-center px-6 text-center">
            <div className="mb-8 inline-flex items-center gap-2.5 rounded-full border border-amber/20 bg-amber/5 px-4 py-1.5 font-mono text-[11px] uppercase tracking-[0.18em] text-amber backdrop-blur-sm">
              <span className="relative flex h-2 w-2">
                <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-amber opacity-75" />
                <span className="relative inline-flex h-2 w-2 rounded-full bg-amber" />
              </span>
              {pillIcon}
              <span>{pillLabel}</span>
            </div>

            <h1 className="mb-8 font-mono text-[44px] font-bold leading-[1.05] tracking-[-0.02em] text-foreground sm:text-[64px] md:text-[72px] lg:text-[84px]">
              {headline}
            </h1>

            <p className="mb-12 max-w-[56ch] font-mono text-[15px] leading-[1.7] text-slate-text md:text-[16px]">
              {subcopy}
            </p>

            <div className="flex flex-col items-center gap-5 sm:flex-row sm:gap-6">
              <Link
                href={primaryHref}
                className="group relative inline-flex h-12 items-center gap-2.5 overflow-hidden rounded-full bg-amber px-8 font-mono text-[13px] font-semibold uppercase tracking-[0.12em] text-primary-foreground transition-[transform,background-color] duration-150 ease-[cubic-bezier(0.23,1,0.32,1)] hover:scale-[1.03] hover:bg-amber-glow"
              >
                {primaryLabel}
              </Link>

              {secondaryHref && secondaryLabel && (
                <Link
                  href={secondaryHref}
                  className="group inline-flex items-center gap-2 font-mono text-[13px] text-slate-text transition-colors duration-150 hover:text-amber"
                >
                  <span className="underline decoration-iron decoration-dotted underline-offset-[6px] transition-colors group-hover:decoration-amber">
                    {secondaryLabel}
                  </span>
                </Link>
              )}
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}
