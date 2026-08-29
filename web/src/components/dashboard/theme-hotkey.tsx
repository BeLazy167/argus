"use client";

import { useEffect } from "react";
import { applyTheme } from "./theme-toggle";

/**
 * Cmd/Ctrl+Shift+D toggles the dashboard theme. Guards text-entry targets
 * (input, textarea, select, contenteditable) FIRST so it never fires while the
 * user is typing, then handles the modifier chord. Dashboard-only — mounted from
 * the dashboard layout, so marketing and auth stay dark.
 */
export function ThemeHotkey() {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement | null;
      const tag = target?.tagName;
      if (
        tag === "INPUT" ||
        tag === "TEXTAREA" ||
        tag === "SELECT" ||
        target?.isContentEditable
      ) {
        return;
      }
      if (!(e.metaKey || e.ctrlKey) || !e.shiftKey) return;
      if (e.key.toLowerCase() !== "d") return;
      e.preventDefault();
      // Flip the dark class; applyTheme normalizes the paired light class,
      // colorScheme, storage, and the same-tab change event.
      const next = document.documentElement.classList.toggle("dark") ? "dark" : "light";
      applyTheme(next);
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, []);

  return null;
}
