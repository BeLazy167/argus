"use client";

import { useEffect, useRef, useState } from "react";

/**
 * Fade-in on scroll via IntersectionObserver.
 *
 * SSR: renders content fully visible (opacity-1) so crawlers see all text.
 * Client: after hydration, hides content then fades in when scrolled into view.
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
  const [visible, setVisible] = useState(false);

  // Mark as mounted after hydration
  useEffect(() => setMounted(true), []);

  useEffect(() => {
    if (!mounted) return;
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
      { threshold: 0.05 }
    );
    observer.observe(el);
    const fallback = setTimeout(() => setVisible(true), 2000 + delay);
    return () => { observer.disconnect(); clearTimeout(fallback); };
  }, [mounted, delay]);

  // SSR: no classes (content visible at opacity 1)
  // Client mounted but not yet visible: opacity-0
  // Client visible: opacity-1 with animation
  const animClass = mounted
    ? `transition-[opacity,transform] duration-700 ease-[cubic-bezier(0.23,1,0.32,1)] ${
        visible ? "opacity-100 translate-y-0" : "opacity-0 translate-y-4"
      }`
    : "";

  return (
    <div ref={ref} className={`${animClass} ${className}`}>
      {children}
    </div>
  );
}
