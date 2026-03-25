import Link from "next/link";

export default function NotFound() {
  return (
    <div className="flex min-h-screen flex-col items-center justify-center bg-black p-8">
      <h1 className="mb-2 font-mono text-6xl font-bold text-zinc-700">404</h1>
      <p className="mb-6 font-mono text-sm text-zinc-500">Page not found</p>
      <Link
        href="/"
        className="rounded border border-amber-500/30 bg-amber-500/10 px-4 py-2 font-mono text-xs text-amber-500 transition-colors hover:bg-amber-500/20"
      >
        Back to home
      </Link>
    </div>
  );
}
