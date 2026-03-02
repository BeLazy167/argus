import { useEffect, useState } from "react";
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

type SSEEvent = {
  type: string;
  data: Record<string, unknown>;
};

type ReviewCache = { review: Review; comments: ReviewComment[] };

/**
 * Connects to the review SSE stream and updates TanStack Query cache in real time.
 * Uses fetch() + ReadableStream (not EventSource) to support auth headers.
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

  useEffect(() => {
    if (!enabled || !reviewId || !active) return;

    const controller = new AbortController();

    (async () => {
      const token = await getToken();
      if (controller.signal.aborted) return;

      const res = await fetch(
        `${API_URL}/api/v1/reviews/${reviewId}/stream`,
        {
          headers: {
            Authorization: `Bearer ${token}`,
            "X-Installation-ID": String(active.id),
          },
          signal: controller.signal,
        },
      ).catch(() => null);

      if (!res?.ok || !res.body) return;
      setConnected(true);

      const reader = res.body.getReader();
      const decoder = new TextDecoder();
      let buffer = "";

      const queryKey = ["review", reviewId, active.id];

      const patchReview = (patch: Partial<Review>) => {
        qc.setQueryData(queryKey, (old: ReviewCache | undefined) => {
          if (!old) return old;
          return { ...old, review: { ...old.review, ...patch } };
        });
      };

      const processEvent = (evt: SSEEvent) => {
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

      try {
        while (true) {
          const { done, value } = await reader.read();
          if (done) break;

          buffer += decoder.decode(value, { stream: true });
          const frames = buffer.split("\n\n");
          buffer = frames.pop() ?? "";

          for (const frame of frames) {
            if (!frame.trim()) continue;
            let eventType = "";
            let data = "";
            for (const line of frame.split("\n")) {
              if (line.startsWith("event: ")) eventType = line.slice(7);
              else if (line.startsWith("data: ")) data = line.slice(6);
            }
            if (eventType && data) {
              try {
                processEvent({ type: eventType, data: JSON.parse(data) });
              } catch (e) {
                console.error("SSE parse error:", e);
              }
            }
          }
        }
      } catch {
        // stream ended or aborted — expected on unmount
      } finally {
        setConnected(false);
      }
    })();

    return () => controller.abort();
  }, [reviewId, enabled, active, getToken, qc]);

  return { stage, failedStage, triageResults, scoringUpdate, connected };
}

function mapStageToStatus(stage: string): Review["status"] {
  if (stage === "completed") return "completed";
  if (stage === "failed") return "failed";
  return "in_progress";
}
