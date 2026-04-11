import type { Metadata } from "next";
import Link from "next/link";
import { Check, X, Minus } from "lucide-react";
import {
  competitors,
  competitorSlugs,
  getCompetitor,
  featureLabels,
  argusFeatures,
} from "@/lib/pseo/competitors";

export function generateStaticParams() {
  return competitorSlugs.map((slug) => ({ slug }));
}

export function generateMetadata({
  params,
}: {
  params: Promise<{ slug: string }>;
}): Promise<Metadata> {
  return params.then(({ slug }) => {
    const c = getCompetitor(slug);
    if (!c) return { title: "Not Found" };
    return {
      title: `Argus vs ${c.name}: AI Code Review Comparison`,
      description: `Argus vs ${c.name} — feature comparison, pricing, and honest analysis of strengths and weaknesses. ${c.tagline}.`,
      alternates: { canonical: `https://argus.reviews/compare/${slug}` },
      openGraph: {
        title: `Argus vs ${c.name}: AI Code Review Comparison`,
        description: `Honest feature comparison between Argus and ${c.name}. See which AI code review tool fits your team.`,
        url: `https://argus.reviews/compare/${slug}`,
        type: "article",
      },
    };
  });
}

function FeatureIcon({ value }: { value: boolean }) {
  return value ? (
    <Check className="h-4 w-4 text-emerald-400" />
  ) : (
    <X className="h-4 w-4 text-red-400/60" />
  );
}

export default async function ComparePage({
  params,
}: {
  params: Promise<{ slug: string }>;
}) {
  const { slug } = await params;
  const c = getCompetitor(slug);
  if (!c) return <div className="py-20 text-center">Not found</div>;

  const featureKeys = Object.keys(featureLabels) as (keyof typeof featureLabels)[];
  const relatedCompetitors = competitors.filter((r) => r.slug !== slug).slice(0, 3);

  return (
    <section className="mx-auto max-w-4xl px-6 py-28">
      <nav className="mb-8 text-xs font-mono text-slate-text">
        <Link href="/" className="hover:text-amber transition-colors duration-150">
          Home
        </Link>
        <span className="mx-2 text-iron">/</span>
        <Link href="/compare" className="hover:text-amber transition-colors duration-150">
          Compare
        </Link>
        <span className="mx-2 text-iron">/</span>
        <span className="text-foreground">Argus vs {c.name}</span>
      </nav>

      <h1 className="font-display text-4xl md:text-5xl font-bold text-foreground mb-4">
        Argus vs {c.name}
      </h1>
      <p className="text-sm text-slate-text font-sans leading-relaxed mb-6 max-w-2xl">
        {c.summary}
      </p>

      <p className="text-[11px] font-mono text-iron mb-12">
        Last updated: {c.updatedAt}
      </p>

      <div className="flex items-center gap-4 mb-12 text-[11px] font-mono text-slate-text">
        <span className="inline-flex items-center gap-1.5">
          <span className="h-1.5 w-1.5 bg-amber rounded-full" />
          Trusted by engineering teams
        </span>
        <span className="text-iron">|</span>
        <span>Free for 3 repos</span>
        <span className="text-iron">|</span>
        <span>Installs in 60 seconds</span>
      </div>

      <h2 className="font-mono text-lg font-bold text-foreground mb-4">
        How does Argus compare to {c.name} in features?
      </h2>

      <div className="border border-iron mb-12 overflow-x-auto">
        <table className="w-full text-xs font-mono">
          <thead>
            <tr className="border-b border-iron bg-charcoal">
              <th className="text-left px-4 py-3 text-slate-text font-medium">Feature</th>
              <th className="text-center px-4 py-3 text-amber font-medium">Argus</th>
              <th className="text-center px-4 py-3 text-foreground font-medium">{c.name}</th>
            </tr>
          </thead>
          <tbody>
            {featureKeys.map((key, i) => (
              <tr
                key={key}
                className={`border-b border-iron/50 ${i % 2 === 0 ? "bg-void" : "bg-charcoal"}`}
              >
                <td className="px-4 py-2.5 text-foreground">{featureLabels[key]}</td>
                <td className="px-4 py-2.5 text-center">
                  <FeatureIcon value={argusFeatures[key]} />
                </td>
                <td className="px-4 py-2.5 text-center">
                  <FeatureIcon value={c.features[key]} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <div className="grid md:grid-cols-2 gap-6 mb-12">
        <div className="border border-iron p-5">
          <h2 className="font-mono text-sm font-bold text-emerald-400 mb-3">
            Where does {c.name} excel?
          </h2>
          <ul className="space-y-2">
            {c.strengths.map((s) => (
              <li key={s} className="text-xs font-sans text-slate-text leading-relaxed flex gap-2">
                <span className="text-emerald-400 shrink-0">+</span>
                {s}
              </li>
            ))}
          </ul>
        </div>
        <div className="border border-amber/30 p-5">
          <h2 className="font-mono text-sm font-bold text-amber mb-3">
            Where does Argus pull ahead?
          </h2>
          <ul className="space-y-2">
            {c.weaknesses.map((w) => (
              <li key={w} className="text-xs font-sans text-slate-text leading-relaxed flex gap-2">
                <span className="text-amber shrink-0">→</span>
                {w}
              </li>
            ))}
          </ul>
          {c.features.selfHosted && (
            <p className="text-xs font-sans text-slate-text mt-3">
              Honest caveat: {c.name} offers self-hosted deployment, which Argus doesn&apos;t currently support. If compliance requires on-prem infrastructure, {c.name} may be the better fit for that requirement.
            </p>
          )}
        </div>
      </div>

      <div className="border border-iron p-5 mb-12">
        <h2 className="font-mono text-sm font-bold text-foreground mb-3">
          Why do teams choose Argus over {c.name}?
        </h2>
        <p className="text-sm font-sans text-slate-text leading-relaxed mb-4">
          {c.argusAdvantage}
        </p>
        <p className="text-[11px] font-mono text-iron">
          {c.stat.claim} — {c.stat.source}
        </p>
      </div>

      <div className="border border-amber/30 bg-amber/5 p-6 mb-12 text-center">
        <p className="text-sm font-mono text-foreground mb-2">
          Stop shipping bugs {c.name} can&apos;t catch.
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

      <h2 className="font-mono text-sm font-bold text-foreground mb-4">
        Compare Argus with other tools
      </h2>
      <div className="space-y-2 mb-8">
        {relatedCompetitors.map((r) => (
          <Link
            key={r.slug}
            href={`/compare/${r.slug}`}
            className="block border border-iron px-4 py-3 text-xs font-mono text-slate-text hover:text-amber hover:border-amber/30 transition-[color,border-color] duration-150"
          >
            Argus vs {r.name} — {r.tagline}
          </Link>
        ))}
      </div>

      <script
        type="application/ld+json"
        dangerouslySetInnerHTML={{
          __html: JSON.stringify({
            "@context": "https://schema.org",
            "@type": "Article",
            headline: `Argus vs ${c.name}: AI Code Review Comparison`,
            description: `Feature comparison between Argus and ${c.name} for AI-powered code review.`,
            dateModified: c.updatedAt,
            author: {
              "@type": "Organization",
              name: "Argus",
              url: "https://argus.reviews",
            },
            publisher: {
              "@type": "Organization",
              name: "Argus",
            },
            breadcrumb: {
              "@type": "BreadcrumbList",
              itemListElement: [
                { "@type": "ListItem", position: 1, name: "Home", item: "https://argus.reviews" },
                { "@type": "ListItem", position: 2, name: "Compare", item: "https://argus.reviews/compare" },
                { "@type": "ListItem", position: 3, name: `Argus vs ${c.name}`, item: `https://argus.reviews/compare/${slug}` },
              ],
            },
          }),
        }}
      />
    </section>
  );
}
