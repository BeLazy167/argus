import type { Metadata } from "next";
import Link from "next/link";
import { Check, Minus } from "lucide-react";
import {
  competitors,
  argusFeatures,
  featureLabels,
  type Competitor,
} from "@/lib/pseo/competitors";

export const metadata: Metadata = {
  title: "Argus vs Alternatives — AI Code Review Comparison",
  description:
    "Side-by-side feature comparison of Argus and other AI code review tools: CodeRabbit, SonarQube, GitHub Copilot, Codacy, Sourcery, Qodo, Semgrep, Greptile, Cubic.",
  alternates: { canonical: "https://argus.reviews/compare" },
};

type FeatureKey = keyof Competitor["features"];

const featureOrder: FeatureKey[] = [
  "memory",
  "patternLearning",
  "multiPass",
  "architectureAnalysis",
  "diagramGeneration",
  "codeSimulation",
  "byok",
  "selfHosted",
];

function Mark({ on }: { on: boolean }) {
  const Icon = on ? Check : Minus;
  return (
    <Icon
      className={`h-4 w-4 mx-auto ${on ? "text-amber" : "text-iron"}`}
      aria-label={on ? "yes" : "no"}
    />
  );
}

export default function CompareHubPage() {
  return (
    <section className="mx-auto max-w-6xl px-6 py-28">
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
      <p className="text-sm font-sans text-slate-text leading-relaxed mb-10 max-w-2xl">
        How Argus compares to other AI code review tools, at a glance. Click any
        competitor for a detailed breakdown of strengths, weaknesses, and when to
        pick which tool.
      </p>

      {/* Main comparison table — sticky first column + horizontal scroll on mobile */}
      <div className="border border-iron overflow-x-auto">
        <table className="w-full min-w-[760px] text-xs font-mono">
          <thead>
            <tr className="border-b border-iron">
              <th className="sticky left-0 z-10 bg-background text-left px-4 py-3 text-slate-text font-medium min-w-[220px]">
                Feature
              </th>
              <th className="text-center px-3 py-3 text-amber font-bold min-w-[90px] bg-amber/5">
                Argus
              </th>
              {competitors.map((c) => (
                <th
                  key={c.slug}
                  className="text-center px-3 py-3 text-foreground font-medium min-w-[110px]"
                >
                  <Link
                    href={`/compare/${c.slug}`}
                    className="hover:text-amber transition-colors duration-150"
                  >
                    {c.name}
                  </Link>
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {featureOrder.map((f) => (
              <tr key={f} className="border-b border-iron/60 hover:bg-iron/10">
                <td className="sticky left-0 z-10 bg-background px-4 py-3 text-foreground">
                  {featureLabels[f]}
                </td>
                <td className="text-center px-3 py-3 bg-amber/5">
                  <Mark on={argusFeatures[f]} />
                </td>
                {competitors.map((c) => (
                  <td key={c.slug} className="text-center px-3 py-3">
                    <Mark on={c.features[f]} />
                  </td>
                ))}
              </tr>
            ))}

            {/* Pricing row */}
            <tr className="border-b border-iron/60 bg-iron/5">
              <td className="sticky left-0 z-10 bg-iron/5 px-4 py-3 text-foreground font-bold">
                Pricing
              </td>
              <td className="text-center px-3 py-3 bg-amber/5 text-amber font-bold whitespace-nowrap">
                Free – $19/mo
              </td>
              {competitors.map((c) => (
                <td key={c.slug} className="text-center px-3 py-3 text-slate-text text-[11px] leading-snug">
                  {c.pricing}
                </td>
              ))}
            </tr>
          </tbody>
        </table>
      </div>

      <p className="mt-4 text-[11px] font-mono text-slate-text">
        Last updated {competitors[0]?.updatedAt ?? ""}. Click any tool above for
        an in-depth comparison.
      </p>

      {/* Per-tool card list (kept for SEO + quick context) */}
      <div className="mt-16">
        <h2 className="font-display text-2xl font-bold text-foreground mb-4">
          Detailed comparisons
        </h2>
        <div className="grid gap-3 sm:grid-cols-2">
          {competitors.map((c) => (
            <Link
              key={c.slug}
              href={`/compare/${c.slug}`}
              className="block border border-iron px-5 py-4 hover:border-amber/30 transition-[border-color] duration-150"
            >
              <div className="flex items-baseline justify-between mb-1 gap-2">
                <h3 className="font-mono text-sm font-bold text-foreground">
                  Argus vs {c.name}
                </h3>
                <span className="text-[11px] font-mono text-iron whitespace-nowrap">
                  {(c.pricing.split("–")[0] ?? c.pricing).trim()}
                </span>
              </div>
              <p className="text-xs font-sans text-slate-text line-clamp-2">
                {c.tagline}
              </p>
            </Link>
          ))}
        </div>
      </div>
    </section>
  );
}
