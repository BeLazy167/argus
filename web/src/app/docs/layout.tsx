import Link from "next/link";
import type { ReactNode } from "react";

/**
 * Minimal docs shell. Sidebar nav + prose content area.
 * Uses plain TSX (no MDX dependency) to avoid heavy infra for v1.
 */
export default function DocsLayout({ children }: { children: ReactNode }) {
  return (
    <div className="flex min-h-screen bg-[#0a0a12] text-slate-200">
      <aside className="w-64 border-r border-slate-800 p-6 shrink-0">
        <Link href="/" className="block text-sm font-mono text-slate-300 hover:text-slate-100 mb-6">
          ← Back
        </Link>
        <h2 className="text-xs font-mono text-slate-500 uppercase mb-3 tracking-wider">Docs</h2>
        <nav className="space-y-1">
          <DocLink href="/docs/features/issue-acceptance">Issue acceptance</DocLink>
          <DocLink href="/docs/features/cross-pr-checks">Cross-repo PR checks</DocLink>
          <DocLink href="/docs/faq">FAQ</DocLink>
        </nav>
      </aside>
      <main className="flex-1 max-w-3xl p-10 font-mono text-slate-300 leading-relaxed">
        {children}
      </main>
    </div>
  );
}

function DocLink({ href, children }: { href: string; children: ReactNode }) {
  return (
    <Link
      href={href}
      className="block text-sm text-slate-400 hover:text-slate-200 py-1 transition-colors"
    >
      {children}
    </Link>
  );
}
