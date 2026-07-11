import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Changelog",
  description:
    "Weekly rollup of what shipped in Argus — bug fixes, features, deploy notes. Subscribe to get each update in your inbox.",
  alternates: { canonical: "https://argus.reviews/changelog" },
};

const ENTRIES: {
  date: string;
  title: string;
  items: string[];
}[] = [
  {
    date: "2026-07-11",
    title: "The review doctrine",
    items: [
      "Review Contract: every PR gets a computed contract — change class (production, migration, one-off script, test, config, docs, generated, revert) from deterministic signals like draft flag, labels, branch prefix, paths, title, and size. The LLM fills intent only when metadata is silent. Visible on every review.",
      "Depth follows the contract: one-off scripts get a single balanced reviewer (correctness + data safety) instead of the full specialist squad; docs and generated changes skip the second pass. The security/migration floor never relaxes.",
      "Review Laws: one severity rubric everywhere, silence is a valid review, no praise filler, style is the linter's job. Every finding must carry a concrete failure scenario, file:line evidence, and a suggested fix.",
      "Judge scoring now runs on every review, every plan — class-aware thresholds, hard cap of 10 inline comments, near-threshold findings fold into a collapsed Minor notes section. No minimum-comment behavior.",
      "Team-feedback memory: dismissals become semantic memories with the reason and change kind; repeated dismissed patterns auto-suppress (security never suppressed). One-off-script dismissals don't silence production reviews.",
      "Re-reviews resolve Argus's own fixed comments (“Resolved by <sha>”) and only post what's new.",
      "Glass Box footer on every review: contract, what was checked, suppressed count, duration. Gauge tracks address rate — whether comments actually led to code changes, human-fix weighted, per category per change class.",
      "Oversized PRs get an honest reduced-confidence note and a split recommendation instead of a fake-thorough review.",
    ],
  },
];

export default function ChangelogPage() {
  return (
    <section className="mx-auto max-w-3xl px-6 py-28">
      <p className="mb-3 text-[11px] font-mono uppercase tracking-[0.15em] text-amber">
        Changelog
      </p>
      <h1 className="font-display text-4xl font-bold text-foreground mb-6">
        What shipped, week by week.
      </h1>
      <p className="text-sm text-slate-text mb-10 max-w-md">
        Bug fixes, features, deploy notes. Subscribe to get each update in
        your inbox.
      </p>

      <div className="space-y-12 mb-16">
        {ENTRIES.map((entry) => (
          <article key={entry.date} className="border-l-2 border-amber/40 pl-6">
            <time
              dateTime={entry.date}
              className="text-[11px] font-mono uppercase tracking-[0.15em] text-amber"
            >
              {entry.date}
            </time>
            <h2 className="font-display text-2xl font-bold text-foreground mt-1 mb-4">
              {entry.title}
            </h2>
            <ul className="space-y-2.5">
              {entry.items.map((item) => (
                <li
                  key={item}
                  className="flex gap-2.5 text-xs font-mono text-slate-text leading-relaxed"
                >
                  <span className="text-amber shrink-0 select-none">+</span>
                  {item}
                </li>
              ))}
            </ul>
          </article>
        ))}
      </div>

      <div className="border border-iron bg-charcoal p-6 max-w-md">
        <p className="text-xs font-mono text-foreground mb-4">
          Get notified when each changelog drops.
        </p>
        <form
          action="https://buttondown.com/api/emails/embed-subscribe/argus"
          method="post"
          target="_blank"
          className="flex gap-2"
        >
          <input
            type="email"
            name="email"
            placeholder="you@company.com"
            required
            className="flex-1 border border-iron bg-background px-3 py-2 text-xs font-mono text-foreground placeholder:text-iron focus:border-amber focus:outline-none"
          />
          <button
            type="submit"
            className="bg-amber px-5 py-2 text-xs font-mono font-medium text-void hover:brightness-110 transition-all shrink-0"
          >
            Subscribe
          </button>
        </form>
        <p className="text-[10px] font-mono text-iron mt-3">
          No spam. Unsubscribe anytime.
        </p>
      </div>
    </section>
  );
}
