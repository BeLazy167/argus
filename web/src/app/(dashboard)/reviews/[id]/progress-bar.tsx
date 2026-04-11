"use client";

import { Check, X, Loader2 } from "lucide-react";
import type { PipelineStage } from "@/lib/hooks/use-review-stream";

const stages: { key: PipelineStage; label: string; optional?: boolean }[] = [
  { key: "pending",       label: "Queued" },
  { key: "triaging",      label: "Triage" },
  { key: "briefing",      label: "Brief",    optional: true },
  { key: "reviewing",     label: "Review" },
  { key: "deduping",      label: "Dedup",    optional: true },
  { key: "validating",    label: "Validate", optional: true },
  { key: "scoring",       label: "Scoring" },
  { key: "pass2",         label: "Pass 2",   optional: true },
  { key: "synthesizing",  label: "Synthesis" },
  { key: "posting",       label: "Posting" },
  { key: "completed",     label: "Done" },
];

const stageOrder: Record<string, number> = {
  pending: 0, triaging: 1, briefing: 2, reviewing: 3, deduping: 4,
  validating: 5, scoring: 6, pass2: 7, synthesizing: 8, posting: 9, completed: 10,
};

const labelColor: Record<string, string> = {
  active: "text-amber font-medium",
  completed: "text-green-400",
  failed: "text-red-400",
  pending: "text-slate-text",
  skipped: "text-slate-600",
};

function getStepState(
  stepKey: PipelineStage,
  stepIsOptional: boolean,
  currentStage: PipelineStage,
  seenStages: Set<string>,
  failedStage?: string,
): "completed" | "active" | "pending" | "failed" | "skipped" {
  // On failure, use the actual failed stage position (not "failed" which isn't in the map)
  const effectiveStage = currentStage === "failed" ? (failedStage ?? "pending") : currentStage;
  const currentIdx = stageOrder[effectiveStage] ?? -1;
  const stepIdx = stageOrder[stepKey] ?? 0;

  // Unknown current stage: hold position at highest seen stage
  if (currentIdx === -1) {
    const maxSeen = Math.max(-1, ...Array.from(seenStages).map(s => stageOrder[s] ?? -1));
    if (stepIdx <= maxSeen) return "completed";
    return "pending";
  }

  if (stepIdx < currentIdx) {
    if (stepIsOptional && !seenStages.has(stepKey)) return "skipped";
    return "completed";
  }
  if (stepIdx > currentIdx) return "pending";
  return currentStage === "failed" ? "failed" : "active";
}

export function PipelineProgress({ stage, failedStage, filesReviewed, totalFiles, seenStages }: { stage: PipelineStage; failedStage?: string; filesReviewed?: number; totalFiles?: number; seenStages?: Set<string> }) {
  const seen = seenStages ?? new Set<string>();
  return (
    <div className="border border-iron bg-charcoal/80 p-4 mb-6">
      <div className="flex items-center gap-1" role="progressbar" aria-label="Pipeline progress" aria-valuetext={stage}>
        {stages.map((step, i) => {
          const state = getStepState(step.key, !!step.optional, stage, seen, failedStage);
          // Compact mode: hide labels for non-active/non-failed steps to fit 11 stages.
          // Keep label visible for active and failed; hide via sr-only otherwise.
          const labelVisible = state === "active" || state === "failed";
          return (
            <div key={step.key} className="flex items-center flex-1 last:flex-none" title={step.label}>
              <div className="flex flex-col items-center gap-1.5">
                <StepIcon state={state} />
                <span className={`text-xs font-mono ${labelColor[state] ?? "text-slate-text"} ${labelVisible ? "" : "sr-only"}`}>
                  {step.label}
                  {step.key === "reviewing" && stage === "reviewing" && filesReviewed != null && totalFiles != null && (
                    <span className="text-xs font-mono text-amber ml-1">{filesReviewed}/{totalFiles}</span>
                  )}
                </span>
              </div>
              {i < stages.length - 1 && (
                <div
                  className={`h-0.5 flex-1 mx-1.5 ${
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

function StepIcon({ state }: { state: "completed" | "active" | "pending" | "failed" | "skipped" }) {
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
    case "skipped":
      return (
        <div className={`${base} bg-iron/5 border border-iron/20`}>
          <div className="h-1 w-1 rounded-full bg-iron/30" />
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
