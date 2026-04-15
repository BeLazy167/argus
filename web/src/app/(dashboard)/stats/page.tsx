"use client";

import { useMemo } from "react";
import Link from "next/link";
import {
  AreaChart, Area, XAxis, YAxis, Tooltip, ResponsiveContainer,
  PieChart, Pie, Cell,
  BarChart, Bar,
} from "recharts";
import { Loader2, TrendingUp, DollarSign, Zap, Clock, AlertTriangle, Target, Users, Cpu, Shield, GitFork } from "lucide-react";
import { useStatsStore } from "@/lib/stores/stats-store";
import {
  useStatsOverview,
  useStatsTimeseries,
  useStatsUsers,
  useStatsModels,
  useStatsFindings,
  useStatsAdoption,
  type Period,
} from "@/lib/queries/org-stats";

const PERIODS: { value: Period; label: string }[] = [
  { value: "7d", label: "7 days" },
  { value: "30d", label: "30 days" },
  { value: "90d", label: "90 days" },
];

const PIE_COLORS = ["#f59e0b", "#3b82f6", "#10b981", "#ef4444", "#8b5cf6", "#ec4899", "#06b6d4", "#84cc16"];

const SEV_COLORS: Record<string, string> = {
  critical: "#ef4444",
  warning: "#f59e0b",
  suggestion: "#3b82f6",
  praise: "#10b981",
};

function formatCost(n: number) {
  return n < 1 ? `$${n.toFixed(3)}` : `$${n.toFixed(2)}`;
}
function formatTokens(n: number) {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`;
  return String(n);
}
function formatSecs(s: number) {
  if (s >= 60) return `${Math.floor(s / 60)}m ${s % 60}s`;
  return `${s}s`;
}

function StatCard({ label, value, icon: Icon, sub }: { label: string; value: string; icon: React.ComponentType<{ className?: string }>; sub?: string }) {
  return (
    <div className="border border-iron bg-charcoal/80 p-4 flex flex-col gap-1 group hover:border-amber/30 transition-colors">
      <div className="flex items-center gap-2 text-slate-text">
        <Icon className="h-3.5 w-3.5" />
        <span className="text-[10px] font-mono uppercase tracking-wider">{label}</span>
      </div>
      <span className="text-2xl font-mono font-bold text-foreground tracking-tight">{value}</span>
      {sub && <span className="text-[10px] font-mono text-slate-text">{sub}</span>}
    </div>
  );
}

function SectionTitle({ children }: { children: React.ReactNode }) {
  return <h2 className="text-xs font-mono font-bold text-foreground uppercase tracking-wider mb-4">{children}</h2>;
}

function LoadingBlock() {
  return (
    <div className="flex items-center justify-center py-12">
      <Loader2 className="h-5 w-5 animate-spin text-slate-text" />
    </div>
  );
}

function ErrorBlock({ label }: { label: string }) {
  return (
    <div className="flex items-center justify-center py-12 text-xs font-mono text-red-400">
      Failed to load {label}
    </div>
  );
}

export default function StatsPage() {
  const { period, setPeriod } = useStatsStore();
  const overview = useStatsOverview(period);
  const timeseries = useStatsTimeseries(period);
  const users = useStatsUsers(period);
  const models = useStatsModels(period);
  const findings = useStatsFindings(period);
  const adoption = useStatsAdoption(period);

  const chartData = useMemo(() => {
    if (!timeseries.data) return [];
    return timeseries.data.map(d => ({
      ...d,
      day: d.day.slice(5), // MM-DD
    }));
  }, [timeseries.data]);

  const modelPieData = useMemo(() => {
    if (!models.data) return [];
    return [...models.data]
      .sort((a, b) => b.total_cost - a.total_cost)
      .slice(0, 8);
  }, [models.data]);

  const sevData = useMemo(() => {
    if (!findings.data) return [];
    return findings.data.by_severity.filter(s => s.severity);
  }, [findings.data]);

  return (
    <>
      {/* Header with sticky period toggle */}
      <div className="sticky top-0 z-10 bg-background/80 backdrop-blur-md border-b border-iron -mx-6 px-6 py-3 mb-6 flex items-center justify-between">
        <div>
          <h1 className="text-lg font-mono font-bold text-foreground">Org Stats</h1>
          <p className="text-[10px] font-mono text-slate-text">Analytics across all repos</p>
        </div>
        <div className="flex items-center gap-1 border border-iron bg-charcoal">
          {PERIODS.map(p => (
            <button
              key={p.value}
              type="button"
              onClick={() => setPeriod(p.value)}
              className={`px-3 py-1.5 text-[11px] font-mono transition-colors cursor-pointer ${
                period === p.value
                  ? "bg-amber/20 text-amber"
                  : "text-slate-text hover:text-foreground"
              }`}
            >
              {p.label}
            </button>
          ))}
        </div>
      </div>

      {/* Overview Cards — bento grid */}
      {overview.isLoading ? <LoadingBlock /> : overview.isError ? <ErrorBlock label="overview" /> : overview.data && (
        <div className="grid grid-cols-2 md:grid-cols-4 lg:grid-cols-7 gap-3 mb-8">
          <StatCard label="Reviews" value={String(overview.data.total_reviews)} icon={TrendingUp} />
          <StatCard label="Cost" value={formatCost(overview.data.total_cost)} icon={DollarSign} />
          <StatCard label="Avg Score" value={`${overview.data.avg_score.toFixed(1)}/10`} icon={Target} />
          <StatCard label="Avg Time" value={formatSecs(overview.data.avg_review_secs)} icon={Clock} />
          <StatCard label="Tokens" value={formatTokens(overview.data.total_tokens)} icon={Zap} />
          <StatCard label="Critical" value={String(overview.data.critical_finds)} icon={AlertTriangle} />
          <StatCard label="Catch Rate" value={`${overview.data.catch_rate}%`} icon={Shield} />
        </div>
      )}

      {/* Trend Charts */}
      <section className="mb-8">
        <SectionTitle>Trends</SectionTitle>
        {timeseries.isLoading ? <LoadingBlock /> : timeseries.isError ? <ErrorBlock label="trends" /> : (
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            <div className="border border-iron bg-charcoal/80 p-4">
              <span className="text-[10px] font-mono text-slate-text uppercase tracking-wider">Reviews / Day</span>
              <div className="h-48 mt-2">
                <ResponsiveContainer width="100%" height="100%">
                  <AreaChart data={chartData}>
                    <XAxis dataKey="day" tick={{ fontSize: 10, fill: "var(--slate-text)" }} />
                    <YAxis tick={{ fontSize: 10, fill: "var(--slate-text)" }} width={30} />
                    <Tooltip contentStyle={{ background: "var(--charcoal)", border: "1px solid var(--iron)", fontSize: 11, fontFamily: "monospace" }} />
                    <Area type="monotone" dataKey="review_count" stroke="#f59e0b" fill="#f59e0b" fillOpacity={0.15} strokeWidth={1.5} />
                  </AreaChart>
                </ResponsiveContainer>
              </div>
            </div>
            <div className="border border-iron bg-charcoal/80 p-4">
              <span className="text-[10px] font-mono text-slate-text uppercase tracking-wider">Cost / Day</span>
              <div className="h-48 mt-2">
                <ResponsiveContainer width="100%" height="100%">
                  <AreaChart data={chartData}>
                    <XAxis dataKey="day" tick={{ fontSize: 10, fill: "var(--slate-text)" }} />
                    <YAxis tick={{ fontSize: 10, fill: "var(--slate-text)" }} width={40} tickFormatter={v => `$${v}`} />
                    <Tooltip contentStyle={{ background: "var(--charcoal)", border: "1px solid var(--iron)", fontSize: 11, fontFamily: "monospace" }} formatter={(v) => [`$${Number(v).toFixed(3)}`, "Cost"]} />
                    <Area type="monotone" dataKey="total_cost" stroke="#3b82f6" fill="#3b82f6" fillOpacity={0.15} strokeWidth={1.5} />
                  </AreaChart>
                </ResponsiveContainer>
              </div>
            </div>
          </div>
        )}
      </section>

      {/* Users + Models — side by side */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6 mb-8">
        {/* Users */}
        <section>
          <SectionTitle>Users</SectionTitle>
          {users.isLoading ? <LoadingBlock /> : users.isError ? <ErrorBlock label="users" /> : (
            <div className="border border-iron bg-charcoal/80 overflow-hidden">
              <table className="w-full text-[11px] font-mono">
                <thead>
                  <tr className="border-b border-iron text-slate-text">
                    <th className="text-left px-3 py-2">Author</th>
                    <th className="text-right px-3 py-2">PRs</th>
                    <th className="text-right px-3 py-2">Avg</th>
                    <th className="text-right px-3 py-2">Cost</th>
                    <th className="text-right px-3 py-2">Crit</th>
                  </tr>
                </thead>
                <tbody>
                  {(users.data ?? []).map(u => (
                    <tr key={u.pr_author} className="border-b border-iron/50 hover:bg-iron/20 transition-colors">
                      <td className="px-3 py-2">
                        <Link href={`/reviews?author=${encodeURIComponent(u.pr_author)}`} className="text-foreground hover:text-amber transition-colors">
                          {u.pr_author}
                        </Link>
                      </td>
                      <td className="text-right px-3 py-2 text-slate-text">{u.review_count}</td>
                      <td className="text-right px-3 py-2">
                        <span className={u.avg_score <= 4 ? "text-red-400" : u.avg_score >= 7 ? "text-green-400" : "text-amber"}>
                          {u.avg_score.toFixed(1)}
                        </span>
                      </td>
                      <td className="text-right px-3 py-2 text-slate-text">{formatCost(u.total_cost)}</td>
                      <td className="text-right px-3 py-2">
                        {u.critical_count > 0 ? <span className="text-red-400">{u.critical_count}</span> : <span className="text-slate-text/50">0</span>}
                      </td>
                    </tr>
                  ))}
                  {(!users.data || users.data.length === 0) && (
                    <tr><td colSpan={5} className="text-center py-8 text-slate-text">No data</td></tr>
                  )}
                </tbody>
              </table>
            </div>
          )}
        </section>

        {/* Models */}
        <section>
          <SectionTitle>Models</SectionTitle>
          {models.isLoading ? <LoadingBlock /> : models.isError ? <ErrorBlock label="models" /> : (
            <div className="border border-iron bg-charcoal/80 p-4">
              <div className="h-52 flex items-center justify-center">
                {modelPieData.length > 0 ? (
                  <ResponsiveContainer width="100%" height="100%">
                    <PieChart>
                      <Pie data={modelPieData} dataKey="total_cost" nameKey="model" cx="50%" cy="50%" innerRadius={40} outerRadius={80} paddingAngle={2}>
                        {modelPieData.map((_, i) => <Cell key={i} fill={PIE_COLORS[i % PIE_COLORS.length]} />)}
                      </Pie>
                      <Tooltip contentStyle={{ background: "var(--charcoal)", border: "1px solid var(--iron)", fontSize: 11, fontFamily: "monospace" }} formatter={(v) => [formatCost(Number(v)), "Cost"]} />
                    </PieChart>
                  </ResponsiveContainer>
                ) : (
                  <span className="text-xs font-mono text-slate-text">No model data</span>
                )}
              </div>
              <div className="mt-3 space-y-1">
                {modelPieData.map((m, i) => (
                  <div key={m.model} className="flex items-center gap-2 text-[10px] font-mono">
                    <span className="w-2 h-2 rounded-full shrink-0" style={{ background: PIE_COLORS[i % PIE_COLORS.length] }} />
                    <span className="text-foreground truncate flex-1">{m.model}</span>
                    <span className="text-slate-text">{formatTokens(m.total_tokens)}</span>
                    <span className="text-slate-text">{formatCost(m.total_cost)}</span>
                  </div>
                ))}
              </div>
            </div>
          )}
        </section>
      </div>

      {/* Findings + Adoption — side by side */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6 mb-8">
        {/* Findings */}
        <section>
          <SectionTitle>Findings</SectionTitle>
          {findings.isLoading ? <LoadingBlock /> : findings.isError ? <ErrorBlock label="findings" /> : findings.data && (
            <div className="space-y-4">
              <div className="border border-iron bg-charcoal/80 p-4">
                <span className="text-[10px] font-mono text-slate-text uppercase tracking-wider block mb-3">By Severity</span>
                <div className="h-32">
                  <ResponsiveContainer width="100%" height="100%">
                    <BarChart data={sevData} layout="vertical">
                      <XAxis type="number" tick={{ fontSize: 10, fill: "var(--slate-text)" }} />
                      <YAxis type="category" dataKey="severity" tick={{ fontSize: 10, fill: "var(--slate-text)" }} width={70} />
                      <Tooltip contentStyle={{ background: "var(--charcoal)", border: "1px solid var(--iron)", fontSize: 11, fontFamily: "monospace" }} />
                      <Bar dataKey="count" radius={[0, 2, 2, 0]}>
                        {sevData.map(s => <Cell key={s.severity} fill={SEV_COLORS[s.severity] ?? "#6b7280"} />)}
                      </Bar>
                    </BarChart>
                  </ResponsiveContainer>
                </div>
              </div>
              <div className="border border-iron bg-charcoal/80 p-4">
                <span className="text-[10px] font-mono text-slate-text uppercase tracking-wider block mb-3">Top Categories</span>
                <div className="space-y-1.5">
                  {(findings.data.by_category ?? []).map(c => {
                    const max = Math.max(...(findings.data?.by_category ?? []).map(x => x.count), 1);
                    return (
                      <div key={c.category} className="flex items-center gap-2 text-[10px] font-mono">
                        <span className="w-20 text-foreground truncate">{c.category || "other"}</span>
                        <div className="flex-1 h-3 bg-iron/30 overflow-hidden">
                          <div className="h-full bg-amber/40" style={{ width: `${(c.count / max) * 100}%` }} />
                        </div>
                        <span className="text-slate-text w-8 text-right">{c.count}</span>
                      </div>
                    );
                  })}
                </div>
              </div>
              <div className="flex gap-4">
                <div className="flex-1 border border-iron bg-charcoal/80 p-3 text-center">
                  <span className="text-lg font-mono font-bold text-foreground">{findings.data.new_findings}</span>
                  <span className="block text-[10px] font-mono text-slate-text">New Findings</span>
                </div>
                <div className="flex-1 border border-iron bg-charcoal/80 p-3 text-center">
                  <span className="text-lg font-mono font-bold text-foreground">{findings.data.pattern_matches}</span>
                  <span className="block text-[10px] font-mono text-slate-text">Pattern Matches</span>
                </div>
              </div>
            </div>
          )}
        </section>

        {/* Adoption */}
        <section>
          <SectionTitle>Adoption</SectionTitle>
          {adoption.isLoading ? <LoadingBlock /> : adoption.isError ? <ErrorBlock label="adoption" /> : adoption.data && (
            <div className="border border-iron bg-charcoal/80 p-4 space-y-5">
              <AdoptionBar label="Deep Review" value={adoption.data.deep_review_pct} icon={Shield} />
              <AdoptionBar label="Incremental" value={adoption.data.incremental_pct} icon={TrendingUp} />
              <div className="flex items-center gap-3 text-[11px] font-mono">
                <GitFork className="h-3.5 w-3.5 text-slate-text" />
                <span className="text-foreground">Active Repos</span>
                <span className="ml-auto text-foreground font-bold">{adoption.data.active_repos}</span>
                <span className="text-slate-text">/ {adoption.data.total_enabled_repos} enabled</span>
              </div>
              <div className="flex items-center gap-3 text-[11px] font-mono">
                <Cpu className="h-3.5 w-3.5 text-slate-text" />
                <span className="text-foreground">Avg Files / Review</span>
                <span className="ml-auto text-foreground font-bold">{adoption.data.avg_files_per_review.toFixed(1)}</span>
              </div>
            </div>
          )}
        </section>
      </div>
    </>
  );
}

function AdoptionBar({ label, value, icon: Icon }: { label: string; value: number; icon: React.ComponentType<{ className?: string }> }) {
  return (
    <div>
      <div className="flex items-center gap-2 mb-1.5">
        <Icon className="h-3.5 w-3.5 text-slate-text" />
        <span className="text-[11px] font-mono text-foreground">{label}</span>
        <span className="ml-auto text-[11px] font-mono text-amber font-bold">{value.toFixed(1)}%</span>
      </div>
      <div className="h-2 bg-iron/30 overflow-hidden">
        <div
          className="h-full bg-amber/60 transition-all duration-700"
          style={{ width: `${Math.min(value, 100)}%` }}
        />
      </div>
    </div>
  );
}
