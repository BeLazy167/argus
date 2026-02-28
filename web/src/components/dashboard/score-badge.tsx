import { scoreColor } from "@/lib/score";

/** Inline score number with color. Used in list rows. */
export function ScoreBadge({ score }: { score?: number }) {
  if (score == null) return <span className="text-lg font-mono text-slate-text">&mdash;</span>;
  return <span className={`font-mono text-lg font-medium ${scoreColor(score)}`}>{score}</span>;
}

/** Larger boxed score. Used on review detail page. */
export function ScoreBox({ score }: { score?: number }) {
  if (score == null) return null;
  const border =
    score >= 8
      ? "text-green-400 border-green-400/20 bg-green-400/5"
      : score >= 5
        ? "text-amber border-amber/20 bg-amber/5"
        : "text-red-400 border-red-400/20 bg-red-400/5";
  return (
    <div className={`flex items-center justify-center h-14 w-14 rounded-lg border ${border}`}>
      <span className="font-mono text-2xl font-bold">{score}</span>
    </div>
  );
}
