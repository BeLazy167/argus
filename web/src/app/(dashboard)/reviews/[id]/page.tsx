"use client";

import { memo, useMemo, useState, useCallback, useEffect, useRef } from "react";
import DOMPurify from "dompurify";
import { useParams } from "next/navigation";
import Link from "next/link";
import dynamic from "next/dynamic";
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
  ChevronUp,
  Copy,
  MessageSquare,
  Filter,
  Zap,
  Square,
} from "lucide-react";
import { useReview, useRetryReview, useCancelReview } from "@/lib/queries/reviews";
import { useRepos } from "@/lib/queries/repos";
import { usePattern } from "@/lib/queries/patterns";
import { githubPrUrl } from "@/lib/github";
import { ScoreBox } from "@/components/dashboard/score-badge";
import { StatusBadge } from "@/components/dashboard/status-badge";
import { formatDistanceToNow } from "@/lib/time";
import { useReviewStream } from "@/lib/hooks/use-review-stream";
import { PipelineProgress } from "./progress-bar";
import { ActivityTimeline } from "./activity-timeline";
import type { Repo, ReviewComment, TokenUsage } from "@/lib/types";

const Markdown = dynamic(() => import("./markdown").then(m => ({ default: m.Markdown })), { ssr: false });
const CodeSnippet = dynamic(() => import("./markdown").then(m => ({ default: m.CodeSnippet })), { ssr: false });

/* ── Helpers ─────────────────────────────────── */

function formatTokens(t: { total_tokens: number }): string {
  const n = t.total_tokens;
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`;
  return String(n);
}

function lineRef(c: ReviewComment): string {
  const { start_line, end_line } = c;
  if (start_line != null && end_line != null && start_line !== end_line)
    return `L${start_line}\u2013${end_line}`;
  const line = end_line ?? start_line;
  return line != null ? `L${line}` : "";
}

const LANG_MAP: Record<string, string> = {
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

function langFromPath(path: string): string {
  const ext = path.split(".").pop()?.toLowerCase() ?? "";
  return LANG_MAP[ext] ?? "text";
}

/**
 * Strip per-file comment listings and code blocks from summary.
 * Keep only the high-level synthesis prose.
 */
function capitalize(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1);
}

/**
 * Strip the auto-generated per-file listing from the summary. The backend
 * includes `### file/path.ts\n- [severity] L42: ...` sections for every file,
 * but those are already rendered as first-class comment cards below the
 * summary — showing them twice is noise. We keep everything ABOVE the first
 * `### \`...\`` heading (the real synthesis prose) plus any `## Issue Coverage`
 * or `## Cross-Repo PR Coverage` sections that come AFTER the file listing.
 */
/**
 * Strip HTML tags and common markdown syntax from a comment body so it
 * renders cleanly in a plain-text preview (top findings card). We keep
 * the human-readable text and drop the scaffolding:
 * - `<details>`, `<summary>`, `<sub>`, `<sup>` tags removed
 * - `**bold**`, `*italic*`, `_emphasis_` markers stripped (text preserved)
 * - `` `code` `` backticks removed
 * - Collapsed internal whitespace
 */
// Hoisted regexes: created once at module load instead of per-call.
const HTML_TAG_RE = /<\/?(details|summary|sub|sup|kbd|mark)[^>]*>/gi;
const BOLD_RE = /\*\*([^*]+)\*\*/g;
const ITALIC_RE = /\*([^*]+)\*/g;
const UNDERSCORE_RE = /_([^_]+)_/g;
const BACKTICK_RE = /`([^`]+)`/g;
const WHITESPACE_RE = /\s+/g;

function stripMarkdownForPreview(s: string): string {
  return s
    .replace(HTML_TAG_RE, "")
    .replace(BOLD_RE, "$1")
    .replace(ITALIC_RE, "$1")
    .replace(UNDERSCORE_RE, "$1")
    .replace(BACKTICK_RE, "$1")
    .replace(WHITESPACE_RE, " ")
    .trim();
}

const ARGUS_HEADING_RE = /^#+\s*Argus Review\s*(\(Incremental\))?\s*\n*/i;
const REVIEWED_LINE_RE = /^Reviewed \d+ files with \d+ comments\.\s*\n*/i;
const TRIM_NEWLINES_RE = /^\n+|\n+$/g;
const FILE_HEADING_RE = /^#{3}\s+`[^`]+`/m;
const COVERAGE_RE = /\n## (Issue Coverage|Cross-Repo PR Coverage)/;

function extractSynthesis(summary: string): string {
  const cleaned = summary
    .replace(ARGUS_HEADING_RE, "")
    .replace(REVIEWED_LINE_RE, "")
    .replace(TRIM_NEWLINES_RE, "");

  // Find the first per-file heading (### `path/to/file`) via .search().
  // Must match at string start OR after a newline — the `m` flag makes `^`
  // match line starts. The previous version required a literal `\n` prefix
  // and missed the first heading when it was at position 0.
  const fileIdx = cleaned.search(FILE_HEADING_RE);
  if (fileIdx < 0) return cleaned;

  // Before-file-listing prose (the real synthesis).
  const prose = cleaned.slice(0, fileIdx).trim();

  // Preserve coverage sections added by synthesize() (## Issue Coverage / ##
  // Cross-Repo PR Coverage) even though they appear after the file listing.
  const afterFiles = cleaned.slice(fileIdx);
  const coverageIdx = afterFiles.search(COVERAGE_RE);
  if (coverageIdx >= 0) {
    const coveragePart = afterFiles.slice(coverageIdx).trim();
    return prose ? `${prose}\n\n${coveragePart}` : coveragePart;
  }
  return prose;
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



/* ── Mermaid ─────────────────────────────────── */

/* ── Friendly Errors ─────────────────────────── */

const HTTP_502_RE = /\b502\b/;
const HTTP_503_RE = /\b503\b/;

function friendlyError(raw: string): {
  title: string;
  detail: string;
  action: string;
} {
  if (raw.includes("secondary rate limit")) {
    return {
      title: "GitHub rate limit reached",
      detail:
        "GitHub temporarily blocked posting due to too many requests. The review is complete \u2014 findings are shown below.",
      action: "Wait a few minutes and click Retry to post to GitHub.",
    };
  }
  if (raw.includes("submitted too quickly")) {
    return {
      title: "GitHub hasn\u2019t processed the diff yet",
      detail:
        "The review was submitted before GitHub finished computing the PR diff.",
      action: "Click Retry \u2014 it usually works on the second attempt.",
    };
  }
  if (HTTP_502_RE.test(raw) || HTTP_503_RE.test(raw)) {
    return {
      title: "GitHub server error",
      detail:
        "GitHub returned a server error but may have posted the review anyway.",
      action: "Check the PR on GitHub. If no review appears, click Retry.",
    };
  }
  if (raw.includes("422")) {
    return {
      title: "GitHub rejected the review",
      detail:
        "GitHub could not process the review, likely due to line positions that no longer match the diff.",
      action: "Click Retry to re-run the review against the latest diff.",
    };
  }
  if (raw.includes("stage") && raw.includes("failed")) {
    return {
      title: "Review pipeline error",
      detail:
        raw.length > 200 ? raw.slice(0, 200) + "\u2026" : raw,
      action: "Click Retry to re-run the review.",
    };
  }
  return {
    title: "Review failed to post",
    detail: raw.length > 200 ? raw.slice(0, 200) + "\u2026" : raw,
    action: "Check your API key and provider settings, then click Retry.",
  };
}

/* ── Mermaid (chart) ─────────────────────────── */

const MermaidChart = memo(function MermaidChart({ chart }: { chart: string }) {
  const ref = useRef<HTMLDivElement>(null);
  const [error, setError] = useState(false);

  useEffect(() => {
    let cancelled = false;
    import("mermaid")
      .then((m) => {
        if (cancelled) return;
        m.default.initialize({
          startOnLoad: false,
          theme: "dark",
          themeVariables: {
            primaryColor: "#44403c",
            primaryTextColor: "#f5f0eb",
            primaryBorderColor: "#57534e",
            lineColor: "#78716c",
            secondaryColor: "#292524",
            tertiaryColor: "#1c1917",
            nodeTextColor: "#f5f0eb",
            nodeBorder: "#57534e",
            mainBkg: "#44403c",
            clusterBkg: "#292524",
            clusterBorder: "#57534e",
            titleColor: "#f5f0eb",
            edgeLabelBackground: "#292524",
            textColor: "#f5f0eb",
          },
        });
        if (!ref.current) return;
        ref.current.textContent = "";
        return m.default.render("mermaid-" + Math.random().toString(36).slice(2), chart);
      })
      .then((result) => {
        if (cancelled || !ref.current || !result) return;
        const clean = DOMPurify.sanitize(result.svg, {
          USE_PROFILES: { svg: true, svgFilters: true },
          ADD_TAGS: ["foreignObject"],
        });
        ref.current.innerHTML = clean;
      })
      .catch(() => {
        if (!cancelled) setError(true);
      });
    return () => { cancelled = true; };
  }, [chart]);

  if (error) return <p className="text-[11px] font-mono text-slate-text">Diagram could not be rendered</p>;
  return <div ref={ref} className="flex justify-center" />;
});

/* ── Sub-components ──────────────────────────── */

function TokenPill({ usage }: { usage: TokenUsage }) {
  const total = usage.total;
  const label = `${formatTokens(total)} tokens${total.cost != null ? ` · $${total.cost.toFixed(3)}` : ""}`;
  const stages: [string, { total_tokens: number; cost?: number; model?: string }][] = [];
  if (usage.triage?.total_tokens) stages.push(["triage", usage.triage]);
  if (usage.enrichment?.total_tokens) stages.push(["enrichment", usage.enrichment]);
  if (usage.conventions?.total_tokens) stages.push(["conventions", usage.conventions]);
  if (usage.patterns?.total_tokens) stages.push(["patterns", usage.patterns]);
  if (usage.review?.length) {
    const reviewTotal = usage.review.reduce((acc, r) => ({
      total_tokens: acc.total_tokens + r.total_tokens,
      cost: (acc.cost ?? 0) + (r.cost ?? 0),
      model: r.model,
    }), { total_tokens: 0, cost: 0, model: undefined as string | undefined });
    stages.push(["review", reviewTotal]);
  }
  if (usage.file_synthesis?.length) {
    const fsTotal = usage.file_synthesis.reduce((acc, r) => ({
      total_tokens: acc.total_tokens + r.total_tokens,
      cost: (acc.cost ?? 0) + (r.cost ?? 0),
      model: r.model,
    }), { total_tokens: 0, cost: 0, model: undefined as string | undefined });
    stages.push(["file_synthesis", fsTotal]);
  }
  if (usage.scoring?.total_tokens) stages.push(["scoring", usage.scoring]);
  if (usage.synthesis?.total_tokens) stages.push(["synthesis", usage.synthesis]);
  if (usage.graph?.total_tokens) stages.push(["graph", usage.graph]);

  const stageLabels: Record<string, string> = {
    triage: "Triage",
    enrichment: "Enrichment",
    conventions: "Conventions",
    patterns: "Patterns",
    review: "Review",
    file_synthesis: "File synthesis",
    scoring: "Scoring",
    synthesis: "Synthesis",
    graph: "Graph",
  };

  // Group model name — show once if all stages use the same model
  const models = stages.map(([, s]) => s.model).filter(Boolean);
  const uniqueModels = [...new Set(models)];
  const singleModel = uniqueModels.length === 1 ? uniqueModels[0] : null;

  return (
    <div className="group relative">
      <span className="inline-flex items-center border border-iron bg-iron/30 px-2.5 py-1 text-[11px] font-mono text-slate-text cursor-default">
        {label}
      </span>
      {stages.length > 0 && (
        <div className="absolute left-0 top-full mt-1.5 z-10 hidden group-hover:block">
          <div className="border border-iron bg-charcoal p-3 shadow-xl w-[240px]">
            {singleModel && (
              <p className="text-[10px] font-mono text-slate-text/60 mb-2 truncate">
                {singleModel}
              </p>
            )}
            <div className="space-y-1">
              {stages.map(([stage, s]) => (
                <div key={stage}>
                  <div className="flex items-center justify-between">
                    <span className="text-[11px] font-mono text-ash">
                      {stageLabels[stage] ?? stage}
                    </span>
                    <span className="text-[11px] font-mono text-foreground tabular-nums">
                      {formatTokens(s)}
                      {s.cost != null && s.cost > 0 && (
                        <span className="text-slate-text ml-1.5">${s.cost.toFixed(3)}</span>
                      )}
                    </span>
                  </div>
                  {!singleModel && s.model && (
                    <p className="text-[9px] font-mono text-slate-text/50 truncate">{s.model}</p>
                  )}
                </div>
              ))}
            </div>
            <div className="mt-2 pt-2 border-t border-iron flex items-center justify-between">
              <span className="text-[11px] font-mono text-amber">Total</span>
              <span className="text-[11px] font-mono text-foreground tabular-nums">
                {formatTokens(total)}
                {total.cost != null && (
                  <span className="text-amber ml-1.5">${total.cost.toFixed(3)}</span>
                )}
              </span>
            </div>
          </div>
        </div>
      )}
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

    navigator.clipboard.writeText(prompt).then(() => setCopied(true)).catch(() => {});
    setTimeout(() => setCopied(false), 2000);
  }, [comment, filePath, ref]);

  return (
    <button
      type="button"
      onClick={handleCopy}
      aria-label={`Copy fix prompt for ${filePath}${ref ? ` ${ref}` : ""} ${comment.severity ?? ""}`}
      className="inline-flex items-center gap-1.5 border border-iron/50 bg-iron/20 px-2.5 py-1 text-[11px] font-mono text-slate-text hover:text-amber hover:border-amber/30 hover:bg-amber/5 transition-[color,border-color,background-color] duration-150 opacity-100 md:opacity-0 md:group-hover:opacity-100 md:focus-visible:opacity-100 cursor-pointer"
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

function PatternDetail({ patternId }: { patternId: number }) {
  const { data: pattern, isLoading } = usePattern(patternId);
  if (isLoading) return <div className="mt-2 text-[11px] font-mono text-slate-text">Loading pattern...</div>;
  if (!pattern) return <div className="mt-2 text-[11px] font-mono text-slate-text">Pattern not found</div>;
  return (
    <div className="mt-2 border border-iron bg-iron/10 p-3">
      <p className="text-[11px] font-mono text-slate-text uppercase tracking-wider mb-1">Matched Pattern</p>
      <p className="text-xs font-mono text-foreground whitespace-pre-wrap">{pattern.content}</p>
      <div className="flex gap-4 mt-2 text-[11px] font-mono text-slate-text">
        {pattern.source && <span>Source: {pattern.source}</span>}
        {pattern.category && <span>Category: {pattern.category}</span>}
      </div>
    </div>
  );
}

function CommentCard({
  comment,
  filePath,
}: {
  comment: ReviewComment;
  filePath: string;
}) {
  const severityClass = comment.severity
    ? severityStyles[comment.severity] ?? "border-iron"
    : "border-iron";
  const [patternExpanded, setPatternExpanded] = useState(false);

  return (
    <div
      className={`group border ${severityClass} hover:bg-iron/15 transition-colors px-5 py-4 my-2`}
    >
      <div className="flex items-center gap-2 mb-3">
        {comment.severity && (
          <span
            className={`inline-flex items-center rounded-sm border px-2 py-0.5 text-[11px] font-mono uppercase tracking-wider font-medium ${severityStyles[comment.severity] ?? ""}`}
          >
            {comment.severity}
          </span>
        )}
        {comment.category && (
          <span className="inline-flex items-center rounded-sm border bg-iron/30 text-slate-text border-iron/60 px-2 py-0.5 text-[11px] font-mono">
            {comment.category}
          </span>
        )}
        {comment.specialist && (
          <span className="inline-flex items-center rounded-sm border bg-purple-400/10 text-purple-400 border-purple-400/30 px-2 py-0.5 text-[11px] font-mono">
            {comment.specialist}
          </span>
        )}
        {comment.confidence_score != null && (
          <span className="text-[11px] font-mono text-slate-text" title="Confidence score">
            {comment.confidence_score}%
          </span>
        )}
        {lineRef(comment) && (
          <span className="text-[11px] font-mono text-slate-text">
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
            <span className="inline-flex items-center gap-1 rounded border border-emerald-500/30 bg-emerald-500/10 px-2 py-0.5 text-[11px] font-mono text-emerald-400">
              New Finding
            </span>
          )}
          {comment.matched_pattern_id && comment.matched_pattern_score && (
            <div>
              <button
                onClick={(e) => {
                  e.stopPropagation();
                  setPatternExpanded((prev) => !prev);
                }}
                className="inline-flex items-center gap-1 rounded border border-amber/30 bg-amber/10 px-2 py-0.5 text-[11px] font-mono text-amber hover:bg-amber/20 transition-colors cursor-pointer"
              >
                Pattern Match ({Math.round(comment.matched_pattern_score * 100)}%)
                <ChevronDown className={`h-2.5 w-2.5 transition-transform ${patternExpanded ? "rotate-180" : ""}`} />
              </button>
              {patternExpanded && (
                <PatternDetail patternId={comment.matched_pattern_id} />
              )}
            </div>
          )}
          {comment.enforced_rule_content && (
            <span className="inline-flex items-center gap-1 rounded border border-purple-500/30 bg-purple-500/10 px-2 py-0.5 text-[11px] font-mono text-purple-400" title={comment.enforced_rule_content}>
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
  forceExpanded,
}: {
  filePath: string;
  fileComments: readonly ReviewComment[];
  id: string;
  hidden: boolean;
  forceExpanded?: boolean;
}) {
  const [expanded, setExpanded] = useState(true);

  useEffect(() => {
    if (forceExpanded !== undefined) setExpanded(forceExpanded);
  }, [forceExpanded]);
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
      className="scroll-mt-6 border border-iron overflow-hidden"
    >
      <button
        type="button"
        onClick={() => setExpanded(!expanded)}
        aria-expanded={expanded}
        aria-controls={contentId}
        className="flex items-center gap-2 w-full bg-charcoal px-4 py-3 border-b border-iron hover:bg-iron/20 transition-colors text-left cursor-pointer"
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
          <span className="text-[11px] font-mono text-slate-text">
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
      <p className="text-[11px] font-mono uppercase tracking-[0.15em] text-slate-text mb-3 px-2">
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
            <span className="ml-auto text-[11px] text-slate-text shrink-0">
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
            <p className="text-[11px] font-mono uppercase tracking-[0.15em] text-slate-text">
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
                  <span className="ml-auto text-slate-text">
                    {severityCounts[sev]}
                  </span>
                </button>
              ) : null,
          )}
          {activeSeverity && (
            <button
              type="button"
              onClick={() => onSeverityFilter(null)}
              className="flex items-center gap-1 w-full px-2 py-1 text-[11px] font-mono text-amber hover:underline cursor-pointer"
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
  const cancelReview = useCancelReview();
  const [showCancelModal, setShowCancelModal] = useState(false);
  const [activeSeverity, setActiveSeverity] = useState<string | null>(null);
  const [allExpanded, setAllExpanded] = useState(true);
  const [expandToggle, setExpandToggle] = useState(0);

  const review = data?.review;
  const comments = data?.comments ?? [];

  const isLive = review?.status === "pending" || review?.status === "in_progress";
  const { stage, triageResults, failedStage, timeline, liveTokens, seenStages } = useReviewStream(id, isLive);

  const filesReviewed = useMemo(() =>
    new Set(timeline.filter(e => e.type === "file").map(e => e.message)).size,
    [timeline]
  );
  const totalFiles = triageResults?.length ?? 0;

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

  const memoryStats = useMemo(() => {
    const newFindings = comments.filter(c => c.is_new_finding).length;
    const patternMatches = comments.filter(c => c.matched_pattern_id || (c.matched_pattern_score && c.matched_pattern_score > 0)).length;
    const rulesEnforced = comments.filter(c => c.enforced_rule_content).length;
    const memoryUsed = patternMatches + rulesEnforced;
    return { newFindings, patternMatches, rulesEnforced, memoryUsed, total: comments.length };
  }, [comments]);

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
  }, [grouped.length]);

  if (isLoading) {
    return (
      <div className="space-y-6 p-6">
        <div className="h-8 w-64 animate-pulse rounded bg-iron/20" />
        <div className="h-40 animate-pulse border border-iron bg-charcoal/40" />
        <div className="h-60 animate-pulse border border-iron bg-charcoal/40" />
        <div className="h-40 animate-pulse border border-iron bg-charcoal/40" />
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
        <span className="text-xs font-sans text-foreground/60 truncate max-w-[300px]" title={review.pr_title}>
          {review.pr_title}
        </span>
      </nav>

      {/* Header card */}
      <header className="border border-iron bg-charcoal p-6 mb-6">
        <div className="flex flex-col sm:flex-row sm:items-start sm:justify-between gap-4 sm:gap-6">
          <div className="flex-1 min-w-0">
            <h1 className="font-mono text-xl font-bold text-foreground mb-2 truncate" title={review.pr_title}>
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
                <span className="inline-flex items-center rounded-sm border bg-purple-400/10 text-purple-400 border-purple-400/30 px-2 py-0.5 text-[11px] font-mono">
                  Deep
                </span>
              )}
              {review.is_incremental && (
                <span className="inline-flex items-center rounded-sm border bg-cyan-400/10 text-cyan-400 border-cyan-400/30 px-2 py-0.5 text-[11px] font-mono">
                  Incremental
                </span>
              )}
              {review.persona && (
                <span className="inline-flex items-center rounded-sm border bg-iron/30 text-slate-text border-iron/60 px-2 py-0.5 text-[11px] font-mono">
                  {review.persona}
                </span>
              )}
            </div>
            <div className="flex items-center gap-3 mt-2.5 text-[11px] font-mono text-slate-text">
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
                className="inline-flex items-center gap-1.5 border border-iron px-3 py-2 text-xs font-mono text-slate-text hover:text-amber hover:border-amber/50 transition-colors"
              >
                <ExternalLink className="h-3.5 w-3.5" />
                GitHub
              </a>
            )}
            {(review.status === "failed" || review.status === "cancelled") && (
              <button
                type="button"
                onClick={() => retryReview.mutate(review.id)}
                disabled={retryReview.isPending}
                className="inline-flex items-center gap-1.5 border border-amber/30 bg-amber/10 px-3 py-2 text-xs font-mono text-amber hover:bg-amber/20 transition-colors cursor-pointer"
              >
                <RotateCcw
                  className={`h-3.5 w-3.5 ${retryReview.isPending ? "animate-spin" : ""}`}
                />
                {retryReview.isPending ? "Retrying\u2026" : "Retry"}
              </button>
            )}
            {isLive && (
              <button
                type="button"
                onClick={() => setShowCancelModal(true)}
                disabled={cancelReview.isPending}
                className="inline-flex items-center gap-1.5 border border-red-500/30 bg-red-500/10 px-3 py-2 text-xs font-mono text-red-400 hover:bg-red-500/20 transition-colors cursor-pointer"
              >
                <Square className="h-3 w-3 fill-current" />
                {cancelReview.isPending ? "Cancelling\u2026" : "Stop Review"}
              </button>
            )}
          </div>
        </div>
      </header>

      {/* Pipeline progress (live reviews) */}
      {isLive && <PipelineProgress stage={stage} failedStage={failedStage} filesReviewed={filesReviewed} totalFiles={totalFiles} seenStages={seenStages} />}

      {/* Activity timeline (live reviews) */}
      {isLive && timeline.length > 0 && (
        <ActivityTimeline
          timeline={timeline}
          liveTokens={liveTokens}
          stage={stage}
          startedAt={review.created_at}
        />
      )}

      {/* Triage results card */}
      {triageResults && isLive && (
        <div className="border border-iron bg-charcoal/80 p-5 mb-6">
          <h3 className="font-mono text-sm font-bold text-foreground mb-3">
            File Classification
          </h3>
          <div className="space-y-1">
            {triageResults.map((t) => (
              <div key={t.file} className="flex items-center gap-2 text-xs font-mono">
                <span
                  className={`inline-flex items-center rounded-sm border px-2 py-0.5 text-[11px] ${
                    t.action === "deep"
                      ? "bg-purple-400/10 text-purple-400 border-purple-400/30"
                      : t.action === "skip"
                        ? "bg-iron/30 text-slate-text border-iron/60"
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
      {review.status === "failed" && review.error && (() => {
        const err = friendlyError(review.error);
        return (
          <div
            className="border border-red-400/30 bg-red-400/5 p-5 mb-6"
            role="alert"
          >
            <div className="flex items-center gap-2 mb-2">
              <AlertTriangle className="h-4 w-4 text-red-400" />
              <h2 className="font-mono text-sm font-bold text-red-400">
                {err.title}
              </h2>
            </div>
            <p className="text-sm text-foreground/70 mb-2">
              {err.detail}
            </p>
            <p className="text-xs text-amber mb-4">
              {err.action}
            </p>
            <div className="flex items-center gap-3">
              <button
                type="button"
                onClick={() => retryReview.mutate(review.id)}
                disabled={retryReview.isPending}
                className="inline-flex items-center gap-1.5 border border-red-400/30 px-3 py-1.5 text-xs font-mono text-red-400 hover:bg-red-400/10 transition-colors cursor-pointer"
              >
                <RotateCcw
                  className={`h-3.5 w-3.5 ${retryReview.isPending ? "animate-spin" : ""}`}
                />
                {retryReview.isPending ? "Retrying\u2026" : "Retry"}
              </button>
            </div>
            <details className="mt-3">
              <summary className="text-xs text-foreground/40 cursor-pointer hover:text-foreground/60">
                Technical details
              </summary>
              <pre className="mt-2 font-mono text-xs text-red-400/60 whitespace-pre-wrap break-all">
                {review.error}
              </pre>
            </details>
          </div>
        );
      })()}

      {/* Summary card */}
      <div className="border border-iron bg-charcoal/80 p-6 mb-8">
        <h2 className="font-mono text-base font-bold text-foreground mb-4">
          Summary
        </h2>

        {/* Verdict — prefer the LLM-generated Brief (same text posted to the GitHub PR
            body). Fall back to extractSynthesis(summary) for legacy reviews without a
            brief column. If even extractSynthesis returns empty (legacy reviews where
            the summary is ALL file-dump with no prose), show the raw summary as-is
            so users see something instead of blank space. */}
        {(() => {
          if (review.brief && review.brief.trim()) {
            return <div className="mb-4"><Markdown>{review.brief}</Markdown></div>;
          }
          if (review.summary && review.summary.trim()) {
            const extracted = extractSynthesis(review.summary).trim();
            if (extracted) {
              return <div className="mb-4"><Markdown>{extracted}</Markdown></div>;
            }
            // Legacy review: brief column is NULL and the summary is pure file-dump.
            // Fall back to rendering the raw summary so users see the per-file findings
            // instead of an empty card.
            return <div className="mb-4"><Markdown>{review.summary}</Markdown></div>;
          }
          return <p className="text-sm text-foreground/50 mb-4">No summary generated.</p>;
        })()}

        {/* Top 3 critical/warning findings */}
        {(() => {
          const topFindings = [...comments]
            .filter((c) => c.severity === "critical" || c.severity === "warning")
            .sort((a, b) => {
              const sevOrder = { critical: 0, warning: 1 } as Record<string, number>;
              const diff = (sevOrder[a.severity ?? ""] ?? 2) - (sevOrder[b.severity ?? ""] ?? 2);
              if (diff !== 0) return diff;
              return (b.confidence_score ?? 0) - (a.confidence_score ?? 0);
            });
          const top3 = topFindings.slice(0, 3);
          const remaining = topFindings.length - 3;
          if (top3.length === 0) return null;
          return (
            <div className="mb-4 pb-4 border-b border-iron/40">
              {top3.map((c) => {
                const shortPath = c.file_path.split("/").slice(-2).join("/");
                const line = c.end_line ?? c.start_line;
                const sevColor = c.severity === "critical" ? "bg-red-400" : "bg-amber";
                const cleanBody = stripMarkdownForPreview(c.body);
                const body = cleanBody.length > 80 ? cleanBody.slice(0, 80) + "\u2026" : cleanBody;
                return (
                  <div key={c.id} className="flex items-start gap-2 py-1.5">
                    <div className={`h-1.5 w-1.5 rounded-full mt-1.5 shrink-0 ${sevColor}`} />
                    <span className="text-[11px] font-mono text-amber shrink-0">
                      {shortPath}{line != null ? `:${line}` : ""}
                    </span>
                    <span className="text-xs text-foreground">{body}</span>
                  </div>
                );
              })}
              {remaining > 0 && (
                <p className="text-[11px] font-mono text-slate-text mt-1 pl-3.5">
                  (+{remaining} more in detail below)
                </p>
              )}
            </div>
          );
        })()}

        {/* Stats row */}
        <div className="flex items-center gap-4 text-[11px] font-mono text-slate-text">
          <span>
            {comments.length} comment{comments.length !== 1 ? "s" : ""}
          </span>
          <span className="text-iron">·</span>
          <span>
            {grouped.length} file{grouped.length !== 1 ? "s" : ""}
          </span>
          {(["critical", "warning", "suggestion", "praise"] as const).map(
            (sev) =>
              severityCounts[sev] ? (
                <div key={sev} className="flex items-center gap-1.5">
                  <div className={`h-2 w-2 rounded-full ${severityDot[sev]}`} />
                  <span>{severityCounts[sev]} {sev}</span>
                </div>
              ) : null,
          )}
        </div>

        {/* Truncation notice */}
        {review.truncated_files && Array.isArray(review.truncated_files) && review.truncated_files.length > 0 && (
          <div className="mb-4 px-3 py-2 border border-amber/30 bg-amber/5 text-xs text-amber">
            Review for {review.truncated_files.map((f: string) => <code key={f}>{f}</code>).reduce<React.ReactNode[]>((prev, curr, i) => i > 0 ? [...prev, ", ", curr] : [curr], [])} was truncated — additional findings may exist.
          </div>
        )}

        {/* Mermaid diagrams */}
        {(() => {
          type DiagramItem = { type?: string; title?: string; mermaid: string };
          let diagrams: DiagramItem[] = [];
          if (review.diagrams && Array.isArray(review.diagrams)) {
            diagrams = review.diagrams;
          } else if (review.diagram) {
            diagrams = [{ type: "dependency", title: review.diagram_title || "Architecture", mermaid: review.diagram }];
          }
          if (diagrams.length === 0) return null;
          return (
            <div className="mt-4 pt-4 border-t border-iron/40 space-y-3">
              {diagrams.map((d, i) => (
                <details key={i} open={i === 0}>
                  <summary className="text-[11px] font-mono text-slate-text cursor-pointer hover:text-foreground transition-colors">
                    {d.title || capitalize(d.type || "Architecture")} diagram
                  </summary>
                  <div className="mt-3 border border-iron bg-void/50 p-4 overflow-x-auto">
                    <MermaidChart chart={d.mermaid} />
                  </div>
                </details>
              ))}
            </div>
          );
        })()}
      </div>

      {/* Intelligence card */}
      {review.status === "completed" && comments.length > 0 && (() => {
        const coverage = memoryStats.total > 0 ? memoryStats.memoryUsed / memoryStats.total : 0;
        const coveragePct = Math.round(coverage * 100);
        const barColor = coverage > 0.6 ? "bg-green-400" : coverage > 0.3 ? "bg-amber" : "bg-blue-400";
        const highCoverage = coverage > 0.6;
        const allNovel = memoryStats.memoryUsed === 0;

        return (
          <div className={`border bg-charcoal/80 p-5 mb-8 transition-colors ${highCoverage ? "border-green-400/20" : "border-iron"}`}>
            <div className="flex items-center gap-2 mb-4">
              <Zap className="h-3.5 w-3.5 text-amber" />
              <span className="text-[11px] font-mono uppercase tracking-wider text-slate-text">
                Intelligence
              </span>
            </div>

            <div className="grid grid-cols-2 sm:grid-cols-4 gap-3 mb-5">
              <div className="bg-iron/10 border border-iron/30 px-4 py-3 text-center hover:bg-iron/20 transition-colors">
                <div className="text-xl font-mono font-bold text-foreground">{memoryStats.total}</div>
                <div className="text-[11px] font-mono text-slate-text mt-0.5">findings total</div>
              </div>
              <div className="bg-iron/10 border border-iron/30 px-4 py-3 text-center hover:bg-iron/20 transition-colors">
                <div className="text-xl font-mono font-bold text-purple-400">{memoryStats.patternMatches}</div>
                <div className="text-[11px] font-mono text-slate-text mt-0.5">pattern matches</div>
              </div>
              <div className="bg-iron/10 border border-iron/30 px-4 py-3 text-center hover:bg-iron/20 transition-colors">
                <div className="text-xl font-mono font-bold text-amber">{memoryStats.rulesEnforced}</div>
                <div className="text-[11px] font-mono text-slate-text mt-0.5">rules enforced</div>
              </div>
              <div className="bg-iron/10 border border-iron/30 px-4 py-3 text-center hover:bg-iron/20 transition-colors">
                <div className="text-xl font-mono font-bold text-emerald-400">{memoryStats.newFindings}</div>
                <div className="text-[11px] font-mono text-slate-text mt-0.5">new findings</div>
              </div>
            </div>

            <div className="space-y-2">
              <div className="flex items-center gap-3">
                <span className="text-[11px] font-mono text-slate-text shrink-0">Memory coverage</span>
                <div className="flex-1 h-1.5 rounded-full bg-iron/20 overflow-hidden">
                  <div
                    className={`h-1.5 rounded-full ${barColor} transition-[width] duration-400`}
                    style={{ width: `${coveragePct}%` }}
                  />
                </div>
                <span className="text-[11px] font-mono text-slate-text shrink-0">{coveragePct}%</span>
              </div>
              <p className="text-[11px] font-mono text-slate-text">
                {allNovel
                  ? "All findings are novel \u2014 memory will improve with future reviews"
                  : `${memoryStats.memoryUsed} of ${memoryStats.total} findings informed by institutional memory`}
              </p>
            </div>
          </div>
        );
      })()}

      {/* Simulation Results */}
      {review.simulation_results && review.simulation_results.length > 0 && (
        <div className="border border-iron bg-charcoal/80 p-4 mb-6">
          <div className="mb-3 flex items-center gap-2">
            <span className="text-sm font-medium text-foreground">Simulation Results</span>
            <span className="rounded px-1.5 py-0.5 text-xs bg-charcoal text-slate-text">
              {review.simulation_results.length} scenario{review.simulation_results.length !== 1 ? 's' : ''}
            </span>
          </div>
          <div className="space-y-2">
            {review.simulation_results.map((result, i) => (
              <div key={`sim-${i}`} className="flex items-start gap-3 border border-iron bg-charcoal px-3 py-2">
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
            <div className="flex items-center gap-2">
              <button
                type="button"
                onClick={() => {
                  setAllExpanded((v) => !v);
                  setExpandToggle((v) => v + 1);
                }}
                className="text-[11px] font-mono text-slate-text hover:text-foreground transition-colors cursor-pointer"
              >
                {allExpanded ? (
                  <span className="inline-flex items-center gap-1"><ChevronUp className="h-3 w-3" />Collapse all</span>
                ) : (
                  <span className="inline-flex items-center gap-1"><ChevronDown className="h-3 w-3" />Expand all</span>
                )}
              </button>
              <span className="text-[11px] font-mono text-slate-text ml-auto">
                Showing {activeSeverity ? comments.filter(c => c.severity === activeSeverity).length : comments.length} of {comments.length}
              </span>
            </div>
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
                  forceExpanded={expandToggle > 0 ? allExpanded : undefined}
                />
              );
            })}
            {activeSeverity && visibleFiles.size === 0 && (
            <div className="border border-iron bg-charcoal p-10">
              <p className="font-mono text-sm text-slate-text">
                // No {activeSeverity} comments found.
              </p>
            </div>
            )}
          </div>
        ) : (
          review.status === "completed" && (
            <div className="border border-iron bg-charcoal p-10">
              <p className="font-mono text-sm text-slate-text">
                // No findings. Code looks good.
              </p>
            </div>
          )
        )}
      </div>
      {/* Cancel confirmation modal — only show if review is still live */}
      {showCancelModal && isLive && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm">
          <div className="bg-charcoal border border-iron p-6 max-w-sm w-full mx-4">
            <h3 className="font-mono text-sm font-bold text-foreground mb-2">Stop Review?</h3>
            <p className="text-xs text-slate-text mb-4">
              This will cancel the review. Cancelled reviews count toward your monthly limit.
            </p>
            <div className="flex gap-2 justify-end">
              <button
                type="button"
                onClick={() => setShowCancelModal(false)}
                className="px-3 py-1.5 text-xs font-mono text-slate-text border border-iron hover:bg-iron/50 transition-colors"
              >
                Keep Running
              </button>
              <button
                type="button"
                disabled={cancelReview.isPending}
                onClick={() => {
                  cancelReview.mutate(review.id, {
                    onSettled: () => setShowCancelModal(false),
                  });
                }}
                className="px-3 py-1.5 text-xs font-mono text-red-400 border border-red-500/30 bg-red-500/10 hover:bg-red-500/20 transition-colors disabled:opacity-50"
              >
                {cancelReview.isPending ? "Cancelling\u2026" : "Stop Review"}
              </button>
            </div>
          </div>
        </div>
      )}
    </>
  );
}
