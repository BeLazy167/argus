export default function DocsPage() {
  return (
    <section className="mx-auto max-w-3xl px-6 py-28">
      <p className="mb-3 text-[11px] font-mono uppercase tracking-[0.15em] text-amber">
        Documentation
      </p>
      <h1 className="font-display text-4xl font-bold text-foreground mb-6">
        Getting Started
      </h1>
      <p className="text-sm text-slate-text mb-10">
        Install the GitHub App. Argus reviews every PR automatically.
      </p>

      <div className="space-y-8">
        {[
          {
            step: "1",
            title: "Install the GitHub App",
            code: "Visit github.com/apps/argus-bot → Install",
          },
          {
            step: "2",
            title: "Select repositories",
            code: "Choose which repos Argus should watch",
          },
          {
            step: "3",
            title: "Open a PR",
            code: "Argus reviews automatically on every push",
          },
          {
            step: "4",
            title: "Customize rules (optional)",
            code: "Add .argus/rules.md to your repo",
          },
        ].map((item) => (
          <div key={item.step} className="flex gap-5">
            <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-md bg-amber/10 text-xs font-mono font-medium text-amber">
              {item.step}
            </span>
            <div>
              <h3 className="text-sm font-bold text-foreground mb-1">
                {item.title}
              </h3>
              <code className="text-xs font-mono text-slate-text bg-iron/50 rounded px-2 py-1">
                {item.code}
              </code>
            </div>
          </div>
        ))}
      </div>
    </section>
  );
}
