import { useEffect, useRef, useState } from "react";
import { useAuth } from "@clerk/nextjs";
import { useQueryClient } from "@tanstack/react-query";
import { useInstallation } from "@/providers/installation-provider";
import type { Review, ReviewComment } from "../types";

const API_URL = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8080";

export type PipelineStage =
  | "pending"
  | "triaging"
  | "reviewing"
  | "scoring"
  | "pass2"
  | "synthesizing"
  | "posting"
  | "completed"
  | "failed";

export type TriageFile = {
  file: string;
  action: string;
  reason: string;
};

export type ScoringUpdate = {
  kept: number;
  dropped: number;
  threshold: number;
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
  const backoffRef = useRef(1000);

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

    const processEvent = (evt: WSEvent) => {
      switch (evt.type) {
        case "stage_changed":
          setStage(evt.data.stage as PipelineStage);
          patchReview({ status: mapStageToStatus(evt.data.stage as string) });
          break;

        case "triage_complete":
          setTriageResults(evt.data.files as TriageFile[]);
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
          break;

        case "scoring_update":
          setScoringUpdate({
            kept: evt.data.kept as number,
            dropped: evt.data.dropped as number,
            threshold: evt.data.threshold as number,
          });
          break;

        case "synthesis":
          patchReview({
            summary: evt.data.summary as string,
            score: evt.data.score as number,
          });
          break;

        case "completed":
          qc.invalidateQueries({ queryKey: ["review", reviewId] });
          qc.invalidateQueries({ queryKey: ["reviews"] });
          setStage("completed");
          break;

        case "error":
          qc.invalidateQueries({ queryKey: ["review", reviewId] });
          qc.invalidateQueries({ queryKey: ["reviews"] });
          setFailedStage(evt.data.stage as string);
          setStage("failed");
          break;
      }
    };

    const connect = async () => {
      if (unmounted) return;
      const token = await getToken();
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
        // onclose will fire after this — reconnect handled there
      };
    };

    connect();

    return () => {
      unmounted = true;
      clearTimeout(reconnectTimer);
      if (ws) ws.close(1000, "unmount");
    };
  }, [reviewId, enabled, active, getToken, qc]);

  return { stage, failedStage, triageResults, scoringUpdate, connected };
}

function mapStageToStatus(stage: string): Review["status"] {
  if (stage === "completed") return "completed";
  if (stage === "failed") return "failed";
  return "in_progress";
}
