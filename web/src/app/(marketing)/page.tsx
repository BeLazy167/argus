import Link from "next/link";
import { EyeSymbol } from "@/components/marketing/eye-symbol";
import {
  Eye,
  Brain,
  GitPullRequest,
  Shield,
  Zap,
  History,
} from "lucide-react";

function FeatureCard({
  icon: Icon,
  title,
  description,
}: {
  icon: React.ComponentType<{ className?: string }>;
  title: string;
  description: string;
}) {
  return (
    <div className="group relative rounded-lg border border-iron bg-charcoal p-6 transition-all hover:border-amber/30 hover:shadow-[0_0_24px_-8px_oklch(0.77_0.15_75/0.2)]">
      <Icon className="mb-4 h-5 w-5 text-amber" />
      <h3 className="mb-2 text-sm font-bold text-foreground">{title}</h3>
      <p className="text-xs leading-relaxed text-slate-text">{description}</p>
    </div>
  );
}

export default function LandingPage() {
  return (
    <>
      {/* ── HERO ── */}
      <section className="relative flex min-h-[90vh] flex-col items-center justify-center overflow-hidden bg-noise">
        {/* Radial amber glow behind the eye */}
        <div
          className="pointer-events-none absolute top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 h-[600px] w-[600px] rounded-full opacity-20"
          style={{
            background:
              "radial-gradient(circle, oklch(0.77 0.15 75 / 0.4) 0%, transparent 70%)",
          }}
        />

        <div className="relative z-10 flex flex-col items-center text-center px-6">
          <EyeSymbol className="mb-8 h-16 w-auto text-amber" />

          <h1 className="wordmark text-5xl md:text-7xl text-foreground mb-4 tracking-[0.15em]">
            ARGUS
          </h1>

          <p className="font-display text-lg md:text-xl text-amber mb-6 font-normal italic">
            Nothing merges unseen.
          </p>

          <p className="max-w-lg text-sm leading-relaxed text-ash mb-10">
            AI code review that builds institutional memory. The longer it runs,
            the smarter it gets about your specific codebase.
          </p>

          <div className="flex gap-4">
            <Link
              href="/sign-up"
              className="inline-flex h-10 items-center rounded-md bg-amber px-6 text-sm font-mono font-medium text-void transition-all hover:brightness-110 hover:shadow-[0_0_20px_-4px_oklch(0.77_0.15_75/0.5)]"
            >
              Install on GitHub
            </Link>
            <Link
              href="/docs"
              className="inline-flex h-10 items-center rounded-md border border-iron px-6 text-sm font-mono text-ash transition-colors hover:border-slate-text hover:text-foreground"
            >
              Read the docs
            </Link>
          </div>
        </div>

        {/* Scroll indicator */}
        <div className="absolute bottom-8 flex flex-col items-center gap-2 text-slate-text">
          <span className="text-[10px] font-mono uppercase tracking-widest">
            Scroll
          </span>
          <div className="h-8 w-px bg-gradient-to-b from-slate-text/50 to-transparent" />
        </div>
      </section>

      {/* ── FEATURES ── */}
      <section className="mx-auto max-w-5xl px-6 py-28">
        <div className="mb-16 text-center">
          <p className="mb-3 text-[11px] font-mono uppercase tracking-[0.15em] text-amber">
            Capabilities
          </p>
          <h2 className="font-display text-3xl font-bold text-foreground">
            Your codebase has a memory now.
          </h2>
        </div>

        <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
          <FeatureCard
            icon={Eye}
            title="Deep Review"
            description="Every PR analyzed for bugs, security, error handling, type design, and test coverage. No surface-level linting."
          />
          <FeatureCard
            icon={Brain}
            title="Institutional Memory"
            description="Remembers past reviews, incidents, and patterns. Flags when you touch code that caused problems before."
          />
          <FeatureCard
            icon={GitPullRequest}
            title="Incremental Re-review"
            description="On new pushes, only reviews the delta. No duplicate noise. Knows what it already said."
          />
          <FeatureCard
            icon={Shield}
            title="Custom Rules"
            description="Org-wide rules from your dashboard. Per-repo overrides via .argus/rules.md. Your standards, enforced."
          />
          <FeatureCard
            icon={Zap}
            title="Any LLM Provider"
            description="OpenRouter, Grok, Claude, GPT, Qwen, GLM, Minimax — any OpenAI-compatible endpoint. You choose the brain."
          />
          <FeatureCard
            icon={History}
            title="Risk Scoring"
            description="Every PR gets a risk score. Critical issues tank the score. Clean code ships with confidence."
          />
        </div>
      </section>

      {/* ── HOW IT WORKS ── */}
      <section className="border-t border-iron bg-charcoal/50 bg-noise">
        <div className="mx-auto max-w-4xl px-6 py-28">
          <div className="mb-16 text-center">
            <p className="mb-3 text-[11px] font-mono uppercase tracking-[0.15em] text-amber">
              Pipeline
            </p>
            <h2 className="font-display text-3xl font-bold text-foreground">
              PR opens. Argus reviews. You ship.
            </h2>
          </div>

          <div className="space-y-0">
            {[
              {
                step: "01",
                label: "TRIAGE",
                desc: "Classifies files by risk. Skips vendored code, configs, lockfiles.",
              },
              {
                step: "02",
                label: "CONTEXT",
                desc: "Retrieves past reviews, rules, and incident history for each file.",
              },
              {
                step: "03",
                label: "REVIEW",
                desc: "Deep analysis per file — bugs, security, error handling, types, tests.",
              },
              {
                step: "04",
                label: "SYNTHESIZE",
                desc: "Aggregates findings. Calculates risk score. Builds the verdict.",
              },
              {
                step: "05",
                label: "POST",
                desc: "Inline comments on the PR. Summary with risk score. Done.",
              },
            ].map((item, i) => (
              <div
                key={item.step}
                className="group flex items-start gap-6 border-l-2 border-iron py-6 pl-8 transition-colors hover:border-amber"
              >
                <span className="font-mono text-xs text-amber min-w-[2rem]">
                  {item.step}
                </span>
                <div>
                  <p className="text-[11px] font-mono uppercase tracking-[0.1em] text-foreground mb-1">
                    {item.label}
                  </p>
                  <p className="text-xs text-slate-text leading-relaxed">
                    {item.desc}
                  </p>
                </div>
              </div>
            ))}
          </div>
        </div>
      </section>

      {/* ── CTA ── */}
      <section className="mx-auto max-w-3xl px-6 py-28 text-center">
        <EyeSymbol className="mx-auto mb-6 h-10 w-auto text-amber/60" />
        <h2 className="font-display text-3xl font-bold text-foreground mb-4">
          The guardian your main branch deserves.
        </h2>
        <p className="text-sm text-ash mb-8 max-w-md mx-auto">
          Install the GitHub App. Connect your repos. Argus reviews every PR
          from the first commit.
        </p>
        <Link
          href="/sign-up"
          className="inline-flex h-11 items-center rounded-md bg-amber px-8 text-sm font-mono font-medium text-void transition-all hover:brightness-110 hover:shadow-[0_0_24px_-4px_oklch(0.77_0.15_75/0.5)]"
        >
          Get started free
        </Link>
      </section>

      {/* ── FOOTER ── */}
      <footer className="border-t border-iron py-10 px-6">
        <div className="mx-auto flex max-w-6xl items-center justify-between">
          <span className="wordmark text-xs text-slate-text tracking-[0.15em]">
            ARGUS
          </span>
          <div className="flex gap-6">
            <Link
              href="/docs"
              className="text-[11px] font-mono text-slate-text hover:text-ash transition-colors"
            >
              Docs
            </Link>
            <Link
              href="/pricing"
              className="text-[11px] font-mono text-slate-text hover:text-ash transition-colors"
            >
              Pricing
            </Link>
            <Link
              href="/blog"
              className="text-[11px] font-mono text-slate-text hover:text-ash transition-colors"
            >
              Blog
            </Link>
            <a
              href="https://github.com/acmeorg/argus"
              target="_blank"
              rel="noopener noreferrer"
              className="text-[11px] font-mono text-slate-text hover:text-ash transition-colors"
            >
              GitHub
            </a>
          </div>
          <span className="text-[11px] font-mono text-iron">
            Firm. Fast. Never wrong twice.
          </span>
        </div>
      </footer>

      {/* Keyframe animations */}
      <style>{`
        @keyframes draw {
          to { stroke-dashoffset: 0; }
        }
        @keyframes fadeIn {
          to { opacity: 1; }
        }
      `}</style>
    </>
  );
}
