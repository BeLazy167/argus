import { ChevronDown } from "lucide-react";
import { PricingTable } from "@clerk/nextjs";

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

      <PricingTable for="organization" />

      {/* FAQ */}
      <div className="mt-20">
        <h2 className="font-display text-2xl font-bold text-foreground mb-8 text-center">
          Questions
        </h2>
        <div className="space-y-2">
          <FaqItem
            q="How does per-org pricing work?"
            a="You pay per organization per month. All repos within that org get full access. Personal accounts use the Free plan."
          />
          <FaqItem
            q="Does it work with monorepos?"
            a="Yes. Argus reviews the diff of each PR regardless of repo structure. It triages files individually, so large PRs in monorepos still get fast, focused reviews."
          />
          <FaqItem
            q="What data does Argus store?"
            a="PR metadata (title, author, branch), the diff, review comments, and optionally past patterns for memory. We never store your full source code. Diffs are processed in-memory and discarded after review."
          />
          <FaqItem
            q="Can I use my own LLM API key?"
            a="Yes. Configure any OpenAI-compatible provider (OpenRouter, Anthropic, OpenAI, etc.) per pipeline stage from the Settings page. Bring your own key or use ours."
          />
          <FaqItem
            q="What if Argus flags something incorrectly?"
            a="Dismiss it. Every comment explains its reasoning — disagree and move on. Over time, Argus learns your codebase patterns and false positives decrease."
          />
          <FaqItem
            q="Is there a free trial?"
            a="All features are free during early access — no trial needed. When paid plans launch, early adopters get a permanent discount."
          />
        </div>
      </div>
    </section>
  );
}
