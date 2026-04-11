import type { Metadata } from "next";
import Link from "next/link";
import { glossaryTerms, glossarySlugs, getGlossaryTerm } from "@/lib/pseo/glossary";

export function generateStaticParams() {
  return glossarySlugs.map((slug) => ({ slug }));
}

export function generateMetadata({
  params,
}: {
  params: Promise<{ slug: string }>;
}): Promise<Metadata> {
  return params.then(({ slug }) => {
    const t = getGlossaryTerm(slug);
    if (!t) return { title: "Not Found" };
    return {
      title: `What is ${t.term}? — Argus Glossary`,
      description: `${t.definition.slice(0, 160)}`,
      alternates: { canonical: `https://argus.reviews/glossary/${slug}` },
      openGraph: {
        title: `What is ${t.term}?`,
        description: t.definition.slice(0, 160),
        url: `https://argus.reviews/glossary/${slug}`,
        type: "article",
      },
    };
  });
}

export default async function GlossaryTermPage({
  params,
}: {
  params: Promise<{ slug: string }>;
}) {
  const { slug } = await params;
  const t = getGlossaryTerm(slug);
  if (!t) return <div className="py-20 text-center">Not found</div>;

  const related = t.relatedTerms
    .map((r) => glossaryTerms.find((g) => g.slug === r))
    .filter(Boolean);

  return (
    <section className="mx-auto max-w-3xl px-6 py-28">
      <nav className="mb-8 text-xs font-mono text-slate-text">
        <Link href="/" className="hover:text-amber transition-colors duration-150">
          Home
        </Link>
        <span className="mx-2 text-iron">/</span>
        <Link href="/glossary" className="hover:text-amber transition-colors duration-150">
          Glossary
        </Link>
        <span className="mx-2 text-iron">/</span>
        <span className="text-foreground">{t.term}</span>
      </nav>

      <h1 className="font-display text-4xl md:text-5xl font-bold text-foreground mb-2">
        What is {t.term}?
      </h1>
      <p className="text-[11px] font-mono text-iron mb-8">
        Last updated: {t.updatedAt}
      </p>

      <div className="text-sm font-sans text-foreground leading-relaxed mb-10">
        {t.definition}
      </div>

      <h2 className="font-mono text-sm font-bold text-foreground mb-3">
        Why does {t.term.toLowerCase()} matter for engineering teams?
      </h2>
      <p className="text-sm font-sans text-slate-text leading-relaxed mb-10">
        {t.whyItMatters}
      </p>

      <h2 className="font-mono text-sm font-bold text-foreground mb-3">
        How does Argus handle {t.term.toLowerCase()}?
      </h2>
      <p className="text-sm font-sans text-slate-text leading-relaxed mb-6">
        {t.howArgusHandles}
      </p>
      <p className="text-[11px] font-mono text-iron mb-10">
        {t.stat.claim} — {t.stat.source}
      </p>

      <div className="border border-amber/30 bg-amber/5 p-6 mb-10 text-center">
        <p className="text-sm font-mono text-foreground mb-2">
          {t.term} starts working on your very first PR.
        </p>
        <p className="text-xs font-sans text-slate-text mb-4">
          Free for up to 3 repos. No credit card required.
        </p>
        <Link
          href="/sign-up"
          className="inline-flex items-center border bg-amber px-6 py-2.5 text-xs font-mono font-medium text-void transition-[transform,filter] duration-150 ease-out hover:brightness-110 active:scale-[0.97]"
        >
          Install Argus on GitHub
        </Link>
      </div>

      {related.length > 0 && (
        <>
          <h2 className="font-mono text-sm font-bold text-foreground mb-3">
            Related terms
          </h2>
          <div className="space-y-2 mb-8">
            {related.map((r) =>
              r ? (
                <Link
                  key={r.slug}
                  href={`/glossary/${r.slug}`}
                  className="block border border-iron px-4 py-3 text-xs font-mono text-slate-text hover:text-amber hover:border-amber/30 transition-[color,border-color] duration-150"
                >
                  {r.term} — {r.definition.slice(0, 80)}…
                </Link>
              ) : null,
            )}
          </div>
        </>
      )}

      <script
        type="application/ld+json"
        dangerouslySetInnerHTML={{
          __html: JSON.stringify({
            "@context": "https://schema.org",
            "@type": "Article",
            headline: `What is ${t.term}?`,
            description: t.definition.slice(0, 160),
            dateModified: t.updatedAt,
            author: {
              "@type": "Organization",
              name: "Argus",
              url: "https://argus.reviews",
            },
            publisher: {
              "@type": "Organization",
              name: "Argus",
            },
          }),
        }}
      />
    </section>
  );
}
