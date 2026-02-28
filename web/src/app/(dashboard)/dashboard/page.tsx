"use client";

import Link from "next/link";
import {
  Eye,
  GitPullRequest,
  AlertTriangle,
  CheckCircle,
  Loader2,
} from "lucide-react";
import { useStats } from "@/lib/queries/stats";
import { useReviews } from "@/lib/queries/reviews";
import { useActiveRepo } from "@/lib/hooks/use-active-repo";
import { formatDistanceToNow } from "@/lib/time";
import { ScoreBadge } from "@/components/dashboard/score-badge";
import { StatusBadge } from "@/components/dashboard/status-badge";
import { RepoSelect } from "@/components/dashboard/repo-select";

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

export default function DashboardPage() {
  const { data: stats, isLoading: statsLoading } = useStats();
  const { repos, activeId, setSelectedId, isLoading: reposLoading } = useActiveRepo();

  const repoMap = new Map(repos.map((r) => [r.id, r]));

  const { data: reviews, isLoading: reviewsLoading } = useReviews(activeId, 10);

  const feedLoading = reposLoading || (activeId > 0 && reviewsLoading);

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

      <div className="rounded-lg border border-iron bg-charcoal">
        <div className="flex items-center justify-between border-b border-iron px-5 py-4">
          <h2 className="text-xs font-mono uppercase tracking-[0.1em] text-foreground">
            Recent Reviews
          </h2>
          <div className="flex items-center gap-4">
            <span className="text-[10px] font-mono text-slate-text">
              {stats?.total_reviews ?? "\u2014"} total
            </span>
            <RepoSelect repos={repos} value={activeId} onChange={setSelectedId} />
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
