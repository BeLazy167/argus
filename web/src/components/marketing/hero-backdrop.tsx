"use client";

import { useRef } from "react";
import { motion, useReducedMotion, useScroll, useTransform } from "motion/react";

/**
 * Parallax hero backdrop — decorative glow / ember / horizon / dot-matrix
 * layers drift at different rates as the user scrolls past the hero, giving
 * the otherwise-flat background perceived depth.
 *
 * All layers are already `absolute inset-0 pointer-events-none` decorations;
 * parallax is applied only via `translateY` + `scale` (compositor-only, no
 * layout). The glow and ember move most, the horizon less, the dot matrix
 * barely — mirroring real-world depth where distant objects shift less.
 *
 * `useScroll({ target, offset: ["start start", "end start"] })` measures the
 * hero's own scroll progress from 0 (top of section pinned to top of viewport)
 * to 1 (bottom of section passes top of viewport) — so the transforms only
 * activate while the hero is visible and don't trigger re-renders once it's
 * scrolled past.
 *
 * Respects `prefers-reduced-motion`: when set, the hook suspends the transform
 * subscription and everything stays at its initial position.
 */
export function HeroBackdrop() {
  const ref = useRef<HTMLDivElement>(null);
  const reduce = useReducedMotion();
  const { scrollYProgress } = useScroll({
    target: ref,
    offset: ["start start", "end start"],
  });

  // Distance each layer drifts over the hero's scroll range (0 → 1).
  // Negative values move up as user scrolls down → classic parallax.
  const glowY = useTransform(scrollYProgress, [0, 1], [0, reduce ? 0 : -140]);
  const glowScale = useTransform(scrollYProgress, [0, 1], [1, reduce ? 1 : 1.15]);
  const emberY = useTransform(scrollYProgress, [0, 1], [0, reduce ? 0 : -90]);
  const horizonY = useTransform(scrollYProgress, [0, 1], [0, reduce ? 0 : -60]);
  const dotsY = useTransform(scrollYProgress, [0, 1], [0, reduce ? 0 : -30]);
  const dotsOpacity = useTransform(scrollYProgress, [0, 0.6, 1], [0.28, 0.18, 0.1]);

  return (
    <div
      ref={ref}
      aria-hidden="true"
      className="pointer-events-none absolute inset-0"
    >
      {/* One anchored amber glow, off-axis — not centered */}
      <motion.div
        style={{ y: glowY, scale: glowScale }}
        className="absolute left-[12%] top-[-4%] h-[560px] w-[560px] opacity-[0.24] blur-[140px] will-change-transform"
      >
        <div
          className="h-full w-full"
          style={{
            background:
              "radial-gradient(circle at center, color-mix(in oklch, var(--color-amber-glow) 75%, transparent) 0%, transparent 68%)",
          }}
        />
      </motion.div>

      {/* Far-right cooler ember hint */}
      <motion.div
        style={{ y: emberY }}
        className="absolute right-[-6%] top-[340px] h-[360px] w-[360px] opacity-[0.10] blur-[160px] will-change-transform"
      >
        <div
          className="h-full w-full"
          style={{
            background:
              "radial-gradient(circle at center, color-mix(in oklch, var(--color-amber-glow) 60%, transparent) 0%, transparent 70%)",
          }}
        />
      </motion.div>

      {/* Thin amber horizon rule */}
      <motion.div
        style={{ y: horizonY }}
        className="absolute left-0 right-0 top-[420px] h-px will-change-transform"
      >
        <div
          className="h-full w-full"
          style={{
            background:
              "linear-gradient(90deg, transparent 0%, color-mix(in oklch, var(--color-amber-glow) 28%, transparent) 18%, color-mix(in oklch, var(--color-amber-glow) 40%, transparent) 50%, color-mix(in oklch, var(--color-amber-glow) 28%, transparent) 82%, transparent 100%)",
          }}
        />
      </motion.div>

      {/* Dot matrix — sparser, fades with scroll */}
      <motion.div
        style={{ y: dotsY, opacity: dotsOpacity }}
        className="absolute inset-0 will-change-transform"
      >
        <div
          className="h-full w-full"
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
      </motion.div>
    </div>
  );
}
