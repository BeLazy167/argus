"use client";

import { motion, useScroll, useSpring } from "motion/react";

/**
 * Spring-damped scroll progress indicator pinned to the top of the viewport.
 *
 * Reads document scroll via Motion's `useScroll` and pipes it through a spring
 * so the bar trails the cursor-like speed of raw scroll — gives the motion a
 * physical feel rather than a linear drip. Compositor-thread transforms only;
 * React never re-renders on scroll.
 *
 * Stiffness/damping tuned for a single noticeable bounce settle on fast flings,
 * and near-imperceptible overshoot on slow scrolls. `restDelta` quantizes the
 * spring off when close to target so the final pixel doesn't drift.
 *
 * `prefers-reduced-motion` is honoured via the global CSS override that zeroes
 * animation/transition durations — Motion respects it by default too.
 */
export function ScrollProgress() {
  const { scrollYProgress } = useScroll();
  const scaleX = useSpring(scrollYProgress, {
    stiffness: 140,
    damping: 28,
    mass: 0.5,
    restDelta: 0.001,
  });

  return (
    <motion.div
      aria-hidden="true"
      style={{ scaleX, transformOrigin: "0% 50%" }}
      className="pointer-events-none fixed inset-x-0 top-0 z-[60] h-[2px] bg-amber/80 shadow-[0_0_10px_color-mix(in_oklch,var(--color-amber-glow)_60%,transparent)]"
    />
  );
}
