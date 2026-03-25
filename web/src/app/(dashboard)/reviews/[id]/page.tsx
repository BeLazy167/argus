"use client";

import { useMemo, useState, useCallback, useEffect } from "react";
import { useParams } from "next/navigation";
import Link from "next/link";
import ReactMarkdown from "react-markdown";
import { Prism as SyntaxHighlighter } from "react-syntax-highlighter";
import { oneDark } from "react-syntax-highlighter/dist/esm/styles/prism";
import {
  ArrowLeft,
  ExternalLink,
  FileCode,
  AlertTriangle,
  RotateCcw,
  Loader2,
  Clock,
  GitPullRequest,
  Check,
  ChevronDown,
  ChevronRight,
  Copy,
  MessageSquare,
  Filter,
} from "lucide-react";
import { useReview, useRetryReview } from "@/lib/queries/reviews";
import { useRepos } from "@/lib/queries/repos";
import { githubPrUrl } from "@/lib/github";
import { ScoreBox } from "@/components/dashboard/score-badge";
import { StatusBadge } from "@/components/dashboard/status-badge";
import { formatDistanceToNow } from "@/lib/time";
import { useReviewStream } from "@/lib/hooks/use-review-stream";
import { PipelineProgress } from "./progress-bar";
import type { Repo, ReviewComment, TokenUsage } from "@/lib/types";

/* ── Helpers ─────────────────────────────────── */

function formatTokens(t: { total_tokens: number }): string {
  const k = t.total_tokens / 1000;
  return k >= 1 ? `${k.toFixed(1)}k` : String(t.total_tokens);
}

function lineRef(c: ReviewComment): string {
  const { start_line, end_line } = c;
  if (start_line != null && end_line != null && start_line !== end_line)
    return `L${start_line}\u2013${end_line}`;
  const line = end_line ?? start_line;
  return line != null ? `L${line}` : "";
}

/** Guess language from file extension for syntax highlighting. */
function langFromPath(path: string): string {
  const ext = path.split(".").pop()?.toLowerCase() ?? "";
  const map: Record<string, string> = {
    ts: "typescript",
    tsx: "tsx",
    js: "javascript",
    jsx: "jsx",
    go: "go",
    py: "python",
    rs: "rust",
    rb: "ruby",
    java: "java",
    kt: "kotlin",
    cs: "csharp",
    css: "css",
    scss: "scss",
    html: "html",
    json: "json",
    yaml: "yaml",
    yml: "yaml",
    toml: "toml",
    sql: "sql",
    sh: "bash",
    bash: "bash",
    md: "markdown",
    dockerfile: "docker",
  };
  return map[ext] ?? "text";
}

/**
 * Strip per-file comment listings and code blocks from summary.
 * Keep only the high-level synthesis prose.
 */
function extractSynthesis(summary: string): string {
  const lines = summary.split("\n");
  const cleaned: string[] = [];
  let skipping = false;
  let inCodeBlock = false;

  for (const line of lines) {
    // Strip ALL code blocks from summary — they duplicate the detail view
    if (/^```/.test(line.trim())) {
      inCodeBlock = !inCodeBlock;
      continue;
    }
    if (inCodeBlock) continue;

    // Skip headings like "# Argus Review" or "## Review Summary"
    if (/^#{1,3}\s*(argus\s*review|review\s*summary)/i.test(line)) continue;
    // Skip "Reviewed N files with M comments" stat lines
    if (/reviewed\s+\d+\s+file/i.test(line)) continue;
    // Skip file name references — start skipping per-file details
    if (
      /^[`*\s]*[\w/.-]+\.(md|ts|tsx|js|jsx|go|py|rs|css|html|json|yaml|yml|toml|sql)[`*\s]*$/i.test(
        line.trim(),
      )
    ) {
      skipping = true;
      continue;
    }
    // Skip file headings like "### src/lib/foo.ts"
    if (/^#{2,4}\s+[`*]*[\w/.-]+\.\w+/i.test(line)) {
      skipping = true;
      continue;
    }
    // Skip bullet lines with severity tags
    if (
      /^\s*[-*•]\s*\*?\*?\[?(critical|warning|suggestion|praise)\]?\*?\*?/i.test(line)
    ) {
      skipping = true;
      continue;
    }
    if (/^\s*[-*•]\s*\*{0,2}\[/.test(line)) {
      skipping = true;
      continue;
    }
    // Skip "If you need to..." fix instruction lines
    if (/^\s*(if you need|consider|you (should|could|can)|instead|use )/i.test(line.trim()) && skipping) {
      continue;
    }
    if (skipping) {
      if (line.trim() === "") skipping = false;
      else if (/^\s{2,}/.test(line) || /^\s*[-*•]/.test(line)) continue;
      else skipping = false;
    }
    if (!skipping) cleaned.push(line);
  }

  return cleaned
    .join("\n")
    .replace(/^\n+|\n+$/g, "")
    .replace(/\n{3,}/g, "\n\n");
}

/* ── Severity Maps ───────────────────────────── */

const severityStyles: Record<string, string> = {
  critical: "bg-red-400/10 text-red-400 border-red-400/30",
  warning: "bg-amber/10 text-amber border-amber/30",
  suggestion: "bg-blue-400/10 text-blue-400 border-blue-400/30",
  praise: "bg-green-400/10 text-green-400 border-green-400/30",
};

const severityDot: Record<string, string> = {
  critical: "bg-red-400",
  warning: "bg-amber",
  suggestion: "bg-blue-400",
  praise: "bg-green-400",
};

const severityBorder: Record<string, string> = {
  critical: "border-l-red-400",
  warning: "border-l-amber",
  suggestion: "border-l-blue-400",
  praise: "border-l-green-400",
};

const severityBg: Record<string, string> = {
  critical: "bg-red-400/[0.03]",
  warning: "bg-amber/[0.03]",
  suggestion: "bg-blue-400/[0.03]",
  praise: "bg-green-400/[0.03]",
};

/* ── Sub-components ──────────────────────────── */

function TokenPill({ usage }: { usage: TokenUsage }) {
  const total = usage.total;
  const label = `${formatTokens(total)} tokens${total.cost != null ? ` · $${total.cost.toFixed(3)}` : ""}`;
  const stages: [string, { total_tokens: number; cost?: number }][] = [];
  if (usage.triage?.total_tokens) stages.push(["triage", usage.triage]);
  if (usage.review?.length) {
    const reviewTotal = usage.review.reduce((acc, r) => ({
      total_tokens: acc.total_tokens + r.total_tokens,
      cost: (acc.cost ?? 0) + (r.cost ?? 0),
    }), { total_tokens: 0, cost: 0 });
    stages.push(["review", reviewTotal]);
  }
  if (usage.scoring?.total_tokens) stages.push(["scoring", usage.scoring]);

  return (
    <div className="group relative">
      <span className="inline-flex items-center rounded-md border border-iron bg-iron/30 px-2.5 py-1 text-[11px] font-mono text-slate-text">
        {label}
      </span>
      {stages.length > 0 && (
        <div className="absolute right-0 top-full mt-1.5 z-10 hidden group-hover:block">
          <div className="rounded-lg border border-iron bg-charcoal p-3 shadow-xl min-w-[180px]">
            <p className="text-[10px] font-mono uppercase tracking-wider text-slate-text mb-2">
              By stage
            </p>
            {stages.map(([stage, s]) => (
              <div
                key={stage}
                className="flex items-center justify-between py-0.5"
              >
                <span className="text-[10px] font-mono text-amber">
                  {stage}
                </span>
                <span className="text-[10px] font-mono text-foreground">
                  {formatTokens(s)}
                  {s.cost != null && <> · ${s.cost.toFixed(3)}</>}
                </span>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

/** Prose markdown with Geist Sans for body, syntax highlighting for code blocks. */
function Markdown({
  children,
  filePath,
}: {
  children: string;
  filePath?: string;
}) {
  const lang = filePath ? langFromPath(filePath) : "text";

  return (
    <div className="font-sans">
      <ReactMarkdown
        components={{
          h1: ({ children }) => (
            <h3 className="font-display text-base font-bold text-foreground mt-5 mb-2 first:mt-0">
              {children}
            </h3>
          ),
          h2: ({ children }) => (
            <h3 className="font-display text-base font-bold text-foreground mt-5 mb-2 first:mt-0">
              {children}
            </h3>
          ),
          h3: ({ children }) => (
            <h4 className="font-display text-sm font-semibold text-foreground mt-4 mb-1.5 first:mt-0">
              {children}
            </h4>
          ),
          p: ({ children }) => (
            <p className="text-[13px] text-foreground/80 leading-[1.75] mb-3 last:mb-0">
              {children}
            </p>
          ),
          ul: ({ children }) => (
            <ul className="list-disc list-outside ml-4 space-y-1.5 mb-3 text-[13px] text-foreground/80">
              {children}
            </ul>
          ),
          ol: ({ children }) => (
            <ol className="list-decimal list-outside ml-4 space-y-1.5 mb-3 text-[13px] text-foreground/80">
              {children}
            </ol>
          ),
          li: ({ children }) => (
            <li className="leading-[1.75] pl-1">{children}</li>
          ),
          strong: ({ children }) => (
            <strong className="font-semibold text-foreground">{children}</strong>
          ),
          code: ({ className, children }) => {
            const match = className?.match(/language-(\w+)/);
            const codeLang = match?.[1] ?? lang;
            const codeStr = String(children).replace(/\n$/, "");

            if (className?.includes("language-") || codeStr.includes("\n")) {
              return (
                <SyntaxHighlighter
                  style={oneDark}
                  language={codeLang}
                  customStyle={{
                    margin: "12px 0",
                    borderRadius: "6px",
                    fontSize: "12px",
                    border: "1px solid oklch(0.18 0 0 / 0.6)",
                    background: "oklch(0.07 0 0 / 0.8)",
                  }}
                >
                  {codeStr}
                </SyntaxHighlighter>
              );
            }
            return (
              <code className="bg-amber/10 border border-amber/20 rounded px-1.5 py-0.5 text-[11px] font-mono text-amber">
                {children}
              </code>
            );
          },
          pre: ({ children }) => <>{children}</>,
          a: ({ href, children }) => (
            <a
              href={href}
              className="text-amber hover:underline underline-offset-2"
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
    </div>
  );
}

function CodeSnippet({
  code,
  startLine,
  language,
}: {
  code: string;
  startLine?: number;
  language: string;
}) {
  if (!code) return null;
  const start = startLine ?? 1;

  return (
    <div className="mx-4 mt-3 mb-1 rounded-md overflow-hidden border border-iron/40">
      <SyntaxHighlighter
        style={oneDark}
        language={language}
        showLineNumbers
        startingLineNumber={start}
        lineNumberStyle={{
          minWidth: "3em",
          paddingRight: "1em",
          color: "oklch(0.18 0 0 / 0.6)",
          borderRight: "1px solid oklch(0.18 0 0 / 0.3)",
          marginRight: "1em",
          userSelect: "none",
        }}
        customStyle={{
          margin: 0,
          borderRadius: 0,
          fontSize: "11px",
          lineHeight: "1.65",
          background: "oklch(0.07 0 0 / 0.8)",
          padding: "8px 0",
        }}
        wrapLines
        lineProps={() => ({
          style: { display: "flex", paddingRight: "1em" },
          className: "hover:bg-[oklch(0.18_0_0/0.15)]",
        })}
      >
        {code}
      </SyntaxHighlighter>
    </div>
  );
}

function CopyFixButton({
  comment,
  filePath,
}: {
  comment: ReviewComment;
  filePath: string;
}) {
  const [copied, setCopied] = useState(false);
  const ref = lineRef(comment);

  const handleCopy = useCallback(() => {
    const prompt = [
      `Fix the following ${comment.severity ?? "issue"} in \`${filePath}\`${ref ? ` at ${ref}` : ""}:`,
      "",
      comment.body,
      "",
      `Category: ${comment.category ?? "general"}`,
    ].join("\n");

    navigator.clipboard.writeText(prompt);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  }, [comment, filePath, ref]);

  return (
    <button
      type="button"
      onClick={handleCopy}
      aria-label={`Copy fix prompt for ${filePath}${ref ? ` ${ref}` : ""} ${comment.severity ?? ""}`}
      className="inline-flex items-center gap-1.5 rounded-md border border-iron/50 bg-iron/20 px-2.5 py-1 text-[10px] font-mono text-slate-text hover:text-amber hover:border-amber/30 hover:bg-amber/5 transition-all opacity-0 group-hover:opacity-100 focus-visible:opacity-100"
    >
      {copied ? (
        <>
          <Check className="h-3 w-3 text-green-400" />
          <span className="text-green-400">Copied</span>
        </>
      ) : (
        <>
          <Copy className="h-3 w-3" />
          <span>Copy fix prompt</span>
        </>
      )}
    </button>
  );
}

function CommentCard({
  comment,
  filePath,
}: {
  comment: ReviewComment;
  filePath: string;
}) {
  const borderClass = comment.severity
    ? (severityBorder[comment.severity] ?? "")
    : "";
  const bgClass = comment.severity
    ? (severityBg[comment.severity] ?? "")
    : "";
  const newFindingBorder = comment.is_new_finding ? "border-l-2 border-l-emerald-500/40" : "";

  return (
    <div
      className={`group border-l-[3px] ${borderClass} ${bgClass} ${newFindingBorder} hover:bg-charcoal/40 transition-colors px-5 py-4 mx-4 my-2 rounded-r-md`}
    >
      <div className="flex items-center gap-2 mb-3">
        {comment.severity && (
          <span
            className={`inline-flex items-center rounded-sm border px-2.5 py-0.5 text-[10px] font-mono uppercase tracking-wider font-medium ${severityStyles[comment.severity] ?? ""}`}
          >
            {comment.severity}
          </span>
        )}
        {comment.category && (
          <span className="inline-flex items-center rounded-sm border bg-iron/30 text-slate-text border-iron/60 px-2.5 py-0.5 text-[10px] font-mono">
            {comment.category}
          </span>
        )}
        {comment.specialist && (
          <span className="inline-flex items-center rounded-sm border bg-purple-400/10 text-purple-400 border-purple-400/30 px-2.5 py-0.5 text-[10px] font-mono">
            {comment.specialist}
          </span>
        )}
        {comment.confidence_score != null && (
          <span className="text-[10px] font-mono text-slate-text" title="Confidence score">
            {comment.confidence_score}%
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
      {(comment.is_new_finding || comment.matched_pattern_score || comment.enforced_rule_content) && (
        <div className="flex flex-wrap gap-1.5 mt-1.5 mb-3">
          {comment.is_new_finding && (
            <span className="inline-flex items-center gap-1 rounded border border-emerald-500/30 bg-emerald-500/10 px-2 py-0.5 text-[10px] font-mono text-emerald-400">
              New Finding
            </span>
          )}
          {comment.matched_pattern_score && (
            <span className="inline-flex items-center gap-1 rounded border border-amber/30 bg-amber/10 px-2 py-0.5 text-[10px] font-mono text-amber">
              Pattern Match ({Math.round(comment.matched_pattern_score * 100)}%)
            </span>
          )}
          {comment.enforced_rule_content && (
            <span className="inline-flex items-center gap-1 rounded border border-purple-500/30 bg-purple-500/10 px-2 py-0.5 text-[10px] font-mono text-purple-400" title={comment.enforced_rule_content}>
              Enforces: {comment.enforced_rule_content.slice(0, 60)}{comment.enforced_rule_content.length > 60 ? '...' : ''}
            </span>
          )}
        </div>
      )}
      <Markdown filePath={filePath}>{comment.body}</Markdown>
    </div>
  );
}

function FileGroup({
  filePath,
  fileComments,
  id,
  hidden,
}: {
  filePath: string;
  fileComments: readonly ReviewComment[];
  id: string;
  hidden: boolean;
}) {
  const [expanded, setExpanded] = useState(true);
  const Chevron = expanded ? ChevronDown : ChevronRight;
  const contentId = `${id}-content`;
  const language = langFromPath(filePath);

  const maxSeverity = fileComments.some((c) => c.severity === "critical")
    ? "critical"
    : fileComments.some((c) => c.severity === "warning")
      ? "warning"
      : null;

  if (hidden) return null;

  return (
    <section
      id={id}
      className="scroll-mt-6 rounded-lg border border-iron overflow-hidden"
    >
      <button
        type="button"
        onClick={() => setExpanded(!expanded)}
        aria-expanded={expanded}
        aria-controls={contentId}
        className="flex items-center gap-2 w-full bg-charcoal px-4 py-3 border-b border-iron hover:bg-iron/20 transition-colors text-left"
      >
        <Chevron className="h-3.5 w-3.5 text-slate-text shrink-0" />
        <FileCode className="h-3.5 w-3.5 text-slate-text shrink-0" />
        <h3 className="font-mono text-xs text-foreground truncate">
          {filePath}
        </h3>
        <div className="flex items-center gap-2 ml-auto shrink-0">
          {maxSeverity && (
            <div
              className={`h-2 w-2 rounded-full ${severityDot[maxSeverity]}`}
            />
          )}
          <span className="text-[10px] font-mono text-slate-text">
            <MessageSquare className="inline h-3 w-3 mr-1 -mt-0.5" />
            {fileComments.length}
          </span>
        </div>
      </button>
      {expanded && (
        <div id={contentId} role="region" aria-label={`Comments for ${filePath}`} className="py-2">
          {fileComments.map((comment, i) => (
            <div key={comment.id}>
              {comment.code_snippet && (
                <CodeSnippet
                  code={comment.code_snippet}
                  startLine={comment.start_line}
                  language={language}
                />
              )}
              <CommentCard comment={comment} filePath={filePath} />
              {i < fileComments.length - 1 && (
                <div className="mx-4 border-b border-iron/30 my-1" />
              )}
            </div>
          ))}
        </div>
      )}
    </section>
  );
}

/** Sticky sidebar with file nav and interactive severity filters. */
function FileTOC({
  grouped,
  severityCounts,
  activeFileId,
  activeSeverity,
  onSeverityFilter,
}: {
  grouped: readonly (readonly [string, readonly ReviewComment[]])[];
  severityCounts: Record<string, number>;
  activeFileId: string | null;
  activeSeverity: string | null;
  onSeverityFilter: (sev: string | null) => void;
}) {
  return (
    <nav className="sticky top-6 space-y-1" aria-label="File navigation">
      <p className="text-[10px] font-mono uppercase tracking-[0.15em] text-slate-text mb-3 px-2">
        Files
      </p>
      {grouped.map(([filePath, fileComments]) => {
        const fid = `file-${filePath.replace(/[^a-zA-Z0-9]/g, "-")}`;
        const isActive = activeFileId === fid;
        const maxSev = fileComments.some((c) => c.severity === "critical")
          ? "critical"
          : fileComments.some((c) => c.severity === "warning")
            ? "warning"
            : fileComments.some((c) => c.severity === "suggestion")
              ? "suggestion"
              : null;

        return (
          <a
            key={filePath}
            href={`#${fid}`}
            className={`flex items-center gap-2 px-2 py-1.5 rounded-md text-[11px] font-mono transition-colors ${
              isActive
                ? "bg-iron/30 text-foreground"
                : "text-slate-text hover:text-foreground hover:bg-iron/20"
            }`}
          >
            {maxSev && (
              <div
                className={`h-1.5 w-1.5 rounded-full shrink-0 ${severityDot[maxSev]}`}
              />
            )}
            <span className="truncate">{filePath.split("/").pop()}</span>
            <span className="ml-auto text-[10px] text-iron shrink-0">
              {fileComments.length}
            </span>
          </a>
        );
      })}

      {/* Interactive severity filters */}
      {Object.keys(severityCounts).length > 0 && (
        <div className="pt-3 mt-3 border-t border-iron/30 space-y-0.5">
          <div className="flex items-center gap-1.5 px-2 mb-2">
            <Filter className="h-3 w-3 text-slate-text" />
            <p className="text-[10px] font-mono uppercase tracking-[0.15em] text-slate-text">
              Filter
            </p>
          </div>
          {(["critical", "warning", "suggestion", "praise"] as const).map(
            (sev) =>
              severityCounts[sev] ? (
                <button
                  key={sev}
                  type="button"
                  aria-pressed={activeSeverity === sev}
                  aria-label={`Filter by ${sev} severity`}
                  onClick={() =>
                    onSeverityFilter(activeSeverity === sev ? null : sev)
                  }
                  className={`flex items-center gap-2 w-full px-2 py-1.5 rounded-md text-[11px] font-mono transition-colors cursor-pointer text-left ${
                    activeSeverity === sev
                      ? "bg-iron/30 text-foreground"
                      : "text-slate-text hover:text-foreground hover:bg-iron/20"
                  }`}
                >
                  <div
                    className={`h-2 w-2 rounded-full ${severityDot[sev]}`}
                  />
                  <span className="capitalize">{sev}</span>
                  <span className="ml-auto text-iron">
                    {severityCounts[sev]}
                  </span>
                </button>
              ) : null,
          )}
          {activeSeverity && (
            <button
              type="button"
              onClick={() => onSeverityFilter(null)}
              className="flex items-center gap-1 w-full px-2 py-1 text-[10px] font-mono text-amber hover:underline cursor-pointer"
            >
              Clear filter
            </button>
          )}
        </div>
      )}
    </nav>
  );
}

/* ── Main Page ───────────────────────────────── */

export default function ReviewDetailPage() {
  const { id } = useParams<{ id: string }>();
  const { data, isLoading } = useReview(id);
  const { data: repos } = useRepos();
  const retryReview = useRetryReview();
  const [activeSeverity, setActiveSeverity] = useState<string | null>(null);

  const review = data?.review;
  const comments = data?.comments ?? [];

  const isLive = review?.status === "pending" || review?.status === "in_progress";
  const { stage, triageResults, failedStage } = useReviewStream(id, isLive);

  const repoMap = useMemo(
    () => new Map<number, Repo>((repos ?? []).map((r) => [r.id, r])),
    [repos],
  );

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

  // Determine which file groups are visible after severity filter
  const visibleFiles = useMemo(() => {
    if (!activeSeverity) return new Set(grouped.map(([p]) => p));
    const set = new Set<string>();
    for (const [path, cs] of grouped) {
      if (cs.some((c) => c.severity === activeSeverity)) set.add(path);
    }
    return set;
  }, [grouped, activeSeverity]);

  // Scroll-aware active file tracking
  const [activeFileId, setActiveFileId] = useState<string | null>(null);

  useEffect(() => {
    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting) {
            setActiveFileId(entry.target.id);
            break;
          }
        }
      },
      { rootMargin: "-10% 0px -60% 0px", threshold: 0 },
    );

    const timer = setTimeout(() => {
      document.querySelectorAll("section[id^='file-']").forEach((el) => {
        observer.observe(el);
      });
    }, 100);

    return () => {
      clearTimeout(timer);
      observer.disconnect();
    };
  }, [grouped]);

  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-20">
        <Loader2 className="h-6 w-6 animate-spin text-slate-text" />
      </div>
    );
  }

  if (!review) {
    return (
      <div className="py-20 text-center font-sans text-sm text-slate-text">
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

  const showSidebar = grouped.length > 1;

  return (
    <>
      {/* Breadcrumb */}
      <nav className="flex items-center gap-2 mb-6" aria-label="Breadcrumb">
        <Link
          href="/reviews"
          className="inline-flex items-center gap-1.5 text-xs font-sans text-slate-text hover:text-amber transition-colors"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          Reviews
        </Link>
        <span className="text-iron text-xs">/</span>
        <span className="text-xs font-sans text-foreground/60 truncate max-w-[300px]">
          {review.pr_title}
        </span>
      </nav>

      {/* Header card */}
      <header className="rounded-lg border border-iron bg-charcoal p-6 mb-6">
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
              <span className="text-iron">·</span>
              <span className="text-xs font-sans text-slate-text">
                by {review.pr_author}
              </span>
              <StatusBadge status={review.status} />
              {review.deep_review && (
                <span className="inline-flex items-center rounded-sm border bg-purple-400/10 text-purple-400 border-purple-400/30 px-2 py-0.5 text-[10px] font-mono">
                  Deep
                </span>
              )}
              {review.is_incremental && (
                <span className="inline-flex items-center rounded-sm border bg-cyan-400/10 text-cyan-400 border-cyan-400/30 px-2 py-0.5 text-[10px] font-mono">
                  Incremental
                </span>
              )}
              {review.persona && (
                <span className="inline-flex items-center rounded-sm border bg-iron/30 text-slate-text border-iron/60 px-2 py-0.5 text-[10px] font-mono">
                  {review.persona}
                </span>
              )}
            </div>
            <div className="flex items-center gap-3 mt-2.5 text-[10px] font-mono text-iron">
              {review.created_at && (
                <span className="inline-flex items-center gap-1">
                  <Clock className="h-3 w-3" />
                  {formatDistanceToNow(review.created_at)}
                </span>
              )}
              {duration && (
                <>
                  <span>·</span>
                  <span>{duration} runtime</span>
                </>
              )}
              {review.token_usage && (
                <>
                  <span>·</span>
                  <TokenPill usage={review.token_usage} />
                </>
              )}
            </div>
          </div>
          <div className="flex items-center gap-3 shrink-0">
            <ScoreBox score={review.score} />
            {ghUrl && (
              <a
                href={ghUrl}
                target="_blank"
                rel="noopener noreferrer"
                className="inline-flex items-center gap-1.5 rounded-md border border-iron px-3 py-2 text-xs font-mono text-slate-text hover:text-amber hover:border-amber/50 transition-colors"
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
                className="inline-flex items-center gap-1.5 rounded-md border border-amber/30 bg-amber/10 px-3 py-2 text-xs font-mono text-amber hover:bg-amber/20 transition-colors"
              >
                <RotateCcw
                  className={`h-3.5 w-3.5 ${retryReview.isPending ? "animate-spin" : ""}`}
                />
                Retry
              </button>
            )}
          </div>
        </div>
      </header>

      {/* Pipeline progress (live reviews) */}
      {isLive && <PipelineProgress stage={stage} failedStage={failedStage} />}

      {/* Triage results card */}
      {triageResults && isLive && (
        <div className="rounded-lg border border-iron bg-charcoal/80 p-5 mb-6">
          <h3 className="font-display text-sm font-bold text-foreground mb-3">
            File Classification
          </h3>
          <div className="space-y-1">
            {triageResults.map((t) => (
              <div key={t.file} className="flex items-center gap-2 text-xs font-mono">
                <span
                  className={`inline-flex items-center rounded-sm border px-2 py-0.5 text-[10px] ${
                    t.action === "deep"
                      ? "bg-purple-400/10 text-purple-400 border-purple-400/30"
                      : t.action === "skip"
                        ? "bg-iron/30 text-iron border-iron/60"
                        : t.action === "security_skim"
                          ? "bg-red-400/10 text-red-400 border-red-400/30"
                          : "bg-blue-400/10 text-blue-400 border-blue-400/30"
                  }`}
                >
                  {t.action}
                </span>
                <span className="text-foreground/70 truncate">{t.file}</span>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Error card */}
      {review.status === "failed" && review.error && (
        <div
          className="rounded-lg border border-red-400/30 bg-red-400/5 p-5 mb-6"
          role="alert"
        >
          <div className="flex items-center gap-2 mb-3">
            <AlertTriangle className="h-4 w-4 text-red-400" />
            <h2 className="font-display text-sm font-bold text-red-400">
              Review Failed
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
      <div className="rounded-lg border border-iron bg-charcoal/80 p-6 mb-8">
        {/* Heading inside card */}
        <h2 className="font-display text-base font-bold text-foreground mb-4">
          Summary
        </h2>

        {/* Stats bar */}
        <div className="flex items-center gap-3 mb-5 pb-4 border-b border-iron/40">
          <span className="text-xs font-sans text-slate-text">
            {comments.length} comment{comments.length !== 1 ? "s" : ""} across{" "}
            {grouped.length} file{grouped.length !== 1 ? "s" : ""}
          </span>
          <div className="h-3.5 w-px bg-iron/40" />
          <div className="flex items-center gap-3">
            {(["critical", "warning", "suggestion", "praise"] as const).map(
              (sev) =>
                severityCounts[sev] ? (
                  <div key={sev} className="flex items-center gap-1.5">
                    <div
                      className={`h-2.5 w-2.5 rounded-full ${severityDot[sev]}`}
                    />
                    <span className="text-xs font-sans text-foreground/70">
                      {severityCounts[sev]}{" "}
                      <span className="hidden sm:inline">{sev}</span>
                    </span>
                  </div>
                ) : null,
            )}
          </div>
        </div>

        {/* AI synthesis — proportional font, stripped of per-file duplication */}
        {review.summary ? (
          <Markdown>{extractSynthesis(review.summary)}</Markdown>
        ) : (
          <p className="text-sm font-sans text-foreground/50">
            No summary generated.
          </p>
        )}
      </div>

      {/* Simulation Results */}
      {review.simulation_results && review.simulation_results.length > 0 && (
        <div className="rounded-lg border border-iron bg-charcoal/80 p-4 mb-6">
          <div className="mb-3 flex items-center gap-2">
            <span className="text-sm font-medium text-foreground">Simulation Results</span>
            <span className="rounded px-1.5 py-0.5 text-xs bg-charcoal text-slate-text">
              {review.simulation_results.length} scenario{review.simulation_results.length !== 1 ? 's' : ''}
            </span>
          </div>
          <div className="space-y-2">
            {review.simulation_results.map((result, i) => (
              <div key={i} className="flex items-start gap-3 rounded-md border border-iron bg-charcoal px-3 py-2">
                <span className={`mt-0.5 shrink-0 text-xs font-bold ${result.passes ? 'text-emerald-500' : 'text-red-500'}`}>
                  {result.passes ? 'PASS' : 'FAIL'}
                </span>
                <div className="min-w-0 flex-1">
                  <p className="text-xs text-foreground/70 truncate">{result.scenario}</p>
                  {!result.passes && result.root_cause && (
                    <p className="mt-0.5 text-xs text-slate-text">{result.root_cause}</p>
                  )}
                </div>
                <span className={`shrink-0 text-xs tabular-nums ${result.confidence >= 0.7 ? 'text-amber' : 'text-slate-text'}`}>
                  {Math.round(result.confidence * 100)}%
                </span>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Findings enrichment summary */}
      {comments.length > 0 && (() => {
        const newFindings = comments.filter((c) => c.is_new_finding === true).length;
        const patternMatches = comments.filter((c) => c.matched_pattern_id).length;
        const rulesEnforced = comments.filter((c) => c.enforced_rule_content).length;
        if (!newFindings && !patternMatches && !rulesEnforced) return null;
        return (
          <div className="flex items-center gap-4 rounded-lg border border-iron bg-charcoal px-5 py-3 mb-6">
            {newFindings > 0 && (
              <div className="flex items-center gap-2 text-xs font-mono">
                <div className="h-2 w-2 rounded-full bg-emerald-500" />
                <span className="text-foreground">{newFindings}</span>
                <span className="text-slate-text">New Findings</span>
              </div>
            )}
            {patternMatches > 0 && (
              <div className="flex items-center gap-2 text-xs font-mono">
                <div className="h-2 w-2 rounded-full bg-amber" />
                <span className="text-foreground">{patternMatches}</span>
                <span className="text-slate-text">Pattern Matches</span>
              </div>
            )}
            {rulesEnforced > 0 && (
              <div className="flex items-center gap-2 text-xs font-mono">
                <div className="h-2 w-2 rounded-full bg-purple-500" />
                <span className="text-foreground">{rulesEnforced}</span>
                <span className="text-slate-text">Rules Enforced</span>
              </div>
            )}
          </div>
        );
      })()}

      {/* Main content: sidebar + file groups */}
      <div className={showSidebar ? "grid grid-cols-1 gap-6 lg:grid-cols-[200px_1fr]" : ""}>
        {showSidebar && (
          <aside className="hidden lg:block">
            <FileTOC
              grouped={grouped}
              severityCounts={severityCounts}
              activeFileId={activeFileId}
              activeSeverity={activeSeverity}
              onSeverityFilter={setActiveSeverity}
            />
          </aside>
        )}

        {grouped.length > 0 ? (
          <div className="space-y-5">
            {grouped.map(([filePath, fileComments]) => {
              const fid = `file-${filePath.replace(/[^a-zA-Z0-9]/g, "-")}`;
              // Filter comments by severity if active
              const filtered = activeSeverity
                ? fileComments.filter((c) => c.severity === activeSeverity)
                : fileComments;

              return (
                <FileGroup
                  key={filePath}
                  id={fid}
                  filePath={filePath}
                  fileComments={filtered}
                  hidden={!visibleFiles.has(filePath)}
                />
              );
            })}
            {activeSeverity && visibleFiles.size === 0 && (
              <div className="rounded-lg border border-iron bg-charcoal p-10 text-center">
                <p className="font-sans text-sm text-slate-text">
                  No {activeSeverity} comments found.
                </p>
              </div>
            )}
          </div>
        ) : (
          review.status === "completed" && (
            <div className="rounded-lg border border-iron bg-charcoal p-10 text-center">
              <p className="font-sans text-sm text-slate-text">
                No comments — the code looks good!
              </p>
            </div>
          )
        )}
      </div>
    </>
  );
}
