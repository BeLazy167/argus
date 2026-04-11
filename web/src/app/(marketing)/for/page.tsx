import type { Metadata } from "next";
import Link from "next/link";
import { personas } from "@/lib/pseo/personas";

export const metadata: Metadata = {
  title: "AI Code Review for Your Team — Argus",
  description:
    "How Argus fits different engineering teams: SRE, startup, platform engineering, and open-source maintainers.",
  alternates: { canonical: "https://argus.reviews/for" },
};

export default function ForHubPage() {
  return (
    <section className="mx-auto max-w-3xl px-6 py-28">
      <nav className="mb-8 text-xs font-mono text-slate-text">
        <Link href="/" className="hover:text-amber transition-colors duration-150">
          Home
        </Link>
        <span className="mx-2 text-iron">/</span>
        <span className="text-foreground">For Teams</span>
      </nav>

      <h1 className="font-display text-4xl md:text-5xl font-bold text-foreground mb-4">
        AI Code Review for Your Team
      </h1>
      <p className="text-sm font-sans text-slate-text leading-relaxed mb-12 max-w-2xl">
        Different teams have different review needs. See how Argus adapts to your
        workflow — from SRE teams tracing blast radius to open-source maintainers
        handling community PRs at scale.
      </p>

      <div className="space-y-3">
        {personas.map((p) => (
          <Link
            key={p.slug}
            href={`/for/${p.slug}`}
            className="block border border-iron px-5 py-4 hover:border-amber/30 transition-[border-color] duration-150"
          >
            <h2 className="font-mono text-sm font-bold text-foreground mb-1">
              {p.title.replace("AI Code Review for ", "")}
            </h2>
            <p className="text-xs font-sans text-slate-text">
              {p.subtitle}
            </p>
          </Link>
        ))}
      </div>
    </section>
  );
}
