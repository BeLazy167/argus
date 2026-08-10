"use client";

import { useCallback, useState } from "react";
import dynamic from "next/dynamic";
import { Check, ChevronDown, Copy } from "lucide-react";
import { track } from "@/lib/analytics";
import type { ReviewComment } from "@/lib/types";
import { lineRef, severityStyles } from "./severity";
import { PatternDetail } from "./pattern-detail";

const Markdown = dynamic(() => import("../markdown").then(m => ({ default: m.Markdown })), { ssr: false });

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
    track("review.copy_fix_prompt", { severity: comment.severity, category: comment.category, file_path: filePath });
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

export function CommentCard({
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
      <div className="ph-mask">
        <Markdown filePath={filePath}>{comment.body}</Markdown>
      </div>
    </div>
  );
}
