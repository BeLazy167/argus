import { useEffect, useRef, useState } from "react";
import { useAuth } from "@clerk/nextjs";
import { useQueryClient } from "@tanstack/react-query";
import { useInstallation } from "@/providers/installation-provider";
import type { Review, ReviewComment } from "../types";

const API_URL = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8080";

export type PipelineStage =
  | "pending"
  | "triaging"
  | "briefing"
  | "reviewing"
  | "deduping"
  | "validating"
  | "scoring"
  | "pass2"
  | "synthesizing"
  | "posting"
  | "completed"
  | "failed"
  | "cancelled";

type TriageFile = {
  file: string;
  action: string;
  reason: string;
};

type ScoringUpdate = {
  kept: number;
  dropped: number;
  /** Severity-tiered cutoffs the scorer applied. Backend emits this as
   *  `thresholds: { critical, warning, suggestion }` — the older `threshold`
   *  singular field was never populated, so activity feeds rendered
   *  "threshold: undefined". */
  thresholds?: { critical?: number; warning?: number; suggestion?: number };
};

export type TimelineEntry = {
  id: string;
  type: string;
  timestamp: Date;
  message: string;
  detail?: string;
  icon:
    | "stage"
    | "file"
    | "comment"
    | "scoring"
    | "done"
    | "error"
    | "memory"
    | "validation"
    | "simulation"
    | "brief"
    | "reply";
};

export type LiveTokens = {
  total_tokens: number;
  cost: number;
};

type WSEvent = {
  type: string;
  data: Record<string, unknown>;
};

type ReviewCache = { review: Review; comments: ReviewComment[] };

/**
 * Connects to the review WebSocket stream and updates TanStack Query cache in real time.
 * Reconnects with exponential backoff on unexpected disconnections.
 */
export function useReviewStream(reviewId: string, enabled: boolean) {
  const { getToken } = useAuth();
  const { active } = useInstallation();
  const qc = useQueryClient();

  const [stage, setStage] = useState<PipelineStage>("pending");
  const [failedStage, setFailedStage] = useState<string | undefined>();
  const [triageResults, setTriageResults] = useState<TriageFile[] | null>(null);
  const [scoringUpdate, setScoringUpdate] = useState<ScoringUpdate | null>(null);
  const [connected, setConnected] = useState(false);
  const [timeline, setTimeline] = useState<TimelineEntry[]>([]);
  const [liveTokens, setLiveTokens] = useState<LiveTokens | null>(null);
  const seenStagesRef = useRef<Set<PipelineStage>>(new Set());
  const backoffRef = useRef(1000);
  // terminalRef captures "the review reached a terminal state during this session"
  // so the reconnect loop stops even when outer state (e.g. `active` from
  // useInstallation) produces a new reference and re-runs the effect. Without
  // this, each re-run spins up a fresh WebSocket that the server immediately
  // closes with 1000 ("review already completed"), flooding the console.
  const terminalRef = useRef(false);
  // synthesisSeenRef dedupes the `synthesis` timeline row. `synthesis` fires
  // mid-pipeline (before `completed`), so `terminalRef` doesn't cover it. If
  // the socket drops between synthesis and completed and the reconnect replays
  // history, synthesis would otherwise add a second "Review complete" row.
  const synthesisSeenRef = useRef(false);
  const getTokenRef = useRef(getToken);
  useEffect(() => { getTokenRef.current = getToken; }, [getToken]);

  // Reset refs when the target review changes — a fresh reviewId means
  // a new session that needs its own WebSocket, even if the previous session
  // completed on the same component instance.
  useEffect(() => {
    terminalRef.current = false;
    synthesisSeenRef.current = false;
  }, [reviewId]);

  useEffect(() => {
    if (!enabled || !reviewId || !active) return;
    if (terminalRef.current) return;

    let ws: WebSocket | null = null;
    let unmounted = false;
    let reconnectTimer: ReturnType<typeof setTimeout>;

    const queryKey = ["review", reviewId, active.id];

    const patchReview = (patch: Partial<Review>) => {
      qc.setQueryData(queryKey, (old: ReviewCache | undefined) => {
        if (!old) return old;
        return { ...old, review: { ...old.review, ...patch } };
      });
    };

    const addEntry = (entry: Omit<TimelineEntry, "id" | "timestamp">) => {
      setTimeline(prev => [...prev, { ...entry, id: crypto.randomUUID(), timestamp: new Date() }]);
    };

    const processEvent = (evt: WSEvent) => {
      switch (evt.type) {
        case "stage_changed":
          setStage(evt.data.stage as PipelineStage);
          seenStagesRef.current.add(evt.data.stage as PipelineStage);
          patchReview({ status: mapStageToStatus(evt.data.stage as string) });
          addEntry({ type: "stage", message: stageMessage(evt.data.stage as string), icon: "stage" });
          break;

        case "triage_complete":
          setTriageResults(evt.data.files as TriageFile[]);
          addEntry({ type: "triage", message: `Classified ${(evt.data.files as TriageFile[]).length} files`, detail: summarizeTriage(evt.data.files as TriageFile[]), icon: "stage" });
          break;

        case "file_review_started":
          addEntry({ type: "file", message: `Reviewing ${shortPath(evt.data.file_path as string)}`, detail: [evt.data.specialist, evt.data.action].filter(Boolean).join(" \u00b7 ") || undefined, icon: "file" });
          break;

        case "comment":
          qc.setQueryData(queryKey, (old: ReviewCache | undefined) => {
            if (!old) return old;
            const comment: ReviewComment = {
              id: crypto.randomUUID(),
              review_id: reviewId,
              file_path: evt.data.file_path as string,
              end_line: evt.data.line as number,
              body: evt.data.body as string,
              severity: evt.data.severity as ReviewComment["severity"],
              category: evt.data.category as string,
              specialist: evt.data.specialist as string,
              created_at: new Date().toISOString(),
            };
            return { ...old, comments: [...old.comments, comment] };
          });
          addEntry({ type: "comment", message: truncate(evt.data.body as string, 60), detail: `${evt.data.severity} \u00b7 ${shortPath(evt.data.file_path as string)}:${evt.data.line}`, icon: "comment" });
          break;

        case "scoring_update": {
          const thresholds = evt.data.thresholds as ScoringUpdate["thresholds"];
          setScoringUpdate({
            kept: evt.data.kept as number,
            dropped: evt.data.dropped as number,
            thresholds,
          });
          const detail =
            thresholds && (thresholds.critical != null || thresholds.warning != null || thresholds.suggestion != null)
              ? `cutoff c:${thresholds.critical ?? "\u2014"} \u00b7 w:${thresholds.warning ?? "\u2014"} \u00b7 s:${thresholds.suggestion ?? "\u2014"}`
              : undefined;
          addEntry({
            type: "scoring",
            message: `Kept ${evt.data.kept}, dropped ${evt.data.dropped}`,
            detail,
            icon: "scoring",
          });
          break;
        }

        case "token_update":
          setLiveTokens({ total_tokens: evt.data.total_tokens as number, cost: evt.data.cost as number });
          break;

        case "synthesis":
          // patchReview is idempotent on re-delivery (it merges fields); the
          // guard only dedupes the visible timeline row.
          patchReview({
            summary: evt.data.summary as string,
            score: evt.data.score as number,
          });
          if (synthesisSeenRef.current) break;
          addEntry({ type: "done", message: `Review complete \u2014 score ${evt.data.score}/10`, icon: "done" });
          synthesisSeenRef.current = true;
          break;

        // Terminal handlers: invalidation runs BEFORE the dedupe guard so
        // a replayed terminal event still refreshes caches (e.g. replica-lag
        // left stale data on the first delivery). Only the visible row add
        // and the stage mutation are gated behind terminalRef.
        case "completed":
          qc.invalidateQueries({ queryKey: ["review", reviewId] });
          qc.invalidateQueries({ queryKey: ["reviews"] });
          if (terminalRef.current) break;
          setStage("completed");
          addEntry({ type: "done", message: "Posted to GitHub", icon: "done" });
          terminalRef.current = true;
          break;

        case "cancelled":
          qc.invalidateQueries({ queryKey: ["review", reviewId] });
          qc.invalidateQueries({ queryKey: ["reviews"] });
          if (terminalRef.current) break;
          setStage("cancelled");
          addEntry({ type: "stage", message: `Cancelled at ${evt.data.stage}`, icon: "error" });
          terminalRef.current = true;
          break;

        case "error":
          qc.invalidateQueries({ queryKey: ["review", reviewId] });
          qc.invalidateQueries({ queryKey: ["reviews"] });
          if (terminalRef.current) break;
          setFailedStage(evt.data.stage as string);
          setStage("failed");
          addEntry({ type: "error", message: `Failed at ${evt.data.stage}: ${evt.data.error}`, icon: "error" });
          terminalRef.current = true;
          break;

        // Per-sub-step events — backend emits one per distinct LLM call / memory
        // upsert / GitHub API action. These live on the timeline as sub-rows of
        // the coarser stage they run inside, so the user can see progress without
        // the stage transition being a black box.
        case "intent_extracted": {
          const goal = typeof evt.data.goal === "string" ? evt.data.goal : undefined;
          addEntry({
            type: "intent",
            message: "Intent extracted",
            detail: goal ? truncate(goal, 80) : undefined,
            icon: "brief",
          });
          break;
        }
        case "intent_verified": {
          const delivers = evt.data.delivers === true;
          const unmet = typeof evt.data.unmet === "number" ? evt.data.unmet : 0;
          addEntry({
            type: "intent",
            message: delivers ? "Intent verified: delivers" : "Intent verified: does not deliver",
            detail: unmet > 0 ? `${unmet} unmet` : undefined,
            icon: "brief",
          });
          break;
        }
        case "findings_enriched": {
          const count = typeof evt.data.count === "number" ? evt.data.count : undefined;
          addEntry({
            type: "enrichment",
            message: "Findings enriched",
            detail: count != null ? `${count} matched` : undefined,
            icon: "stage",
          });
          break;
        }
        case "brief_generated": {
          const length = typeof evt.data.length === "number" ? evt.data.length : undefined;
          addEntry({
            type: "brief",
            message: "Brief generated",
            detail: length != null ? `${length} chars` : undefined,
            icon: "brief",
          });
          break;
        }
        case "lead_brief":
          addEntry({ type: "lead", message: "Lead brief drafted", icon: "brief" });
          break;
        case "lead_broadcast": {
          const specialists = Array.isArray(evt.data.specialists) ? evt.data.specialists.length : undefined;
          addEntry({
            type: "lead",
            message: "Lead broadcast",
            detail: specialists != null ? `${specialists} specialists` : undefined,
            icon: "stage",
          });
          break;
        }
        case "second_pass": {
          const files = typeof evt.data.files === "number" ? evt.data.files : undefined;
          addEntry({
            type: "pass2",
            message: "Second pass",
            detail: files != null ? `${files} files` : undefined,
            icon: "stage",
          });
          break;
        }
        case "blast_radius": {
          const affected = typeof evt.data.affected === "number" ? evt.data.affected : undefined;
          addEntry({
            type: "blast",
            message: "Blast radius analyzed",
            detail: affected != null ? `${affected} affected` : undefined,
            icon: "validation",
          });
          break;
        }
        case "lead_cross_check": {
          const matched = typeof evt.data.matched === "number" ? evt.data.matched : undefined;
          addEntry({
            type: "lead",
            message: "Lead cross-check",
            detail: matched != null ? `${matched} matches` : undefined,
            icon: "validation",
          });
          break;
        }
        case "acceptance_checked": {
          const accepted = typeof evt.data.accepted === "number" ? evt.data.accepted : undefined;
          const rejected = typeof evt.data.rejected === "number" ? evt.data.rejected : undefined;
          const detail =
            accepted != null || rejected != null
              ? `accepted ${accepted ?? 0} · rejected ${rejected ?? 0}`
              : undefined;
          addEntry({ type: "acceptance", message: "Acceptance checked", detail, icon: "validation" });
          break;
        }
        case "cross_pr_checked": {
          const incompat =
            typeof evt.data.incompatibilities === "number" ? evt.data.incompatibilities : undefined;
          addEntry({
            type: "cross_pr",
            message: "Cross-PR checked",
            detail: incompat != null ? `${incompat} incompatibilities` : undefined,
            icon: "validation",
          });
          break;
        }
        case "simulations_complete": {
          const total = typeof evt.data.total === "number" ? evt.data.total : undefined;
          const passed = typeof evt.data.passed === "number" ? evt.data.passed : undefined;
          const detail = total != null ? `${passed ?? 0}/${total} passed` : undefined;
          addEntry({ type: "simulation", message: "Simulations complete", detail, icon: "simulation" });
          break;
        }
        case "scenario_simulated": {
          const verdict = typeof evt.data.verdict === "string" ? evt.data.verdict : undefined;
          const id = typeof evt.data.scenario_id === "number" ? evt.data.scenario_id : undefined;
          const detail = [id != null ? `#${id}` : null, verdict].filter(Boolean).join(" \u00b7 ") || undefined;
          addEntry({ type: "simulation", message: "Scenario simulated", detail, icon: "simulation" });
          break;
        }
        case "memory_indexed": {
          const kind = typeof evt.data.kind === "string" ? evt.data.kind : undefined;
          const success = evt.data.success !== false;
          addEntry({
            type: "memory",
            message: kind ? `Memory indexed (${kind})` : "Memory indexed",
            detail: success ? undefined : "failed",
            icon: "memory",
          });
          break;
        }
        case "posted_to_github": {
          // Mid-stage signal: GitHub POST succeeded. The terminal "completed"
          // event fires later after memory indexing, backfill, and pattern
          // learning finish, so phrase this one distinctly to avoid two
          // indistinguishable "Posted to GitHub" rows on the timeline.
          const inline = typeof evt.data.inline === "number" ? evt.data.inline : undefined;
          const folded = typeof evt.data.folded === "number" ? evt.data.folded : undefined;
          const detail =
            inline != null || folded != null
              ? `${inline ?? 0} inline \u00b7 ${folded ?? 0} folded`
              : undefined;
          addEntry({
            type: "done",
            message: "Posted to GitHub (post-processing continues)",
            detail,
            icon: "done",
          });
          break;
        }
        case "reply_generated": {
          const length = typeof evt.data.length === "number" ? evt.data.length : undefined;
          addEntry({
            type: "reply",
            message: "Reply generated",
            detail: length != null ? `${length} chars` : undefined,
            icon: "reply",
          });
          break;
        }
        case "memory_matched": {
          // Fired by enrichFindings when a finding matches a Supermemory-backed
          // pattern / convention / rule / similarity hit. Detail line carries
          // kind + source PR so authors can trace the attribution shown on the
          // inline comment body.
          const kind = typeof evt.data.kind === "string" ? evt.data.kind : undefined;
          const pr = typeof evt.data.pr === "number" && evt.data.pr > 0 ? evt.data.pr : undefined;
          const parts: string[] = [];
          if (kind) parts.push(kind);
          if (pr != null) parts.push(`PR #${pr}`);
          addEntry({
            type: "memory",
            message: "Memory match",
            detail: parts.length > 0 ? parts.join(" \u00b7 ") : undefined,
            icon: "memory",
          });
          break;
        }
      }
    };

    const connect = async () => {
      if (unmounted) return;
      // Terminal-state short-circuit. The effect-setup guard only fires on
      // effect re-runs; this path reaches `connect` via `ws.onclose →
      // setTimeout(connect, …)` which bypasses the effect, so we must
      // re-check here. Otherwise a rewritten close code (Fly proxy flips
      // 1000 → 1006) opens a fresh socket on a completed review and the
      // server replays terminal events forever.
      if (terminalRef.current) return;
      const token = await getTokenRef.current();
      if (unmounted || !token) return;

      const wsBase = API_URL.replace(/^http/, "ws");
      const url = `${wsBase}/api/v1/reviews/${reviewId}/stream?token=${encodeURIComponent(token)}&installation_id=${active.id}`;

      ws = new WebSocket(url);

      ws.onopen = () => {
        setConnected(true);
        backoffRef.current = 1000;
      };

      ws.onmessage = (msg) => {
        try {
          const evt: WSEvent = JSON.parse(msg.data);
          processEvent(evt);
        } catch (e) {
          console.error("WS parse error:", e);
        }
      };

      ws.onclose = (e) => {
        setConnected(false);
        if (unmounted) return;
        // Terminal-state short-circuit. Required because the Fly WS proxy
        // can rewrite `StatusNormalClosure` (1000) to 1006, making the
        // 1000-check below unreliable in production. Without this, a
        // completed review keeps reopening sockets in a tight loop.
        if (terminalRef.current) return;
        // Don't reconnect on clean close (server sent terminal event)
        if (e.code === 1000) return;
        // Exponential backoff: 1s → 2s → 4s → 8s → 16s max
        reconnectTimer = setTimeout(connect, backoffRef.current);
        backoffRef.current = Math.min(backoffRef.current * 2, 16000);
      };

      ws.onerror = () => {
        console.error("WebSocket error");
      };
    };

    connect().catch(() => {});

    return () => {
      unmounted = true;
      clearTimeout(reconnectTimer);
      if (ws) ws.close(1000, "unmount");
    };
  }, [reviewId, enabled, active, qc]);

  return { stage, failedStage, triageResults, scoringUpdate, connected, timeline, liveTokens, seenStages: seenStagesRef.current };
}

function mapStageToStatus(stage: string): Review["status"] {
  if (stage === "completed") return "completed";
  if (stage === "failed") return "failed";
  if (stage === "cancelled") return "cancelled";
  return "in_progress";
}

function stageMessage(stage: string): string {
  const map: Record<string, string> = {
    triaging: "Triaging files...",
    briefing: "Building lead brief...",
    reviewing: "Starting file reviews...",
    deduping: "Deduplicating findings...",
    validating: "Validating (SAST + blast + acceptance + cross-PR)...",
    scoring: "Scoring comments...",
    pass2: "Re-reviewing hot files...",
    synthesizing: "Generating synthesis...",
    posting: "Posting to GitHub...",
  };
  return map[stage] ?? `Stage: ${stage}`;
}

function summarizeTriage(files: TriageFile[]): string {
  const counts: Record<string, number> = {};
  for (const f of files) {
    counts[f.action] = (counts[f.action] ?? 0) + 1;
  }
  return Object.entries(counts).map(([k, v]) => `${v} ${k}`).join(" \u00b7 ");
}

function shortPath(path: string): string {
  const parts = path.split("/");
  return parts.length <= 2 ? path : parts.slice(-2).join("/");
}

function truncate(str: string, max: number): string {
  if (str.length <= max) return str;
  return str.slice(0, max - 1) + "\u2026";
}
