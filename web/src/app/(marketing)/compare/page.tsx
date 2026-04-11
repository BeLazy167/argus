import type { Metadata } from "next";
import Link from "next/link";
import { competitors } from "@/lib/pseo/competitors";

export const metadata: Metadata = {
  title: "Argus vs Alternatives — AI Code Review Comparisons",
  description:
    "Honest feature comparisons between Argus and other AI code review tools. See how Argus compares to CodeRabbit, SonarQube, GitHub Copilot, and more.",
  alternates: { canonical: "https://argus.reviews/compare" },
};

export default function CompareHubPage() {
  return (
    <section className="mx-auto max-w-3xl px-6 py-28">
      <nav className="mb-8 text-xs font-mono text-slate-text">
        <Link href="/" className="hover:text-amber transition-colors duration-150">
          Home
        </Link>
        <span className="mx-2 text-iron">/</span>
        <span className="text-foreground">Compare</span>
      </nav>

      <h1 className="font-display text-4xl md:text-5xl font-bold text-foreground mb-4">
        Argus vs Alternatives
      </h1>
      <p className="text-sm font-sans text-slate-text leading-relaxed mb-12 max-w-2xl">
        Honest, feature-by-feature comparisons between Argus and other code review
        tools. We highlight where competitors excel and where Argus pulls ahead —
        because the right tool depends on your team's needs.
      </p>

      <div className="space-y-3">
        {competitors.map((c) => (
          <Link
            key={c.slug}
            href={`/compare/${c.slug}`}
            className="block border border-iron px-5 py-4 hover:border-amber/30 transition-[border-color] duration-150"
          >
            <div className="flex items-baseline justify-between mb-1">
              <h2 className="font-mono text-sm font-bold text-foreground">
                Argus vs {c.name}
              </h2>
              <span className="text-[11px] font-mono text-iron">{c.pricing}</span>
            </div>
            <p className="text-xs font-sans text-slate-text">{c.tagline}</p>
          </Link>
        ))}
      </div>
    </section>
  );
}
