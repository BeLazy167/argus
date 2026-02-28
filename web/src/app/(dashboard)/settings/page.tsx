"use client";

import { useState } from "react";
import { Settings, ChevronDown, Loader2, Save } from "lucide-react";
import { useRepos } from "@/lib/queries/repos";
import {
  useModelConfigs,
  useUpsertModelConfig,
  useDeleteModelConfig,
} from "@/lib/queries/model-configs";

const STAGES = ["triage", "review", "synthesis", "embedding"] as const;

const STAGE_DESCRIPTIONS: Record<string, string> = {
  triage: "Decides which files need detailed review vs. can be skimmed",
  review: "Analyzes code changes and writes review comments",
  synthesis: "Combines per-file reviews into a unified summary",
  embedding: "Generates embeddings for memory and pattern matching",
};

function ConfigCard({
  stage,
  repoId,
  existing,
}: {
  stage: string;
  repoId: number;
  existing?: { provider: string; model: string; base_url?: string; max_tokens: number; temperature: number };
}) {
  const [provider, setProvider] = useState(existing?.provider ?? "");
  const [model, setModel] = useState(existing?.model ?? "");
  const [maxTokens, setMaxTokens] = useState(existing?.max_tokens ?? 4096);
  const [temperature, setTemperature] = useState(existing?.temperature ?? 0.2);

  const upsert = useUpsertModelConfig();
  const del = useDeleteModelConfig();

  const handleSave = () => {
    if (!model) return;
    upsert.mutate({ repoId, stage, provider, model, max_tokens: maxTokens, temperature });
  };

  return (
    <div className="rounded-lg border border-iron bg-charcoal p-5">
      <div className="flex items-center justify-between mb-1">
        <div className="flex items-center gap-2">
          <span className="text-xs font-mono uppercase tracking-wider text-amber">
            {stage}
          </span>
          {existing ? (
            <span className="inline-flex items-center rounded-sm border border-amber/20 bg-amber/10 px-1.5 py-0.5 text-[9px] font-mono uppercase tracking-wider text-amber">
              Custom
            </span>
          ) : (
            <span className="inline-flex items-center rounded-sm border border-iron bg-iron/50 px-1.5 py-0.5 text-[9px] font-mono uppercase tracking-wider text-slate-text">
              Default
            </span>
          )}
        </div>
        {existing && (
          <button
            type="button"
            onClick={() => del.mutate({ repoId, stage })}
            className="text-[11px] font-mono text-slate-text hover:text-red-400 transition-colors"
          >
            Reset to default
          </button>
        )}
      </div>
      <p className="text-[11px] font-mono text-slate-text mb-4">
        {STAGE_DESCRIPTIONS[stage]}
      </p>
      <div className="grid grid-cols-2 gap-3 mb-3">
        <div>
          <label className="block text-[10px] font-mono text-slate-text mb-1">
            Provider
          </label>
          <input
            type="text"
            value={provider}
            onChange={(e) => setProvider(e.target.value)}
            placeholder="Using default"
            className="w-full rounded border border-iron bg-background px-2 py-1.5 text-xs font-mono text-foreground placeholder:text-iron focus:border-amber focus:outline-none"
          />
        </div>
        <div>
          <label className="block text-[10px] font-mono text-slate-text mb-1">
            Model
          </label>
          <input
            type="text"
            value={model}
            onChange={(e) => setModel(e.target.value)}
            placeholder="Using default model"
            className="w-full rounded border border-iron bg-background px-2 py-1.5 text-xs font-mono text-foreground placeholder:text-iron focus:border-amber focus:outline-none"
          />
        </div>
        <div>
          <label className="block text-[10px] font-mono text-slate-text mb-1">
            Max tokens
          </label>
          <input
            type="number"
            value={maxTokens}
            onChange={(e) => setMaxTokens(Number(e.target.value))}
            className="w-full rounded border border-iron bg-background px-2 py-1.5 text-xs font-mono text-foreground focus:border-amber focus:outline-none"
          />
        </div>
        <div>
          <label className="block text-[10px] font-mono text-slate-text mb-1">
            Temperature
          </label>
          <input
            type="number"
            step="0.1"
            min="0"
            max="2"
            value={temperature}
            onChange={(e) => setTemperature(Number(e.target.value))}
            className="w-full rounded border border-iron bg-background px-2 py-1.5 text-xs font-mono text-foreground focus:border-amber focus:outline-none"
          />
        </div>
      </div>
      <button
        type="button"
        onClick={handleSave}
        disabled={upsert.isPending || !model}
        className="flex items-center gap-2 rounded border border-amber/30 bg-amber/10 px-3 py-1 text-[11px] font-mono text-amber hover:bg-amber/20 transition-colors disabled:opacity-50"
      >
        <Save className="h-3 w-3" />
        {upsert.isPending ? "Saving..." : "Save"}
      </button>
      <p className="text-[10px] font-mono text-iron mt-3">
        Set provider to &quot;openai&quot;, &quot;anthropic&quot;, etc. to use your own API key.
      </p>
    </div>
  );
}

export default function SettingsPage() {
  const { data: repos, isLoading: reposLoading } = useRepos();
  const [selectedRepoId, setSelectedRepoId] = useState<number>(0);

  const firstRepoId = repos?.[0]?.id ?? 0;
  const activeRepoId = selectedRepoId || firstRepoId;

  const { data: configs, isLoading: configsLoading } =
    useModelConfigs(activeRepoId);

  const loading = reposLoading || (activeRepoId > 0 && configsLoading);

  const configMap = new Map(configs?.map((c) => [c.stage, c]));

  return (
    <>
      <div className="mb-8 flex items-center justify-between">
        <div>
          <h1 className="font-display text-2xl font-bold text-foreground">
            AI Configuration
          </h1>
          <p className="text-xs font-mono text-slate-text mt-1">
            Configure which AI models Argus uses for each review stage.
          </p>
        </div>
        {repos && repos.length > 0 && (
          <div className="relative">
            <select
              value={activeRepoId}
              onChange={(e) => setSelectedRepoId(Number(e.target.value))}
              className="appearance-none rounded-md border border-iron bg-charcoal px-4 py-2 pr-8 text-xs font-mono text-foreground focus:border-amber focus:outline-none"
            >
              {repos.map((r) => (
                <option key={r.id} value={r.id}>
                  {r.full_name}
                </option>
              ))}
            </select>
            <ChevronDown className="pointer-events-none absolute right-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-slate-text" />
          </div>
        )}
      </div>

      {loading ? (
        <div className="flex items-center justify-center py-20">
          <Loader2 className="h-6 w-6 animate-spin text-slate-text" />
        </div>
      ) : activeRepoId === 0 ? (
        <div className="rounded-lg border border-iron bg-charcoal p-10 text-center">
          <Settings className="h-8 w-8 text-slate-text mx-auto mb-3" />
          <p className="text-xs font-mono text-slate-text">
            No repos connected yet.
          </p>
        </div>
      ) : (
        <div className="grid gap-4 md:grid-cols-2">
          {STAGES.map((stage) => (
            <ConfigCard
              key={stage}
              stage={stage}
              repoId={activeRepoId}
              existing={configMap.get(stage)}
            />
          ))}
        </div>
      )}
    </>
  );
}
