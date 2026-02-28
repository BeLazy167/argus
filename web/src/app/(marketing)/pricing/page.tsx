import Link from "next/link";
import { Check, ChevronDown } from "lucide-react";

interface PlanProps {
  name: string;
  price: string;
  period: string;
  description: string;
  features: string[];
  cta: string;
  href: string;
  highlighted?: boolean;
  badge?: string;
}

function PlanCard({
  name,
  price,
  period,
  description,
  features,
  cta,
  href,
  highlighted = false,
  badge,
}: PlanProps) {
  return (
    <div
      className={`relative flex flex-col rounded-lg border p-6 ${
        highlighted
          ? "border-amber/40 bg-charcoal shadow-[0_0_32px_-8px_oklch(0.77_0.15_75/0.2)]"
          : "border-iron bg-charcoal"
      }`}
    >
      {badge && (
        <span className="absolute -top-3 left-6 rounded-sm bg-amber px-3 py-0.5 text-[10px] font-mono font-medium uppercase tracking-wider text-void">
          {badge}
        </span>
      )}

      <h3 className="font-display text-lg font-bold text-foreground">{name}</h3>
      <p className="mt-1 text-xs text-slate-text">{description}</p>

      <div className="my-6">
        <span className="font-mono text-3xl font-medium text-foreground">
          {price}
        </span>
        <span className="ml-1 text-xs text-slate-text">{period}</span>
      </div>

      <ul className="flex-1 space-y-3 mb-8">
        {features.map((f) => (
          <li key={f} className="flex items-start gap-2 text-xs text-ash">
            <Check className="mt-0.5 h-3.5 w-3.5 shrink-0 text-amber" />
            {f}
          </li>
        ))}
      </ul>

      <Link
        href={href}
        className={`inline-flex h-10 items-center justify-center rounded-md text-sm font-mono font-medium transition-all ${
          highlighted
            ? "bg-amber text-void hover:brightness-110"
            : "border border-iron text-ash hover:border-slate-text hover:text-foreground"
        }`}
      >
        {cta}
      </Link>
    </div>
  );
}

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
          Free for open source. Pay for private repos.
        </p>
      </div>

      {/* Anchoring line */}
      <p className="text-center text-xs font-mono text-iron mb-16">
        One critical bug in production costs more than a year of Argus.
      </p>

      <div className="grid gap-6 md:grid-cols-3">
        <PlanCard
          name="Open Source"
          price="$0"
          period="/forever"
          description="Public repos. Unlimited reviews."
          features={[
            "Unlimited public repos",
            "Full review pipeline",
            "Community rules",
            "Risk scoring",
          ]}
          cta="Get started"
          href="/sign-up"
        />
        <PlanCard
          name="Team"
          price="$29"
          period="/mo per repo"
          description="Private repos. Full memory."
          features={[
            "Everything in Open Source",
            "Private repositories",
            "Institutional memory (RAG)",
            "Custom org rules",
            "Per-repo model config",
            "Priority support",
          ]}
          cta="Start free trial"
          href="/sign-up"
          highlighted
          badge="Most popular"
        />
        <PlanCard
          name="Enterprise"
          price="From $199"
          period="/mo"
          description="Self-hosted. Your infrastructure."
          features={[
            "Everything in Team",
            "Self-hosted deployment",
            "SSO / SAML",
            "Audit logs",
            "Custom SLA",
            "Dedicated Slack channel",
          ]}
          cta="Talk to us"
          href="mailto:hello@argus.dev"
        />
      </div>

      {/* Beta notice */}
      <div className="mt-8 rounded-lg border border-amber/20 bg-amber/5 px-5 py-3 text-center">
        <p className="text-xs font-mono text-amber">
          All plans are free during early access. No credit card required.
        </p>
      </div>

      {/* FAQ */}
      <div className="mt-20">
        <h2 className="font-display text-2xl font-bold text-foreground mb-8 text-center">
          Questions
        </h2>
        <div className="space-y-2">
          <FaqItem
            q="How does per-repo pricing work?"
            a="You pay for each private repo with Argus enabled. Disable a repo anytime to stop charges. Public repos are always free."
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
            q="Can I self-host?"
            a="Enterprise plan includes self-hosted deployment. Argus is a single Go binary + PostgreSQL. Runs on any container platform."
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
