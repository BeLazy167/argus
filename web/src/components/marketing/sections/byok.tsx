import { FadeIn } from "@/components/marketing/fade-in";

type Provider = {
  id: string;
  name: string;
  model: string;
  active?: boolean;
};

const PROVIDERS: Provider[] = [
  { id: "anthropic", name: "Anthropic", model: "claude-sonnet-4.5", active: true },
  { id: "openai", name: "OpenAI", model: "gpt-5" },
  { id: "google", name: "Vertex", model: "gemini-2.5" },
  { id: "openrouter", name: "OpenRouter", model: "router" },
  { id: "groq", name: "Groq", model: "llama-3.3" },
  { id: "together", name: "Together", model: "open-models" },
  { id: "deepseek", name: "DeepSeek", model: "v3" },
  { id: "bedrock", name: "Bedrock", model: "aws" },
  { id: "azure", name: "Azure", model: "openai" },
  { id: "fireworks", name: "Fireworks", model: "mixtral" },
  { id: "zhipu", name: "Zhipu", model: "glm-4" },
];

type AgentRow = {
  name: string;
  model: string;
  tokens: string;
  cost: string;
};

const AGENT_ROWS: AgentRow[] = [
  { name: "triage", model: "haiku-4.5", tokens: "1,742", cost: "0.002" },
  { name: "security", model: "haiku-4.5", tokens: "3,012", cost: "0.005" },
  { name: "memory", model: "sonnet-4.5", tokens: "4,128", cost: "0.006" },
  { name: "synthesis", model: "sonnet-4.5", tokens: "5,190", cost: "0.030" },
];

type ModelOption = {
  name: string;
  input: string;
  output: string;
  hint: string;
  selected?: boolean;
};

const MODELS: ModelOption[] = [
  { name: "haiku-4.5", input: "0.80", output: "4.00", hint: "fast / cheap", selected: true },
  { name: "sonnet-4.5", input: "3.00", output: "15.00", hint: "balanced" },
  { name: "opus-4.1", input: "15.00", output: "75.00", hint: "deep analysis" },
];

type UsageRow = {
  label: string;
  reviews: string;
  spend: string;
  /** 0..1 relative bar length */
  weight: number;
  /** editorial rule thickness in px */
  thickness: number;
};

const USAGE: UsageRow[] = [
  { label: "quiet week", reviews: "12 reviews", spend: "$0.52", weight: 0.14, thickness: 1 },
  { label: "busy day", reviews: "84 reviews", spend: "$3.61", weight: 0.52, thickness: 2 },
  { label: "hiring sprint", reviews: "240 reviews", spend: "$10.32", weight: 1.0, thickness: 4 },
];

function SectionHeader() {
  return (
    <div className="flex flex-col gap-6">
      <div className="font-mono text-[11px] tracking-[0.24em] uppercase text-amber-glow">
        <span className="text-amber-glow/60">03</span>
        <span className="mx-2 text-iron">/</span>
        <span>BYOK &amp; Transparency</span>
      </div>
      <h2 className="font-mono text-4xl md:text-5xl lg:text-[56px] leading-[1.05] tracking-tight text-foreground max-w-[26ch]">
        Your LLM. Your cost.{" "}
        <span className="text-amber-glow">Full transparency.</span>
      </h2>
      <p className="font-mono text-[15px] leading-relaxed text-slate-text max-w-[62ch]">
        Bring your own key — 11 providers supported. See exact token spend per
        agent per review. Route Haiku for triage, Opus for deep analysis. No
        surprises, no lock-in.
      </p>
    </div>
  );
}

function CardShell({
  eyebrow,
  title,
  description,
  children,
  className = "",
}: {
  eyebrow: string;
  title: string;
  description?: string;
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <div
      className={`group relative flex flex-col bg-charcoal/40 border border-iron/70 hover:border-amber-glow/40 transition-colors ${className}`}
    >
      {/* corner ticks */}
      <span
        aria-hidden
        className="absolute left-0 top-0 h-2 w-2 border-t border-l border-amber-glow/60"
      />
      <span
        aria-hidden
        className="absolute right-0 bottom-0 h-2 w-2 border-b border-r border-amber-glow/60"
      />
      <div className="relative flex flex-col gap-5 p-7">
        <header className="flex flex-col gap-2">
          <span className="font-mono text-[10px] tracking-[0.26em] uppercase text-amber-glow/70">
            {eyebrow}
          </span>
          <h3 className="font-mono text-2xl leading-tight text-foreground">
            {title}
          </h3>
          {description ? (
            <p className="font-mono text-[13px] leading-relaxed text-slate-text max-w-[52ch]">
              {description}
            </p>
          ) : null}
        </header>
        <div className="flex flex-col gap-4">{children}</div>
      </div>
    </div>
  );
}

/* ── Provider tiles — monochrome inline SVG logos ── */

function ProviderTile({ provider }: { provider: Provider }) {
  const { id, name, model, active } = provider;
  const Logo: LogoComponent = PROVIDER_LOGOS[id] ?? DefaultLogo;
  return (
    <div
      className={`relative flex items-center gap-3 px-3 py-3 border transition-colors ${
        active
          ? "border-amber-glow/60 bg-amber-glow/[0.05]"
          : "border-iron/50 bg-transparent hover:border-iron hover:bg-charcoal/30"
      }`}
    >
      <Logo
        className={`h-5 w-5 shrink-0 ${
          active ? "text-amber-glow" : "text-slate-text/80"
        }`}
      />
      <div className="flex min-w-0 flex-col gap-0.5">
        <span
          className={`font-mono text-[13px] leading-none truncate ${
            active ? "text-foreground" : "text-foreground/80"
          }`}
        >
          {name}
        </span>
        <span className="font-mono text-[10px] tracking-[0.1em] text-slate-text/60 truncate">
          {model}
        </span>
      </div>
      {active ? (
        <span className="absolute -top-px -right-px inline-flex items-center gap-1 px-1.5 py-0.5 bg-amber-glow text-void font-mono text-[9px] tracking-[0.2em] uppercase font-bold">
          Live
        </span>
      ) : null}
    </div>
  );
}

function ProvidersCard() {
  return (
    <CardShell
      eyebrow="Bring your own key"
      title="Eleven providers. One of yours."
      description="Connect any provider. Rotate keys anytime. Never touch ours."
    >
      <div className="grid grid-cols-2 sm:grid-cols-4 gap-2">
        {PROVIDERS.map((p) => (
          <ProviderTile key={p.id} provider={p} />
        ))}
      </div>
      <div className="flex items-center gap-2 pt-3 border-t border-amber-glow/15">
        <LockGlyph className="h-3 w-3 text-amber-glow/80" />
        <span className="font-mono text-[11px] text-slate-text">
          Keys stored encrypted locally. Never leave your machine.
        </span>
      </div>
    </CardShell>
  );
}

/* ── Cost card — terminal readout ── */

function CostCard() {
  return (
    <CardShell eyebrow="Live cost breakdown" title="Per agent. Per review.">
      <div className="flex items-center justify-between font-mono text-[10px] tracking-[0.24em] uppercase">
        <span className="inline-flex items-center gap-2 text-amber-glow">
          <span className="relative flex h-1.5 w-1.5">
            <span className="absolute inset-0 bg-amber-glow rounded-full animate-ping opacity-70" />
            <span className="relative h-1.5 w-1.5 bg-amber-glow rounded-full" />
          </span>
          Streaming
        </span>
        <span className="text-slate-text/60">pr #4128 · main</span>
      </div>

      {/* Hero total */}
      <div className="flex items-baseline justify-between gap-3 border-y border-amber-glow/25 py-4">
        <div className="flex flex-col gap-1">
          <span className="font-mono text-[10px] tracking-[0.24em] uppercase text-slate-text/60">
            Review total
          </span>
          <span className="font-mono text-[11px] tabular-nums text-slate-text/70">
            14,072 tokens · 4 agents
          </span>
        </div>
        <div className="flex items-baseline gap-1 text-amber-glow">
          <span className="font-mono text-2xl leading-none">$</span>
          <span className="font-mono text-5xl tabular-nums leading-none tracking-tight">
            0.043
          </span>
        </div>
      </div>

      {/* Per-agent rows */}
      <ul className="flex flex-col font-mono text-[12px]">
        {AGENT_ROWS.map((row, i) => (
          <li
            key={row.name}
            className="group/row grid grid-cols-[auto_1fr_auto_auto] items-center gap-3 py-2 border-b border-iron/30 last:border-b-0"
          >
            <span className="tabular-nums text-slate-text/40 w-6">
              {String(i + 1).padStart(2, "0")}
            </span>
            <span className="text-foreground/90 truncate">{row.name}</span>
            <span className="hidden sm:inline text-slate-text/60 tabular-nums text-[11px]">
              {row.model}
            </span>
            <span className="flex items-baseline gap-2">
              <span className="tabular-nums text-slate-text/50 text-[11px]">
                {row.tokens}t
              </span>
              <span className="tabular-nums text-foreground w-[52px] text-right">
                <span className="text-amber-glow/60">$</span>
                {row.cost}
              </span>
            </span>
          </li>
        ))}
      </ul>
    </CardShell>
  );
}

/* ── Model picker — interactive dropdown look ── */

function ModelRow({ model }: { model: ModelOption }) {
  const { name, input, output, hint, selected } = model;
  return (
    <div
      className={`grid grid-cols-[auto_1fr_auto] items-center gap-3 px-3 py-2.5 border transition-colors ${
        selected
          ? "border-amber-glow/50 bg-amber-glow/[0.06]"
          : "border-transparent hover:border-iron/60 hover:bg-charcoal/30"
      }`}
    >
      <span
        aria-hidden
        className={`h-1.5 w-1.5 rounded-full ${
          selected ? "bg-amber-glow" : "bg-iron/70"
        }`}
      />
      <div className="flex items-baseline gap-3 min-w-0">
        <span
          className={`font-mono text-[13px] truncate ${
            selected ? "text-foreground" : "text-foreground/70"
          }`}
        >
          {name}
        </span>
        <span
          className={`font-mono text-[10px] tracking-[0.14em] uppercase truncate ${
            selected ? "text-amber-glow/80" : "text-slate-text/50"
          }`}
        >
          {hint}
        </span>
      </div>
      <div className="flex items-baseline gap-3 font-mono text-[11px] tabular-nums text-slate-text/70">
        <span>
          <span className="text-slate-text/40">in </span>
          {input}
        </span>
        <span className="text-iron">·</span>
        <span>
          <span className="text-slate-text/40">out </span>
          {output}
        </span>
      </div>
    </div>
  );
}

function ModelPickerCard() {
  const selected: ModelOption =
    MODELS.find((m) => m.selected) ??
    ({ name: "haiku-4.5", input: "0.80", output: "4.00", hint: "fast / cheap", selected: true } as const);
  return (
    <CardShell
      eyebrow="Per-agent tuning"
      title="Match model to task."
      description="Cheap models for triage. Powerful models for depth. Switch per agent."
    >
      {/* Dropdown-style trigger */}
      <div className="flex items-stretch border border-iron/70 bg-charcoal/50">
        <div className="flex-1 flex items-center gap-3 px-3 py-2.5">
          <span className="font-mono text-[10px] tracking-[0.24em] uppercase text-slate-text/60 w-[72px]">
            Triage
          </span>
          <span className="font-mono text-sm text-foreground">
            {selected.name}
          </span>
          <span className="font-mono text-[10px] tracking-[0.14em] uppercase text-amber-glow/80 hidden sm:inline">
            {selected.hint}
          </span>
        </div>
        <div className="flex items-center gap-1 px-2 border-l border-iron/70 text-slate-text/60">
          <kbd className="font-mono text-[10px] px-1.5 py-0.5 border border-iron/60 text-slate-text/70">
            ↓
          </kbd>
          <kbd className="font-mono text-[10px] px-1.5 py-0.5 border border-iron/60 text-slate-text/70 hidden sm:inline">
            ⏎
          </kbd>
        </div>
      </div>

      {/* Column header */}
      <div className="grid grid-cols-[auto_1fr_auto] items-center gap-3 px-3 font-mono text-[10px] tracking-[0.2em] uppercase text-slate-text/50">
        <span className="h-1.5 w-1.5" />
        <span>Model · role</span>
        <span>USD / 1M tok</span>
      </div>

      {/* Options */}
      <div className="flex flex-col">
        {MODELS.map((m) => (
          <ModelRow key={m.name} model={m} />
        ))}
      </div>
    </CardShell>
  );
}

/* ── Pay-per-review — editorial horizontal rules ── */

function UsageBarRow({ bar, max }: { bar: UsageRow; max: number }) {
  const lengthPct = (bar.weight / max) * 100;
  return (
    <div className="grid grid-cols-[110px_1fr_auto] items-center gap-4 py-2">
      <div className="flex flex-col gap-0.5 min-w-0">
        <span className="font-mono text-[12px] text-foreground/90 truncate">
          {bar.label}
        </span>
        <span className="font-mono text-[10px] tracking-[0.08em] text-slate-text/55 truncate">
          {bar.reviews}
        </span>
      </div>
      <div className="relative flex items-center h-5">
        {/* baseline rule — full width, thin */}
        <span
          aria-hidden
          className="absolute inset-x-0 top-1/2 h-px bg-iron/40"
        />
        {/* measured rule — variable thickness */}
        <span
          aria-hidden
          className="absolute left-0 top-1/2 -translate-y-1/2 bg-amber-glow"
          style={{
            width: `${lengthPct}%`,
            height: `${bar.thickness}px`,
          }}
        />
        {/* terminal tick */}
        <span
          aria-hidden
          className="absolute top-1/2 h-3 w-px -translate-y-1/2 bg-amber-glow"
          style={{ left: `calc(${lengthPct}% - 1px)` }}
        />
      </div>
      <span className="font-mono tabular-nums text-[13px] text-amber-glow w-[68px] text-right">
        {bar.spend}
      </span>
    </div>
  );
}

function PayPerReviewCard() {
  const max = Math.max(...USAGE.map((u) => u.weight));
  return (
    <CardShell
      eyebrow="No per-seat tax"
      title="Flat org pricing. LLM cost is your own."
      description="Seat licenses punish growing teams. Argus is a flat org fee — your LLM provider bills you for tokens directly, and we never see the invoice."
    >
      <div className="flex flex-col divide-y divide-iron/30">
        {USAGE.map((bar) => (
          <UsageBarRow key={bar.label} bar={bar} max={max} />
        ))}
      </div>
      <div className="flex items-center gap-2 pt-3 border-t border-amber-glow/15">
        <KeyGlyph className="h-3 w-3 text-amber-glow/80" />
        <span className="font-mono text-[11px] text-slate-text">
          Spend shown is what your provider charges you — OpenAI, Anthropic, Bedrock. Argus takes zero cut.
        </span>
      </div>
    </CardShell>
  );
}

/* ── Inline glyphs (sharp, 1px-stroke, editorial) ── */

function LockGlyph({ className = "" }: { className?: string }) {
  return (
    <svg viewBox="0 0 12 12" fill="none" className={className} aria-hidden>
      <rect x="2" y="5" width="8" height="6" stroke="currentColor" />
      <path d="M4 5V3.5a2 2 0 0 1 4 0V5" stroke="currentColor" />
    </svg>
  );
}

function KeyGlyph({ className = "" }: { className?: string }) {
  return (
    <svg viewBox="0 0 12 12" fill="none" className={className} aria-hidden>
      <circle cx="4" cy="6" r="2" stroke="currentColor" />
      <path d="M6 6h5M9 6v2M11 6v1.5" stroke="currentColor" />
    </svg>
  );
}

/* ── Provider logos — monochrome, currentColor, editorial ── */

type LogoProps = { className?: string };
type LogoComponent = React.ComponentType<LogoProps>;

function DefaultLogo({ className = "" }: LogoProps) {
  return (
    <svg viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.2" className={className} aria-hidden>
      <rect x="3" y="3" width="14" height="14" />
    </svg>
  );
}

const PROVIDER_LOGOS: Record<string, LogoComponent> = {
  // Anthropic — abstract "A" mark (simplified, recognizable)
  anthropic: ({ className = "" }: LogoProps) => (
    <svg viewBox="0 0 20 20" fill="currentColor" className={className} aria-hidden>
      <path d="M6.2 3.2h2.4l4.2 13.6H10.3l-.9-3H4.7l-.9 3H1.5L6.2 3.2Zm-.9 8.4h3.3L7 6.4l-1.7 5.2Z" />
      <path d="M13.4 3.2h2.3l4.3 13.6h-2.4L13.4 3.2Z" />
    </svg>
  ),
  // OpenAI — hexagonal knot (simplified outline)
  openai: ({ className = "" }: LogoProps) => (
    <svg viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.2" className={className} aria-hidden>
      <path d="M10 2.5 16.5 6v8L10 17.5 3.5 14V6L10 2.5Z" />
      <path d="M10 6v8M3.5 6l6.5 4 6.5-4M3.5 14l6.5-4 6.5 4" />
    </svg>
  ),
  // Google — stylized G square (monochrome line)
  google: ({ className = "" }: LogoProps) => (
    <svg viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.4" className={className} aria-hidden>
      <path d="M15.5 10.2a5.5 5.5 0 1 1-1.6-3.9" />
      <path d="M15.5 10.2H10" />
    </svg>
  ),
  // Groq — fast "Q" — angular, circuit-like
  groq: ({ className = "" }: LogoProps) => (
    <svg viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.4" className={className} aria-hidden>
      <circle cx="9" cy="10" r="5.5" />
      <path d="m12 13 4 4" />
    </svg>
  ),
  // Mistral — stepped M / wind
  mistral: ({ className = "" }: LogoProps) => (
    <svg viewBox="0 0 20 20" fill="currentColor" className={className} aria-hidden>
      <rect x="2" y="4" width="4" height="4" />
      <rect x="6" y="8" width="4" height="4" />
      <rect x="10" y="4" width="4" height="4" />
      <rect x="14" y="8" width="4" height="4" />
      <rect x="2" y="12" width="4" height="4" opacity=".5" />
      <rect x="14" y="12" width="4" height="4" opacity=".5" />
    </svg>
  ),
  // Cohere — three dots cascade
  cohere: ({ className = "" }: LogoProps) => (
    <svg viewBox="0 0 20 20" fill="currentColor" className={className} aria-hidden>
      <circle cx="4" cy="10" r="2" />
      <circle cx="10" cy="10" r="2.4" />
      <circle cx="16" cy="10" r="1.6" opacity=".6" />
    </svg>
  ),
  // Together — two overlapping rings
  together: ({ className = "" }: LogoProps) => (
    <svg viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.3" className={className} aria-hidden>
      <circle cx="7.5" cy="10" r="4.5" />
      <circle cx="12.5" cy="10" r="4.5" />
    </svg>
  ),
  // Bedrock — layered rock strata
  bedrock: ({ className = "" }: LogoProps) => (
    <svg viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.2" className={className} aria-hidden>
      <path d="M2 6h16M3 10h14M5 14h10" />
    </svg>
  ),
};

/**
 * Section 03 — BYOK & Transparency.
 *
 * Four cards telling the BYOK story: provider choice, live per-agent cost,
 * per-task model selection, and usage-based pricing. All accents use amber
 * tokens; no new palette colors introduced.
 */
export function Byok() {
  return (
    <section id="byok" className="relative">
      {/* subtle top/bottom section hairlines to match editorial feel */}
      <div aria-hidden className="absolute inset-x-0 top-0 h-px bg-iron/40" />
      <div aria-hidden className="absolute inset-x-0 bottom-0 h-px bg-iron/40" />
      <div className="mx-auto max-w-6xl px-6 py-24 lg:py-32">
        <FadeIn>
          <SectionHeader />
        </FadeIn>
        <div className="mt-14 grid grid-cols-1 lg:grid-cols-12 gap-4">
          <FadeIn delay={80} className="lg:col-span-7">
            <ProvidersCard />
          </FadeIn>
          <FadeIn delay={160} className="lg:col-span-5">
            <CostCard />
          </FadeIn>
          <FadeIn delay={240} className="lg:col-span-7">
            <ModelPickerCard />
          </FadeIn>
          <FadeIn delay={320} className="lg:col-span-5">
            <PayPerReviewCard />
          </FadeIn>
        </div>
      </div>
    </section>
  );
}
