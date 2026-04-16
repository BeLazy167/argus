"use client";

import { useEffect, useRef, useState } from "react";

/**
 * Fade + rise reveal as content enters the viewport.
 *
 * Progressive enhancement:
 *   1. SSR renders fully opaque (crawlers + no-JS readers see everything).
 *   2. Modern browsers (`animation-timeline: view()` supported) let the
 *      compositor drive the entry animation off scroll position via the
 *      `.scroll-reveal` class in globals.css — zero main-thread work.
 *   3. Browsers without support fall back to an IntersectionObserver
 *      that toggles a tailwind transition once on first intersection.
 *
 * `delay` is only meaningful on the JS fallback path. Browsers running the
 * CSS scroll-timeline path ignore it — the view-range itself encodes the
 * perceived stagger since items enter at different scroll positions.
 */
export function FadeIn({
  children,
  delay = 0,
  className = "",
}: {
  children: React.ReactNode;
  delay?: number;
  className?: string;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const [mounted, setMounted] = useState(false);
  const [supportsScrollTimeline, setSupportsScrollTimeline] = useState(false);
  const [visible, setVisible] = useState(false);

  useEffect(() => {
    setMounted(true);
    if (typeof CSS !== "undefined" && CSS.supports?.("animation-timeline: view()")) {
      setSupportsScrollTimeline(true);
    }
  }, []);

  useEffect(() => {
    if (!mounted || supportsScrollTimeline) return;
    const el = ref.current;
    if (!el) return;
    const observer = new IntersectionObserver(
      ([entry]) => {
        if (entry?.isIntersecting) {
          if (delay > 0) setTimeout(() => setVisible(true), delay);
          else setVisible(true);
          observer.disconnect();
        }
      },
      { threshold: 0.05 },
    );
    observer.observe(el);
    const fallback = setTimeout(() => setVisible(true), 2000 + delay);
    return () => {
      observer.disconnect();
      clearTimeout(fallback);
    };
  }, [mounted, supportsScrollTimeline, delay]);

  // SSR / pre-hydration: visible by default.
  if (!mounted) {
    return (
      <div ref={ref} className={className}>
        {children}
      </div>
    );
  }

  // Modern path: let CSS drive the reveal.
  if (supportsScrollTimeline) {
    return (
      <div ref={ref} className={`scroll-reveal ${className}`}>
        {children}
      </div>
    );
  }

  // Fallback: JS toggle + tailwind transition.
  const animClass = `transition-[opacity,transform] duration-700 ease-[cubic-bezier(0.23,1,0.32,1)] ${
    visible ? "opacity-100 translate-y-0" : "opacity-0 translate-y-4"
  }`;

  return (
    <div ref={ref} className={`${animClass} ${className}`}>
      {children}
    </div>
  );
}
