"use client";

import { useMemo, useState, useCallback } from "react";
import { useParams } from "next/navigation";
import Link from "next/link";
import ReactMarkdown from "react-markdown";
import {
  ArrowLeft,
  ExternalLink,
  FileCode,
  AlertTriangle,
  RotateCcw,
  Loader2,
  Clock,
  GitPullRequest,
  Copy,
  Check,
  ChevronDown,
  ChevronRight,
  Sparkles,
} from "lucide-react";
import { useReview, useRetryReview } from "@/lib/queries/reviews";
import { useRepos } from "@/lib/queries/repos";
import { githubPrUrl } from "@/lib/github";
import { ScoreBox } from "@/components/dashboard/score-badge";
import { StatusBadge } from "@/components/dashboard/status-badge";
import type { Repo, ReviewComment } from "@/lib/types";

const severityStyles: Record<string, string> = {
  critical: "bg-red-400/10 text-red-400 border-red-400/20",
  warning: "bg-amber/10 text-amber border-amber/20",
  suggestion: "bg-blue-400/10 text-blue-400 border-blue-400/20",
  praise: "bg-green-400/10 text-green-400 border-green-400/20",
};

const severityDot: Record<string, string> = {
  critical: "bg-red-400",
  warning: "bg-amber",
  suggestion: "bg-blue-400",
  praise: "bg-green-400",
};

/** Formats a line reference string like "L5" or "L5-10". */
function lineRef(c: ReviewComment): string {
  const { start_line, end_line } = c;
  if (start_line != null && end_line != null && start_line !== end_line) {
    return `L${start_line}-${end_line}`;
  }
  const line = end_line ?? start_line;
  return line != null ? `L${line}` : "";
}

/** Shared markdown renderer for summary + comment bodies. */
function Markdown({ children }: { children: string }) {
  return (
    <ReactMarkdown
      components={{
        h1: ({ children }) => (
          <h3 className="font-display text-sm font-bold text-foreground mt-4 mb-2 first:mt-0">
            {children}
          </h3>
        ),
        h2: ({ children }) => (
          <h3 className="font-display text-sm font-bold text-foreground mt-4 mb-2 first:mt-0">
            {children}
          </h3>
        ),
        h3: ({ children }) => (
          <h4 className="font-mono text-xs font-semibold text-foreground mt-3 mb-1.5 first:mt-0">
            {children}
          </h4>
        ),
        p: ({ children }) => (
          <p className="font-mono text-xs text-foreground/80 leading-relaxed mb-2 last:mb-0">
            {children}
          </p>
        ),
        ul: ({ children }) => (
          <ul className="list-disc list-inside space-y-1 mb-2 text-xs font-mono text-foreground/80">
            {children}
          </ul>
        ),
        ol: ({ children }) => (
          <ol className="list-decimal list-inside space-y-1 mb-2 text-xs font-mono text-foreground/80">
            {children}
          </ol>
        ),
        li: ({ children }) => <li className="leading-relaxed">{children}</li>,
        strong: ({ children }) => (
          <strong className="font-semibold text-foreground">{children}</strong>
        ),
        code: ({ className, children }) => {
          if (className?.includes("language-")) {
            return (
              <code className="block bg-void/80 border border-iron/50 rounded-md px-3 py-2 text-[11px] font-mono text-foreground/90 overflow-x-auto my-2">
                {children}
              </code>
            );
          }
          return (
            <code className="bg-iron/40 rounded px-1 py-0.5 text-[11px] font-mono text-amber/90">
              {children}
            </code>
          );
        },
        pre: ({ children }) => <div className="my-2">{children}</div>,
        a: ({ href, children }) => (
          <a
            href={href}
            className="text-amber hover:underline"
            target="_blank"
            rel="noopener noreferrer"
          >
            {children}
          </a>
        ),
      }}
    >
      {children}
    </ReactMarkdown>
  );
}

/** Generates a fix prompt and copies to clipboard. */
function CopyFixButton({
  comment,
  filePath,
}: {
  comment: ReviewComment;
  filePath: string;
}) {
  const [copied, setCopied] = useState(false);

  const handleCopy = useCallback(() => {
    const ref = lineRef(comment);
    const prompt = [
      `Fix the following ${comment.severity ?? "issue"} in \`${filePath}\`${ref ? ` at ${ref.replace("L", "line ").replace("-", "-")}` : ""}:`,
      "",
      comment.body,
      "",
      `Category: ${comment.category ?? "general"}`,
    ].join("\n");

    navigator.clipboard.writeText(prompt);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  }, [comment, filePath]);

  return (
    <button
      type="button"
      onClick={handleCopy}
      className="inline-flex items-center gap-1.5 rounded-md px-2 py-1 text-[10px] font-mono text-slate-text hover:text-amber hover:bg-amber/5 transition-all group"
      title="Copy fix prompt"
    >
      {copied ? (
        <>
          <Check className="h-3 w-3 text-green-400" />
          <span className="text-green-400">Copied</span>
        </>
      ) : (
        <>
          <Sparkles className="h-3 w-3 group-hover:text-amber transition-colors" />
          <span>Copy fix prompt</span>
        </>
      )}
    </button>
  );
}

/** Collapsible file group. */
function FileGroup({
  filePath,
  fileComments,
}: {
  filePath: string;
  fileComments: readonly ReviewComment[];
}) {
  const [expanded, setExpanded] = useState(true);
  const Chevron = expanded ? ChevronDown : ChevronRight;

  return (
    <div className="rounded-lg border border-iron overflow-hidden transition-all">
      <button
        type="button"
        onClick={() => setExpanded(!expanded)}
        className="flex items-center gap-2 w-full bg-charcoal px-4 py-2.5 border-b border-iron hover:bg-iron/30 transition-colors text-left"
      >
        <Chevron className="h-3.5 w-3.5 text-slate-text shrink-0" />
        <FileCode className="h-3.5 w-3.5 text-slate-text shrink-0" />
        <span className="font-mono text-xs text-foreground truncate">
          {filePath}
        </span>
        <div className="flex items-center gap-2 ml-auto shrink-0">
          {fileComments.some((c) => c.severity === "critical") && (
            <div className="h-1.5 w-1.5 rounded-full bg-red-400" />
          )}
          {fileComments.some((c) => c.severity === "warning") && (
            <div className="h-1.5 w-1.5 rounded-full bg-amber" />
          )}
          <span className="text-[10px] font-mono text-slate-text">
            {fileComments.length} comment
            {fileComments.length !== 1 ? "s" : ""}
          </span>
        </div>
      </button>
      {expanded && (
        <div>
          {fileComments.map((comment, i) => (
            <div
              key={comment.id}
              className={`px-4 py-4 bg-background hover:bg-charcoal/30 transition-colors ${i < fileComments.length - 1 ? "border-b border-iron/50" : ""}`}
            >
              <div className="flex items-center gap-2 mb-2.5">
                {comment.severity && (
                  <span
                    className={`inline-flex items-center rounded-sm border px-2 py-0.5 text-[10px] font-mono uppercase tracking-wider ${severityStyles[comment.severity] ?? ""}`}
                  >
                    {comment.severity}
                  </span>
                )}
                {comment.category && (
                  <span className="inline-flex items-center rounded-sm border bg-iron/30 text-slate-text border-iron/60 px-2 py-0.5 text-[10px] font-mono">
                    {comment.category}
                  </span>
                )}
                {lineRef(comment) && (
                  <span className="text-[10px] font-mono text-slate-text">
                    {lineRef(comment)}
                  </span>
                )}
                <div className="ml-auto">
                  <CopyFixButton comment={comment} filePath={filePath} />
                </div>
              </div>
              <Markdown>{comment.body}</Markdown>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

export default function ReviewDetailPage() {
  const { id } = useParams<{ id: string }>();
  const { data, isLoading } = useReview(id);
  const { data: repos } = useRepos();
  const retryReview = useRetryReview();

  const review = data?.review;
  const comments = data?.comments ?? [];

  const repoMap = useMemo(() => {
    return new Map<number, Repo>((repos ?? []).map((r) => [r.id, r]));
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

  const severityCounts = useMemo(() => {
    const counts: Record<string, number> = {};
    for (const c of comments) {
      if (c.severity) counts[c.severity] = (counts[c.severity] ?? 0) + 1;
    }
    return counts;
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
  const ghUrl = repo
    ? githubPrUrl(repo.full_name, review.pr_number, review.github_review_id)
    : undefined;
  const duration = review.duration_ms
    ? (review.duration_ms / 1000).toFixed(1) + "s"
    : null;

  return (
    <>
      <Link
        href="/reviews"
        className="inline-flex items-center gap-1.5 text-xs font-mono text-slate-text hover:text-amber transition-colors mb-6"
      >
        <ArrowLeft className="h-3.5 w-3.5" />
        Back to Reviews
      </Link>

      {/* Header card */}
      <div className="rounded-lg border border-iron bg-charcoal p-6 mb-6">
        <div className="flex items-start justify-between gap-6">
          <div className="flex-1 min-w-0">
            <h1 className="font-display text-xl font-bold text-foreground mb-2 truncate">
              {review.pr_title}
            </h1>
            <div className="flex items-center gap-3 flex-wrap">
              <span className="inline-flex items-center gap-1.5 text-xs font-mono text-slate-text">
                <GitPullRequest className="h-3.5 w-3.5" />
                {repo?.full_name ?? "unknown"} #{review.pr_number}
              </span>
              <span className="text-iron">&middot;</span>
              <span className="text-xs font-mono text-slate-text">
                by {review.pr_author}
              </span>
              {duration && (
                <>
                  <span className="text-iron">&middot;</span>
                  <span className="inline-flex items-center gap-1 text-xs font-mono text-slate-text">
                    <Clock className="h-3 w-3" />
                    {duration}
                  </span>
                </>
              )}
              <StatusBadge status={review.status} />
            </div>
          </div>
          <div className="flex items-center gap-3 shrink-0">
            <ScoreBox score={review.score} />
            {ghUrl && (
              <a
                href={ghUrl}
                target="_blank"
                rel="noopener noreferrer"
                className="inline-flex items-center gap-1.5 rounded-md border border-iron px-3 py-1.5 text-xs font-mono text-slate-text hover:text-amber hover:border-amber/50 transition-colors"
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

      {/* Severity overview bar */}
      {Object.keys(severityCounts).length > 0 && (
        <div className="flex items-center gap-4 mb-6 px-1">
          {["critical", "warning", "suggestion", "praise"].map((sev) =>
            severityCounts[sev] ? (
              <div key={sev} className="flex items-center gap-1.5">
                <div
                  className={`h-2 w-2 rounded-full ${severityDot[sev] ?? "bg-slate-text"}`}
                />
                <span className="text-[11px] font-mono text-slate-text">
                  {severityCounts[sev]} {sev}
                </span>
              </div>
            ) : null,
          )}
          <span className="text-[11px] font-mono text-iron ml-auto">
            {comments.length} comment{comments.length !== 1 ? "s" : ""} across{" "}
            {grouped.length} file{grouped.length !== 1 ? "s" : ""}
          </span>
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

      {/* Summary card */}
      {review.summary && (
        <div className="rounded-lg border border-iron bg-charcoal p-5 mb-6">
          <h2 className="text-xs font-mono uppercase tracking-[0.1em] text-slate-text mb-3">
            Summary
          </h2>
          <Markdown>{review.summary}</Markdown>
        </div>
      )}

      {/* File-grouped comments */}
      {grouped.length > 0 && (
        <div className="space-y-4">
          {grouped.map(([filePath, fileComments]) => (
            <FileGroup
              key={filePath}
              filePath={filePath}
              fileComments={fileComments}
            />
          ))}
        </div>
      )}

      {/* No comments state */}
      {grouped.length === 0 && review.status === "completed" && (
        <div className="rounded-lg border border-iron bg-charcoal p-10 text-center">
          <p className="text-xs font-mono text-slate-text">
            No comments &mdash; the code looks good!
          </p>
        </div>
      )}
    </>
  );
}
