"use client";

import { useEffect, useState } from "react";
import dynamic from "next/dynamic";
import { ChevronDown, ChevronRight, FileCode, MessageSquare } from "lucide-react";
import type { ReviewComment } from "@/lib/types";
import { langFromPath } from "./format";
import { severityDot } from "./severity";
import { CommentCard } from "./comment-card";

const CodeSnippet = dynamic(() => import("../markdown").then(m => ({ default: m.CodeSnippet })), { ssr: false });

export function FileGroup({
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
