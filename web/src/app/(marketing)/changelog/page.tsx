import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Changelog",
  description:
    "Weekly rollup of what shipped in Argus — bug fixes, features, deploy notes. Subscribe to get each update in your inbox.",
  alternates: { canonical: "https://argus.reviews/changelog" },
};

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
        A weekly rollup is on the way. First entry drops with the launch out of
        beta. Subscribe to get it in your inbox.
      </p>

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
