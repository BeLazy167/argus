"use client";

import { useState, useMemo } from "react";
import Link from "next/link";
import {
  MessageSquare,
  Loader2,
  RotateCcw,
  ChevronDown,
  ChevronRight,
  ExternalLink,
} from "lucide-react";
import { usePagination, PaginationBar, useSearchParamState, useUpdateSearchParams } from "@/components/dashboard/pagination";
import { useReviews, useRetryReview } from "@/lib/queries/reviews";
import { useActiveRepo } from "@/lib/hooks/use-active-repo";
import { formatDistanceToNow } from "@/lib/time";
import { githubPrUrl } from "@/lib/github";
import { ScoreBadge } from "@/components/dashboard/score-badge";
import { StatusBadge } from "@/components/dashboard/status-badge";

import type { Review } from "@/lib/types";

const FETCH_LIMIT = 200;

type PRGroup = {
  key: string;
  prNumber: number;
  prTitle: string;
  author: string;
  branch: string;
  repoName: string;
  repoId: number;
  reviews: Review[];
  latestScore?: number;
};

function ReviewRow({
  review,
  onRetry,
  retrying,
  githubUrl,
}: {
  review: Review;
  onRetry: () => void;
  retrying: boolean;
  githubUrl?: string;
}) {
  const typeBadge = review.is_incremental ? "Inc" : review.deep_review ? "Deep" : "Review";
  const typeTitle = review.is_incremental
    ? "Incremental \u2014 re-review of updated PR"
    : review.deep_review
      ? "Deep Review \u2014 multi-specialist analysis"
      : "Standard review";
  const badgeColor = review.deep_review
    ? "bg-purple-400/10 text-purple-400 border-purple-400/20"
    : review.is_incremental
      ? "bg-blue-400/10 text-blue-400 border-blue-400/20"
      : "bg-iron/30 text-slate-text border-iron";

  return (
    <div className="flex items-center justify-between py-2 group">
      <Link
        href={`/reviews/${review.id}`}
        className="flex items-center gap-3 flex-1 min-w-0 cursor-pointer"
      >
        <span
          className={`inline-flex items-center rounded-sm border px-2 py-0.5 text-[11px] font-mono uppercase tracking-wider shrink-0 ${badgeColor}`}
          title={typeTitle}
        >
          {typeBadge}
        </span>
        <ScoreBadge score={review.score} />
        <StatusBadge status={review.status} />
        <span className="text-[11px] font-mono text-slate-text">
          {formatDistanceToNow(review.created_at)}
        </span>
      </Link>
      <div className="flex items-center gap-2">
        {review.status === "failed" && (
          <button
            type="button"
            onClick={(e) => {
              e.preventDefault();
              e.stopPropagation();
              onRetry();
            }}
            disabled={retrying}
            className="text-slate-text hover:text-amber transition-colors"
            title="Retry review"
          >
            <RotateCcw className={`h-3.5 w-3.5 ${retrying ? "animate-spin" : ""}`} />
          </button>
        )}
        {githubUrl && (
          <a
            href={githubUrl}
            target="_blank"
            rel="noopener noreferrer"
            onClick={(e) => e.stopPropagation()}
            className="text-slate-text hover:text-amber transition-colors"
            title="View on GitHub"
          >
            <ExternalLink className="h-3.5 w-3.5" />
          </a>
        )}
        <Link
          href={`/reviews/${review.id}`}
          className="text-slate-text hover:text-amber transition-colors"
          title="View review"
        >
          <ChevronRight className="h-3.5 w-3.5" />
        </Link>
      </div>
    </div>
  );
}

function PRAccordionRow({
  group,
  expanded,
  onToggle,
  repoFullName,
  retryReview,
}: {
  group: PRGroup;
  expanded: boolean;
  onToggle: () => void;
  repoFullName?: string;
  retryReview: ReturnType<typeof useRetryReview>;
}) {
  const latestReview = group.reviews[0] as Review | undefined;
  const githubUrl = repoFullName && latestReview
    ? githubPrUrl(repoFullName, group.prNumber, latestReview.github_review_id)
    : undefined;

  return (
    <div className="border-b border-iron/50 last:border-0">
      {/* PR header row */}
      <button
        type="button"
        onClick={onToggle}
        className="flex items-center justify-between w-full py-3 -mx-5 px-5 hover:bg-iron/15 transition-colors text-left cursor-pointer"
      >
        <div className="flex items-center gap-4 min-w-0">
          <ScoreBadge score={group.latestScore} />
          <div className="min-w-0">
            <p className="text-xs font-mono text-foreground truncate max-w-md">
              <span className="text-amber">#{group.prNumber}</span> {group.prTitle}
            </p>
            <p className="text-[11px] font-mono text-slate-text">
              {group.branch && <><span className="text-blue-400">{group.branch}</span> &middot; </>}
              {group.author} &middot; {group.reviews.length} review{group.reviews.length !== 1 ? "s" : ""}
            </p>
          </div>
        </div>
        <div className="flex items-center gap-3 shrink-0">
          {githubUrl && (
            <a
              href={githubUrl}
              target="_blank"
              rel="noopener noreferrer"
              onClick={(e) => e.stopPropagation()}
              className="text-slate-text hover:text-amber transition-colors"
              title="View on GitHub"
            >
              <ExternalLink className="h-3.5 w-3.5" />
            </a>
          )}
          <ChevronDown
            className={`h-4 w-4 text-slate-text transition-transform duration-200 ${expanded ? "rotate-0" : "-rotate-90"}`}
          />
        </div>
      </button>

      {/* Expanded reviews */}
      {expanded && (
        <div className="ml-8 mr-2 mb-3 border-l border-iron/40 pl-4 transition-all duration-200 ease-out">
          {group.reviews.map((review) => {
            const url = repoFullName
              ? githubPrUrl(repoFullName, review.pr_number, review.github_review_id)
              : undefined;
            return (
              <ReviewRow
                key={review.id}
                review={review}
                githubUrl={url}
                onRetry={() => retryReview.mutate(review.id)}
                retrying={retryReview.isPending}
              />
            );
          })}
        </div>
      )}
    </div>
  );
}

export default function ReviewsPage() {
  const { repos, activeId, setSelectedId, isLoading: reposLoading } = useActiveRepo();
  const [statusFilter] = useSearchParamState("status", "all");
  const updateParams = useUpdateSearchParams();
  const [expandedPR, setExpandedPR] = useState<string | null>(null);

  const repoMap = useMemo(() => new Map(repos?.map((r) => [r.id, r]) ?? []), [repos]);

  const { data: reviews, isLoading: reviewsLoading } = useReviews(
    activeId,
    FETCH_LIMIT,
  );
  const retryReview = useRetryReview();

  const filtered = statusFilter === "all"
    ? (reviews ?? [])
    : (reviews ?? []).filter((r) => r.status === statusFilter);

  /** Group filtered reviews by repo_id:pr_number */
  const grouped: PRGroup[] = useMemo(() => {
    const map = new Map<string, Review[]>();
    for (const r of filtered) {
      const key = `${r.repo_id}:${r.pr_number}`;
      const list = map.get(key) ?? [];
      list.push(r);
      map.set(key, list);
    }
    return Array.from(map.entries())
      .map(([key, revs]) => {
        const sorted = revs.sort(
          (a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime(),
        );
        const latest = sorted[0]!;
        return {
          key,
          prNumber: latest.pr_number,
          prTitle: latest.pr_title,
          author: latest.pr_author,
          branch: latest.head_ref || "",
          repoName: repoMap.get(latest.repo_id)?.full_name ?? "",
          repoId: latest.repo_id,
          reviews: sorted,
          latestScore: latest.score,
        };
      })
      .sort(
        (a, b) =>
          new Date(b.reviews[0]!.created_at).getTime() -
          new Date(a.reviews[0]!.created_at).getTime(),
      );
  }, [filtered, repoMap]);

  const { page, setPage, totalPages, paginated, pageSize, total, hasNext, hasPrev } =
    usePagination(grouped);

  const loading = reposLoading || reviewsLoading;

  return (
    <>
      <div className="mb-8 flex items-center justify-between">
        <div>
          <h1 className="font-mono text-2xl font-bold text-foreground">
            Reviews
          </h1>
          <p className="text-xs font-mono text-slate-text mt-1">
            All PR reviews across your repos.
          </p>
        </div>

        <div className="flex items-center gap-3">
          <div className="relative">
            <select
              value={statusFilter}
              aria-label="Filter by status"
              onChange={(e) => updateParams({ status: e.target.value === "all" ? "" : e.target.value, page: "" })}
              style={{ backgroundColor: 'var(--card)', color: 'var(--foreground)' }}
              className="appearance-none border border-iron bg-charcoal px-4 py-2 pr-8 text-xs font-mono text-foreground focus:border-amber focus:outline-none"
            >
              <option value="all">All statuses</option>
              <option value="pending">Pending</option>
              <option value="in_progress">In Progress</option>
              <option value="completed">Completed</option>
              <option value="failed">Failed</option>
            </select>
            <ChevronDown className="pointer-events-none absolute right-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-slate-text" />
          </div>
          <span className="text-[11px] font-mono text-slate-text">
            Showing {filtered.length} of {(reviews ?? []).length}
          </span>
        </div>
      </div>

      <div className="border border-iron bg-charcoal">
        <div className="px-5">
          {loading ? (
            <div className="flex items-center justify-center py-16">
              <Loader2 className="h-5 w-5 animate-spin text-slate-text" />
            </div>
          ) : grouped.length === 0 ? (
            <div className="py-16 text-center">
              <MessageSquare className="h-8 w-8 text-slate-text mx-auto mb-3" />
              <p className="text-xs font-mono text-slate-text">
                {statusFilter !== "all"
                  ? `// No ${statusFilter.replace("_", " ")} reviews found.`
                  : "// No reviews yet for this repo."}
              </p>
            </div>
          ) : (
            paginated.map((group) => {
              const repo = repoMap.get(group.repoId);
              return (
                <PRAccordionRow
                  key={group.key}
                  group={group}
                  expanded={expandedPR === group.key}
                  onToggle={() =>
                    setExpandedPR(expandedPR === group.key ? null : group.key)
                  }
                  repoFullName={repo?.full_name}
                  retryReview={retryReview}
                />
              );
            })
          )}
        </div>
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
