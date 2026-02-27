import Link from "next/link";
import { SignedIn, SignedOut } from "@clerk/nextjs";

export function Navbar() {
  return (
    <nav className="fixed top-0 z-50 w-full border-b border-iron/50 bg-void/80 backdrop-blur-md">
      <div className="mx-auto flex h-14 max-w-6xl items-center justify-between px-6">
        <Link href="/" className="wordmark text-sm text-amber tracking-[0.2em]">
          ARGUS
        </Link>

        <div className="flex items-center gap-8">
          <Link
            href="/pricing"
            className="text-xs font-mono text-slate-text hover:text-ash transition-colors"
          >
            Pricing
          </Link>
          <Link
            href="/docs"
            className="text-xs font-mono text-slate-text hover:text-ash transition-colors"
          >
            Docs
          </Link>
          <Link
            href="/blog"
            className="text-xs font-mono text-slate-text hover:text-ash transition-colors"
          >
            Blog
          </Link>

          <SignedOut>
            <Link
              href="/sign-in"
              className="text-xs font-mono text-slate-text hover:text-ash transition-colors"
            >
              Sign in
            </Link>
            <Link
              href="/sign-up"
              className="inline-flex h-8 items-center rounded-md bg-amber px-4 text-xs font-mono font-medium text-void transition-all hover:brightness-110"
            >
              Get started
            </Link>
          </SignedOut>
          <SignedIn>
            <Link
              href="/dashboard"
              className="inline-flex h-8 items-center rounded-md bg-amber px-4 text-xs font-mono font-medium text-void transition-all hover:brightness-110"
            >
              Dashboard
            </Link>
          </SignedIn>
        </div>
      </div>
    </nav>
  );
}
