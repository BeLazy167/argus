"use client";

import { createContext, useContext, useEffect, useState } from "react";
import {
  getStoredTheme,
  toggleTheme,
  THEME_CHANGE_EVENT,
  type Theme,
} from "@/components/dashboard/theme-toggle";

type ThemeContextValue = { theme: Theme; toggle: () => void };

const ThemeContext = createContext<ThemeContextValue | null>(null);

export function useTheme(): ThemeContextValue {
  const ctx = useContext(ThemeContext);
  if (!ctx) throw new Error("useTheme must be used within ThemeProvider");
  return ctx;
}

/**
 * App-shell theme boundary. Tracks the active class-based theme and exposes it
 * plus a toggle via context. It is intentionally observational: it does NOT
 * force a theme. Per-route application stays route-scoped — DashboardTheme
 * applies the stored preference inside the dashboard, while marketing and auth
 * render dark by default — so mounting this at the root shell does not disturb
 * the dark-only marketing surface.
 */
export function ThemeProvider({
  attribute = "class",
  children,
}: {
  attribute?: "class" | "data-theme";
  children: React.ReactNode;
}) {
  const [theme, setTheme] = useState<Theme>("dark");

  useEffect(() => {
    setTheme(getStoredTheme());
    const onChange = (e: Event) => {
      const next = (e as CustomEvent<Theme>).detail;
      if (next === "light" || next === "dark") setTheme(next);
    };
    window.addEventListener(THEME_CHANGE_EVENT, onChange);
    return () => window.removeEventListener(THEME_CHANGE_EVENT, onChange);
  }, []);

  // `attribute` documents the theming strategy (class-based); referenced so the
  // shell boundary is self-describing.
  void attribute;

  return (
    <ThemeContext.Provider value={{ theme, toggle: toggleTheme }}>
      {children}
    </ThemeContext.Provider>
  );
}
