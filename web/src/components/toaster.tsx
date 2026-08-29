"use client";

import { Toaster } from "sonner";

/**
 * App-wide toast surface. Styled with the Argus theme tokens (charcoal / iron /
 * amber) via `unstyled` + `classNames` so toasts read correctly on both the
 * dark marketing shell and the light/dark dashboard rather than following
 * Sonner's own `theme` prop (which tracks `prefers-color-scheme`, not our class).
 */
export function AppToaster() {
  return (
    <Toaster
      position="bottom-right"
      gap={10}
      toastOptions={{
        unstyled: true,
        classNames: {
          toast:
            "flex w-full items-center gap-2.5 rounded-sm border border-iron bg-charcoal px-3.5 py-3 font-mono text-xs text-foreground shadow-lg shadow-black/40",
          title: "font-medium text-foreground",
          description: "text-slate-text",
          icon: "shrink-0",
          success: "border-amber/40",
          error: "border-red-500/40",
          closeButton: "text-slate-text hover:text-foreground",
        },
      }}
    />
  );
}
