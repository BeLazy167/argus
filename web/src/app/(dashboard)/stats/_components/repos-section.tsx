"use client";

import { SectionHeader, Loading, Err } from "./stat-cards";
import { fmt$, fmtTok, fmtSecs, scoreColor } from "./formatters";

interface RepoStat {
  repo_id: number;
  full_name: string;
  review_count: number;
  avg_score: number;
  total_cost: number;
  avg_review_secs: number;
  total_tokens: number;
}

interface ReposSectionProps {
  isLoading: boolean;
  isError: boolean;
  data: RepoStat[] | undefined;
}

export function ReposSection({ isLoading, isError, data }: ReposSectionProps) {
  return (
    <section className="pt-10 mb-10">
      <SectionHeader title="By Repository" tip="Metrics broken down per enabled repo" />
      {isLoading ? (
        <Loading />
      ) : isError ? (
        <Err label="repos" />
      ) : (
        <>
          {/* Desktop table */}
          <div className="hidden md:block border border-border bg-card overflow-x-auto">
            <table className="w-full text-[11px] font-mono">
              <thead>
                <tr className="border-b border-border">
                  <th className="text-left px-4 py-3 text-muted-foreground font-medium">Repo</th>
                  <th className="text-right px-4 py-3 text-muted-foreground font-medium">
                    Reviews
                  </th>
                  <th className="text-right px-4 py-3 text-muted-foreground font-medium">
                    Avg Score
                  </th>
                  <th className="text-right px-4 py-3 text-muted-foreground font-medium">Cost</th>
                  <th className="text-right px-4 py-3 text-muted-foreground font-medium">
                    Avg Time
                  </th>
                  <th className="text-right px-4 py-3 text-muted-foreground font-medium">
                    Tokens
                  </th>
                </tr>
              </thead>
              <tbody>
                {(data ?? []).map((r) => (
                  <tr
                    key={r.repo_id}
                    className="border-b border-border/30 hover:bg-accent/50 transition-colors"
                  >
                    <td className="px-4 py-3 text-foreground">
                      {r.full_name.split("/")[1] || r.full_name}
                    </td>
                    <td className="text-right px-4 py-3 text-muted-foreground">{r.review_count}</td>
                    <td className="text-right px-4 py-3">
                      <span className={scoreColor(r.avg_score)}>{r.avg_score.toFixed(1)}</span>
                    </td>
                    <td className="text-right px-4 py-3 text-muted-foreground">
                      {fmt$(r.total_cost)}
                    </td>
                    <td className="text-right px-4 py-3 text-muted-foreground">
                      {r.avg_review_secs > 0 ? fmtSecs(r.avg_review_secs) : "—"}
                    </td>
                    <td className="text-right px-4 py-3 text-muted-foreground">
                      {fmtTok(r.total_tokens)}
                    </td>
                  </tr>
                ))}
                {(!data || data.length === 0) && (
                  <tr>
                    <td colSpan={6} className="text-center py-8 text-muted-foreground">
                      No repos
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
          {/* Mobile cards */}
          <div className="md:hidden space-y-3">
            {(data ?? []).map((r) => (
              <div key={r.repo_id} className="border border-border bg-card p-4">
                <span className="text-sm font-mono font-bold text-foreground block mb-3">
                  {r.full_name.split("/")[1] || r.full_name}
                </span>
                <div className="grid grid-cols-4 gap-2 text-[10px] font-mono">
                  <div>
                    <span
                      className="text-muted-foreground uppercase block"
                      style={{ letterSpacing: "0.5px" }}
                    >
                      Reviews
                    </span>
                    <span className="text-foreground font-bold">{r.review_count}</span>
                  </div>
                  <div>
                    <span
                      className="text-muted-foreground uppercase block"
                      style={{ letterSpacing: "0.5px" }}
                    >
                      Score
                    </span>
                    <span className={`font-bold ${scoreColor(r.avg_score)}`}>
                      {r.avg_score.toFixed(1)}
                    </span>
                  </div>
                  <div>
                    <span
                      className="text-muted-foreground uppercase block"
                      style={{ letterSpacing: "0.5px" }}
                    >
                      Cost
                    </span>
                    <span className="text-foreground font-bold">{fmt$(r.total_cost)}</span>
                  </div>
                  <div>
                    <span
                      className="text-muted-foreground uppercase block"
                      style={{ letterSpacing: "0.5px" }}
                    >
                      Time
                    </span>
                    <span className="text-foreground font-bold">
                      {r.avg_review_secs > 0 ? fmtSecs(r.avg_review_secs) : "—"}
                    </span>
                  </div>
                </div>
              </div>
            ))}
            {(!data || data.length === 0) && (
              <div className="border border-border bg-card p-8 text-center text-xs font-mono text-muted-foreground">
                No repos
              </div>
            )}
          </div>
        </>
      )}
    </section>
  );
}
