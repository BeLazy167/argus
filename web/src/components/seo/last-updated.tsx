/**
 * Renders a "Last updated" line for docs/blog pages. AI systems weight recency
 * heavily when selecting citation sources — visible timestamps beat undated
 * content even when the undated content is newer. Emits a <time> element with
 * machine-readable dateTime so both humans and crawlers can parse it.
 *
 * Usage:
 *   <LastUpdated date="2026-04-17" />
 */
export function LastUpdated({ date }: { date: string }) {
  const display = new Date(date + "T00:00:00Z").toLocaleDateString("en-US", {
    year: "numeric",
    month: "long",
    day: "numeric",
    timeZone: "UTC",
  });
  return (
    <p className="mb-8 font-mono text-[11px] uppercase tracking-[0.18em] text-slate-500">
      Last updated{" "}
      <time dateTime={date} className="text-slate-300">
        {display}
      </time>
    </p>
  );
}
