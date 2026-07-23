"use client";

import { useCallback, useEffect, useState } from "react";
import { Sun, Moon } from "lucide-react";

export type Theme = "dark" | "light";

/**
 * Fired on `window` whenever the theme changes so same-tab listeners (the toggle
 * button, other dashboard surfaces) can sync. The native `storage` event only
 * fires cross-tab, so a custom event is needed for same-tab coordination.
 */
export const THEME_CHANGE_EVENT = "argus-theme-change";

export function getStoredTheme(): Theme {
  if (typeof window === "undefined") return "dark";
  const stored = localStorage.getItem("argus-theme") as Theme | null;
  if (stored === "light" || stored === "dark") return stored;
  return "dark";
}

export function applyTheme(theme: Theme) {
  const html = document.documentElement;
  html.classList.remove("dark", "light");
  html.classList.add(theme);
  html.style.colorScheme = theme;
  localStorage.setItem("argus-theme", theme);
  window.dispatchEvent(new CustomEvent<Theme>(THEME_CHANGE_EVENT, { detail: theme }));
}

/**
 * Flips the currently-applied theme. Reads the live `<html>` class (the true
 * visual state) rather than storage, then persists and applies the opposite.
 * Shared by the toggle button, the Cmd+Shift+D hotkey, and the command menu.
 */
export function toggleTheme(): Theme {
  const current: Theme = document.documentElement.classList.contains("light") ? "light" : "dark";
  const next: Theme = current === "dark" ? "light" : "dark";
  applyTheme(next);
  return next;
}

export function ThemeToggle() {
  const [theme, setTheme] = useState<Theme>("dark");

  useEffect(() => {
    const t = getStoredTheme();
    setTheme(t);
    applyTheme(t);
  }, []);

  // Keep the icon in sync when the theme is flipped elsewhere (hotkey / command menu).
  useEffect(() => {
    const onChange = (e: Event) => {
      const next = (e as CustomEvent<Theme>).detail;
      if (next === "light" || next === "dark") setTheme(next);
    };
    window.addEventListener(THEME_CHANGE_EVENT, onChange);
    return () => window.removeEventListener(THEME_CHANGE_EVENT, onChange);
  }, []);

  const toggle = useCallback(() => {
    setTheme(toggleTheme());
  }, []);

  return (
    <button
      onClick={toggle}
      className="p-1.5 text-slate-text hover:text-foreground transition-colors"
      title={theme === "dark" ? "Switch to light mode" : "Switch to dark mode"}
      aria-label={theme === "dark" ? "Switch to light mode" : "Switch to dark mode"}
    >
      {theme === "dark" ? <Sun className="h-3.5 w-3.5" /> : <Moon className="h-3.5 w-3.5" />}
    </button>
  );
}
