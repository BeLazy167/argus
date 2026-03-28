"use client";

import { Check, X, Loader2 } from "lucide-react";
import type { PipelineStage } from "@/lib/hooks/use-review-stream";

const stages: { key: PipelineStage; label: string }[] = [
  { key: "pending", label: "Queued" },
  { key: "triaging", label: "Triage" },
  { key: "reviewing", label: "Review" },
  { key: "scoring", label: "Scoring" },
  { key: "synthesizing", label: "Synthesis" },
  { key: "posting", label: "Posting" },
  { key: "completed", label: "Done" },
];

const stageOrder: Record<string, number> = {
  pending: 0, triaging: 1, reviewing: 2, scoring: 3,
  pass2: 3, synthesizing: 4, posting: 5, completed: 6,
};

const labelColor: Record<string, string> = {
  active: "text-amber font-medium",
  completed: "text-green-400",
  failed: "text-red-400",
  pending: "text-slate-text",
};

function getStepState(
  stepKey: PipelineStage,
  currentStage: PipelineStage,
  failedStage?: string,
): "completed" | "active" | "pending" | "failed" {
  // On failure, use the actual failed stage position (not "failed" which isn't in the map)
  const effectiveStage = currentStage === "failed" ? (failedStage ?? "pending") : currentStage;
  const currentIdx = stageOrder[effectiveStage] ?? 0;
  const stepIdx = stageOrder[stepKey] ?? 0;
  if (stepIdx < currentIdx) return "completed";
  if (stepIdx > currentIdx) return "pending";
  return currentStage === "failed" ? "failed" : "active";
}

export function PipelineProgress({ stage, failedStage }: { stage: PipelineStage; failedStage?: string }) {
  return (
    <div className="rounded-lg border border-iron bg-charcoal/80 p-4 mb-6">
      <div className="flex items-center gap-1" role="progressbar" aria-label="Pipeline progress" aria-valuetext={stage}>
        {stages.map((step, i) => {
          const state = getStepState(step.key, stage, failedStage);
          return (
            <div key={step.key} className="flex items-center flex-1 last:flex-none">
              <div className="flex flex-col items-center gap-1.5">
                <StepIcon state={state} />
                <span className={`text-[11px] font-mono ${labelColor[state] ?? "text-slate-text"}`}>
                  {step.label}
                </span>
              </div>
              {i < stages.length - 1 && (
                <div
                  className={`h-px flex-1 mx-1.5 mt-[-18px] ${
                    state === "completed" ? "bg-green-400/40" : "bg-iron/30"
                  }`}
                />
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}

function StepIcon({ state }: { state: "completed" | "active" | "pending" | "failed" }) {
  const base = "h-5 w-5 rounded-full flex items-center justify-center";
  switch (state) {
    case "completed":
      return (
        <div className={`${base} bg-green-400/20 border border-green-400/40`}>
          <Check className="h-3 w-3 text-green-400" />
        </div>
      );
    case "active":
      return (
        <div className={`${base} bg-amber/20 border border-amber/40`}>
          <Loader2 className="h-3 w-3 text-amber animate-spin" />
        </div>
      );
    case "failed":
      return (
        <div className={`${base} bg-red-400/20 border border-red-400/40`}>
          <X className="h-3 w-3 text-red-400" />
        </div>
      );
    default:
      return (
        <div className={`${base} bg-iron/10 border border-iron/30`}>
          <div className="h-1.5 w-1.5 rounded-full bg-iron/40" />
        </div>
      );
  }
}
