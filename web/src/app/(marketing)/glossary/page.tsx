import type { Metadata } from "next";
import Link from "next/link";
import { glossaryTerms } from "@/lib/pseo/glossary";

export const metadata: Metadata = {
  title: "Code Review Glossary — Argus",
  description:
    "Definitions and deep dives on code review concepts: institutional memory, architecture tracing, failure scenario testing, PR enrichment, and more.",
  alternates: { canonical: "https://argus.reviews/glossary" },
};

export default function GlossaryHubPage() {
  return (
    <section className="mx-auto max-w-3xl px-6 py-28">
      <nav className="mb-8 text-xs font-mono text-slate-text">
        <Link href="/" className="hover:text-amber transition-colors duration-150">
          Home
        </Link>
        <span className="mx-2 text-iron">/</span>
        <span className="text-foreground">Glossary</span>
      </nav>

      <h1 className="font-display text-4xl md:text-5xl font-bold text-foreground mb-4">
        Code Review Glossary
      </h1>
      <p className="text-sm font-sans text-slate-text leading-relaxed mb-12 max-w-2xl">
        In-depth explanations of the concepts that power modern code review — from
        institutional memory to failure scenario simulation. Each term includes
        why it matters and how Argus implements it.
      </p>

      <div className="space-y-3">
        {glossaryTerms.map((t) => (
          <Link
            key={t.slug}
            href={`/glossary/${t.slug}`}
            className="block border border-iron px-5 py-4 hover:border-amber/30 transition-[border-color] duration-150"
          >
            <h2 className="font-mono text-sm font-bold text-foreground mb-1">
              {t.term}
            </h2>
            <p className="text-xs font-sans text-slate-text line-clamp-2">
              {t.definition.slice(0, 140)}…
            </p>
          </Link>
        ))}
      </div>
    </section>
  );
}
