"use client";

import { useEffect } from "react";

/**
 * Applies the stored theme on initial client-side render to prevent flash.
 * Runs once on mount — the ThemeToggle component handles subsequent changes.
 */
export function ThemeScript() {
  useEffect(() => {
    const stored = localStorage.getItem("argus-theme");
    const theme =
      stored === "light" || stored === "dark"
        ? stored
        : window.matchMedia("(prefers-color-scheme: light)").matches
          ? "light"
          : "dark";
    const html = document.documentElement;
    html.classList.remove("dark", "light");
    html.classList.add(theme);
    html.style.colorScheme = theme;
  }, []);

  return null;
}
