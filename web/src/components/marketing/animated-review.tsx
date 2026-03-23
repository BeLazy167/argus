"use client";

import { useEffect, useRef, useState } from "react";

interface ReviewComment {
  line: number;
  severity: "critical" | "warning" | "suggestion";
  text: string;
  delay: number;
}

const DIFF_LINES = [
  { type: "context", num: 42, content: "  const session = await getSession(req);" },
  { type: "removed", num: 43, content: "  if (!session) return res.status(401).end();" },
  { type: "added", num: 43, content: "  if (!session) return res.json({ ok: false });" },
  { type: "added", num: 44, content: "  const user = session.user;" },
  { type: "context", num: 45, content: "  const data = await db.query(req.body.sql);" },
  { type: "added", num: 46, content: '  cache.set(`user_${user.id}`, data, 0);' },
  { type: "context", num: 47, content: "  return res.json({ data });" },
] as const;

const REVIEW_COMMENTS: ReviewComment[] = [
  {
    line: 2,
    severity: "warning",
    text: "Returning 200 with { ok: false } instead of 401 — downstream clients may mishandle unauthenticated state.",
    delay: 1800,
  },
  {
    line: 4,
    severity: "critical",
    text: "SQL injection: req.body.sql passed directly to db.query(). Use parameterized queries.",
    delay: 3600,
  },
  {
    line: 5,
    severity: "suggestion",
    text: "TTL of 0 means infinite cache. Stale user data will persist until restart.",
    delay: 5400,
  },
];

const SEVERITY_STYLES = {
  critical: {
    badge: "bg-red-500/20 text-red-400 border-red-500/30",
    label: "critical",
    line: "border-red-500/40",
  },
  warning: {
    badge: "bg-yellow-500/20 text-yellow-400 border-yellow-500/30",
    label: "warning",
    line: "border-yellow-500/40",
  },
  suggestion: {
    badge: "bg-blue-500/20 text-blue-400 border-blue-500/30",
    label: "suggestion",
    line: "border-blue-500/40",
  },
};

export function AnimatedReview() {
  const [, forceRender] = useState(0);
  const [hasStarted, setHasStarted] = useState(false);
  const containerRef = useRef<HTMLDivElement>(null);
  const animFrameRef = useRef<number>(0);
  const startTimeRef = useRef<number>(0);
  const visibleRef = useRef<Set<number>>(new Set());
  const typedRef = useRef<Record<number, number>>({});

  useEffect(() => {
    const observer = new IntersectionObserver(
      (entries) => {
        const entry = entries[0];
        if (entry && entry.isIntersecting) {
          setHasStarted(true);
          observer.disconnect();
        }
      },
      { threshold: 0.3 }
    );

    const el = containerRef.current;
    if (el) observer.observe(el);
    return () => observer.disconnect();
  }, []);

  useEffect(() => {
    if (!hasStarted) return;

    startTimeRef.current = performance.now();

    const tick = (now: number) => {
      const elapsed = now - startTimeRef.current;
      let changed = false;

      REVIEW_COMMENTS.forEach((comment, i) => {
        if (elapsed >= comment.delay && !visibleRef.current.has(i)) {
          visibleRef.current.add(i);
          changed = true;
        }

        if (elapsed >= comment.delay) {
          const typeElapsed = elapsed - comment.delay;
          const charsToShow = Math.min(
            Math.floor(typeElapsed / 20),
            comment.text.length
          );
          if ((typedRef.current[i] ?? 0) !== charsToShow) {
            typedRef.current[i] = charsToShow;
            changed = true;
          }
        }
      });

      if (changed) forceRender((n) => n + 1);

      const allDone = REVIEW_COMMENTS.every(
        (c, i) =>
          visibleRef.current.has(i) &&
          (typedRef.current[i] ?? 0) >= c.text.length
      );

      if (!allDone) {
        animFrameRef.current = requestAnimationFrame(tick);
      }
    };

    animFrameRef.current = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(animFrameRef.current);
  }, [hasStarted]);

  return (
    <div ref={containerRef} className="w-full max-w-2xl mx-auto">
      {/* File header */}
      <div className="flex items-center gap-2 rounded-t-lg border border-iron bg-charcoal px-4 py-2.5">
        <div className="flex gap-1.5">
          <div className="h-2.5 w-2.5 rounded-full bg-iron" />
          <div className="h-2.5 w-2.5 rounded-full bg-iron" />
          <div className="h-2.5 w-2.5 rounded-full bg-iron" />
        </div>
        <span className="ml-2 text-[11px] font-mono text-slate-text">
          api/routes/user.ts
        </span>
        <span className="ml-auto text-[10px] font-mono text-slate-text/60">
          +3 -1
        </span>
      </div>

      {/* Diff content */}
      <div className="border-x border-b border-iron rounded-b-lg overflow-hidden bg-void">
        {DIFF_LINES.map((line, i) => {
          const commentForLine = REVIEW_COMMENTS.find(
            (c) => c.line === i && visibleRef.current.has(REVIEW_COMMENTS.indexOf(c))
          );
          const commentIndex = commentForLine
            ? REVIEW_COMMENTS.indexOf(commentForLine)
            : -1;

          return (
            <div key={i}>
              <div
                className={`flex items-center text-[12px] font-mono leading-6 ${
                  line.type === "added"
                    ? "bg-emerald-500/[0.07]"
                    : line.type === "removed"
                      ? "bg-red-500/[0.07]"
                      : ""
                }`}
              >
                {/* Line number */}
                <span className="w-10 shrink-0 select-none text-right pr-3 text-slate-text/40 text-[11px]">
                  {line.num}
                </span>
                {/* +/- indicator */}
                <span
                  className={`w-5 shrink-0 select-none text-center text-[11px] ${
                    line.type === "added"
                      ? "text-emerald-400"
                      : line.type === "removed"
                        ? "text-red-400"
                        : "text-transparent"
                  }`}
                >
                  {line.type === "added" ? "+" : line.type === "removed" ? "-" : " "}
                </span>
                {/* Code */}
                <code className="text-ash/90 whitespace-pre">{line.content}</code>
              </div>

              {/* Review comment */}
              {commentForLine && (
                <div
                  className={`mx-2 my-1.5 rounded-md border ${SEVERITY_STYLES[commentForLine.severity].line} bg-charcoal/80 overflow-hidden animate-[reviewSlideIn_0.3s_ease-out_forwards]`}
                >
                  <div className="flex items-center gap-2 px-3 py-1.5 border-b border-iron/50">
                    <div className="h-4 w-4 rounded-full bg-amber/20 flex items-center justify-center">
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
                    <span className="text-[11px] font-mono font-medium text-amber">
                      argus
                    </span>
                    <span
                      className={`text-[9px] font-mono uppercase tracking-wider px-1.5 py-0.5 rounded border ${SEVERITY_STYLES[commentForLine.severity].badge}`}
                    >
                      {SEVERITY_STYLES[commentForLine.severity].label}
                    </span>
                  </div>
                  <div className="px-3 py-2">
                    <p className="text-[11px] font-mono text-ash/80 leading-relaxed">
                      {commentForLine.text.slice(0, typedRef.current[commentIndex] ?? 0)}
                      {(typedRef.current[commentIndex] ?? 0) < commentForLine.text.length && (
                        <span className="inline-block w-[5px] h-[13px] bg-amber/70 ml-px animate-[cursorBlink_0.8s_step-end_infinite]" />
                      )}
                    </p>
                  </div>
                </div>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}
