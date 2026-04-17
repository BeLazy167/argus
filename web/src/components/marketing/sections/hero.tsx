import { HeroDitheringCard } from "@/components/ui/hero-dithering-card";

/**
 * Hero — dithering-card variant.
 *
 * The hero is now the `HeroDitheringCard` primitive from `/components/ui`:
 * a rounded card with a subtle WebGL dithering shader behind copy. Copy,
 * tokens, and CTAs are Argus-specific; the primitive itself is reusable.
 *
 * The shader plays in `colorFront: var(--color-amber-glow)` at ~0.22 opacity
 * with screen blending, so it reads as ambient texture rather than content —
 * headline and CTA stay the focal point. Speed ramps on hover.
 */
export function Hero() {
  return (
    <section
      id="hero"
      aria-labelledby="hero-title"
      className="relative bg-background pb-20 pt-10 sm:pt-16 lg:pb-28 lg:pt-20"
    >
      <HeroDitheringCard
        pillLabel="Beta · Built in the open"
        headline={
          <span id="hero-title" className="block">
            <span className="block text-slate-text/85">
              Argus remembers what{" "}
              <span className="italic text-amber-glow">broke last time.</span>
            </span>
            <span className="mt-2 block text-slate-text/85">
              It simulates what could{" "}
              <span className="italic text-amber-glow">break next.</span>
            </span>
          </span>
        }
        subcopy={
          <>
            <span className="text-foreground">Learns</span> from your team&rsquo;s review
            feedback. <span className="text-foreground">Simulates</span> real failure
            scenarios against your diff.{" "}
            <span className="text-foreground">Runs on your own keys</span> — zero hidden
            costs.
          </>
        }
        primaryHref="/sign-up"
        primaryLabel={
          <>
            <GithubMark />
            Install GitHub App
            <span
              className="mx-1 h-3 w-px bg-primary-foreground/40"
              aria-hidden="true"
            />
            <span className="text-[11px] tracking-[0.14em] text-primary-foreground/80">
              free
            </span>
            <CaretRight />
          </>
        }
        secondaryHref="/compare"
        secondaryLabel={<>vs. CodeRabbit, Greptile &amp; Cubic &rarr;</>}
      />
    </section>
  );
}

function GithubMark() {
  return (
    <svg
      viewBox="0 0 16 16"
      width="14"
      height="14"
      fill="currentColor"
      aria-hidden="true"
    >
      <path d="M8 0a8 8 0 0 0-2.53 15.59c.4.08.55-.17.55-.38v-1.33c-2.23.48-2.7-1.07-2.7-1.07-.36-.93-.89-1.18-.89-1.18-.72-.5.06-.49.06-.49.8.06 1.23.83 1.23.83.72 1.23 1.88.87 2.34.66.07-.52.28-.87.5-1.07-1.78-.2-3.65-.89-3.65-3.96 0-.88.31-1.59.83-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82a7.62 7.62 0 0 1 4 0c1.53-1.03 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.52.56.83 1.27.83 2.15 0 3.08-1.87 3.76-3.65 3.95.29.25.54.74.54 1.48v2.2c0 .21.15.47.55.38A8 8 0 0 0 8 0Z" />
    </svg>
  );
}

function CaretRight() {
  return (
    <svg
      viewBox="0 0 16 16"
      width="14"
      height="14"
      fill="currentColor"
      aria-hidden="true"
    >
      <path d="M6 4l4 4-4 4" stroke="currentColor" strokeWidth="1.5" fill="none" />
    </svg>
  );
}
