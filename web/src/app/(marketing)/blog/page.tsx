import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Engineering Log",
  description:
    "Technical deep dives on AI code review, codebase comprehension, and how Argus gets smarter with every pull request.",
  alternates: { canonical: "https://argus.reviews/blog" },
};

export default function BlogPage() {
  return (
    <section className="mx-auto max-w-3xl px-6 py-28">
      <p className="mb-3 text-[11px] font-mono uppercase tracking-[0.15em] text-amber">
        Blog
      </p>
      <h1 className="font-display text-4xl font-bold text-foreground mb-6">
        Engineering Log
      </h1>
      <p className="text-sm text-slate-text mb-10 max-w-md">
        We&apos;re heads-down building. Posts are coming. Subscribe to get
        notified when we publish.
      </p>

      <div className="border border-iron bg-charcoal p-6 max-w-md">
        <p className="text-xs font-mono text-foreground mb-4">
          Get notified on new posts and product updates.
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
            className="bg-amber px-5 py-2 text-xs font-mono font-medium text-void hover:brightness-110 transition-[filter] shrink-0"
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
