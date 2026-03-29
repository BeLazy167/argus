import type { Metadata } from "next";
import Link from "next/link";
import { ChevronDown, Check } from "lucide-react";

const FAQ_ITEMS = [
  {
    q: "How does per-org pricing work?",
    a: "You pay per organization per month. All repos within that org get full access. Personal accounts use the Free plan.",
  },
  {
    q: "Does it work with monorepos?",
    a: "Yes. Argus reviews the diff of each PR regardless of repo structure. It triages files individually, so large PRs in monorepos still get fast, focused reviews.",
  },
  {
    q: "What data does Argus store?",
    a: "PR metadata (title, author, branch), the diff, review comments, and optionally past patterns for memory. We never store your full source code. Diffs are processed in-memory and discarded after review.",
  },
  {
    q: "Can I use my own LLM API key?",
    a: "Yes. Configure any OpenAI-compatible provider (OpenRouter, Anthropic, OpenAI, etc.) per pipeline stage from the Settings page. Bring your own key or use ours.",
  },
  {
    q: "What if Argus flags something incorrectly?",
    a: "Dismiss it. Every comment explains its reasoning — disagree and move on. Over time, Argus learns your codebase patterns and false positives decrease.",
  },
  {
    q: "Is there a free trial?",
    a: "All features are free during early access — no trial needed. When paid plans launch, early adopters get a permanent discount.",
  },
];

export const metadata: Metadata = {
  title: "Pricing",
  description:
    "Free to start, Pro at $19/mo. AI code review for teams that ship fast. Bring your own LLM key.",
  alternates: { canonical: "https://argusai.vercel.app/pricing" },
};

function FaqItem({ q, a }: { q: string; a: string }) {
  return (
    <details className="group rounded-lg border border-iron bg-charcoal">
      <summary className="flex cursor-pointer items-center justify-between px-5 py-4 text-sm font-mono text-foreground hover:text-amber transition-colors list-none">
        {q}
        <ChevronDown className="h-4 w-4 text-slate-text shrink-0 transition-transform group-open:rotate-180" />
      </summary>
      <div className="px-5 pb-4 text-xs font-mono text-slate-text leading-relaxed">
        {a}
      </div>
    </details>
  );
}

export default function PricingPage() {
  return (
    <section className="mx-auto max-w-4xl px-6 py-28">
      <div className="mb-6 text-center">
        <p className="mb-3 text-[11px] font-mono uppercase tracking-[0.15em] text-amber">
          Pricing
        </p>
        <h1 className="font-display text-4xl font-bold text-foreground mb-4">
          Ship with confidence.
        </h1>
        <p className="text-sm text-slate-text">
          Free to start. Pro when you need it.
        </p>
      </div>

      {/* Anchoring line */}
      <p className="text-center text-xs font-mono text-iron mb-16">
        One critical bug in production costs more than a year of Argus.
      </p>

      {/* Custom pricing cards */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-6 max-w-2xl mx-auto">
        {/* Free */}
        <div className="rounded-lg border border-iron bg-charcoal p-6">
          <h3 className="font-display text-lg font-bold text-foreground mb-1">Free</h3>
          <div className="flex items-baseline gap-1 mb-1">
            <span className="font-display text-3xl font-bold text-foreground">$0</span>
          </div>
          <p className="text-[11px] font-mono text-slate-text mb-6">Always free</p>
          <ul className="space-y-3 mb-8">
            {[
              "3 repos",
              "50 reviews / month",
              "Basic single-pass review",
              "Pattern memory",
              "BYOK — bring your own LLM key",
            ].map((f) => (
              <li key={f} className="flex items-start gap-2.5 text-xs font-mono text-ash">
                <Check className="h-3.5 w-3.5 text-amber shrink-0 mt-0.5" />
                {f}
              </li>
            ))}
          </ul>
          <Link
            href="/sign-up"
            className="block w-full rounded-md border border-iron bg-iron/30 py-2.5 text-center text-xs font-mono text-foreground transition-colors hover:bg-iron/50"
          >
            Get started free
          </Link>
        </div>

        {/* Pro */}
        <div className="rounded-lg border border-amber/40 bg-charcoal p-6 relative">
          <div className="absolute -top-3 left-6 rounded-full bg-amber px-3 py-0.5 text-[10px] font-mono font-medium text-void">
            Recommended
          </div>
          <h3 className="font-display text-lg font-bold text-amber mb-1">Pro</h3>
          <div className="flex items-baseline gap-1 mb-1">
            <span className="font-display text-3xl font-bold text-foreground">$19</span>
            <span className="text-xs font-mono text-slate-text">/month</span>
          </div>
          <p className="text-[11px] font-mono text-slate-text mb-6">Per workspace, billed monthly</p>
          <ul className="space-y-3 mb-8">
            {[
              "Unlimited repos",
              "500 reviews / month",
              "4 specialist deep review + Pass 2",
              "Full memory — patterns, scenarios, traces",
              "Code simulation",
              "PR diagrams (sequence + data flow)",
              "Priority support",
            ].map((f) => (
              <li key={f} className="flex items-start gap-2.5 text-xs font-mono text-ash">
                <Check className="h-3.5 w-3.5 text-amber shrink-0 mt-0.5" />
                {f}
              </li>
            ))}
          </ul>
          <Link
            href="/sign-up"
            className="block w-full rounded-md bg-amber py-2.5 text-center text-xs font-mono font-medium text-void transition-[transform,filter] duration-200 ease-out hover:brightness-110 active:scale-[0.97]"
          >
            Start Pro trial
          </Link>
        </div>
      </div>

      {/* FAQ */}
      <div className="mt-20">
        <h2 className="font-display text-2xl font-bold text-foreground mb-8 text-center">
          Questions
        </h2>
        <div className="space-y-2">
          {FAQ_ITEMS.map((item) => (
            <FaqItem key={item.q} q={item.q} a={item.a} />
          ))}
        </div>
      </div>

      {/* FAQ JSON-LD for rich results */}
      <script
        type="application/ld+json"
        dangerouslySetInnerHTML={{
          __html: JSON.stringify({
            "@context": "https://schema.org",
            "@type": "FAQPage",
            mainEntity: FAQ_ITEMS.map((item) => ({
              "@type": "Question",
              name: item.q,
              acceptedAnswer: {
                "@type": "Answer",
                text: item.a,
              },
            })),
          }),
        }}
      />
    </section>
  );
}
