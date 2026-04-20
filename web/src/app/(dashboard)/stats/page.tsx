"use client";

import { useMemo } from "react";
import Link from "next/link";
import {
  AreaChart, Area, XAxis, YAxis, Tooltip, ResponsiveContainer,
  PieChart, Pie, Cell,
} from "recharts";
import {
  Loader2, TrendingUp, DollarSign, Zap, Clock, AlertTriangle, Target,
  Shield, Info, Timer, Check, Network, Brain, FlaskConical, History, ThumbsUp,
} from "lucide-react";
import { useStatsStore } from "@/lib/stores/stats-store";
import {
  useStatsOverview,
  useStatsTimeseries,
  useStatsUsers,
  useStatsModels,
  useStatsFindings,
  useStatsAdoption,
  useStatsRepos,
  useStatsReviewTimes,
  useStatsCostPerStage,
  type Period,
} from "@/lib/queries/org-stats";
import { stageColor, stageLabel } from "@/lib/stage-labels";

const PERIODS: { value: Period; label: string }[] = [
  { value: "7d", label: "7d" },
  { value: "30d", label: "30d" },
  { value: "90d", label: "90d" },
];

const PIE_COLORS = ["#f59e0b", "#3b82f6", "#10b981", "#ef4444", "#8b5cf6", "#ec4899", "#06b6d4", "#84cc16"];
const SEV_COLORS: Record<string, string> = { critical: "#ef4444", warning: "#f59e0b", suggestion: "#3b82f6", praise: "#10b981" };

function fmt$(n: number) { return n < 1 ? `$${n.toFixed(3)}` : `$${n.toFixed(2)}`; }
function fmtTok(n: number) { return n >= 1e6 ? `${(n / 1e6).toFixed(1)}M` : n >= 1e3 ? `${(n / 1e3).toFixed(1)}k` : String(n); }
function fmtSecs(s: number) { return s >= 60 ? `${Math.floor(s / 60)}m ${s % 60}s` : `${s}s`; }
/** Tailwind class for a review score (1-10 scale). Red <=4, green >=7, amber otherwise. */
function scoreColor(n: number): string {
  if (n <= 4) return "text-destructive";
  if (n >= 7) return "text-green-500";
  return "text-primary";
}

/* --- Reusable Components --- */

function Tip({ text }: { text: string }) {
  return (
    <span className="group relative inline-flex ml-1 cursor-help">
      <Info className="h-3 w-3 text-muted-foreground/50 group-hover:text-muted-foreground transition-colors" />
      <span className="pointer-events-none absolute bottom-full left-1/2 -translate-x-1/2 mb-1.5 w-52 bg-popover border border-border px-2.5 py-1.5 text-[10px] font-mono text-popover-foreground opacity-0 group-hover:opacity-100 transition-opacity z-50 shadow-lg">
        {text}
      </span>
    </span>
  );
}

function StatCard({ label, value, icon: Icon, tip, sub, valueColor, iconColor }: {
  label: string; value: string; icon: React.ComponentType<{ className?: string }>;
  tip?: string; sub?: string; valueColor?: string; iconColor?: string;
}) {
  return (
    <div className="border border-border bg-card p-5 flex flex-col gap-2 hover:border-primary/30 transition-colors">
      <div className="flex items-center gap-1.5">
        <Icon className={`h-3.5 w-3.5 ${iconColor ?? "text-muted-foreground"}`} />
        <span className="text-[10px] font-mono uppercase text-muted-foreground" style={{ letterSpacing: "1.2px" }}>{label}</span>
        {tip && <Tip text={tip} />}
      </div>
      <span className={`text-[32px] leading-none font-mono font-bold tracking-tight ${valueColor ?? "text-foreground"}`}>{value}</span>
      {sub && <span className="text-[10px] font-mono text-muted-foreground">{sub}</span>}
    </div>
  );
}

function SectionHeader({ title, tip }: { title: string; tip?: string }) {
  return (
    <div className="flex items-center gap-1.5 mb-4">
      <h2 className="text-xs font-mono font-bold text-foreground uppercase" style={{ letterSpacing: "1.5px" }}>{title}</h2>
      {tip && <Tip text={tip} />}
    </div>
  );
}

function Loading() { return <div className="flex items-center justify-center py-12"><Loader2 className="h-5 w-5 animate-spin text-muted-foreground" /></div>; }
function Err({ label }: { label: string }) { return <div className="flex items-center justify-center py-12 text-xs font-mono text-destructive">Failed to load {label}</div>; }

const tooltipStyle = { background: "hsl(var(--card))", border: "1px solid hsl(var(--border))", fontSize: 11, fontFamily: "monospace", color: "hsl(var(--foreground))" };

/* --- Page --- */

export default function StatsPage() {
  const { period, setPeriod } = useStatsStore();
  const overview = useStatsOverview({ variables: { period } });
  const timeseries = useStatsTimeseries({ variables: { period } });
  const users = useStatsUsers({ variables: { period } });
  const models = useStatsModels({ variables: { period } });
  const findings = useStatsFindings({ variables: { period } });
  const adoption = useStatsAdoption({ variables: { period } });
  const repos = useStatsRepos({ variables: { period } });
  const reviewTimes = useStatsReviewTimes({ variables: { period } });
  const costPerStage = useStatsCostPerStage({ variables: { period } });

  const chartData = useMemo(() => (timeseries.data ?? []).map(d => ({ ...d, day: d.day.slice(5) })), [timeseries.data]);
  const allModels = useMemo(() => [...(models.data ?? [])].sort((a, b) => b.total_cost - a.total_cost), [models.data]);
  const modelPieData = useMemo(() => allModels.slice(0, 8), [allModels]);
  const sevData = useMemo(() => (findings.data?.by_severity ?? []).filter(s => s.severity), [findings.data]);
  const stageCostData = useMemo(() => [...(costPerStage.data ?? [])].sort((a, b) => b.total_cost - a.total_cost), [costPerStage.data]);
  const costPerFinding = useMemo(() => {
    if (!overview.data || !findings.data) return 0;
    const totalFindings = findings.data.by_severity.reduce((s, v) => s + v.count, 0);
    return totalFindings > 0 ? overview.data.total_cost / totalFindings : 0;
  }, [overview.data, findings.data]);
  const latestPoint = chartData[chartData.length - 1];
  // Totals include ALL models, not just the top-8 shown in the donut — otherwise
  // the legend's "%" column would sum to 100% of the visible slice instead of
  // 100% of the true spend, misrepresenting tail-model cost.
  const totalModelCost = useMemo(() => allModels.reduce((s, m) => s + m.total_cost, 0), [allModels]);

  return (
    <>
      {/* Sticky header */}
      <div className="sticky top-0 z-10 bg-background/80 backdrop-blur-md border-b border-border -mx-6 px-6 py-3 mb-8 flex items-center justify-between">
        <div>
          <h1 className="text-lg font-mono font-bold text-foreground">Org Analytics</h1>
          <p className="text-[10px] font-mono text-muted-foreground">All repos &middot; last {period}</p>
        </div>
        <div className="flex items-center border border-border bg-card">
          {PERIODS.map(p => (
            <button key={p.value} type="button" onClick={() => setPeriod(p.value)}
              className={`w-12 h-8 text-[11px] font-mono transition-colors cursor-pointer ${
                period === p.value ? "bg-primary/10 text-primary font-semibold" : "text-muted-foreground hover:text-foreground"
              }`}>
              {p.label}
            </button>
          ))}
        </div>
      </div>

      {/* Overview cards */}
      {overview.isLoading ? <Loading /> : overview.isError ? <Err label="overview" /> : overview.data && (
        <>
          <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mb-4">
            <StatCard label="Reviews" value={String(overview.data.total_reviews)} icon={TrendingUp}
              tip="Total completed/failed/cancelled reviews in period" />
            <StatCard label="Cost" value={fmt$(overview.data.total_cost)} icon={DollarSign}
              tip="Sum of LLM API costs across all stages" />
            <StatCard label="Score" value={`${overview.data.avg_score.toFixed(1)}/10`} icon={Target}
              tip="Mean review score (1=critical, 10=clean)"
              valueColor="text-primary" />
            <StatCard label="Time" value={fmtSecs(overview.data.avg_review_secs)} icon={Clock}
              tip="Average wall-clock time per review" />
          </div>
          <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mb-4">
            <StatCard label="Tokens" value={fmtTok(overview.data.total_tokens)} icon={Zap}
              tip="Total LLM tokens consumed (input + output)" />
            <StatCard label="Critical" value={String(overview.data.critical_finds)} icon={AlertTriangle}
              tip="Review comments with severity=critical"
              valueColor="text-destructive" iconColor="text-destructive" />
            <StatCard label="Detection" value={`${overview.data.catch_rate}%`} icon={Shield}
              tip="% of reviews where issues were found (score < 10)"
              valueColor="text-green-500" iconColor="text-green-500" />
            <StatCard label="Cost/Finding" value={costPerFinding > 0 ? fmt$(costPerFinding) : "\u2014"} icon={DollarSign}
              tip="Total cost \u00f7 total findings" />
          </div>
        </>
      )}

      {/* Review time percentiles */}
      {reviewTimes.data && reviewTimes.data.count > 0 && (
        <div className="grid grid-cols-3 gap-4 mb-10">
          <StatCard label="p50" value={fmtSecs(reviewTimes.data.p50)} icon={Timer}
            tip="Median review duration" />
          <StatCard label="p75" value={fmtSecs(reviewTimes.data.p75)} icon={Timer}
            tip="75th percentile" />
          <StatCard label="p95" value={fmtSecs(reviewTimes.data.p95)} icon={Timer}
            tip="95th percentile"
            valueColor="text-destructive" iconColor="text-destructive" />
        </div>
      )}

      {/* Automated hygiene — diff-only operations, zero LLM cost.
          Surfacing these separately so users don't confuse them with the
          LLM-paid review activity above. */}
      {overview.data && (
        <section className="pt-4 mb-10">
          <SectionHeader title="Automated hygiene" tip="Free operations we ran on your behalf — no LLM tokens consumed" />
          <div className="grid grid-cols-2 md:grid-cols-3 gap-4">
            <StatCard
              label="Auto-resolves"
              value={String(overview.data.auto_resolves)}
              icon={Check}
              tip="Stale review threads we closed automatically when your push modified the flagged lines. Diff-based, no LLM call."
              valueColor="text-green-500"
              iconColor="text-green-500"
            />
            <StatCard
              label="Attempts"
              value={String(overview.data.auto_resolve_attempts)}
              icon={Target}
              tip="Thread-close attempts, including any that GitHub rejected. Resolved ≤ Attempts because GitHub may already have marked a thread resolved."
            />
            <StatCard
              label="GitHub API"
              value={String(overview.data.auto_resolve_api_calls)}
              icon={Network}
              tip="GitHub API calls auto-resolve issued — visible for rate-limit accounting on your installation token."
            />
          </div>
          {overview.data.auto_resolve_events === 0 && (
            <p className="mt-3 text-[10px] font-mono text-muted-foreground">
              No auto-resolves yet. Push a commit that changes a line Argus flagged to see this in action.
            </p>
          )}
        </section>
      )}

      {/* Learn layer — BYOK-paid side effects of the memory pipeline.
          Shown separately from the review cards because the cost lives
          in your Supermemory bill, not the LLM bill. */}
      {overview.data && (
        <section className="pt-4 mb-10">
          <SectionHeader title="Learn layer" tip="Memory activity for this period. Each counter maps to a row your BYOK Supermemory account is storing." />
          <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
            <StatCard
              label="Patterns"
              value={String(overview.data.patterns_learned)}
              icon={Brain}
              tip="New cross-repo patterns the pipeline learned this period."
            />
            <StatCard
              label="Scenarios"
              value={String(overview.data.scenarios_stored)}
              icon={FlaskConical}
              tip="New failure scenarios stored. Drive the code-simulation pass on future reviews."
            />
            <StatCard
              label="Decisions"
              value={String(overview.data.decision_traces)}
              icon={History}
              tip="Decision-trace rows — Argus findings linked to dev agrees/dismisses/fixes."
            />
            <StatCard
              label="Feedback"
              value={String(overview.data.feedback_indexed)}
              icon={ThumbsUp}
              tip="Reactions/outcomes captured from reviewer feedback on posted comments."
            />
          </div>
        </section>
      )}

      {/* Trends */}
      <section className="pt-10 mb-10">
        <SectionHeader title="Trends" tip="Daily aggregates for the selected period" />
        {timeseries.isLoading ? <Loading /> : timeseries.isError ? <Err label="trends" /> : (
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            <ChartCard title="Reviews / Day" value={latestPoint ? String(latestPoint.review_count) : "\u2014"} color="#10b981">
              <AreaChart data={chartData}>
                <XAxis dataKey="day" tick={{ fontSize: 10, fill: "hsl(var(--muted-foreground))" }} stroke="hsl(var(--border))" />
                <YAxis tick={{ fontSize: 10, fill: "hsl(var(--muted-foreground))" }} width={30} stroke="hsl(var(--border))" />
                <Tooltip contentStyle={tooltipStyle} />
                <Area type="monotone" dataKey="review_count" stroke="#10b981" fill="#10b981" fillOpacity={0.15} strokeWidth={1.5} />
              </AreaChart>
            </ChartCard>
            <ChartCard title="Cost / Day" value={latestPoint ? fmt$(latestPoint.total_cost) : "\u2014"} color="#3b82f6">
              <AreaChart data={chartData}>
                <XAxis dataKey="day" tick={{ fontSize: 10, fill: "hsl(var(--muted-foreground))" }} stroke="hsl(var(--border))" />
                <YAxis tick={{ fontSize: 10, fill: "hsl(var(--muted-foreground))" }} width={40} stroke="hsl(var(--border))" tickFormatter={v => `$${v}`} />
                <Tooltip contentStyle={tooltipStyle} formatter={(v) => [`$${Number(v).toFixed(3)}`, "Cost"]} />
                <Area type="monotone" dataKey="total_cost" stroke="#3b82f6" fill="#3b82f6" fillOpacity={0.15} strokeWidth={1.5} />
              </AreaChart>
            </ChartCard>
            <ChartCard title="Score / Day" value={latestPoint ? latestPoint.avg_score.toFixed(1) : "\u2014"} color="#10b981">
              <AreaChart data={chartData}>
                <XAxis dataKey="day" tick={{ fontSize: 10, fill: "hsl(var(--muted-foreground))" }} stroke="hsl(var(--border))" />
                <YAxis tick={{ fontSize: 10, fill: "hsl(var(--muted-foreground))" }} width={30} domain={[0, 10]} stroke="hsl(var(--border))" />
                <Tooltip contentStyle={tooltipStyle} />
                <Area type="monotone" dataKey="avg_score" stroke="#10b981" fill="#10b981" fillOpacity={0.15} strokeWidth={1.5} />
              </AreaChart>
            </ChartCard>
            <ChartCard title="Tokens / Day" value={latestPoint ? fmtTok(latestPoint.total_tokens) : "\u2014"} color="#8b5cf6">
              <AreaChart data={chartData}>
                <XAxis dataKey="day" tick={{ fontSize: 10, fill: "hsl(var(--muted-foreground))" }} stroke="hsl(var(--border))" />
                <YAxis tick={{ fontSize: 10, fill: "hsl(var(--muted-foreground))" }} width={40} stroke="hsl(var(--border))" tickFormatter={v => fmtTok(Number(v))} />
                <Tooltip contentStyle={tooltipStyle} formatter={(v) => [fmtTok(Number(v)), "Tokens"]} />
                <Area type="monotone" dataKey="total_tokens" stroke="#8b5cf6" fill="#8b5cf6" fillOpacity={0.15} strokeWidth={1.5} />
              </AreaChart>
            </ChartCard>
          </div>
        )}
      </section>

      {/* Per-Repo breakdown */}
      <section className="pt-10 mb-10">
        <SectionHeader title="By Repository" tip="Metrics broken down per enabled repo" />
        {repos.isLoading ? <Loading /> : repos.isError ? <Err label="repos" /> : (
          <>
            {/* Desktop table */}
            <div className="hidden md:block border border-border bg-card overflow-x-auto">
              <table className="w-full text-[11px] font-mono">
                <thead>
                  <tr className="border-b border-border">
                    <th className="text-left px-4 py-3 text-muted-foreground font-medium">Repo</th>
                    <th className="text-right px-4 py-3 text-muted-foreground font-medium">Reviews</th>
                    <th className="text-right px-4 py-3 text-muted-foreground font-medium">Avg Score</th>
                    <th className="text-right px-4 py-3 text-muted-foreground font-medium">Cost</th>
                    <th className="text-right px-4 py-3 text-muted-foreground font-medium">Avg Time</th>
                    <th className="text-right px-4 py-3 text-muted-foreground font-medium">Tokens</th>
                  </tr>
                </thead>
                <tbody>
                  {(repos.data ?? []).map(r => (
                    <tr key={r.repo_id} className="border-b border-border/30 hover:bg-accent/50 transition-colors">
                      <td className="px-4 py-3 text-foreground">{r.full_name.split("/")[1] || r.full_name}</td>
                      <td className="text-right px-4 py-3 text-muted-foreground">{r.review_count}</td>
                      <td className="text-right px-4 py-3">
                        <span className={scoreColor(r.avg_score)}>
                          {r.avg_score.toFixed(1)}
                        </span>
                      </td>
                      <td className="text-right px-4 py-3 text-muted-foreground">{fmt$(r.total_cost)}</td>
                      <td className="text-right px-4 py-3 text-muted-foreground">{r.avg_review_secs > 0 ? fmtSecs(r.avg_review_secs) : "\u2014"}</td>
                      <td className="text-right px-4 py-3 text-muted-foreground">{fmtTok(r.total_tokens)}</td>
                    </tr>
                  ))}
                  {(!repos.data || repos.data.length === 0) && (
                    <tr><td colSpan={6} className="text-center py-8 text-muted-foreground">No repos</td></tr>
                  )}
                </tbody>
              </table>
            </div>
            {/* Mobile cards */}
            <div className="md:hidden space-y-3">
              {(repos.data ?? []).map(r => (
                <div key={r.repo_id} className="border border-border bg-card p-4">
                  <span className="text-sm font-mono font-bold text-foreground block mb-3">
                    {r.full_name.split("/")[1] || r.full_name}
                  </span>
                  <div className="grid grid-cols-4 gap-2 text-[10px] font-mono">
                    <div>
                      <span className="text-muted-foreground uppercase block" style={{ letterSpacing: "0.5px" }}>Reviews</span>
                      <span className="text-foreground font-bold">{r.review_count}</span>
                    </div>
                    <div>
                      <span className="text-muted-foreground uppercase block" style={{ letterSpacing: "0.5px" }}>Score</span>
                      <span className={`font-bold ${scoreColor(r.avg_score)}`}>
                        {r.avg_score.toFixed(1)}
                      </span>
                    </div>
                    <div>
                      <span className="text-muted-foreground uppercase block" style={{ letterSpacing: "0.5px" }}>Cost</span>
                      <span className="text-foreground font-bold">{fmt$(r.total_cost)}</span>
                    </div>
                    <div>
                      <span className="text-muted-foreground uppercase block" style={{ letterSpacing: "0.5px" }}>Time</span>
                      <span className="text-foreground font-bold">{r.avg_review_secs > 0 ? fmtSecs(r.avg_review_secs) : "\u2014"}</span>
                    </div>
                  </div>
                </div>
              ))}
              {(!repos.data || repos.data.length === 0) && (
                <div className="border border-border bg-card p-8 text-center text-xs font-mono text-muted-foreground">No repos</div>
              )}
            </div>
          </>
        )}
      </section>

      {/* Users + Cost by Model */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4 pt-10 mb-10">
        <section>
          <SectionHeader title="Users" tip="PR authors ranked by review count" />
          {users.isLoading ? <Loading /> : users.isError ? <Err label="users" /> : (
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
                  {(users.data ?? []).map(u => (
                    <tr key={u.pr_author} className="border-b border-border/30 hover:bg-accent/50 transition-colors">
                      <td className="px-4 py-3">
                        <Link href={`/reviews?author=${encodeURIComponent(u.pr_author)}`} className="text-primary hover:text-primary/80 transition-colors">{u.pr_author}</Link>
                      </td>
                      <td className="text-right px-4 py-3 text-muted-foreground">{u.review_count}</td>
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
                      <td className="text-right px-4 py-3 text-muted-foreground">{fmt$(u.total_cost)}</td>
                      <td className="text-right px-4 py-3">
                        {u.critical_count > 0 ? <span className="text-destructive">{u.critical_count}</span> : <span className="text-muted-foreground/50">0</span>}
                      </td>
                    </tr>
                  ))}
                  {(!users.data || users.data.length === 0) && <tr><td colSpan={5} className="text-center py-8 text-muted-foreground">No data</td></tr>}
                </tbody>
              </table>
            </div>
          )}
        </section>

        <section>
          <SectionHeader title="Cost by Model" tip="LLM cost aggregated across all pipeline stages" />
          {models.isLoading ? <Loading /> : models.isError ? <Err label="models" /> : (
            <div className="border border-border bg-card p-5">
              <div className="h-52 flex items-center justify-center relative">
                {modelPieData.length > 0 ? (
                  <>
                    <ResponsiveContainer width="100%" height="100%">
                      <PieChart>
                        <Pie data={modelPieData} dataKey="total_cost" nameKey="model" cx="50%" cy="50%" innerRadius={45} outerRadius={80} paddingAngle={2} strokeWidth={0}>
                          {modelPieData.map((entry, i) => <Cell key={entry.model} fill={PIE_COLORS[i % PIE_COLORS.length]} />)}
                        </Pie>
                        <Tooltip contentStyle={tooltipStyle} formatter={(v) => [fmt$(Number(v)), "Cost"]} />
                      </PieChart>
                    </ResponsiveContainer>
                    <div className="absolute inset-0 flex items-center justify-center pointer-events-none">
                      <span className="text-sm font-mono font-bold text-foreground">{fmt$(totalModelCost)}</span>
                    </div>
                  </>
                ) : <span className="text-xs font-mono text-muted-foreground">No model data</span>}
              </div>
              <div className="mt-4 space-y-2">
                {modelPieData.map((m, i) => (
                  <div key={m.model} className="flex items-center gap-2 text-[10px] font-mono">
                    <span className="w-2 h-2 rounded-full shrink-0" style={{ background: PIE_COLORS[i % PIE_COLORS.length] }} />
                    <span className="text-foreground truncate flex-1">{m.model}</span>
                    <span className="text-muted-foreground">{totalModelCost > 0 ? `${((m.total_cost / totalModelCost) * 100).toFixed(0)}%` : "\u2014"}</span>
                  </div>
                ))}
              </div>
            </div>
          )}
        </section>
      </div>

      {/* Cost by Stage + Findings */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4 pt-10 mb-10">
        <section>
          <SectionHeader title="Cost by Stage" tip="LLM cost breakdown by pipeline stage" />
          {costPerStage.isLoading ? <Loading /> : costPerStage.isError ? <Err label="cost per stage" /> : (
            <div className="border border-border bg-card p-5">
              {stageCostData.length > 0 ? (
                <div className="space-y-3">
                  {(() => {
                    const stageMax = Math.max(...stageCostData.map(x => x.total_cost), 0.01);
                    return stageCostData.map(s => (
                      <div key={s.stage} className="flex items-center gap-2 text-[10px] font-mono">
                        <span className="w-28 text-muted-foreground shrink-0 truncate" title={stageLabel(s.stage)}>{stageLabel(s.stage)}</span>
                        <div className="flex-1 h-4 bg-border/30 overflow-hidden" style={{ borderRadius: 2 }}>
                          <div className="h-full" style={{ width: `${(s.total_cost / stageMax) * 100}%`, background: stageColor(s.stage), borderRadius: 2 }} />
                        </div>
                        <span className="w-14 text-right text-muted-foreground shrink-0">{fmt$(s.total_cost)}</span>
                      </div>
                    ));
                  })()}
                </div>
              ) : <span className="text-xs font-mono text-muted-foreground">No data</span>}
            </div>
          )}
        </section>

        <section>
          <SectionHeader title="Findings" tip="Review comments by severity" />
          {findings.isLoading ? <Loading /> : findings.isError ? <Err label="findings" /> : findings.data && (
            <div className="border border-border bg-card p-5">
              <div className="space-y-2.5 mb-5">
                {(() => {
                  const sevMax = Math.max(...sevData.map(x => x.count), 1);
                  return sevData.map(s => (
                    <div key={s.severity} className="flex items-center gap-2 text-[10px] font-mono">
                      <span className="w-[70px] shrink-0 font-medium" style={{ color: SEV_COLORS[s.severity] ?? "#6b7280" }}>{s.severity}</span>
                      <div className="flex-1 h-3 bg-border/30 overflow-hidden" style={{ borderRadius: 2 }}>
                        <div className="h-full" style={{ width: `${(s.count / sevMax) * 100}%`, background: SEV_COLORS[s.severity] ?? "#6b7280", borderRadius: 2 }} />
                      </div>
                      <span className="w-8 text-right text-muted-foreground shrink-0">{s.count}</span>
                    </div>
                  ));
                })()}
              </div>
              <div className="flex gap-3 pt-3 border-t border-border/30">
                <div className="flex-1 border border-border bg-background p-3 text-center">
                  <span className="text-[10px] font-mono text-muted-foreground uppercase block mb-1">New</span>
                  <span className="text-lg font-mono font-bold text-foreground">{findings.data.new_findings}</span>
                </div>
                <div className="flex-1 border border-border bg-background p-3 text-center">
                  <span className="text-[10px] font-mono text-muted-foreground uppercase block mb-1">Pattern</span>
                  <span className="text-lg font-mono font-bold text-foreground">{findings.data.pattern_matches}</span>
                </div>
              </div>
            </div>
          )}
        </section>
      </div>

      {/* Adoption */}
      <section className="pt-10 mb-10">
        <SectionHeader title="Adoption" tip="Feature usage rates across all reviews" />
        {adoption.isLoading ? <Loading /> : adoption.isError ? <Err label="adoption" /> : adoption.data && (
          <div className="border border-border bg-card p-5 space-y-5 max-w-[640px]">
            <AdoptionBar label="Deep Review" value={adoption.data.deep_review_pct} />
            <AdoptionBar label="Incremental" value={adoption.data.incremental_pct} />
            <div className="grid grid-cols-2 gap-3 pt-3 border-t border-border/30">
              <div className="border border-border bg-background p-3">
                <span className="text-[10px] font-mono text-muted-foreground uppercase block mb-1">Active Repos</span>
                <span className="text-2xl font-mono font-bold text-foreground">{adoption.data.active_repos}/{adoption.data.total_repos}</span>
              </div>
              <div className="border border-border bg-background p-3">
                <span className="text-[10px] font-mono text-muted-foreground uppercase block mb-1">Avg Files</span>
                <span className="text-2xl font-mono font-bold text-foreground">{adoption.data.avg_files_per_review.toFixed(1)}</span>
              </div>
            </div>
          </div>
        )}
      </section>
    </>
  );
}

/* --- Sub-components --- */

function ChartCard({ title, value, color, children }: { title: string; value?: string; color: string; children: React.ReactNode }) {
  return (
    <div className="border border-border bg-card p-5 overflow-hidden">
      <div className="flex items-center justify-between">
        <span className="text-[10px] font-mono text-muted-foreground uppercase" style={{ letterSpacing: "1.2px" }}>{title}</span>
        {value && <span className="text-sm font-mono font-bold" style={{ color }}>{value}</span>}
      </div>
      <div className="h-48 mt-3">
        <ResponsiveContainer width="100%" height="100%">
          {children as React.ReactElement}
        </ResponsiveContainer>
      </div>
    </div>
  );
}

function AdoptionBar({ label, value }: { label: string; value: number }) {
  return (
    <div>
      <div className="flex items-center justify-between mb-2">
        <span className="text-[11px] font-mono text-foreground">{label}</span>
        <span className="text-[11px] font-mono text-primary font-bold">{value.toFixed(1)}%</span>
      </div>
      <div className="h-2 bg-border/30 overflow-hidden" style={{ borderRadius: 4 }}>
        <div className="h-full bg-primary/60 transition-all duration-700" style={{ width: `${Math.min(value, 100)}%`, borderRadius: 4 }} />
      </div>
    </div>
  );
}
