"use client";

export default function MarketingError({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  return (
    <div className="flex min-h-screen flex-col items-center justify-center bg-black p-8">
      <div className="rounded-lg border border-red-500/20 bg-red-500/5 p-6 text-center">
        <h2 className="mb-2 font-mono text-sm font-medium text-red-400">
          Something went wrong
        </h2>
        <p className="mb-4 font-mono text-xs text-zinc-500">
          {error.message || "An unexpected error occurred"}
        </p>
        <button
          onClick={reset}
          className="rounded border border-amber-500/30 bg-amber-500/10 px-4 py-2 font-mono text-xs text-amber-500 transition-colors hover:bg-amber-500/20"
        >
          Try again
        </button>
      </div>
    </div>
  );
}
