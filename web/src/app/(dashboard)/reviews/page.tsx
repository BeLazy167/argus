"use client";

import { useState } from "react";
import {
  MessageSquare,
  Loader2,
  RotateCcw,
  ChevronDown,
  ExternalLink,
} from "lucide-react";
import { useRepos } from "@/lib/queries/repos";
import { useReviews, useRetryReview } from "@/lib/queries/reviews";
import { formatDistanceToNow } from "@/lib/time";
import type { Review } from "@/lib/types";

function ScoreBadge({ score }: { score?: number }) {
  if (score == null) return null;
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

function ReviewRow({
  review,
  onRetry,
  retrying,
}: {
  review: Review;
  onRetry: () => void;
  retrying: boolean;
}) {
  return (
    <div className="flex items-center justify-between border-b border-iron/50 py-3 last:border-0">
      <div className="flex items-center gap-4">
        <ScoreBadge score={review.score} />
        <div>
          <p className="text-xs font-mono text-foreground">
            #{review.pr_number} — {review.pr_title}
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
            onClick={onRetry}
            disabled={retrying}
            className="text-slate-text hover:text-amber transition-colors"
            title="Retry review"
          >
            <RotateCcw className={`h-3.5 w-3.5 ${retrying ? "animate-spin" : ""}`} />
          </button>
        )}
        {review.github_review_id && (
          <a
            href={`https://github.com/${review.pr_title}/pull/${review.pr_number}#pullrequestreview-${review.github_review_id}`}
            target="_blank"
            rel="noopener noreferrer"
            className="text-slate-text hover:text-amber transition-colors"
            title="View on GitHub"
          >
            <ExternalLink className="h-3.5 w-3.5" />
          </a>
        )}
      </div>
    </div>
  );
}

export default function ReviewsPage() {
  const { data: repos, isLoading: reposLoading } = useRepos();
  const [selectedRepoId, setSelectedRepoId] = useState<number>(0);

  const firstRepoId = repos?.[0]?.id ?? 0;
  const activeRepoId = selectedRepoId || firstRepoId;

  const { data: reviews, isLoading: reviewsLoading } = useReviews(
    activeRepoId,
    50,
  );
  const retryReview = useRetryReview();

  const loading = reposLoading || (activeRepoId > 0 && reviewsLoading);

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

        {/* Repo selector */}
        {repos && repos.length > 0 && (
          <div className="relative">
            <select
              value={activeRepoId}
              onChange={(e) => setSelectedRepoId(Number(e.target.value))}
              className="appearance-none rounded-md border border-iron bg-charcoal px-4 py-2 pr-8 text-xs font-mono text-foreground focus:border-amber focus:outline-none"
            >
              {repos.map((r) => (
                <option key={r.id} value={r.id}>
                  {r.full_name}
                </option>
              ))}
            </select>
            <ChevronDown className="pointer-events-none absolute right-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-slate-text" />
          </div>
        )}
      </div>

      <div className="rounded-lg border border-iron bg-charcoal">
        <div className="px-5">
          {loading ? (
            <div className="flex items-center justify-center py-16">
              <Loader2 className="h-5 w-5 animate-spin text-slate-text" />
            </div>
          ) : !reviews || reviews.length === 0 ? (
            <div className="py-16 text-center">
              <MessageSquare className="h-8 w-8 text-slate-text mx-auto mb-3" />
              <p className="text-xs font-mono text-slate-text">
                No reviews yet for this repo.
              </p>
            </div>
          ) : (
            reviews.map((r) => (
              <ReviewRow
                key={r.id}
                review={r}
                onRetry={() => retryReview.mutate(r.id)}
                retrying={retryReview.isPending}
              />
            ))
          )}
        </div>
      </div>
    </>
  );
}
