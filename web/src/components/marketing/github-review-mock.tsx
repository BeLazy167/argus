"use client";

import { useEffect, useRef, useState } from "react";

interface ReviewItem {
  file: string;
  lineRange: string;
  severity: "critical" | "warning" | "suggestion";
  code: string;
  comment: string;
}

const REVIEWS: ReviewItem[] = [
  {
    file: "lib/auth/session.ts",
    lineRange: "L24-L28",
    severity: "critical",
    code: `- const token = req.headers.authorization;
+ const token = req.headers.authorization?.split(" ")[1];
  const decoded = jwt.verify(token, SECRET);`,
    comment:
      "Raw header includes 'Bearer ' prefix. jwt.verify will fail silently on malformed tokens and return the header string as the decoded payload. This is a privilege escalation vector.",
  },
  {
    file: "services/billing.ts",
    lineRange: "L112",
    severity: "warning",
    code: `  await stripe.subscriptions.del(sub.id);
+ await db.subscriptions.update({ id: sub.id, status: "cancelled" });`,
    comment:
      "Stripe cancellation succeeds but if the DB update fails, the user is cancelled in Stripe but active in your system. Wrap in a transaction or add a reconciliation check.",
  },
  {
    file: "utils/cache.ts",
    lineRange: "L7-L9",
    severity: "suggestion",
    code: `  export function memoize<T>(fn: () => T): () => T {
    let cached: T | undefined;
    return () => cached ?? (cached = fn());`,
    comment:
      "This memoize implementation holds references indefinitely. For server-side usage, consider a WeakRef or TTL to avoid memory leaks across long-running requests.",
  },
];

const SEVERITY_CONFIG = {
  critical: {
    badge: "bg-red-500/15 text-red-400 border-red-500/25",
    icon: (
      <svg viewBox="0 0 16 16" className="h-3.5 w-3.5 text-red-400" fill="currentColor">
        <path d="M8 1.5a6.5 6.5 0 100 13 6.5 6.5 0 000-13zM0 8a8 8 0 1116 0A8 8 0 010 8zm9-3a1 1 0 00-2 0v4a1 1 0 002 0V5zm-1 7.5a1 1 0 100-2 1 1 0 000 2z" />
      </svg>
    ),
  },
  warning: {
    badge: "bg-yellow-500/15 text-yellow-400 border-yellow-500/25",
    icon: (
      <svg viewBox="0 0 16 16" className="h-3.5 w-3.5 text-yellow-400" fill="currentColor">
        <path d="M6.457 1.047c.659-1.234 2.427-1.234 3.086 0l6.082 11.378A1.75 1.75 0 0114.082 15H1.918a1.75 1.75 0 01-1.543-2.575L6.457 1.047zM8 5a.75.75 0 00-.75.75v2.5a.75.75 0 001.5 0v-2.5A.75.75 0 008 5zm1 6a1 1 0 11-2 0 1 1 0 012 0z" />
      </svg>
    ),
  },
  suggestion: {
    badge: "bg-blue-500/15 text-blue-400 border-blue-500/25",
    icon: (
      <svg viewBox="0 0 16 16" className="h-3.5 w-3.5 text-blue-400" fill="currentColor">
        <path d="M8 1.5a6.5 6.5 0 100 13 6.5 6.5 0 000-13zM0 8a8 8 0 1116 0A8 8 0 010 8zm6.5-.25A.75.75 0 017.25 7h1a.75.75 0 01.75.75v2.75h.25a.75.75 0 010 1.5h-2a.75.75 0 010-1.5h.25v-2h-.25a.75.75 0 01-.75-.75zM8 6a1 1 0 100-2 1 1 0 000 2z" />
      </svg>
    ),
  },
};

export function GitHubReviewMock() {
  const [visibleReviews, setVisibleReviews] = useState<number[]>([]);
  const sectionRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const observer = new IntersectionObserver(
      (entries) => {
        const entry = entries[0];
        if (entry && entry.isIntersecting) {
          REVIEWS.forEach((_, i) => {
            setTimeout(() => {
              setVisibleReviews((prev) =>
                prev.includes(i) ? prev : [...prev, i]
              );
            }, i * 300);
          });
          observer.disconnect();
        }
      },
      { threshold: 0.3 }
    );

    const el = sectionRef.current;
    if (el) observer.observe(el);
    return () => observer.disconnect();
  }, []);

  return (
    <div ref={sectionRef} className="w-full max-w-3xl mx-auto">
      {/* PR Header */}
      <div className="rounded-t-lg border border-iron bg-charcoal px-5 py-4">
        <div className="flex items-center gap-3 mb-2">
          <span className="inline-flex items-center gap-1.5 rounded-full bg-emerald-500/15 border border-emerald-500/25 px-2.5 py-0.5 text-[10px] font-mono text-emerald-400">
            <span className="h-1.5 w-1.5 rounded-full bg-emerald-400" />
            Open
          </span>
          <h3 className="font-display text-sm font-bold text-foreground">
            feat: add subscription billing &amp; session refresh
          </h3>
        </div>
        <div className="flex items-center gap-3 text-[11px] font-mono text-slate-text">
          <span className="flex items-center gap-1.5">
            <span className="h-4 w-4 rounded-full bg-iron" />
            sarah-dev
          </span>
          <span className="text-iron">wants to merge</span>
          <code className="rounded bg-iron/60 px-1.5 py-0.5 text-[10px] text-amber">
            feat/billing
          </code>
          <span className="text-iron">&rarr;</span>
          <code className="rounded bg-iron/60 px-1.5 py-0.5 text-[10px] text-ash">
            main
          </code>
        </div>
      </div>

      {/* Review summary bar */}
      <div className="border-x border-iron bg-void/50 px-5 py-3 flex items-center justify-between">
        <div className="flex items-center gap-2">
          <div className="h-5 w-5 rounded-full bg-amber/20 flex items-center justify-center">
            <svg viewBox="0 0 120 60" className="h-3 w-3 text-amber" fill="none">
              <path
                d="M10 30C10 30 30 8 60 8C90 8 110 30 110 30C110 30 90 52 60 52C30 52 10 30 10 30Z"
                stroke="currentColor"
                strokeWidth="8"
                fill="none"
              />
              <circle cx="60" cy="30" r="10" fill="currentColor" />
            </svg>
          </div>
          <span className="text-[11px] font-mono font-medium text-amber">
            argus
          </span>
          <span className="text-[11px] font-mono text-slate-text">
            reviewed 2 minutes ago
          </span>
        </div>
        <div className="flex items-center gap-3">
          <span className="text-[10px] font-mono text-red-400">1 critical</span>
          <span className="text-[10px] font-mono text-yellow-400">1 warning</span>
          <span className="text-[10px] font-mono text-blue-400">1 suggestion</span>
        </div>
      </div>

      {/* Review comments */}
      <div className="border-x border-b border-iron rounded-b-lg overflow-hidden divide-y divide-iron">
        {REVIEWS.map((review, i) => {
          const config = SEVERITY_CONFIG[review.severity];
          const isVisible = visibleReviews.includes(i);

          return (
            <div
              key={i}
              className="transition-[opacity,transform] duration-400 ease-out"
              style={{
                opacity: isVisible ? 1 : 0,
                transform: isVisible ? "translateY(0)" : "translateY(6px)",
              }}
            >
              {/* File path header */}
              <div className="flex items-center gap-2 bg-charcoal/60 px-4 py-2 border-b border-iron/50">
                <svg viewBox="0 0 16 16" className="h-3.5 w-3.5 text-slate-text" fill="currentColor">
                  <path d="M2 1.75C2 .784 2.784 0 3.75 0h6.586c.464 0 .909.184 1.237.513l2.914 2.914c.329.328.513.773.513 1.237v9.586A1.75 1.75 0 0113.25 16h-9.5A1.75 1.75 0 012 14.25V1.75z" />
                </svg>
                <span className="text-[11px] font-mono text-ash">{review.file}</span>
                <span className="text-[10px] font-mono text-slate-text/50">{review.lineRange}</span>
              </div>

              {/* Code snippet */}
              <div className="bg-void px-0 py-0">
                {review.code.split("\n").map((line, li) => (
                  <div
                    key={li}
                    className={`flex text-[11px] font-mono leading-6 px-4 ${
                      line.startsWith("+")
                        ? "bg-emerald-500/[0.06]"
                        : line.startsWith("-")
                          ? "bg-red-500/[0.06]"
                          : ""
                    }`}
                  >
                    <span
                      className={`w-4 shrink-0 select-none ${
                        line.startsWith("+")
                          ? "text-emerald-400/70"
                          : line.startsWith("-")
                            ? "text-red-400/70"
                            : "text-transparent"
                      }`}
                    >
                      {line.charAt(0)}
                    </span>
                    <code className="text-ash/80 whitespace-pre">{line.slice(1)}</code>
                  </div>
                ))}
              </div>

              {/* Comment */}
              <div className="bg-charcoal/40 px-4 py-3">
                <div className="flex items-center gap-2 mb-2">
                  <div className="h-4 w-4 rounded-full bg-amber/20 flex items-center justify-center shrink-0">
                    <svg viewBox="0 0 120 60" className="h-2.5 w-2.5 text-amber" fill="none">
                      <path
                        d="M10 30C10 30 30 8 60 8C90 8 110 30 110 30C110 30 90 52 60 52C30 52 10 30 10 30Z"
                        stroke="currentColor"
                        strokeWidth="8"
                        fill="none"
                      />
                      <circle cx="60" cy="30" r="10" fill="currentColor" />
                    </svg>
                  </div>
                  <span className="text-[11px] font-mono font-medium text-amber">argus</span>
                  <span className={`text-[9px] font-mono uppercase tracking-wider px-1.5 py-0.5 rounded border ${config.badge}`}>
                    {review.severity === "critical" ? "\uD83D\uDD34 blocker" : review.severity === "warning" ? "\uD83D\uDFE1 should fix" : "\uD83D\uDCA1 suggestion"}
                  </span>
                </div>
                <div className="text-[11px] font-mono text-ash/75 leading-relaxed pl-6 space-y-1.5">
                  {review.comment.split("\n\n").map((block, bi) => (
                    <p key={bi}>
                      {block.startsWith("**What:**") ? (
                        <>
                          {block.replace("**What:** ", "")}
                        </>
                      ) : block.startsWith("**Why:**") ? (
                        <>
                          {block.replace("**Why:** ", "")}
                        </>
                      ) : (
                        block
                      )}
                    </p>
                  ))}
                </div>
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}
