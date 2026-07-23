"use client";

import { useEffect } from "react";
import { toggleTheme } from "./theme-toggle";

const TEXT_ENTRY_TAGS = new Set(["INPUT", "TEXTAREA", "SELECT"]);

/**
 * Cmd/Ctrl+Shift+D toggles the dashboard theme. Ignores keystrokes that
 * originate in text-entry controls (inputs, textareas, selects, contenteditable)
 * so it never hijacks a user typing. Dashboard-only — mounted from the dashboard
 * layout, so marketing and auth stay dark.
 */
export function ThemeHotkey() {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (!(e.metaKey || e.ctrlKey) || !e.shiftKey) return;
      if (e.key.toLowerCase() !== "d") return;
      const target = e.target as HTMLElement | null;
      if (target && (TEXT_ENTRY_TAGS.has(target.tagName) || target.isContentEditable)) return;
      e.preventDefault();
      toggleTheme();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, []);

  return null;
}
