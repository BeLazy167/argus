"use client";

import { useMemo } from "react";
import { useParams } from "next/navigation";
import Link from "next/link";
import {
  ArrowLeft,
  ExternalLink,
  FileCode,
  AlertTriangle,
  RotateCcw,
  Loader2,
} from "lucide-react";
import { useReview, useRetryReview } from "@/lib/queries/reviews";
import { useRepos } from "@/lib/queries/repos";
import type { Repo, Review, ReviewComment } from "@/lib/types";

const severityStyles: Record<string, string> = {
  critical: "bg-red-400/10 text-red-400 border-red-400/20",
  warning: "bg-amber/10 text-amber border-amber/20",
  suggestion: "bg-blue-400/10 text-blue-400 border-blue-400/20",
  praise: "bg-green-400/10 text-green-400 border-green-400/20",
};

function ScoreBadge({ score }: { score?: number }) {
  if (score == null) return null;
  const color =
    score >= 8
      ? "text-green-400"
      : score >= 5
        ? "text-amber"
        : "text-red-400";
  return (
    <span className={`font-mono text-3xl font-bold ${color}`}>{score}</span>
  );
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

function LineLabel({
  startLine,
  endLine,
}: {
  startLine?: number;
  endLine?: number;
}) {
  if (startLine != null && endLine != null && startLine !== endLine) {
    return (
      <span className="text-[10px] font-mono text-slate-text">
        Lines {startLine}-{endLine}
      </span>
    );
  }
  const line = endLine ?? startLine;
  if (line != null) {
    return (
      <span className="text-[10px] font-mono text-slate-text">
        Line {line}
      </span>
    );
  }
  return null;
}

export default function ReviewDetailPage() {
  const { id } = useParams<{ id: string }>();
  const { data, isLoading } = useReview(id);
  const { data: repos } = useRepos();
  const retryReview = useRetryReview();

  const review = data?.review;
  const comments = data?.comments ?? [];

  const repoMap = useMemo(() => {
    const map = new Map<number, Repo>();
    for (const r of repos ?? []) {
      map.set(r.id, r);
    }
    return map;
  }, [repos]);

  const grouped = useMemo(() => {
    const map = new Map<string, ReviewComment[]>();
    for (const c of comments) {
      const arr = map.get(c.file_path) ?? [];
      arr.push(c);
      map.set(c.file_path, arr);
    }
    return [...map.entries()]
      .sort(([a], [b]) => a.localeCompare(b))
      .map(
        ([path, cs]) =>
          [
            path,
            cs.sort(
              (a, b) =>
                (a.start_line ?? a.end_line ?? 0) -
                (b.start_line ?? b.end_line ?? 0),
            ),
          ] as const,
      );
  }, [comments]);

  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-20">
        <Loader2 className="h-6 w-6 animate-spin text-slate-text" />
      </div>
    );
  }

  if (!review) {
    return (
      <div className="py-20 text-center text-xs font-mono text-slate-text">
        Review not found.
      </div>
    );
  }

  const repo = repoMap.get(review.repo_id);
  const githubUrl = repo
    ? `https://github.com/${repo.full_name}/pull/${review.pr_number}${review.github_review_id ? `#pullrequestreview-${review.github_review_id}` : ""}`
    : undefined;
  const duration = review.duration_ms
    ? (review.duration_ms / 1000).toFixed(1) + "s"
    : null;

  return (
    <>
      {/* Back link */}
      <Link
        href="/reviews"
        className="inline-flex items-center gap-1.5 text-xs font-mono text-slate-text hover:text-amber transition-colors mb-6"
      >
        <ArrowLeft className="h-3.5 w-3.5" />
        Back to Reviews
      </Link>

      {/* Header card */}
      <div className="rounded-lg border border-iron bg-charcoal p-6 mb-6">
        <div className="flex items-start justify-between">
          <div>
            <h1 className="font-display text-xl font-bold text-foreground mb-1">
              {review.pr_title}
            </h1>
            <p className="text-xs font-mono text-slate-text">
              {repo?.full_name ?? "unknown"} &middot; #{review.pr_number}{" "}
              &middot; by {review.pr_author}
            </p>
          </div>
          <div className="flex items-center gap-4">
            <ScoreBadge score={review.score} />
            <StatusBadge status={review.status} />
            {duration && (
              <span className="text-xs font-mono text-slate-text">
                {duration}
              </span>
            )}
            {githubUrl && (
              <a
                href={githubUrl}
                target="_blank"
                rel="noopener noreferrer"
                className="inline-flex items-center gap-1.5 rounded-md border border-iron px-3 py-1.5 text-xs font-mono text-slate-text hover:text-amber hover:border-amber transition-colors"
              >
                <ExternalLink className="h-3.5 w-3.5" />
                GitHub
              </a>
            )}
            {review.status === "failed" && (
              <button
                type="button"
                onClick={() => retryReview.mutate(review.id)}
                disabled={retryReview.isPending}
                className="inline-flex items-center gap-1.5 rounded-md border border-amber/30 bg-amber/10 px-3 py-1.5 text-xs font-mono text-amber hover:bg-amber/20 transition-colors"
              >
                <RotateCcw
                  className={`h-3.5 w-3.5 ${retryReview.isPending ? "animate-spin" : ""}`}
                />
                Retry
              </button>
            )}
          </div>
        </div>
      </div>

      {/* Summary card */}
      {review.summary && (
        <div className="rounded-lg border border-iron bg-charcoal p-5 mb-6">
          <h2 className="text-xs font-mono uppercase tracking-[0.1em] text-slate-text mb-3">
            Summary
          </h2>
          <p className="font-mono text-xs text-foreground/80 whitespace-pre-wrap">
            {review.summary}
          </p>
        </div>
      )}

      {/* Error card */}
      {review.status === "failed" && review.error && (
        <div className="rounded-lg border border-red-400/30 bg-red-400/5 p-5 mb-6">
          <div className="flex items-center gap-2 mb-3">
            <AlertTriangle className="h-4 w-4 text-red-400" />
            <h2 className="text-xs font-mono uppercase tracking-[0.1em] text-red-400">
              Error
            </h2>
          </div>
          <p className="font-mono text-xs text-red-400/80 whitespace-pre-wrap mb-4">
            {review.error}
          </p>
          <button
            type="button"
            onClick={() => retryReview.mutate(review.id)}
            disabled={retryReview.isPending}
            className="inline-flex items-center gap-1.5 rounded-md border border-red-400/30 px-3 py-1.5 text-xs font-mono text-red-400 hover:bg-red-400/10 transition-colors"
          >
            <RotateCcw
              className={`h-3.5 w-3.5 ${retryReview.isPending ? "animate-spin" : ""}`}
            />
            Retry
          </button>
        </div>
      )}

      {/* Comments section */}
      {grouped.length > 0 && (
        <div className="space-y-4">
          <h2 className="text-xs font-mono uppercase tracking-[0.1em] text-slate-text">
            Comments ({comments.length})
          </h2>
          {grouped.map(([filePath, fileComments]) => (
            <div key={filePath}>
              {/* File header */}
              <div className="flex items-center gap-2 rounded-t-lg border border-iron bg-charcoal px-4 py-2">
                <FileCode className="h-3.5 w-3.5 text-slate-text" />
                <span className="font-mono text-xs text-foreground">
                  {filePath}
                </span>
                <span className="text-[10px] font-mono text-slate-text ml-auto">
                  {fileComments.length} comment
                  {fileComments.length !== 1 ? "s" : ""}
                </span>
              </div>
              {/* Comment cards */}
              {fileComments.map((comment) => (
                <div
                  key={comment.id}
                  className="border border-iron border-t-0 last:rounded-b-lg px-4 py-3"
                >
                  <div className="flex items-center gap-2 mb-2">
                    {comment.severity && (
                      <span
                        className={`inline-flex items-center rounded-sm border px-2 py-0.5 text-[10px] font-mono uppercase tracking-wider ${severityStyles[comment.severity] ?? ""}`}
                      >
                        {comment.severity}
                      </span>
                    )}
                    {comment.category && (
                      <span className="inline-flex items-center rounded-sm border bg-iron/50 text-slate-text border-iron px-2 py-0.5 text-[10px] font-mono">
                        {comment.category}
                      </span>
                    )}
                    <LineLabel
                      startLine={comment.start_line}
                      endLine={comment.end_line}
                    />
                  </div>
                  <p className="font-mono text-xs text-foreground/80 whitespace-pre-wrap">
                    {comment.body}
                  </p>
                </div>
              ))}
            </div>
          ))}
        </div>
      )}
    </>
  );
}
