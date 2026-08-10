export function EmptyState({ text }: { text: string }) {
  return (
    <p className="text-[10px] font-mono text-[var(--graph-text-muted)] py-1">{text}</p>
  );
}

export function MetricRow({
  label,
  value,
  pct,
}: {
  label: string;
  value: number | string;
  pct?: string;
}) {
  return (
    <div className="flex items-center justify-between text-[10px] font-mono">
      <span className="text-[var(--graph-text-muted)]">{label}</span>
      <div className="flex items-center gap-2">
        <span className="text-[var(--graph-text)] tabular-nums">{value}</span>
        {pct && <span className="text-amber-500/80">{pct}</span>}
      </div>
    </div>
  );
}
