"use client";

import {
  Eye,
  GitPullRequest,
  AlertTriangle,
  CheckCircle,
  Loader2,
} from "lucide-react";
import { useStats, useActivity } from "@/lib/queries/stats";
import { formatDistanceToNow } from "@/lib/time";

function StatCard({
  label,
  value,
  icon: Icon,
  accent = false,
  loading = false,
}: {
  label: string;
  value: string | number;
  icon: React.ComponentType<{ className?: string }>;
  accent?: boolean;
  loading?: boolean;
}) {
  return (
    <div
      className={`rounded-lg border p-5 ${
        accent
          ? "border-amber/30 bg-charcoal shadow-[0_0_16px_-6px_oklch(0.77_0.15_75/0.2)]"
          : "border-iron bg-charcoal"
      }`}
    >
      <div className="flex items-center justify-between mb-3">
        <span className="text-[11px] font-mono uppercase tracking-[0.1em] text-slate-text">
          {label}
        </span>
        <Icon
          className={`h-4 w-4 ${accent ? "text-amber" : "text-slate-text"}`}
        />
      </div>
      <p
        className={`text-2xl font-mono font-medium ${
          accent ? "text-amber" : "text-foreground"
        }`}
      >
        {loading ? <Loader2 className="h-5 w-5 animate-spin" /> : value}
      </p>
    </div>
  );
}

function ActivityRow({
  action,
  resource,
  actor,
  time,
}: {
  action: string;
  resource?: string;
  actor?: string;
  time: string;
}) {
  const label = action.replaceAll("_", " ");

  return (
    <div className="flex items-center justify-between border-b border-iron/50 py-3 last:border-0">
      <div className="flex items-center gap-3">
        <div
          className={`h-2 w-2 rounded-full ${
            action.includes("fail")
              ? "bg-red-400"
              : action.includes("complete")
                ? "bg-green-400"
                : "bg-amber"
          }`}
        />
        <div>
          <p className="text-xs font-mono text-foreground capitalize">
            {label}
          </p>
          {resource && (
            <p className="text-[11px] font-mono text-slate-text">{resource}</p>
          )}
        </div>
      </div>
      <div className="flex items-center gap-3">
        {actor && (
          <span className="text-[11px] font-mono text-slate-text">
            {actor}
          </span>
        )}
        <span className="text-[11px] font-mono text-iron">{time}</span>
      </div>
    </div>
  );
}

export default function DashboardPage() {
  const { data: stats, isLoading: statsLoading } = useStats();
  const { data: activity, isLoading: activityLoading } = useActivity(20);

  return (
    <>
      <div className="mb-8">
        <h1 className="font-display text-2xl font-bold text-foreground">
          Mission Control
        </h1>
        <p className="text-xs font-mono text-slate-text mt-1">
          Nothing merges unseen.
        </p>
      </div>

      {/* Stats grid */}
      <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4 mb-10">
        <StatCard
          label="Reviews today"
          value={stats?.completed_today ?? 0}
          icon={Eye}
          accent
          loading={statsLoading}
        />
        <StatCard
          label="Active repos"
          value={stats?.active_repos ?? 0}
          icon={GitPullRequest}
          loading={statsLoading}
        />
        <StatCard
          label="Critical finds"
          value={stats?.critical_finds ?? 0}
          icon={AlertTriangle}
          loading={statsLoading}
        />
        <StatCard
          label="Avg score"
          value={stats?.avg_score ?? 0}
          icon={CheckCircle}
          loading={statsLoading}
        />
      </div>

      {/* Activity feed */}
      <div className="rounded-lg border border-iron bg-charcoal">
        <div className="flex items-center justify-between border-b border-iron px-5 py-4">
          <h2 className="text-xs font-mono uppercase tracking-[0.1em] text-foreground">
            Recent Activity
          </h2>
          <span className="text-[10px] font-mono text-slate-text">
            {stats?.total_reviews ?? "—"} total reviews
          </span>
        </div>
        <div className="px-5">
          {activityLoading ? (
            <div className="flex items-center justify-center py-10">
              <Loader2 className="h-5 w-5 animate-spin text-slate-text" />
            </div>
          ) : activity?.length === 0 ? (
            <div className="py-10 text-center text-xs font-mono text-slate-text">
              No activity yet. Open a PR to get started.
            </div>
          ) : (
            activity?.map((a) => (
              <ActivityRow
                key={a.id}
                action={a.action}
                resource={a.resource ?? undefined}
                actor={a.actor ?? undefined}
                time={formatDistanceToNow(a.created_at)}
              />
            ))
          )}
        </div>
      </div>
    </>
  );
}
