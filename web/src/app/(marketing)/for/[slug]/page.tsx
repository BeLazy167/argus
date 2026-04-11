import type { Metadata } from "next";
import Link from "next/link";
import { personas, personaSlugs, getPersona } from "@/lib/pseo/personas";

export function generateStaticParams() {
  return personaSlugs.map((slug) => ({ slug }));
}

export function generateMetadata({
  params,
}: {
  params: Promise<{ slug: string }>;
}): Promise<Metadata> {
  return params.then(({ slug }) => {
    const p = getPersona(slug);
    if (!p) return { title: "Not Found" };
    const shortTitle = p.title.replace("AI Code Review for ", "");
    return {
      title: `${p.title} — Argus`,
      description: p.subtitle,
      alternates: { canonical: `https://argus.reviews/for/${slug}` },
      openGraph: {
        title: p.title,
        description: p.subtitle,
        url: `https://argus.reviews/for/${slug}`,
        type: "article",
      },
    };
  });
}

export default async function PersonaPage({
  params,
}: {
  params: Promise<{ slug: string }>;
}) {
  const { slug } = await params;
  const p = getPersona(slug);
  if (!p) return <div className="py-20 text-center">Not found</div>;

  return (
    <section className="mx-auto max-w-3xl px-6 py-28">
      <nav className="mb-8 text-xs font-mono text-slate-text">
        <Link href="/" className="hover:text-amber transition-colors duration-150">
          Home
        </Link>
        <span className="mx-2 text-iron">/</span>
        <Link href="/for" className="hover:text-amber transition-colors duration-150">
          For Teams
        </Link>
        <span className="mx-2 text-iron">/</span>
        <span className="text-foreground">{p.title.replace("AI Code Review for ", "")}</span>
      </nav>

      <h1 className="font-display text-4xl md:text-5xl font-bold text-foreground mb-3">
        {p.title}
      </h1>
      <p className="text-sm font-sans text-slate-text leading-relaxed mb-4">
        {p.subtitle}
      </p>
      <p className="text-[11px] font-mono text-iron mb-12">
        Last updated: {p.updatedAt}
      </p>

      <h2 className="font-mono text-sm font-bold text-red-400 mb-3">
        What problems do {p.title.replace("AI Code Review for ", "")}s face in code review?
      </h2>
      <ul className="space-y-3 mb-10">
        {p.painPoints.map((point) => (
          <li key={point} className="border border-iron px-4 py-3 text-xs font-sans text-slate-text leading-relaxed">
            {point}
          </li>
        ))}
      </ul>

      <h2 className="font-mono text-sm font-bold text-amber mb-3">
        How does Argus fit your workflow?
      </h2>
      <ul className="space-y-3 mb-10">
        {p.howArgusFits.map((fit) => (
          <li key={fit} className="border border-amber/30 bg-amber/5 px-4 py-3 text-xs font-sans text-foreground leading-relaxed">
            {fit}
          </li>
        ))}
      </ul>

      <h2 className="font-mono text-sm font-bold text-foreground mb-4">
        Which Argus features matter most for your team?
      </h2>
      <div className="space-y-3 mb-10">
        {p.featureCallouts.map((fc) => (
          <div key={fc.feature} className="border border-iron px-4 py-3">
            <p className="text-xs font-mono font-bold text-amber mb-1">{fc.feature}</p>
            <p className="text-xs font-sans text-slate-text leading-relaxed">{fc.reason}</p>
          </div>
        ))}
      </div>

      <div className="border border-iron p-5 mb-10">
        <p className="text-[11px] font-mono text-iron">
          {p.stat.claim} — {p.stat.source}
        </p>
      </div>

      <div className="border border-amber/30 bg-amber/5 p-6 mb-10 text-center">
        <p className="text-sm font-mono text-foreground mb-2">
          Don&apos;t let another risky PR merge without the review your team needs.
        </p>
        <p className="text-xs font-sans text-slate-text mb-4">
          Free for up to 3 repos. Institutional memory starts on your first PR.
        </p>
        <Link
          href="/sign-up"
          className="inline-flex items-center border bg-amber px-6 py-2.5 text-xs font-mono font-medium text-void transition-[transform,filter] duration-150 ease-out hover:brightness-110 active:scale-[0.97]"
        >
          Install Argus on GitHub
        </Link>
      </div>

      <script
        type="application/ld+json"
        dangerouslySetInnerHTML={{
          __html: JSON.stringify({
            "@context": "https://schema.org",
            "@type": "Article",
            headline: p.title,
            description: p.subtitle,
            dateModified: p.updatedAt,
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
