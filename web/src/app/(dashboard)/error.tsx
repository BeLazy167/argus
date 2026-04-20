"use client";

import { useEffect } from "react";
import posthog from "posthog-js";
import { usePathname } from "next/navigation";

/**
 * Next.js App Router error boundary for the dashboard segment.
 *
 * Routes any render-time crash to PostHog so unknown failures surface in the
 * same `Errors` dashboard as `log.error`/`log.warn` from the backend. The
 * existing minimal UI is preserved — only telemetry is added.
 */
export default function DashboardError({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  const pathname = usePathname();

  useEffect(() => {
    posthog.captureException(error, {
      digest: error.digest,
      route: pathname,
    });
  }, [error, pathname]);

  return (
    <div className="flex h-full flex-col items-center justify-center gap-4 p-8">
      <div className="border border-red-500/20 bg-red-500/5 p-6 text-center">
        <h2 className="mb-2 font-mono text-sm font-medium text-red-400">
          Something went wrong
        </h2>
        <p className="mb-4 font-mono text-xs text-zinc-500">
          {error.message || "An unexpected error occurred"}
        </p>
        <button
          type="button"
          onClick={reset}
          className="rounded border border-zinc-700 bg-zinc-800 px-4 py-1.5 font-mono text-xs text-zinc-300 transition-colors hover:bg-zinc-700"
        >
          Try again
        </button>
      </div>
    </div>
  );
}
