"use client";

import Link from "next/link";
import { PieChart, Pie, Cell, Tooltip, ResponsiveContainer } from "recharts";
import { SectionHeader, Loading, Err } from "./stat-cards";
import { fmt$, scoreColor, PIE_COLORS, tooltipStyle } from "./formatters";

interface UserStat {
  pr_author: string;
  review_count: number;
  avg_score: number;
  score_stddev: number;
  total_cost: number;
  critical_count: number;
}

interface ModelStat {
  model: string;
  total_tokens: number;
  total_cost: number;
  review_count: number;
}

interface UsersModelsSectionProps {
  usersLoading: boolean;
  usersError: boolean;
  usersData: UserStat[] | undefined;
  modelsLoading: boolean;
  modelsError: boolean;
  modelPieData: ModelStat[];
  totalModelCost: number;
}

export function UsersModelsSection({
  usersLoading,
  usersError,
  usersData,
  modelsLoading,
  modelsError,
  modelPieData,
  totalModelCost,
}: UsersModelsSectionProps) {
  return (
    <div className="grid grid-cols-1 lg:grid-cols-2 gap-4 pt-10 mb-10">
      <section>
        <SectionHeader title="Users" tip="PR authors ranked by review count" />
        {usersLoading ? (
          <Loading />
        ) : usersError ? (
          <Err label="users" />
        ) : (
          <div className="border border-border bg-card overflow-hidden">
            <table className="w-full text-[11px] font-mono">
              <thead>
                <tr className="border-b border-border">
                  <th className="text-left px-4 py-3 text-muted-foreground font-medium">Author</th>
                  <th className="text-right px-4 py-3 text-muted-foreground font-medium">PRs</th>
                  <th className="text-right px-4 py-3 text-muted-foreground font-medium">Avg</th>
                  <th className="text-right px-4 py-3 text-muted-foreground font-medium">Cost</th>
                  <th className="text-right px-4 py-3 text-muted-foreground font-medium">Crit</th>
                </tr>
              </thead>
              <tbody>
                {(usersData ?? []).map((u) => (
                  <tr
                    key={u.pr_author}
                    className="border-b border-border/30 hover:bg-accent/50 transition-colors"
                  >
                    <td className="px-4 py-3">
                      <Link
                        href={`/reviews?author=${encodeURIComponent(u.pr_author)}`}
                        className="text-primary hover:text-primary/80 transition-colors"
                      >
                        {u.pr_author}
                      </Link>
                    </td>
                    <td className="text-right px-4 py-3 text-muted-foreground">
                      {u.review_count}
                    </td>
                    <td className="text-right px-4 py-3">
                      <span className={scoreColor(u.avg_score)}>{u.avg_score.toFixed(1)}</span>
                      {/* Only show stddev with ≥2 reviews — a single value
                          has stddev 0 but that's misleading, it just means
                          "we don't know the spread yet". */}
                      {u.review_count >= 2 && (
                        <span
                          className="ml-1 text-muted-foreground/70"
                          title="Standard deviation — higher = more variance across this author's review scores"
                        >
                          ± {u.score_stddev.toFixed(1)}
                        </span>
                      )}
                    </td>
                    <td className="text-right px-4 py-3 text-muted-foreground">
                      {fmt$(u.total_cost)}
                    </td>
                    <td className="text-right px-4 py-3">
                      {u.critical_count > 0 ? (
                        <span className="text-destructive">{u.critical_count}</span>
                      ) : (
                        <span className="text-muted-foreground/50">0</span>
                      )}
                    </td>
                  </tr>
                ))}
                {(!usersData || usersData.length === 0) && (
                  <tr>
                    <td colSpan={5} className="text-center py-8 text-muted-foreground">
                      No data
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        )}
      </section>

      <section>
        <SectionHeader
          title="Cost by Model"
          tip="LLM cost aggregated across all pipeline stages"
        />
        {modelsLoading ? (
          <Loading />
        ) : modelsError ? (
          <Err label="models" />
        ) : (
          <div className="border border-border bg-card p-5">
            <div className="h-52 flex items-center justify-center relative">
              {modelPieData.length > 0 ? (
                <>
                  <ResponsiveContainer width="100%" height="100%">
                    <PieChart>
                      <Pie
                        data={modelPieData}
                        dataKey="total_cost"
                        nameKey="model"
                        cx="50%"
                        cy="50%"
                        innerRadius={45}
                        outerRadius={80}
                        paddingAngle={2}
                        strokeWidth={0}
                      >
                        {modelPieData.map((entry, i) => (
                          <Cell key={entry.model} fill={PIE_COLORS[i % PIE_COLORS.length]} />
                        ))}
                      </Pie>
                      <Tooltip
                        contentStyle={tooltipStyle}
                        formatter={(v) => [fmt$(Number(v)), "Cost"]}
                      />
                    </PieChart>
                  </ResponsiveContainer>
                  <div className="absolute inset-0 flex items-center justify-center pointer-events-none">
                    <span className="text-sm font-mono font-bold text-foreground">
                      {fmt$(totalModelCost)}
                    </span>
                  </div>
                </>
              ) : (
                <span className="text-xs font-mono text-muted-foreground">No model data</span>
              )}
            </div>
            <div className="mt-4 space-y-2">
              {modelPieData.map((m, i) => (
                <div key={m.model} className="flex items-center gap-2 text-[10px] font-mono">
                  <span
                    className="w-2 h-2 rounded-full shrink-0"
                    style={{ background: PIE_COLORS[i % PIE_COLORS.length] }}
                  />
                  <span className="text-foreground truncate flex-1">{m.model}</span>
                  <span className="text-muted-foreground">
                    {totalModelCost > 0
                      ? `${((m.total_cost / totalModelCost) * 100).toFixed(0)}%`
                      : "—"}
                  </span>
                </div>
              ))}
            </div>
          </div>
        )}
      </section>
    </div>
  );
}
