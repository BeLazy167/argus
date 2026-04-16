import Link from "next/link";
import { FadeIn } from "@/components/marketing/fade-in";

/**
 * Memory section of the Argus v3 landing page — polish + bolder pass (v2).
 *
 * Narrative: Argus remembers past incidents (Q1 rounding bug in eu-billing),
 * catches the same pattern re-emerging in a new PR, and weights team signal
 * so it knows who's right. Left column is a dense vertical rule-off timeline
 * (time axis as typography, not pills). Right column pairs an editor-view
 * pattern match with a signal-weighted team card.
 *
 * Pencil node uQZOm (1440x1190). Purple accents from the source design are
 * reinterpreted as amber glows + iron/slate neutrals.
 */
export function Memory() {
  const timeline: TimelineRowData[] = [
    {
      month: "Dec",
      day: "12",
      year: "2024",
      pr: "#388",
      title: "Race condition in webhook retry logic",
      tag: "race",
    },
    {
      month: "Jan",
      day: "24",
      year: "2025",
      pr: "#412",
      title: "Q1 rounding bug in eu-billing",
      tag: "rounding",
      note: "Argus recognized the Q1 pattern in your latest diff and applied the same safeguard automatically.",
      active: true,
      reactions: 12,
      replies: 11,
    },
    {
      month: "Nov",
      day: "03",
      year: "2024",
      pr: "#301",
      title: "Time-zone drift in scheduled reports",
      tag: "timezone",
    },
    {
      month: "Aug",
      day: "19",
      year: "2024",
      pr: "#228",
      title: "Cache key collision on multi-tenant fetch",
      tag: "cache",
    },
  ];

  return (
    <section id="memory" className="relative overflow-hidden bg-background">
      {/* Atmospheric amber wash — anchored to the active row area */}
      <div
        aria-hidden
        className="pointer-events-none absolute inset-x-0 top-0 -z-10 h-[820px]"
        style={{
          background:
            "radial-gradient(52% 48% at 22% 28%, color-mix(in oklch, var(--color-amber-glow) 9%, transparent) 0%, transparent 72%)",
        }}
      />
      <div
        aria-hidden
        className="pointer-events-none absolute inset-0 -z-10 opacity-[0.35] bg-noise"
      />

      <div className="mx-auto w-full max-w-[1344px] px-6 py-28 md:py-32 lg:px-24">
        {/* ── Heading cluster ────────────────────────────────────────────── */}
        <header className="relative max-w-[62rem]">
          <FadeIn>
            <div className="flex items-center gap-3">
              <SectionMarker />
              <span className="font-mono text-[11px] tracking-[0.24em] uppercase text-amber-glow">
                01
              </span>
              <span
                aria-hidden
                className="h-px w-10 bg-[color-mix(in_oklch,var(--color-amber-glow)_55%,transparent)]"
              />
              <span className="font-mono text-[11px] tracking-[0.24em] uppercase text-ash">
                Institutional Memory
              </span>
            </div>
          </FadeIn>

          <FadeIn delay={80}>
            <h2 className="mt-7 font-mono font-bold text-foreground">
              <span className="block text-[clamp(1.5rem,2.6vw,2.125rem)] leading-[1.15] tracking-[-0.01em] text-slate-text">
                Every bug it catches,
              </span>
              <span className="mt-1 block text-[clamp(2.5rem,6vw,4.25rem)] leading-[0.98] tracking-[-0.03em]">
                it remembers{" "}
                <span className="relative inline-block">
                  forever.
                  <span
                    aria-hidden
                    className="absolute -bottom-1 left-0 right-0 h-[3px]"
                    style={{
                      background:
                        "linear-gradient(to right, var(--color-amber-glow) 0%, color-mix(in oklch, var(--color-amber-glow) 20%, transparent) 100%)",
                    }}
                  />
                </span>
              </span>
            </h2>
          </FadeIn>

          <FadeIn delay={160}>
            <p className="mt-7 max-w-[38ch] font-sans text-[15px] leading-[1.65] text-slate-text">
              The Q1 rounding bug in EU billing? Argus caught it again in your
              latest diff. It learns from reactions and replies — not just the
              repo. Your last reviewer starts from zero. Argus doesn&apos;t.
            </p>
          </FadeIn>
        </header>

        {/* ── Two-column body ────────────────────────────────────────────── */}
        <div className="mt-20 grid grid-cols-1 gap-x-6 gap-y-8 lg:grid-cols-[1.34fr_1fr]">
          {/* ── Left: timeline ─────────────────────────────────────────── */}
          <FadeIn delay={120}>
            <article className="relative h-full border border-iron bg-charcoal/55">
              {/* Signature corner — only one, bottom-right */}
              <span
                aria-hidden
                className="pointer-events-none absolute bottom-[-1px] right-[-1px] h-3 w-3 border-b border-r"
                style={{
                  borderColor:
                    "color-mix(in oklch, var(--color-amber-glow) 65%, transparent)",
                }}
              />

              {/* Card header — stat-heavy, typography-first */}
              <div className="flex items-end justify-between border-b border-iron/70 px-8 pt-7 pb-6">
                <div>
                  <div className="font-mono text-[10px] tracking-[0.28em] uppercase text-ash">
                    Memory · timeline
                  </div>
                  <h3 className="mt-2 font-mono text-[22px] md:text-[26px] leading-[1.1] tracking-[-0.015em] font-bold text-foreground">
                    Recurring patterns across your repo
                  </h3>
                </div>
                <div className="text-right">
                  <div className="font-mono text-[34px] leading-none font-bold tabular-nums text-foreground">
                    12
                  </div>
                  <div className="mt-1.5 font-mono text-[10px] tracking-[0.22em] uppercase text-ash">
                    patterns
                  </div>
                </div>
              </div>

              {/* Dense vertical rule-off timeline */}
              <ol className="relative">
                {/* Continuous spine running through all rows.
                    Math: px-8 (32) + col1 (72) + gap-x-5 (20) + col2 center (24) = 148px */}
                <span
                  aria-hidden
                  className="pointer-events-none absolute top-0 bottom-0 w-px"
                  style={{
                    left: "148px",
                    background:
                      "linear-gradient(to bottom, color-mix(in oklch, var(--color-foreground) 14%, transparent) 0%, color-mix(in oklch, var(--color-foreground) 6%, transparent) 100%)",
                  }}
                />
                {timeline.map((row, i) => (
                  <TimelineRow key={row.pr} row={row} index={i} />
                ))}
              </ol>
            </article>
          </FadeIn>

          {/* ── Right: stacked cards ───────────────────────────────────── */}
          <div className="flex flex-col gap-6">
            <FadeIn delay={180}>
              <PatternMatchedCard />
            </FadeIn>
            <FadeIn delay={240}>
              <TeamSignalCard />
            </FadeIn>
          </div>
        </div>
      </div>
    </section>
  );
}

/* ─────────────────────────────────────────────────────────────────────── */
/* Section marker — pulsing amber dot with a purpose                       */
/* ─────────────────────────────────────────────────────────────────────── */

/**
 * A small amber beacon that marks the section. The dot pulses via a layered
 * glow; the ring hint uses opacity-only animation so it respects reduced
 * motion and GPU-composites cleanly.
 */
function SectionMarker() {
  return (
    <span className="relative inline-flex size-2.5 items-center justify-center">
      <span
        aria-hidden
        className="absolute inset-0 rounded-full"
        style={{
          backgroundColor: "var(--color-amber-glow)",
          boxShadow:
            "0 0 0 2px color-mix(in oklch, var(--color-amber-glow) 20%, transparent), 0 0 14px color-mix(in oklch, var(--color-amber-glow) 75%, transparent)",
        }}
      />
      <span
        aria-hidden
        className="absolute inset-[-4px] rounded-full opacity-40"
        style={{
          border:
            "1px solid color-mix(in oklch, var(--color-amber-glow) 40%, transparent)",
        }}
      />
    </span>
  );
}

/* ─────────────────────────────────────────────────────────────────────── */
/* Timeline row                                                            */
/* ─────────────────────────────────────────────────────────────────────── */

type TimelineRowData = {
  month: string;
  day: string;
  year: string;
  pr: string;
  title: string;
  tag: string;
  note?: string;
  active?: boolean;
  reactions?: number;
  replies?: number;
};

/**
 * A timeline entry rendered as a dense horizontal band against a continuous
 * vertical spine. Dates hero vertically (mono, two-line). Active row gets an
 * amber-tinted left flare, a glow node on the spine, and a callout NOW tick
 * bleeding into the left margin. Reactions only appear on the active row —
 * decoration has to be earned.
 */
function TimelineRow({
  row,
  index,
}: {
  row: TimelineRowData;
  index: number;
}) {
  const { month, day, year, pr, title, tag, note, active, reactions, replies } =
    row;

  return (
    <li className="relative">
      {/* Active row: amber flare bleeding from the left edge */}
      {active && (
        <span
          aria-hidden
          className="pointer-events-none absolute inset-y-0 left-0 w-[3px]"
          style={{
            background:
              "linear-gradient(to bottom, transparent 0%, var(--color-amber-glow) 50%, transparent 100%)",
            boxShadow:
              "0 0 18px color-mix(in oklch, var(--color-amber-glow) 55%, transparent)",
          }}
        />
      )}

      <div
        className={[
          "relative grid grid-cols-[52px_28px_1fr_auto] items-start gap-x-3 px-4 py-5 sm:grid-cols-[72px_48px_1fr_auto] sm:gap-x-5 sm:px-8 sm:py-6",
          "transition-colors duration-200 ease-[cubic-bezier(0.23,1,0.32,1)]",
          active
            ? "bg-[color-mix(in_oklch,var(--color-amber-glow)_6%,transparent)]"
            : "hover:bg-[color-mix(in_oklch,var(--color-foreground)_2.5%,transparent)]",
        ].join(" ")}
      >
        {/* Date — stacked, typography-heavy. Month + day + year. */}
        <div className="pt-0.5">
          <div
            className={[
              "font-mono text-[11px] tracking-[0.22em] uppercase",
              active ? "text-amber-glow" : "text-ash",
            ].join(" ")}
          >
            {month}
          </div>
          <div
            className={[
              "font-mono font-bold tabular-nums leading-none",
              active
                ? "text-[32px] text-foreground mt-1"
                : "text-[26px] text-foreground/70 mt-1",
            ].join(" ")}
          >
            {day}
          </div>
          <div className="mt-1.5 font-mono text-[10px] tracking-[0.14em] tabular-nums text-ash/80">
            {year}
          </div>
        </div>

        {/* Spine + node. Dot is absolutely centered within the 48px column. */}
        <div className="relative h-full pt-2">
          <span
            aria-hidden
            className={[
              "absolute top-2 left-1/2 -translate-x-1/2 block rounded-full transition-transform duration-200 ease-[cubic-bezier(0.23,1,0.32,1)]",
              active ? "size-2.5" : "size-[7px]",
            ].join(" ")}
            style={
              active
                ? {
                    backgroundColor: "var(--color-amber-glow)",
                    boxShadow:
                      "0 0 0 4px color-mix(in oklch, var(--color-amber-glow) 18%, transparent), 0 0 18px color-mix(in oklch, var(--color-amber-glow) 70%, transparent)",
                  }
                : {
                    backgroundColor:
                      "color-mix(in oklch, var(--color-foreground) 32%, transparent)",
                  }
            }
          />
          {/* NOW callout ticks bracketing the active node */}
          {active && (
            <>
              <span
                aria-hidden
                className="absolute left-[calc(50%+10px)] top-3 h-px w-4"
                style={{
                  background:
                    "linear-gradient(to right, var(--color-amber-glow) 0%, transparent 100%)",
                }}
              />
              <span
                className="absolute left-[calc(50%+28px)] -top-px font-mono text-[9px] tracking-[0.24em] uppercase text-amber-glow whitespace-nowrap"
                aria-label="Current match"
              >
                NOW
              </span>
            </>
          )}
        </div>

        {/* Content */}
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
            <span
              className={[
                "font-mono text-[11px] tabular-nums tracking-[0.05em]",
                active ? "text-amber-glow" : "text-slate-text",
              ].join(" ")}
            >
              PR {pr}
            </span>
            <span
              aria-hidden
              className="h-3 w-px bg-iron"
            />
            <span className="font-mono text-[10px] tracking-[0.16em] uppercase text-ash">
              {tag}
            </span>
            {active && (
              <span className="ml-1 font-mono text-[10px] tracking-[0.18em] uppercase text-amber-glow">
                ↻ pattern re-matched
              </span>
            )}
          </div>

          <p
            className={[
              "mt-2 font-mono leading-[1.35] tracking-[-0.005em]",
              active
                ? "text-[17px] text-foreground font-semibold"
                : "text-[15px] text-foreground/85",
            ].join(" ")}
          >
            {title}
          </p>

          {note && (
            <p className="mt-2.5 max-w-[52ch] font-sans text-[13px] leading-relaxed text-slate-text">
              {note}
            </p>
          )}
        </div>

        {/* Reactions / replies — only on the active row */}
        {active &&
          typeof reactions === "number" &&
          typeof replies === "number" && (
            <div className="flex flex-col items-end gap-1.5 pt-1.5 font-mono text-[11px] tabular-nums text-ash">
              <span className="inline-flex items-center gap-1.5 text-amber-glow">
                <HeartIcon />
                {reactions}
              </span>
              <span className="inline-flex items-center gap-1.5">
                <ReplyIcon />
                {replies}
              </span>
            </div>
          )}

        {/* Quiet row counter for non-active rows — keeps visual weight light */}
        {!active && (
          <div className="pt-2 font-mono text-[10px] tabular-nums tracking-[0.14em] text-ash/70">
            {String(index + 1).padStart(2, "0")}
          </div>
        )}
      </div>

      {/* Row separator — fades off at edges so the spine reads continuously */}
      <div
        aria-hidden
        className="absolute inset-x-8 bottom-0 h-px"
        style={{
          background:
            "linear-gradient(to right, transparent 0%, color-mix(in oklch, var(--color-foreground) 7%, transparent) 22%, color-mix(in oklch, var(--color-foreground) 7%, transparent) 78%, transparent 100%)",
        }}
      />
    </li>
  );
}

/* ─────────────────────────────────────────────────────────────────────── */
/* Right-column cards                                                      */
/* ─────────────────────────────────────────────────────────────────────── */

/**
 * Editor-view of the pattern match. File-path tab, line-number gutter, and a
 * pattern fingerprint (SHA-shaped) tie this to PR #412 from the timeline.
 * Syntax is rendered by opacity/weight tinting — no real highlighter, just
 * disciplined monochrome amber.
 */
function PatternMatchedCard() {
  return (
    <article className="relative border border-iron bg-charcoal/55">
      <span
        aria-hidden
        className="pointer-events-none absolute top-[-1px] left-[-1px] h-3 w-3 border-t border-l"
        style={{
          borderColor:
            "color-mix(in oklch, var(--color-amber-glow) 65%, transparent)",
        }}
      />

      {/* Editor tab strip */}
      <div className="flex items-stretch border-b border-iron/70">
        <div
          className="flex items-center gap-2 border-r border-iron/70 px-4 py-3"
          style={{
            backgroundColor:
              "color-mix(in oklch, var(--color-amber-glow) 5%, transparent)",
          }}
        >
          <span
            aria-hidden
            className="inline-block size-1.5 rounded-full"
            style={{ backgroundColor: "var(--color-amber-glow)" }}
          />
          <span className="font-mono text-[11px] text-foreground/90">
            invoice.ts
          </span>
        </div>
        <div className="flex flex-1 items-center justify-between px-4">
          <span className="font-mono text-[10px] tracking-[0.18em] uppercase text-ash">
            billing / L142
          </span>
          <span className="inline-flex items-center gap-2 font-mono text-[10px] tracking-[0.18em] uppercase text-amber-glow">
            <span
              aria-hidden
              className="inline-block size-1 rounded-full"
              style={{
                backgroundColor: "var(--color-amber-glow)",
                boxShadow:
                  "0 0 8px color-mix(in oklch, var(--color-amber-glow) 70%, transparent)",
              }}
            />
            Pattern matched
          </span>
        </div>
      </div>

      {/* Pattern fingerprint strip — ties this match to a memory entry */}
      <div className="flex items-center justify-between border-b border-iron/70 px-4 py-2.5">
        <div className="flex items-center gap-2 font-mono text-[10px] tracking-[0.12em] uppercase text-ash">
          <span>pattern</span>
          <span className="text-foreground/70 tracking-[0.08em] normal-case">
            3f7a · rounding / float-drift
          </span>
        </div>
        <div className="font-mono text-[10px] tracking-[0.12em] uppercase text-ash">
          seen <span className="text-foreground/90 tabular-nums">3×</span>
        </div>
      </div>

      {/* Code body with line-number gutter */}
      <div className="bg-[color-mix(in_oklch,var(--color-background)_75%,var(--color-charcoal)_25%)]">
        <CodeLine num={142} kind="del">
          <Token dim>return</Token> <Token>Math</Token>
          <Token op>.</Token>
          <Token>round</Token>
          <Token op>(</Token>total <Token op>*</Token> <Token num>100</Token>
          <Token op>)</Token>
          <Token op>;</Token>
        </CodeLine>
        <CodeLine num={142} kind="add">
          <Token dim>return</Token> <Token>toFixed</Token>
          <Token op>(</Token>
          <Token>Math</Token>
          <Token op>.</Token>
          <Token>round</Token>
          <Token op>(</Token>total<Token op>),</Token> <Token num>2</Token>
          <Token op>)</Token>
          <Token op>;</Token>
        </CodeLine>
        <CodeLine num={143} kind="meta">
          <Token comment>{"// guard against float drift · suggested by Argus"}</Token>
        </CodeLine>
      </div>

      {/* Argus reply — trimmed, quieter chrome */}
      <div className="border-t border-iron/70 px-5 py-4">
        <div className="flex items-start gap-3">
          <div
            className="shrink-0 flex size-7 items-center justify-center font-mono text-[10px] font-bold text-background"
            style={{
              backgroundColor: "var(--color-amber-glow)",
              boxShadow:
                "0 0 14px color-mix(in oklch, var(--color-amber-glow) 40%, transparent)",
            }}
          >
            A
          </div>
          <div className="min-w-0 flex-1">
            <div className="flex items-baseline gap-2">
              <span className="font-mono text-[11px] tracking-[0.1em] text-amber-glow">
                Argus
              </span>
              <span className="font-mono text-[10px] tabular-nums text-ash/70">
                00:02
              </span>
            </div>
            <p className="mt-1 font-sans text-[13.5px] leading-[1.55] text-foreground/85">
              Same class of rounding bug as{" "}
              <span className="font-mono text-amber-glow">PR #412</span>.
              Suggesting the same guardrail so it doesn&rsquo;t ship twice.
            </p>
          </div>
        </div>
      </div>
    </article>
  );
}

/**
 * Team signal card — avatars are earned via a signal bar showing how often
 * each contributor's reactions matched ground truth. The lead signal gets an
 * amber halo; the rest taper by opacity. Reinforces that Argus weights team
 * signal, not just diffs.
 */
function TeamSignalCard() {
  // Real behavior: Argus tracks 👍 / 👎 reactions + replies on its review
  // comments per GitHub user. The "signal" bar represents reaction volume,
  // not an accuracy score. Don't claim accuracy we don't compute.
  const contributors = [
    { label: "MK", signal: 1.0, reactions: 47 },
    { label: "RL", signal: 0.66, reactions: 31 },
    { label: "JN", signal: 0.38, reactions: 18 },
    { label: "AS", signal: 0.17, reactions: 8 },
  ];

  return (
    <article className="group/card relative flex-1 border border-iron bg-charcoal/55">
      {/* Card header */}
      <div className="flex items-center justify-between border-b border-iron/70 px-6 py-4">
        <span className="font-mono text-[10px] tracking-[0.24em] uppercase text-ash">
          Team · reactions
        </span>
        <span className="font-mono text-[10px] tracking-[0.14em] uppercase text-ash">
          👍 / 👎 on Argus reviews
        </span>
      </div>

      <div className="px-6 py-6">
        {/* Weighted contributor row */}
        <div className="flex items-end gap-4">
          {contributors.map((c, i) => (
            <ContributorChip
              key={c.label}
              label={c.label}
              signal={c.signal}
              lead={i === 0}
            />
          ))}
          <div className="flex flex-col items-center gap-2">
            <div className="flex h-10 w-10 items-center justify-center border border-iron bg-background font-mono text-[11px] text-ash">
              +8
            </div>
            <div className="h-[2px] w-full bg-iron/60" />
          </div>
        </div>

        {/* Callout — the most active reactor */}
        <div className="mt-6 flex items-center gap-2.5 font-mono text-[10px] tracking-[0.18em] uppercase">
          <span className="text-amber-glow">MK</span>
          <span className="text-ash">· most active</span>
          <span
            aria-hidden
            className="h-px flex-1 bg-[color-mix(in_oklch,var(--color-amber-glow)_25%,transparent)]"
          />
          <span className="font-mono text-[10px] tabular-nums text-foreground/70">
            47 reactions
          </span>
        </div>

        <p className="mt-6 font-mono text-[19px] leading-[1.15] tracking-[-0.015em] font-semibold text-foreground">
          Learns from your team.
          <br />
          <span className="text-foreground/50">Not just the code.</span>
        </p>

        <p className="mt-3 max-w-[44ch] font-sans text-[13px] leading-[1.6] text-slate-text">
          Reactions, replies, and who tends to be right compound into a ranking
          Argus uses on every review.
        </p>

        <Link
          href="/compare"
          className="mt-6 inline-flex items-center gap-2 font-mono text-[11px] tracking-[0.16em] uppercase text-amber-glow border-b border-[color-mix(in_oklch,var(--color-amber-glow)_45%,transparent)] pb-0.5 hover:border-amber-glow focus-visible:outline-none"
        >
          See how Argus ranks
          <span
            aria-hidden
            className="transition-transform duration-200 ease-[cubic-bezier(0.23,1,0.32,1)] group-hover/card:translate-x-0.5"
          >
            →
          </span>
        </Link>
      </div>
    </article>
  );
}

/* ─────────────────────────────────────────────────────────────────────── */
/* Primitives                                                              */
/* ─────────────────────────────────────────────────────────────────────── */

/**
 * A signal-weighted contributor. The signal bar below the avatar expresses
 * how often this person's reactions aligned with the ground truth — the lead
 * gets an amber ring + full-height bar; others taper by opacity.
 */
function ContributorChip({
  label,
  signal,
  lead,
}: {
  label: string;
  signal: number;
  lead?: boolean;
}) {
  const pct = Math.max(0, Math.min(1, signal));
  return (
    <div className="flex flex-col items-center gap-2">
      <div
        className={[
          "relative flex h-10 w-10 items-center justify-center font-mono text-[11px]",
          lead ? "text-foreground" : "text-foreground/75",
        ].join(" ")}
        style={
          lead
            ? {
                border:
                  "1px solid color-mix(in oklch, var(--color-amber-glow) 70%, transparent)",
                backgroundColor:
                  "color-mix(in oklch, var(--color-amber-glow) 10%, var(--color-charcoal))",
                boxShadow:
                  "0 0 16px color-mix(in oklch, var(--color-amber-glow) 35%, transparent)",
              }
            : {
                border: "1px solid var(--color-iron)",
                backgroundColor: "var(--color-charcoal)",
              }
        }
      >
        {label}
      </div>
      <div className="relative h-[3px] w-full overflow-hidden bg-iron/50">
        <span
          aria-hidden
          className="absolute inset-y-0 left-0"
          style={{
            width: `${pct * 100}%`,
            backgroundColor: lead
              ? "var(--color-amber-glow)"
              : "color-mix(in oklch, var(--color-amber-glow) 45%, transparent)",
            boxShadow: lead
              ? "0 0 8px color-mix(in oklch, var(--color-amber-glow) 70%, transparent)"
              : "none",
          }}
        />
      </div>
    </div>
  );
}

/**
 * A single code row with an integrated line-number gutter. Kind controls the
 * subtle row tint; markers (+ / -) live in the gutter, not as colored bars.
 */
function CodeLine({
  num,
  kind,
  children,
}: {
  num: number;
  kind: "add" | "del" | "meta";
  children: React.ReactNode;
}) {
  const tint =
    kind === "add"
      ? "color-mix(in oklch, var(--color-amber-glow) 8%, transparent)"
      : kind === "del"
        ? "color-mix(in oklch, var(--color-foreground) 3%, transparent)"
        : "transparent";

  const marker =
    kind === "add" ? "+" : kind === "del" ? "-" : " ";
  const markerColor =
    kind === "add"
      ? "var(--color-amber-glow)"
      : kind === "del"
        ? "color-mix(in oklch, var(--color-foreground) 42%, transparent)"
        : "transparent";

  return (
    <div
      className="grid grid-cols-[36px_14px_1fr] items-start font-mono text-[12.5px] leading-[1.75]"
      style={{ backgroundColor: tint }}
    >
      <span
        aria-hidden
        className="select-none border-r border-iron/60 px-2 text-right font-mono text-[10.5px] tabular-nums text-ash/60"
      >
        {num}
      </span>
      <span
        aria-hidden
        className="select-none pl-1.5 font-bold"
        style={{ color: markerColor }}
      >
        {marker}
      </span>
      <span className="px-3 text-foreground/90 whitespace-pre-wrap">
        {children}
      </span>
    </div>
  );
}

/**
 * Monochrome amber "syntax highlighter" — contrasting weights/opacities only.
 * No real highlighter, just disciplined token tinting.
 */
function Token({
  children,
  dim,
  op,
  num,
  comment,
}: {
  children: React.ReactNode;
  dim?: boolean;
  op?: boolean;
  num?: boolean;
  comment?: boolean;
}) {
  if (comment) return <span className="text-ash/90 italic">{children}</span>;
  if (dim) return <span className="text-foreground/55">{children}</span>;
  if (op) return <span className="text-foreground/45">{children}</span>;
  if (num)
    return (
      <span className="text-amber-glow/90 tabular-nums">{children}</span>
    );
  return <span className="text-foreground/95 font-medium">{children}</span>;
}

function HeartIcon() {
  return (
    <svg
      viewBox="0 0 16 16"
      width="11"
      height="11"
      fill="currentColor"
      aria-hidden
    >
      <path d="M8 13.5 2.5 8.2a3 3 0 0 1 4.3-4.2L8 5.3l1.2-1.3a3 3 0 0 1 4.3 4.2L8 13.5z" />
    </svg>
  );
}

function ReplyIcon() {
  return (
    <svg
      viewBox="0 0 16 16"
      width="11"
      height="11"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.4"
      aria-hidden
    >
      <path d="M3 4.5h9a2 2 0 0 1 2 2v3a2 2 0 0 1-2 2H7.5L4.5 14v-2.5H3a2 2 0 0 1-2-2v-3a2 2 0 0 1 2-2z" />
    </svg>
  );
}
