"use client";

import { useEffect } from "react";

/**
 * Owns the dashboard's theme divergence — the dashboard is the only surface
 * that may render light.
 *
 * On enter, applies the user's stored preference (default dark). On leave,
 * restores dark so marketing and auth stay dark-only even across client-side
 * navigation, where the shared root <html> element (and any class the toggle
 * imperatively set) persists and is never re-rendered back to dark.
 *
 * This runs independently of the ThemeToggle button, which is only mounted when
 * the sidebar is expanded; without this, a collapsed sidebar on a fresh load
 * would strand a stored light preference in dark.
 *
 * Does not write localStorage — the stored preference is preserved for the next
 * dashboard visit; only the applied <html> class follows the route.
 */
export function DashboardTheme() {
  useEffect(() => {
    const html = document.documentElement;
    const stored = localStorage.getItem("argus-theme");
    const theme = stored === "light" || stored === "dark" ? stored : "dark";
    html.classList.remove("dark", "light");
    html.classList.add(theme);
    html.style.colorScheme = theme;
    return () => {
      html.classList.remove("dark", "light");
      html.classList.add("dark");
      html.style.colorScheme = "dark";
    };
  }, []);

  return null;
}
