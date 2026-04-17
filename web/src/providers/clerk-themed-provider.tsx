"use client";

import { ClerkProvider } from "@clerk/nextjs";
import { dark } from "@clerk/themes";
import { useSyncExternalStore } from "react";

type Theme = "dark" | "light";

function subscribe(cb: () => void) {
  // Watch html[class] for theme toggle changes. ThemeToggle flips the class; Clerk's
  // baseTheme then re-resolves so its internal labels (org switcher, user menu) stop
  // rendering dark-theme defaults on top of a light sidebar.
  const observer = new MutationObserver(cb);
  observer.observe(document.documentElement, { attributes: true, attributeFilter: ["class"] });
  return () => observer.disconnect();
}

function getTheme(): Theme {
  if (typeof document === "undefined") return "dark";
  return document.documentElement.classList.contains("light") ? "light" : "dark";
}

/**
 * Wraps ClerkProvider and flips `baseTheme` in sync with Argus's theme class on <html>.
 * Clerk's appearance.variables stay branded (amber primary, JetBrains Mono) across both
 * themes — only the surface color tokens follow light/dark.
 */
export function ClerkThemedProvider({ children }: { children: React.ReactNode }) {
  const theme = useSyncExternalStore(
    subscribe,
    getTheme,
    () => "dark" as Theme, // SSR fallback — hydration flips to the real value
  );

  const isDark = theme === "dark";

  return (
    <ClerkProvider
      appearance={{
        baseTheme: isDark ? dark : undefined,
        variables: {
          colorPrimary: "#F5A623",
          colorBackground: isDark ? "#1A1A1A" : "#FFFFFF",
          colorText: isDark ? "#F5F5F5" : "#1A1A1A",
          colorInputBackground: isDark ? "#2C2C2C" : "#F5F5F7",
          colorInputText: isDark ? "#F5F5F5" : "#1A1A1A",
          fontFamily: '"JetBrains Mono", monospace',
        },
      }}
    >
      {children}
    </ClerkProvider>
  );
}
