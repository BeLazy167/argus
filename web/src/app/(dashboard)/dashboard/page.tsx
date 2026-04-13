"use client";

import { useMemo } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import {
  GitPullRequest,
  Loader2,
  AlertTriangle,
  AlertCircle,
  Check,
  X,
} from "lucide-react";
import { usePagination, PaginationBar } from "@/components/dashboard/pagination";
import { useStats } from "@/lib/queries/stats";
import { useReviews } from "@/lib/queries/reviews";
import { useActiveRepo } from "@/lib/hooks/use-active-repo";
import { formatDistanceToNow } from "@/lib/time";


function StatReadout({
  label,
  value,
  accent = false,
  loading = false,
}: {
  label: string;
  value: string | number;
  accent?: boolean;
  loading?: boolean;
}) {
  return (
    <div className="border-b border-iron/40 pb-3">
      <span className="text-[10px] font-mono uppercase tracking-[0.15em] text-slate-text block">
        {label}
      </span>
      <p
        className={`text-xl font-mono font-medium mt-0.5 ${
          accent ? "text-amber" : "text-foreground"
        }`}
      >
        {loading ? <Loader2 className="h-4 w-4 animate-spin" /> : value}
      </p>
    </div>
  );
}

function RiskBadge({ score, status }: { score?: number; status?: string }) {
  if (score == null) return <span className="text-[10px] font-mono text-slate-text/50">{status === "pending" || status === "in_progress" ? "Pending" : "\u2014"}</span>;

  let label: string;
  let classes: string;
  if (score <= 4) {
    label = "HIGH";
    classes = "bg-red-500/15 text-red-400 border-red-500/30";
  } else if (score <= 7) {
    label = "MED";
    classes = "bg-amber/15 text-amber border-amber/30";
  } else {
    label = "LOW";
    classes = "bg-emerald-500/15 text-emerald-400 border-emerald-500/30";
  }

  return (
    <span className={`inline-flex items-center gap-1.5 border px-2 py-0.5 text-[10px] font-mono font-medium ${classes}`} title="Review quality score (0-10)">
      {label}
      <span className="opacity-60">{score}</span>
    </span>
  );
}

function getVerdict(review: { score?: number; status: string }): { label: string; className: string; icon?: React.ComponentType<{ className?: string }> } {
  if (review.status === "pending" || review.status === "in_progress") {
    return { label: "In progress", className: "text-blue-400" };
  }
  if (review.status === "failed") {
    return { label: "Failed", className: "text-red-400", icon: X };
  }
  if (review.score == null) return { label: "\u2014", className: "text-slate-text/50" };
  if (review.score <= 3) return { label: "Escalated", className: "text-red-400", icon: AlertTriangle };
  if (review.score <= 6) return { label: "Review required", className: "text-amber", icon: AlertTriangle };
  if (review.score <= 8) return { label: "Minor issues", className: "text-blue-400", icon: AlertCircle };
  return { label: "Clean", className: "text-emerald-400", icon: Check };
}

function formatReviewTime(ms: number): string {
  if (ms < 60000) return `${Math.round(ms / 1000)}s`;
  return `${(ms / 60000).toFixed(1)}min`;
}

export default function DashboardPage() {
  const router = useRouter();
  const { repos, activeId, isLoading: reposLoading } = useActiveRepo();
  const { data: stats, isLoading: statsLoading } = useStats(activeId || undefined);

  const repoMap = useMemo(() => new Map(repos?.map((r) => [r.id, r]) ?? []), [repos]);
  const { data: reviews, isLoading: reviewsLoading } = useReviews(activeId, 200);
  const { page, setPage, totalPages, paginated, pageSize, total, hasNext, hasPrev } = usePagination(reviews ?? []);

  const feedLoading = reposLoading || reviewsLoading;

  return (
    <>
      <div className="mb-8 flex items-end justify-between">
        <div>
          <h1 className="font-mono text-2xl font-bold text-foreground">
            Mission Control
          </h1>
          <p className="text-xs font-mono text-slate-text mt-1">
            Nothing merges unseen.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <span className="h-2 w-2 rounded-full bg-amber animate-pulse" />
          <span className="text-[10px] font-mono text-amber/70 uppercase tracking-wider">
            Operational
          </span>
        </div>
      </div>

      {/* Stat Readouts */}
      <div className="grid gap-x-6 gap-y-3 grid-cols-2 sm:grid-cols-3 lg:grid-cols-5 mb-10">
        <StatReadout
          label="Catch Rate"
          value={stats ? `${stats.catch_rate}%` : "--"}
          accent
          loading={statsLoading}
        />
        <StatReadout
          label="PRs This Week"
          value={stats?.prs_this_week ?? 0}
          loading={statsLoading}
        />
        <StatReadout
          label="High Risk"
          value={stats?.high_risk_count ?? 0}
          loading={statsLoading}
        />
        <StatReadout
          label="Deep Reviews"
          value={stats?.deep_review_count ?? 0}
          loading={statsLoading}
        />
        <StatReadout
          label="Avg Review Time"
          value={stats ? formatReviewTime(stats.avg_review_time_ms) : "--"}
          loading={statsLoading}
        />
      </div>

      {/* PR Table */}
      <div className="border border-iron bg-charcoal overflow-x-auto">
        <div className="flex items-center justify-between border-b border-iron px-5 py-4">
          <h2 className="text-xs font-mono uppercase tracking-[0.1em] text-foreground">
            Recent Reviews
          </h2>
          <span className="text-[10px] font-mono text-slate-text">
            {reviews?.length ?? 0} total
          </span>
        </div>

        {feedLoading ? (
          <div className="flex items-center justify-center py-10">
            <Loader2 className="h-5 w-5 animate-spin text-slate-text" />
          </div>
        ) : !reviews || reviews.length === 0 ? (
          <div className="py-10 text-center">
            <GitPullRequest className="h-8 w-8 text-slate-text mx-auto mb-3" />
            <p className="text-sm font-mono text-slate-text">
              // No reviews yet. Open a PR to get started.
            </p>
          </div>
        ) : (
          <table className="w-full min-w-[700px]">
            <thead>
              <tr className="border-b border-iron/50 text-[10px] font-mono uppercase tracking-wider text-slate-text">
                <th className="text-left px-5 py-2.5 font-medium whitespace-nowrap">Pull Request</th>
                <th className="text-left px-3 py-2.5 font-medium whitespace-nowrap">Author</th>
                <th className="text-left px-3 py-2.5 font-medium whitespace-nowrap">Risk</th>
                <th className="text-center px-3 py-2.5 font-medium whitespace-nowrap">Files</th>
                <th className="text-left px-3 py-2.5 font-medium whitespace-nowrap">Verdict</th>
                <th className="text-right px-5 py-2.5 font-medium whitespace-nowrap">Time</th>
              </tr>
            </thead>
            <tbody>
              {paginated.map((review) => {
                const repo = repoMap.get(review.repo_id);
                const verdict = getVerdict(review);
                return (
                  <tr
                    key={review.id}
                    className="border-b border-iron/30 last:border-0 hover:bg-iron/10 transition-colors cursor-pointer"
                    role="link"
                    tabIndex={0}
                    onClick={() => router.push(`/reviews/${review.id}`)}
                    onKeyDown={(e) => { if (e.key === "Enter") router.push(`/reviews/${review.id}`); }}
                  >
                    <td className="px-5 py-3">
                      <div className="flex items-center gap-2">
                        <span className="text-[11px] font-mono text-slate-text">
                          #{review.pr_number}
                        </span>
                        <span className="text-xs font-mono text-foreground truncate max-w-[240px]">
                          {review.pr_title}
                        </span>
                        {review.deep_review && (
                          <span className="inline-flex items-center rounded-sm border bg-purple-400/10 text-purple-400 border-purple-400/30 px-1.5 py-0 text-[9px] font-mono" title="Deep Review — multi-specialist analysis">
                            Deep
                          </span>
                        )}
                        {review.is_incremental && (
                          <span className="inline-flex items-center rounded-sm border bg-cyan-400/10 text-cyan-400 border-cyan-400/30 px-1.5 py-0 text-[9px] font-mono" title="Incremental — re-review of updated PR">
                            Inc
                          </span>
                        )}
                      </div>
                      <p className="text-[10px] font-mono text-slate-text/60 mt-0.5">
                        {repo?.full_name}
                      </p>
                    </td>
                    <td className="px-3 py-3">
                      <span className="text-[11px] font-mono text-slate-text">
                        @{review.pr_author}
                      </span>
                    </td>
                    <td className="px-3 py-3">
                      <RiskBadge score={review.score} status={review.status} />
                    </td>
                    <td className="px-3 py-3 text-center">
                      <span className={`text-[11px] font-mono ${review.file_count != null ? "text-slate-text" : "text-slate-text/50"}`}>
                        {review.file_count != null ? review.file_count : (review.status === "pending" || review.status === "in_progress" ? "Pending" : "\u2014")}
                      </span>
                    </td>
                    <td className="px-3 py-3">
                      <span className={`inline-flex items-center gap-1 text-[11px] font-mono font-medium ${verdict.className}`}>
                        {verdict.icon && <verdict.icon className="h-3 w-3" />}
                        {verdict.label}
                      </span>
                    </td>
                    <td className="px-5 py-3 text-right">
                      <span className="text-[10px] font-mono text-slate-text">
                        {formatDistanceToNow(review.created_at)}
                      </span>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
        <PaginationBar
          page={page}
          totalPages={totalPages}
          total={total}
          pageSize={pageSize}
          hasNext={hasNext}
          hasPrev={hasPrev}
          onNext={() => setPage(page + 1)}
          onPrev={() => setPage(page - 1)}
        />
      </div>
    </>
  );
}
