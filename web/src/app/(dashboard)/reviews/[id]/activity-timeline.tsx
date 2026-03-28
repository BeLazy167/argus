"use client";

import { useEffect, useRef, useState } from "react";
import { FileCode, MessageSquare, Zap, Check, X, Loader2 } from "lucide-react";
import type { TimelineEntry, LiveTokens } from "@/lib/hooks/use-review-stream";

type Props = {
  timeline: TimelineEntry[];
  liveTokens: LiveTokens | null;
  stage: string;
  startedAt: string;
};

const dotColor: Record<string, string> = {
  stage: "bg-green-400",
  file: "bg-blue-400",
  comment: "bg-amber",
  scoring: "bg-purple-400",
  done: "bg-green-400",
  error: "bg-red-400",
};

const iconMap: Record<string, typeof FileCode> = {
  stage: Zap,
  file: FileCode,
  comment: MessageSquare,
  scoring: Zap,
  done: Check,
  error: X,
};

function formatElapsed(seconds: number): string {
  return `${Math.floor(seconds / 60)}:${(seconds % 60).toString().padStart(2, "0")}`;
}

function formatTokenCount(tokens: number): string {
  return tokens >= 1000 ? `${(tokens / 1000).toFixed(1)}k` : String(tokens);
}

function relativeTimestamp(entry: Date, start: Date): string {
  const diff = Math.max(0, Math.floor((entry.getTime() - start.getTime()) / 1000));
  return formatElapsed(diff);
}

const COLLAPSED_THRESHOLD = 8;

export function ActivityTimeline({ timeline, liveTokens, stage, startedAt }: Props) {
  const scrollRef = useRef<HTMLDivElement>(null);
  const bottomRef = useRef<HTMLDivElement>(null);
  const [elapsed, setElapsed] = useState(0);
  const [showAll, setShowAll] = useState(false);

  const startDate = new Date(startedAt);
  const isActive = stage !== "completed" && stage !== "failed";

  useEffect(() => {
    const start = startDate.getTime();
    const interval = setInterval(() => setElapsed(Math.floor((Date.now() - start) / 1000)), 1000);
    return () => clearInterval(interval);
  }, [startedAt]);

  useEffect(() => {
    const container = scrollRef.current;
    if (!container) return;
    const atBottom = container.scrollHeight - container.scrollTop - container.clientHeight < 60;
    if (atBottom) {
      bottomRef.current?.scrollIntoView({ behavior: "smooth" });
    }
  }, [timeline.length]);

  const collapsed = !showAll && timeline.length > COLLAPSED_THRESHOLD;
  const visibleEntries = collapsed ? timeline.slice(-COLLAPSED_THRESHOLD) : timeline;
  const hiddenCount = collapsed ? timeline.length - COLLAPSED_THRESHOLD : 0;

  return (
    <div className="rounded-lg border border-iron bg-charcoal/80 p-5 mb-8">
      {/* Header */}
      <div className="flex items-center justify-between mb-4">
        <span className="text-[11px] font-mono uppercase tracking-wider text-slate-text">
          Activity
        </span>
        <span className="inline-flex items-center rounded-md border border-amber/30 bg-amber/10 px-2 py-0.5 text-[11px] font-mono text-amber">
          {isActive && <Loader2 className="h-3 w-3 mr-1.5 animate-spin" />}
          {formatElapsed(elapsed)}
        </span>
      </div>

      {/* Scrollable timeline */}
      <div ref={scrollRef} className="max-h-[400px] overflow-y-auto">
        {hiddenCount > 0 && (
          <button
            type="button"
            onClick={() => setShowAll(true)}
            className="text-[11px] font-mono text-amber hover:underline mb-2 cursor-pointer"
          >
            Show {hiddenCount} earlier
          </button>
        )}

        <div className="space-y-0">
          {visibleEntries.map((entry, i) => {
            const isLast = i === visibleEntries.length - 1;
            const Icon = iconMap[entry.icon] ?? Zap;
            return (
              <div key={entry.id} className="flex gap-3">
                {/* Left rail: dot + connecting line */}
                <div className="flex flex-col items-center w-4 shrink-0">
                  <div className="relative flex items-center justify-center h-5">
                    <div
                      className={`h-2 w-2 rounded-full ${dotColor[entry.icon] ?? "bg-iron"} ${
                        isLast && isActive ? "animate-pulse" : ""
                      }`}
                    />
                  </div>
                  {!isLast && (
                    <div className="flex-1 border-l border-iron/40" />
                  )}
                </div>

                {/* Content */}
                <div className="pb-3 min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <Icon className="h-3 w-3 text-slate-text shrink-0" />
                    <span className="text-[11px] font-mono text-slate-text shrink-0">
                      {relativeTimestamp(entry.timestamp, startDate)}
                    </span>
                    <span className="text-sm text-foreground truncate">
                      {entry.message}
                    </span>
                  </div>
                  {entry.detail && (
                    <div className="ml-5 mt-0.5">
                      <span className="text-[11px] font-mono text-slate-text">
                        {entry.detail}
                      </span>
                    </div>
                  )}
                </div>
              </div>
            );
          })}
        </div>
        <div ref={bottomRef} />
      </div>

      {/* Token ticker */}
      {liveTokens && (
        <div className="mt-3 pt-3 border-t border-iron/40">
          <span className="text-[11px] font-mono text-slate-text">
            {formatTokenCount(liveTokens.total_tokens)} tokens &middot; ${liveTokens.cost.toFixed(3)}
          </span>
        </div>
      )}
    </div>
  );
}
