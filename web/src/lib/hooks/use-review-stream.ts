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
  | "failed";

type TriageFile = {
  file: string;
  action: string;
  reason: string;
};

type ScoringUpdate = {
  kept: number;
  dropped: number;
  threshold: number;
};

export type TimelineEntry = {
  id: string;
  type: string;
  timestamp: Date;
  message: string;
  detail?: string;
  icon: "stage" | "file" | "comment" | "scoring" | "done" | "error";
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
  const getTokenRef = useRef(getToken);
  useEffect(() => { getTokenRef.current = getToken; }, [getToken]);

  useEffect(() => {
    if (!enabled || !reviewId || !active) return;

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

        case "scoring_update":
          setScoringUpdate({
            kept: evt.data.kept as number,
            dropped: evt.data.dropped as number,
            threshold: evt.data.threshold as number,
          });
          addEntry({ type: "scoring", message: `Kept ${evt.data.kept}, dropped ${evt.data.dropped}`, detail: `threshold: ${evt.data.threshold}`, icon: "scoring" });
          break;

        case "token_update":
          setLiveTokens({ total_tokens: evt.data.total_tokens as number, cost: evt.data.cost as number });
          break;

        case "synthesis":
          patchReview({
            summary: evt.data.summary as string,
            score: evt.data.score as number,
          });
          addEntry({ type: "done", message: `Review complete \u2014 score ${evt.data.score}/10`, icon: "done" });
          break;

        case "completed":
          qc.invalidateQueries({ queryKey: ["review", reviewId] });
          qc.invalidateQueries({ queryKey: ["reviews"] });
          setStage("completed");
          addEntry({ type: "done", message: "Posted to GitHub", icon: "done" });
          break;

        case "error":
          qc.invalidateQueries({ queryKey: ["review", reviewId] });
          qc.invalidateQueries({ queryKey: ["reviews"] });
          setFailedStage(evt.data.stage as string);
          setStage("failed");
          addEntry({ type: "error", message: `Failed at ${evt.data.stage}: ${evt.data.error}`, icon: "error" });
          break;
      }
    };

    const connect = async () => {
      if (unmounted) return;
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
