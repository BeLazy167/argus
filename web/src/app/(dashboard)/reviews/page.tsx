"use client";

import { useState } from "react";
import Link from "next/link";
import {
  MessageSquare,
  Loader2,
  RotateCcw,
  ChevronDown,
  ExternalLink,
  ChevronLeft,
  ChevronRight,
} from "lucide-react";
import { useReviews, useRetryReview } from "@/lib/queries/reviews";
import { useActiveRepo } from "@/lib/hooks/use-active-repo";
import { formatDistanceToNow } from "@/lib/time";
import { githubPrUrl } from "@/lib/github";
import { ScoreBadge } from "@/components/dashboard/score-badge";
import { StatusBadge } from "@/components/dashboard/status-badge";
import { RepoSelect } from "@/components/dashboard/repo-select";
import type { Review } from "@/lib/types";

const PAGE_SIZE = 20;

function ReviewRow({
  review,
  repoFullName,
  githubUrl,
  onRetry,
  retrying,
}: {
  review: Review;
  repoFullName?: string;
  githubUrl?: string;
  onRetry: () => void;
  retrying: boolean;
}) {
  return (
    <Link
      href={`/reviews/${review.id}`}
      className="flex items-center justify-between border-b border-iron/50 py-3 last:border-0 hover:bg-iron/10 -mx-5 px-5 transition-colors"
    >
      <div className="flex items-center gap-4">
        <ScoreBadge score={review.score} />
        <div>
          <p className="text-xs font-mono text-foreground truncate max-w-md">
            {repoFullName && <span className="text-slate-text">{repoFullName} &gt; </span>}
            #{review.pr_number} {review.pr_title}
          </p>
          <p className="text-[11px] font-mono text-slate-text">
            by {review.pr_author} &middot;{" "}
            {formatDistanceToNow(review.created_at)}
          </p>
        </div>
      </div>
      <div className="flex items-center gap-3">
        <StatusBadge status={review.status} />
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
      </div>
    </Link>
  );
}

export default function ReviewsPage() {
  const { repos, activeId, setSelectedId, isLoading: reposLoading } = useActiveRepo();
  const [statusFilter, setStatusFilter] = useState("all");
  const [page, setPage] = useState(0);

  const repoMap = new Map(repos.map((r) => [r.id, r]));

  const { data: reviews, isLoading: reviewsLoading } = useReviews(
    activeId,
    PAGE_SIZE,
    page * PAGE_SIZE,
  );
  const retryReview = useRetryReview();

  const filtered = statusFilter === "all"
    ? reviews
    : reviews?.filter((r) => r.status === statusFilter);

  const loading = reposLoading || (activeId > 0 && reviewsLoading);

  return (
    <>
      <div className="mb-8 flex items-center justify-between">
        <div>
          <h1 className="font-display text-2xl font-bold text-foreground">
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
              onChange={(e) => setStatusFilter(e.target.value)}
              className="appearance-none rounded-md border border-iron bg-charcoal px-4 py-2 pr-8 text-xs font-mono text-foreground focus:border-amber focus:outline-none"
            >
              <option value="all">All statuses</option>
              <option value="pending">Pending</option>
              <option value="in_progress">In Progress</option>
              <option value="completed">Completed</option>
              <option value="failed">Failed</option>
            </select>
            <ChevronDown className="pointer-events-none absolute right-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-slate-text" />
          </div>

          <RepoSelect
            repos={repos}
            value={activeId}
            onChange={(id) => {
              setSelectedId(id);
              setPage(0);
            }}
          />
        </div>
      </div>

      <div className="rounded-lg border border-iron bg-charcoal">
        <div className="px-5">
          {loading ? (
            <div className="flex items-center justify-center py-16">
              <Loader2 className="h-5 w-5 animate-spin text-slate-text" />
            </div>
          ) : !filtered || filtered.length === 0 ? (
            <div className="py-16 text-center">
              <MessageSquare className="h-8 w-8 text-slate-text mx-auto mb-3" />
              <p className="text-xs font-mono text-slate-text">
                {statusFilter !== "all"
                  ? `No ${statusFilter.replace("_", " ")} reviews found.`
                  : "No reviews yet for this repo."}
              </p>
            </div>
          ) : (
            filtered.map((review) => {
              const repo = repoMap.get(review.repo_id);
              const url = repo
                ? githubPrUrl(repo.full_name, review.pr_number, review.github_review_id)
                : undefined;
              return (
                <ReviewRow
                  key={review.id}
                  review={review}
                  repoFullName={repo?.full_name}
                  githubUrl={url}
                  onRetry={() => retryReview.mutate(review.id)}
                  retrying={retryReview.isPending}
                />
              );
            })
          )}
        </div>
      </div>

      <div className="flex items-center justify-between mt-4">
        <button
          type="button"
          onClick={() => setPage((p) => Math.max(0, p - 1))}
          disabled={page === 0}
          className="flex items-center gap-1 text-xs font-mono text-slate-text hover:text-foreground disabled:opacity-30 disabled:cursor-not-allowed transition-colors"
        >
          <ChevronLeft className="h-3.5 w-3.5" /> Prev
        </button>
        <span className="text-[11px] font-mono text-slate-text">
          Page {page + 1}
        </span>
        <button
          type="button"
          onClick={() => setPage((p) => p + 1)}
          disabled={!reviews || reviews.length < PAGE_SIZE}
          className="flex items-center gap-1 text-xs font-mono text-slate-text hover:text-foreground disabled:opacity-30 disabled:cursor-not-allowed transition-colors"
        >
          Next <ChevronRight className="h-3.5 w-3.5" />
        </button>
      </div>
    </>
  );
}
