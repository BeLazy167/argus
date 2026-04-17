"use client";

import { useSyncExternalStore } from "react";

/**
 * Subscribe to a CSS media query. Re-renders when the query match flips.
 * SSR-safe — always returns `ssrDefault` during server render and the first client tick.
 *
 * Example:
 *   const isLg = useMediaQuery("(min-width: 1024px)");
 */
export function useMediaQuery(query: string, ssrDefault = false): boolean {
  return useSyncExternalStore(
    (cb) => {
      const mql = window.matchMedia(query);
      mql.addEventListener("change", cb);
      return () => mql.removeEventListener("change", cb);
    },
    () => window.matchMedia(query).matches,
    () => ssrDefault,
  );
}
