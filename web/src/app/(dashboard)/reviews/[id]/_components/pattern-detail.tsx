import { usePattern } from "@/lib/queries/patterns";

export function PatternDetail({ patternId }: { patternId: number }) {
  const { data: pattern, isLoading } = usePattern({ variables: { id: patternId } });
  if (isLoading) return <div className="mt-2 text-[11px] font-mono text-slate-text">Loading pattern...</div>;
  if (!pattern) return <div className="mt-2 text-[11px] font-mono text-slate-text">Pattern not found</div>;
  return (
    <div className="mt-2 border border-iron bg-iron/10 p-3">
      <p className="text-[11px] font-mono text-slate-text uppercase tracking-wider mb-1">Matched Pattern</p>
      <p className="text-xs font-mono text-foreground whitespace-pre-wrap">{pattern.content}</p>
      <div className="flex gap-4 mt-2 text-[11px] font-mono text-slate-text">
        {pattern.source && <span>Source: {pattern.source}</span>}
        {pattern.category && <span>Category: {pattern.category}</span>}
      </div>
    </div>
  );
}
