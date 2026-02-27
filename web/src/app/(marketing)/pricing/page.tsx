import Link from "next/link";
import { Check } from "lucide-react";

interface PlanProps {
  name: string;
  price: string;
  period: string;
  description: string;
  features: string[];
  cta: string;
  href: string;
  highlighted?: boolean;
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
}: PlanProps) {
  return (
    <div
      className={`relative flex flex-col rounded-lg border p-6 ${
        highlighted
          ? "border-amber/40 bg-charcoal shadow-[0_0_32px_-8px_oklch(0.77_0.15_75/0.2)]"
          : "border-iron bg-charcoal"
      }`}
    >
      {highlighted && (
        <span className="absolute -top-3 left-6 rounded-sm bg-amber px-3 py-0.5 text-[10px] font-mono font-medium uppercase tracking-wider text-void">
          Most popular
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

export default function PricingPage() {
  return (
    <section className="mx-auto max-w-4xl px-6 py-28">
      <div className="mb-16 text-center">
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
        />
        <PlanCard
          name="Enterprise"
          price="Custom"
          period=""
          description="Self-hosted. Your infrastructure."
          features={[
            "Everything in Team",
            "Self-hosted deployment",
            "SSO / SAML",
            "Audit logs",
            "Custom SLA",
            "Dedicated support",
          ]}
          cta="Contact us"
          href="mailto:hello@argus.dev"
        />
      </div>
    </section>
  );
}
