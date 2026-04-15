"use client";

import { useMemo } from "react";
import Link from "next/link";
import {
  AreaChart, Area, XAxis, YAxis, Tooltip, ResponsiveContainer,
  PieChart, Pie, Cell,
  BarChart, Bar,
} from "recharts";
import {
  Loader2, TrendingUp, DollarSign, Zap, Clock, AlertTriangle, Target,
  Users, Cpu, Shield, GitFork, Info, Layers, Timer,
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

/* --- Reusable Components --- */

function Tip({ text }: { text: string }) {
  return (
    <span className="group relative inline-flex ml-1 cursor-help">
      <Info className="h-3 w-3 text-muted-foreground/50 group-hover:text-muted-foreground transition-colors" />
      <span className="pointer-events-none absolute bottom-full left-1/2 -translate-x-1/2 mb-1.5 w-52 rounded bg-popover border border-border px-2.5 py-1.5 text-[10px] font-mono text-popover-foreground opacity-0 group-hover:opacity-100 transition-opacity z-50 shadow-lg">
        {text}
      </span>
    </span>
  );
}

function StatCard({ label, value, icon: Icon, tip, sub }: {
  label: string; value: string; icon: React.ComponentType<{ className?: string }>; tip?: string; sub?: string;
}) {
  return (
    <div className="border border-border bg-card p-4 flex flex-col gap-1 hover:border-primary/30 transition-colors">
      <div className="flex items-center gap-1.5 text-muted-foreground">
        <Icon className="h-3.5 w-3.5" />
        <span className="text-[10px] font-mono uppercase tracking-wider">{label}</span>
        {tip && <Tip text={tip} />}
      </div>
      <span className="text-2xl font-mono font-bold text-foreground tracking-tight">{value}</span>
      {sub && <span className="text-[10px] font-mono text-muted-foreground">{sub}</span>}
    </div>
  );
}

function Section({ title, children, tip }: { title: string; children: React.ReactNode; tip?: string }) {
  return (
    <section className="mb-8">
      <div className="flex items-center gap-1.5 mb-4">
        <h2 className="text-xs font-mono font-bold text-foreground uppercase tracking-wider">{title}</h2>
        {tip && <Tip text={tip} />}
      </div>
      {children}
    </section>
  );
}

function Loading() { return <div className="flex items-center justify-center py-12"><Loader2 className="h-5 w-5 animate-spin text-muted-foreground" /></div>; }
function Err({ label }: { label: string }) { return <div className="flex items-center justify-center py-12 text-xs font-mono text-destructive">Failed to load {label}</div>; }

const tooltipStyle = { background: "hsl(var(--card))", border: "1px solid hsl(var(--border))", fontSize: 11, fontFamily: "monospace", color: "hsl(var(--foreground))" };

/* --- Page --- */

export default function StatsPage() {
  const { period, setPeriod } = useStatsStore();
  const overview = useStatsOverview(period);
  const timeseries = useStatsTimeseries(period);
  const users = useStatsUsers(period);
  const models = useStatsModels(period);
  const findings = useStatsFindings(period);
  const adoption = useStatsAdoption(period);
  const repos = useStatsRepos(period);
  const reviewTimes = useStatsReviewTimes(period);
  const costPerStage = useStatsCostPerStage(period);

  const chartData = useMemo(() => (timeseries.data ?? []).map(d => ({ ...d, day: d.day.slice(5) })), [timeseries.data]);
  const modelPieData = useMemo(() => [...(models.data ?? [])].sort((a, b) => b.total_cost - a.total_cost).slice(0, 8), [models.data]);
  const sevData = useMemo(() => (findings.data?.by_severity ?? []).filter(s => s.severity), [findings.data]);
  const stageCostData = useMemo(() => [...(costPerStage.data ?? [])].sort((a, b) => b.total_cost - a.total_cost), [costPerStage.data]);
  const costPerFinding = useMemo(() => {
    if (!overview.data || !findings.data) return 0;
    const totalFindings = findings.data.by_severity.reduce((s, v) => s + v.count, 0);
    return totalFindings > 0 ? overview.data.total_cost / totalFindings : 0;
  }, [overview.data, findings.data]);

  return (
    <>
      {/* Sticky header */}
      <div className="sticky top-0 z-10 bg-background/80 backdrop-blur-md border-b border-border -mx-6 px-6 py-3 mb-6 flex items-center justify-between">
        <div>
          <h1 className="text-lg font-mono font-bold text-foreground">Org Analytics</h1>
          <p className="text-[10px] font-mono text-muted-foreground">All repos &middot; last {period}</p>
        </div>
        <div className="flex items-center gap-0.5 border border-border bg-card rounded-sm">
          {PERIODS.map(p => (
            <button key={p.value} type="button" onClick={() => setPeriod(p.value)}
              className={`px-3 py-1.5 text-[11px] font-mono transition-colors cursor-pointer rounded-sm ${
                period === p.value ? "bg-primary/20 text-primary" : "text-muted-foreground hover:text-foreground"
              }`}>
              {p.label}
            </button>
          ))}
        </div>
      </div>

      {/* Overview cards */}
      {overview.isLoading ? <Loading /> : overview.isError ? <Err label="overview" /> : overview.data && (
        <div className="grid grid-cols-2 md:grid-cols-4 gap-3 mb-8">
          <StatCard label="Reviews" value={String(overview.data.total_reviews)} icon={TrendingUp} tip="Total completed/failed/cancelled reviews in period" />
          <StatCard label="Total Cost" value={fmt$(overview.data.total_cost)} icon={DollarSign} tip="Sum of LLM API costs across all stages" />
          <StatCard label="Avg Score" value={`${overview.data.avg_score.toFixed(1)}/10`} icon={Target} tip="Mean review score. Lower = more issues found (1=critical, 10=clean)" />
          <StatCard label="Avg Time" value={fmtSecs(overview.data.avg_review_secs)} icon={Clock} tip="Average wall-clock time from review start to completion" />
          <StatCard label="Tokens" value={fmtTok(overview.data.total_tokens)} icon={Zap} tip="Total LLM tokens consumed (input + output)" />
          <StatCard label="Critical Findings" value={String(overview.data.critical_finds)} icon={AlertTriangle} tip="Review comments with severity=critical" />
          <StatCard label="Detection Rate" value={`${overview.data.catch_rate}%`} icon={Shield} tip="% of scored reviews where issues were found (score < 10)" />
          <StatCard label="Cost / Finding" value={costPerFinding > 0 ? fmt$(costPerFinding) : "—"} icon={DollarSign} tip="Total cost ÷ total findings. Lower = more cost-efficient" />
        </div>
      )}

      {/* Review time percentiles */}
      {reviewTimes.data && reviewTimes.data.count > 0 && (
        <div className="grid grid-cols-3 gap-3 mb-8">
          <StatCard label="p50 Time" value={fmtSecs(reviewTimes.data.p50)} icon={Timer} tip="Median review duration (50th percentile)" />
          <StatCard label="p75 Time" value={fmtSecs(reviewTimes.data.p75)} icon={Timer} tip="75th percentile — 75% of reviews finish faster" />
          <StatCard label="p95 Time" value={fmtSecs(reviewTimes.data.p95)} icon={Timer} tip="95th percentile — outlier threshold" />
        </div>
      )}

      {/* Trends */}
      <Section title="Trends" tip="Daily aggregates for the selected period">
        {timeseries.isLoading ? <Loading /> : timeseries.isError ? <Err label="trends" /> : (
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            <ChartCard title="Reviews / Day">
              <AreaChart data={chartData}>
                <XAxis dataKey="day" tick={{ fontSize: 10, fill: "hsl(var(--muted-foreground))" }} stroke="hsl(var(--border))" />
                <YAxis tick={{ fontSize: 10, fill: "hsl(var(--muted-foreground))" }} width={30} stroke="hsl(var(--border))" />
                <Tooltip contentStyle={tooltipStyle} />
                <Area type="monotone" dataKey="review_count" stroke="hsl(var(--primary))" fill="hsl(var(--primary))" fillOpacity={0.15} strokeWidth={1.5} />
              </AreaChart>
            </ChartCard>
            <ChartCard title="Cost / Day">
              <AreaChart data={chartData}>
                <XAxis dataKey="day" tick={{ fontSize: 10, fill: "hsl(var(--muted-foreground))" }} stroke="hsl(var(--border))" />
                <YAxis tick={{ fontSize: 10, fill: "hsl(var(--muted-foreground))" }} width={40} stroke="hsl(var(--border))" tickFormatter={v => `$${v}`} />
                <Tooltip contentStyle={tooltipStyle} formatter={(v) => [`$${Number(v).toFixed(3)}`, "Cost"]} />
                <Area type="monotone" dataKey="total_cost" stroke="#3b82f6" fill="#3b82f6" fillOpacity={0.15} strokeWidth={1.5} />
              </AreaChart>
            </ChartCard>
            <ChartCard title="Avg Score / Day">
              <AreaChart data={chartData}>
                <XAxis dataKey="day" tick={{ fontSize: 10, fill: "hsl(var(--muted-foreground))" }} stroke="hsl(var(--border))" />
                <YAxis tick={{ fontSize: 10, fill: "hsl(var(--muted-foreground))" }} width={30} domain={[0, 10]} stroke="hsl(var(--border))" />
                <Tooltip contentStyle={tooltipStyle} />
                <Area type="monotone" dataKey="avg_score" stroke="#10b981" fill="#10b981" fillOpacity={0.15} strokeWidth={1.5} />
              </AreaChart>
            </ChartCard>
            <ChartCard title="Tokens / Day">
              <AreaChart data={chartData}>
                <XAxis dataKey="day" tick={{ fontSize: 10, fill: "hsl(var(--muted-foreground))" }} stroke="hsl(var(--border))" />
                <YAxis tick={{ fontSize: 10, fill: "hsl(var(--muted-foreground))" }} width={40} stroke="hsl(var(--border))" tickFormatter={v => fmtTok(Number(v))} />
                <Tooltip contentStyle={tooltipStyle} formatter={(v) => [fmtTok(Number(v)), "Tokens"]} />
                <Area type="monotone" dataKey="total_tokens" stroke="#8b5cf6" fill="#8b5cf6" fillOpacity={0.15} strokeWidth={1.5} />
              </AreaChart>
            </ChartCard>
          </div>
        )}
      </Section>

      {/* Per-Repo breakdown */}
      <Section title="By Repository" tip="Metrics broken down per enabled repo">
        {repos.isLoading ? <Loading /> : repos.isError ? <Err label="repos" /> : (
          <div className="border border-border bg-card overflow-x-auto">
            <table className="w-full text-[11px] font-mono">
              <thead>
                <tr className="border-b border-border text-muted-foreground">
                  <th className="text-left px-3 py-2">Repo</th>
                  <th className="text-right px-3 py-2">Reviews</th>
                  <th className="text-right px-3 py-2">Avg Score</th>
                  <th className="text-right px-3 py-2">Cost</th>
                  <th className="text-right px-3 py-2">Avg Time</th>
                  <th className="text-right px-3 py-2">Tokens</th>
                </tr>
              </thead>
              <tbody>
                {(repos.data ?? []).map(r => (
                  <tr key={r.repo_id} className="border-b border-border/50 hover:bg-accent/50 transition-colors">
                    <td className="px-3 py-2 text-foreground">{r.full_name.split("/")[1] || r.full_name}</td>
                    <td className="text-right px-3 py-2 text-muted-foreground">{r.review_count}</td>
                    <td className="text-right px-3 py-2">
                      <span className={r.avg_score <= 4 ? "text-destructive" : r.avg_score >= 7 ? "text-green-500" : "text-primary"}>
                        {r.avg_score.toFixed(1)}
                      </span>
                    </td>
                    <td className="text-right px-3 py-2 text-muted-foreground">{fmt$(r.total_cost)}</td>
                    <td className="text-right px-3 py-2 text-muted-foreground">{r.avg_review_secs > 0 ? fmtSecs(r.avg_review_secs) : "—"}</td>
                    <td className="text-right px-3 py-2 text-muted-foreground">{fmtTok(r.total_tokens)}</td>
                  </tr>
                ))}
                {(!repos.data || repos.data.length === 0) && (
                  <tr><td colSpan={6} className="text-center py-8 text-muted-foreground">No repos</td></tr>
                )}
              </tbody>
            </table>
          </div>
        )}
      </Section>

      {/* Users + Models side by side */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6 mb-8">
        <Section title="Users" tip="PR authors ranked by review count">
          {users.isLoading ? <Loading /> : users.isError ? <Err label="users" /> : (
            <div className="border border-border bg-card overflow-hidden">
              <table className="w-full text-[11px] font-mono">
                <thead>
                  <tr className="border-b border-border text-muted-foreground">
                    <th className="text-left px-3 py-2">Author</th>
                    <th className="text-right px-3 py-2">PRs</th>
                    <th className="text-right px-3 py-2">Avg</th>
                    <th className="text-right px-3 py-2">Cost</th>
                    <th className="text-right px-3 py-2">Crit</th>
                  </tr>
                </thead>
                <tbody>
                  {(users.data ?? []).map(u => (
                    <tr key={u.pr_author} className="border-b border-border/50 hover:bg-accent/50 transition-colors">
                      <td className="px-3 py-2">
                        <Link href={`/reviews?author=${encodeURIComponent(u.pr_author)}`} className="text-foreground hover:text-primary transition-colors">{u.pr_author}</Link>
                      </td>
                      <td className="text-right px-3 py-2 text-muted-foreground">{u.review_count}</td>
                      <td className="text-right px-3 py-2">
                        <span className={u.avg_score <= 4 ? "text-destructive" : u.avg_score >= 7 ? "text-green-500" : "text-primary"}>{u.avg_score.toFixed(1)}</span>
                      </td>
                      <td className="text-right px-3 py-2 text-muted-foreground">{fmt$(u.total_cost)}</td>
                      <td className="text-right px-3 py-2">
                        {u.critical_count > 0 ? <span className="text-destructive">{u.critical_count}</span> : <span className="text-muted-foreground/50">0</span>}
                      </td>
                    </tr>
                  ))}
                  {(!users.data || users.data.length === 0) && <tr><td colSpan={5} className="text-center py-8 text-muted-foreground">No data</td></tr>}
                </tbody>
              </table>
            </div>
          )}
        </Section>

        <Section title="Models" tip="LLM token usage and cost aggregated across all pipeline stages">
          {models.isLoading ? <Loading /> : models.isError ? <Err label="models" /> : (
            <div className="border border-border bg-card p-4">
              <div className="h-52 flex items-center justify-center">
                {modelPieData.length > 0 ? (
                  <ResponsiveContainer width="100%" height="100%">
                    <PieChart>
                      <Pie data={modelPieData} dataKey="total_cost" nameKey="model" cx="50%" cy="50%" innerRadius={40} outerRadius={80} paddingAngle={2}>
                        {modelPieData.map((_, i) => <Cell key={i} fill={PIE_COLORS[i % PIE_COLORS.length]} />)}
                      </Pie>
                      <Tooltip contentStyle={tooltipStyle} formatter={(v) => [fmt$(Number(v)), "Cost"]} />
                    </PieChart>
                  </ResponsiveContainer>
                ) : <span className="text-xs font-mono text-muted-foreground">No model data</span>}
              </div>
              <div className="mt-3 space-y-1">
                {modelPieData.map((m, i) => (
                  <div key={m.model} className="flex items-center gap-2 text-[10px] font-mono">
                    <span className="w-2 h-2 rounded-full shrink-0" style={{ background: PIE_COLORS[i % PIE_COLORS.length] }} />
                    <span className="text-foreground truncate flex-1">{m.model}</span>
                    <span className="text-muted-foreground">{fmtTok(m.total_tokens)}</span>
                    <span className="text-muted-foreground">{fmt$(m.total_cost)}</span>
                  </div>
                ))}
              </div>
            </div>
          )}
        </Section>
      </div>

      {/* Cost per Stage + Findings side by side */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6 mb-8">
        <Section title="Cost by Stage" tip="LLM cost breakdown by pipeline stage (triage, review, synthesis, etc.)">
          {costPerStage.isLoading ? <Loading /> : costPerStage.isError ? <Err label="cost per stage" /> : (
            <div className="border border-border bg-card p-4">
              {stageCostData.length > 0 ? (
                <>
                  <div className="h-48">
                    <ResponsiveContainer width="100%" height="100%">
                      <BarChart data={stageCostData} layout="vertical">
                        <XAxis type="number" tick={{ fontSize: 10, fill: "hsl(var(--muted-foreground))" }} stroke="hsl(var(--border))" tickFormatter={v => `$${v}`} />
                        <YAxis type="category" dataKey="stage" tick={{ fontSize: 10, fill: "hsl(var(--muted-foreground))" }} width={80} stroke="hsl(var(--border))" />
                        <Tooltip contentStyle={tooltipStyle} formatter={(v) => [fmt$(Number(v)), "Cost"]} />
                        <Bar dataKey="total_cost" fill="hsl(var(--primary))" radius={[0, 2, 2, 0]} />
                      </BarChart>
                    </ResponsiveContainer>
                  </div>
                  <div className="mt-3 text-[10px] font-mono text-muted-foreground text-right">
                    Total: {fmt$(stageCostData.reduce((s, v) => s + v.total_cost, 0))}
                  </div>
                </>
              ) : <span className="text-xs font-mono text-muted-foreground">No data</span>}
            </div>
          )}
        </Section>

        <Section title="Findings" tip="Distribution of review comments by severity and category">
          {findings.isLoading ? <Loading /> : findings.isError ? <Err label="findings" /> : findings.data && (
            <div className="space-y-4">
              <div className="border border-border bg-card p-4">
                <span className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider block mb-3">By Severity</span>
                <div className="h-32">
                  <ResponsiveContainer width="100%" height="100%">
                    <BarChart data={sevData} layout="vertical">
                      <XAxis type="number" tick={{ fontSize: 10, fill: "hsl(var(--muted-foreground))" }} stroke="hsl(var(--border))" />
                      <YAxis type="category" dataKey="severity" tick={{ fontSize: 10, fill: "hsl(var(--muted-foreground))" }} width={70} stroke="hsl(var(--border))" />
                      <Tooltip contentStyle={tooltipStyle} />
                      <Bar dataKey="count" radius={[0, 2, 2, 0]}>
                        {sevData.map(s => <Cell key={s.severity} fill={SEV_COLORS[s.severity] ?? "#6b7280"} />)}
                      </Bar>
                    </BarChart>
                  </ResponsiveContainer>
                </div>
              </div>
              <div className="border border-border bg-card p-4">
                <span className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider block mb-3">Top Categories</span>
                <div className="space-y-1.5">
                  {(findings.data.by_category ?? []).map(c => {
                    const max = Math.max(...(findings.data?.by_category ?? []).map(x => x.count), 1);
                    return (
                      <div key={c.category} className="flex items-center gap-2 text-[10px] font-mono">
                        <span className="w-20 text-foreground truncate">{c.category || "other"}</span>
                        <div className="flex-1 h-2.5 bg-accent overflow-hidden rounded-sm">
                          <div className="h-full bg-primary/50 rounded-sm" style={{ width: `${(c.count / max) * 100}%` }} />
                        </div>
                        <span className="text-muted-foreground w-8 text-right">{c.count}</span>
                      </div>
                    );
                  })}
                </div>
              </div>
              <div className="flex gap-4">
                <div className="flex-1 border border-border bg-card p-3 text-center">
                  <span className="text-lg font-mono font-bold text-foreground">{findings.data.new_findings}</span>
                  <span className="block text-[10px] font-mono text-muted-foreground">New Findings</span>
                </div>
                <div className="flex-1 border border-border bg-card p-3 text-center">
                  <span className="text-lg font-mono font-bold text-foreground">{findings.data.pattern_matches}</span>
                  <span className="block text-[10px] font-mono text-muted-foreground">Pattern Matches</span>
                </div>
              </div>
            </div>
          )}
        </Section>
      </div>

      {/* Adoption */}
      <Section title="Adoption" tip="Feature usage rates across all reviews in period">
        {adoption.isLoading ? <Loading /> : adoption.isError ? <Err label="adoption" /> : adoption.data && (
          <div className="border border-border bg-card p-4 space-y-5 max-w-lg">
            <AdoptionBar label="Deep Review" value={adoption.data.deep_review_pct} icon={Shield} />
            <AdoptionBar label="Incremental" value={adoption.data.incremental_pct} icon={TrendingUp} />
            <div className="flex items-center gap-3 text-[11px] font-mono">
              <GitFork className="h-3.5 w-3.5 text-muted-foreground" />
              <span className="text-foreground">Active Repos</span>
              <span className="ml-auto text-foreground font-bold">{adoption.data.active_repos}</span>
              <span className="text-muted-foreground">/ {adoption.data.total_repos} total ({adoption.data.total_enabled_repos} enabled)</span>
            </div>
            <div className="flex items-center gap-3 text-[11px] font-mono">
              <Cpu className="h-3.5 w-3.5 text-muted-foreground" />
              <span className="text-foreground">Avg Files / Review</span>
              <span className="ml-auto text-foreground font-bold">{adoption.data.avg_files_per_review.toFixed(1)}</span>
            </div>
          </div>
        )}
      </Section>
    </>
  );
}

/* --- Sub-components --- */

function ChartCard({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="border border-border bg-card p-4">
      <span className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider">{title}</span>
      <div className="h-48 mt-2">
        <ResponsiveContainer width="100%" height="100%">
          {children as React.ReactElement}
        </ResponsiveContainer>
      </div>
    </div>
  );
}

function AdoptionBar({ label, value, icon: Icon }: { label: string; value: number; icon: React.ComponentType<{ className?: string }> }) {
  return (
    <div>
      <div className="flex items-center gap-2 mb-1.5">
        <Icon className="h-3.5 w-3.5 text-muted-foreground" />
        <span className="text-[11px] font-mono text-foreground">{label}</span>
        <span className="ml-auto text-[11px] font-mono text-primary font-bold">{value.toFixed(1)}%</span>
      </div>
      <div className="h-2 bg-accent overflow-hidden rounded-sm">
        <div className="h-full bg-primary/60 transition-all duration-700 rounded-sm" style={{ width: `${Math.min(value, 100)}%` }} />
      </div>
    </div>
  );
}
