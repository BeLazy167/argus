"use client";

import { useEffect, useState } from "react";
import { Loader2, Save, Sliders } from "lucide-react";
import { useFeatureFlags, useSaveFeatureFlags, type FeatureFlags } from "@/lib/queries/features";
import { track } from "@/lib/analytics";

const defaultFlags: FeatureFlags = {
  issue_acceptance: true,
  cross_pr_checks: false,
  max_linked_prs: 5,
};

export default function FeaturesPage() {
  const { data, isLoading } = useFeatureFlags();
  const save = useSaveFeatureFlags();
  const [draft, setDraft] = useState<FeatureFlags>(defaultFlags);
  const [dirty, setDirty] = useState(false);

  useEffect(() => {
    if (data) {
      setDraft(data);
      setDirty(false);
    }
  }, [data]);

  const update = (patch: Partial<FeatureFlags>) => {
    setDraft(prev => ({ ...prev, ...patch }));
    setDirty(true);
  };

  const onSave = async () => {
    await save.mutateAsync(draft);
    setDirty(false);
  };

  return (
    <div className="min-h-screen bg-[#0a0a12] text-slate-200">
      <div className="max-w-3xl mx-auto p-8 space-y-8">
        <header>
          <div className="flex items-center gap-2 mb-2">
            <Sliders className="h-5 w-5 text-slate-500" />
            <h1 className="text-2xl font-mono text-slate-100">Features</h1>
          </div>
          <p className="text-sm text-slate-500 font-mono">
            Per-installation toggles for optional review passes. Changes apply to the next review.
          </p>
        </header>

        {isLoading ? (
          <div className="flex items-center gap-2 text-slate-500 font-mono text-sm">
            <Loader2 className="h-4 w-4 animate-spin" />
            Loading features...
          </div>
        ) : (
          <div className="space-y-4">
            <FeatureToggle
              label="Issue acceptance check"
              description="Verify PRs against linked issue acceptance criteria. Argus uses GitHub's closingIssuesReferences to find linked issues (works with both Closes #N in the PR body and the Development UI panel), extracts criteria from the issue body, and judges the diff per-criterion."
              cost="~1 extra LLM call per linked issue"
              enabled={draft.issue_acceptance}
              onChange={v => {
                track("settings.toggle_changed", {
                  setting_key: "issue_acceptance",
                  new_value: v,
                });
                update({ issue_acceptance: v });
              }}
            />

            <FeatureToggle
              label="Cross-repo PR checks"
              description="Runs asynchronously after review completion. Probes 9 combination-failure categories (schema race, serialization drift, deploy ordering, security posture, enum exhaustiveness, and more) against each linked PR's diff + prior findings. Splits into: (a) cross-PR risk judge, (b) joint issue coverage when 2+ linked PRs share an issue. Sibling completion triggers a debounced refresh so late-arriving PRs update earlier ones. Inaccessible repos are noted as 'partial coverage' — severity unaffected."
              cost="1–5 LLM calls per review depending on linked-PR + shared-issue count. Bounded by per-install rate limit (30/hour) and per-PR refresh cap (2 per 10 min)."
              enabled={draft.cross_pr_checks}
              onChange={v => {
                track("settings.toggle_changed", {
                  setting_key: "cross_pr_checks",
                  new_value: v,
                });
                update({ cross_pr_checks: v });
              }}
            />

            <div className="border border-iron bg-charcoal/60 p-4">
              <label className="block text-sm font-mono text-slate-200 mb-1">
                Max linked PRs per review
              </label>
              <p className="text-xs font-mono text-slate-500 mb-3">
                How many linked PRs the cross-repo worker will fetch and consider. Bounded 1–20.
              </p>
              <input
                type="number"
                min={1}
                max={20}
                value={draft.max_linked_prs}
                onChange={e => {
                  const n = parseInt(e.target.value, 10);
                  if (!isNaN(n)) update({ max_linked_prs: Math.max(1, Math.min(20, n)) });
                }}
                className="w-24 bg-[#0a0a12] border border-iron px-3 py-2 text-sm font-mono text-slate-200 focus:border-slate-500 focus:outline-none"
              />
            </div>

            <div className="flex items-center justify-between pt-4">
              <p className="text-xs font-mono text-slate-600">
                {dirty ? "Unsaved changes" : save.isSuccess ? "Saved" : ""}
              </p>
              <button
                onClick={onSave}
                disabled={!dirty || save.isPending}
                className="flex items-center gap-2 px-4 py-2 bg-slate-800 border border-iron text-sm font-mono text-slate-200 hover:bg-slate-700 disabled:opacity-40 disabled:cursor-not-allowed transition-colors"
              >
                {save.isPending ? (
                  <Loader2 className="h-4 w-4 animate-spin" />
                ) : (
                  <Save className="h-4 w-4" />
                )}
                Save
              </button>
            </div>

            {save.isError && (
              <p className="text-xs font-mono text-red-400">
                Failed to save — check connection and retry.
              </p>
            )}
          </div>
        )}
      </div>
    </div>
  );
}

function FeatureToggle({
  label,
  description,
  cost,
  enabled,
  onChange,
}: {
  label: string;
  description: string;
  cost: string;
  enabled: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <div className="border border-iron bg-charcoal/60 p-4">
      <div className="flex items-start justify-between gap-4">
        <div className="flex-1">
          <h3 className="text-sm font-mono text-slate-200 mb-1">{label}</h3>
          <p className="text-xs font-mono text-slate-500 leading-relaxed mb-2">{description}</p>
          <p className="text-[10px] font-mono text-slate-600 uppercase tracking-wide">{cost}</p>
        </div>
        <button
          onClick={() => onChange(!enabled)}
          aria-pressed={enabled}
          role="switch"
          className={`relative h-6 w-11 shrink-0 transition-colors ${
            enabled ? "bg-amber" : "bg-iron"
          }`}
        >
          <span
            className={`absolute top-0.5 h-5 w-5 bg-slate-100 transition-transform ${
              enabled ? "translate-x-5" : "translate-x-0.5"
            }`}
          />
        </button>
      </div>
    </div>
  );
}
