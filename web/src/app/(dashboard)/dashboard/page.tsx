"use client";

import { useState } from "react";
import Link from "next/link";
import {
  Eye,
  GitPullRequest,
  AlertTriangle,
  CheckCircle,
  Loader2,
  ChevronDown,
} from "lucide-react";
import { useStats } from "@/lib/queries/stats";
import { useRepos } from "@/lib/queries/repos";
import { useReviews } from "@/lib/queries/reviews";
import { formatDistanceToNow } from "@/lib/time";
import type { Review } from "@/lib/types";

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

function ScoreBadge({ score }: { score?: number }) {
  if (score == null) return <span className="text-lg font-mono text-slate-text">—</span>;
  const color =
    score >= 8
      ? "text-green-400"
      : score >= 5
        ? "text-amber"
        : "text-red-400";
  return <span className={`font-mono text-lg font-medium ${color}`}>{score}</span>;
}

function StatusBadge({ status }: { status: Review["status"] }) {
  const styles = {
    completed: "bg-green-400/10 text-green-400 border-green-400/20",
    in_progress: "bg-amber/10 text-amber border-amber/20",
    pending: "bg-blue-400/10 text-blue-400 border-blue-400/20",
    failed: "bg-red-400/10 text-red-400 border-red-400/20",
  }[status];

  return (
    <span
      className={`inline-flex items-center rounded-sm border px-2 py-0.5 text-[10px] font-mono uppercase tracking-wider ${styles}`}
    >
      {status.replace("_", " ")}
    </span>
  );
}

export default function DashboardPage() {
  const { data: stats, isLoading: statsLoading } = useStats();
  const { data: repos, isLoading: reposLoading } = useRepos();
  const [selectedRepoId, setSelectedRepoId] = useState<number>(0);

  const firstRepoId = repos?.[0]?.id ?? 0;
  const activeRepoId = selectedRepoId || firstRepoId;

  const repoMap = new Map(repos?.map((r) => [r.id, r]));

  const { data: reviews, isLoading: reviewsLoading } = useReviews(
    activeRepoId,
    10,
  );

  const feedLoading = reposLoading || (activeRepoId > 0 && reviewsLoading);

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

      {/* Recent reviews feed */}
      <div className="rounded-lg border border-iron bg-charcoal">
        <div className="flex items-center justify-between border-b border-iron px-5 py-4">
          <h2 className="text-xs font-mono uppercase tracking-[0.1em] text-foreground">
            Recent Reviews
          </h2>
          <div className="flex items-center gap-4">
            <span className="text-[10px] font-mono text-slate-text">
              {stats?.total_reviews ?? "—"} total
            </span>
            {repos && repos.length > 0 && (
              <div className="relative">
                <select
                  value={activeRepoId}
                  onChange={(e) => setSelectedRepoId(Number(e.target.value))}
                  className="appearance-none rounded-md border border-iron bg-background px-3 py-1 pr-7 text-[11px] font-mono text-foreground focus:border-amber focus:outline-none"
                >
                  {repos.map((r) => (
                    <option key={r.id} value={r.id}>
                      {r.full_name}
                    </option>
                  ))}
                </select>
                <ChevronDown className="pointer-events-none absolute right-2 top-1/2 h-3 w-3 -translate-y-1/2 text-slate-text" />
              </div>
            )}
          </div>
        </div>
        <div className="px-5">
          {feedLoading ? (
            <div className="flex items-center justify-center py-10">
              <Loader2 className="h-5 w-5 animate-spin text-slate-text" />
            </div>
          ) : !reviews || reviews.length === 0 ? (
            <div className="py-10 text-center text-xs font-mono text-slate-text">
              No reviews yet. Open a PR to get started.
            </div>
          ) : (
            reviews.map((review) => {
              const repo = repoMap.get(review.repo_id);
              return (
                <Link
                  key={review.id}
                  href={`/reviews/${review.id}`}
                  className="flex items-center justify-between border-b border-iron/50 py-3 last:border-0 hover:bg-iron/10 -mx-5 px-5 transition-colors"
                >
                  <div className="flex items-center gap-4">
                    <ScoreBadge score={review.score} />
                    <div>
                      <p className="text-xs font-mono text-foreground truncate max-w-md">
                        {repo?.full_name} &gt; #{review.pr_number} {review.pr_title}
                      </p>
                      <p className="text-[11px] font-mono text-slate-text">
                        by {review.pr_author} &middot;{" "}
                        {formatDistanceToNow(review.created_at)}
                      </p>
                    </div>
                  </div>
                  <StatusBadge status={review.status} />
                </Link>
              );
            })
          )}
        </div>
      </div>
    </>
  );
}
