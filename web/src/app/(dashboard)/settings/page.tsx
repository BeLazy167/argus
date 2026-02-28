"use client";

import { useState } from "react";
import { Settings, Loader2, Save, Trash2, Key, Cpu, ChevronDown } from "lucide-react";
import {
  useModelConfigs,
  useUpsertModelConfig,
  useDeleteModelConfig,
} from "@/lib/queries/model-configs";
import {
  useProviderKeys,
  useUpsertProviderKey,
  useDeleteProviderKey,
} from "@/lib/queries/provider-keys";
import { useActiveRepo } from "@/lib/hooks/use-active-repo";
import { useInstallation } from "@/providers/installation-provider";
import { RepoSelect } from "@/components/dashboard/repo-select";
import type { ProviderKey } from "@/lib/types";

/* ── Providers & model quick-picks ── */

const PROVIDERS = ["openrouter", "openai", "anthropic"] as const;
type Provider = (typeof PROVIDERS)[number];

const PROVIDER_LABELS: Record<Provider, string> = {
  openrouter: "OpenRouter",
  openai: "OpenAI",
  anthropic: "Anthropic",
};

const MODEL_PICKS: Record<Provider, string[]> = {
  openrouter: [
    "anthropic/claude-sonnet-4",
    "openai/gpt-4o",
    "google/gemini-2.5-pro",
  ],
  openai: ["gpt-4o", "gpt-4o-mini"],
  anthropic: ["claude-sonnet-4-20250514"],
};

const STAGES = ["triage", "review", "synthesis"] as const;

const STAGE_DESCRIPTIONS: Record<string, string> = {
  triage: "Decides which files need detailed review vs. can be skimmed",
  review: "Analyzes code changes and writes review comments",
  synthesis: "Combines per-file reviews into a unified summary",
};

/* ── API Key Card ── */

function ProviderKeyCard({
  provider,
  existing,
}: {
  provider: Provider;
  existing?: ProviderKey;
}) {
  const [apiKey, setApiKey] = useState("");
  const [baseUrl, setBaseUrl] = useState(existing?.base_url ?? "");
  const upsert = useUpsertProviderKey();
  const del = useDeleteProviderKey();

  const handleSave = () => {
    if (!apiKey && !existing) return;
    upsert.mutate({
      provider,
      api_key: apiKey,
      base_url: baseUrl || undefined,
    });
    setApiKey("");
  };

  return (
    <div className="rounded-lg border border-iron bg-charcoal p-5">
      <div className="flex items-center justify-between mb-3">
        <div className="flex items-center gap-2">
          <Key className="h-3.5 w-3.5 text-amber" />
          <span className="text-xs font-mono font-medium text-foreground">
            {PROVIDER_LABELS[provider]}
          </span>
        </div>
        {existing && (
          <span className="inline-flex items-center rounded-sm border border-green-400/20 bg-green-400/10 px-1.5 py-0.5 text-[9px] font-mono uppercase tracking-wider text-green-400">
            Active
          </span>
        )}
      </div>

      {existing && (
        <p className="text-[11px] font-mono text-slate-text mb-3">
          Key: {existing.api_key_masked}
        </p>
      )}

      <div className="space-y-2 mb-3">
        <div>
          <label className="block text-[10px] font-mono text-slate-text mb-1">
            {existing ? "Replace API key" : "API key"}
          </label>
          <input
            type="password"
            value={apiKey}
            onChange={(e) => setApiKey(e.target.value)}
            placeholder={existing ? "Enter new key to replace" : "sk-..."}
            className="w-full rounded border border-iron bg-background px-2 py-1.5 text-xs font-mono text-foreground placeholder:text-iron focus:border-amber focus:outline-none"
          />
        </div>
        <div>
          <label className="block text-[10px] font-mono text-slate-text mb-1">
            Base URL override (optional)
          </label>
          <input
            type="text"
            value={baseUrl}
            onChange={(e) => setBaseUrl(e.target.value)}
            placeholder="https://api.openai.com/v1"
            className="w-full rounded border border-iron bg-background px-2 py-1.5 text-xs font-mono text-foreground placeholder:text-iron focus:border-amber focus:outline-none"
          />
        </div>
      </div>

      <div className="flex items-center gap-2">
        <button
          type="button"
          onClick={handleSave}
          disabled={upsert.isPending || (!apiKey && !existing)}
          className="flex items-center gap-2 rounded border border-amber/30 bg-amber/10 px-3 py-1 text-[11px] font-mono text-amber hover:bg-amber/20 transition-colors disabled:opacity-50"
        >
          <Save className="h-3 w-3" />
          {upsert.isPending ? "Saving..." : "Save"}
        </button>
        {existing && (
          <button
            type="button"
            onClick={() => del.mutate(existing!.id)}
            disabled={del.isPending}
            className="flex items-center gap-2 rounded border border-red-400/30 px-3 py-1 text-[11px] font-mono text-red-400 hover:bg-red-400/10 transition-colors disabled:opacity-50"
          >
            <Trash2 className="h-3 w-3" />
            {del.isPending ? "Deleting..." : "Delete"}
          </button>
        )}
      </div>
    </div>
  );
}

/* ── Model Config Card ── */

function ConfigCard({
  stage,
  repoId,
  existing,
  savedProviders,
}: {
  stage: string;
  repoId: number;
  existing?: { provider: string; model: string; base_url?: string; max_tokens: number; temperature: number };
  savedProviders: string[];
}) {
  const [provider, setProvider] = useState(existing?.provider ?? "");
  const [model, setModel] = useState(existing?.model ?? "");
  const [customModel, setCustomModel] = useState("");
  const [isCustom, setIsCustom] = useState(false);
  const [maxTokens, setMaxTokens] = useState(existing?.max_tokens ?? 4096);
  const [temperature, setTemperature] = useState(existing?.temperature ?? 0.2);

  const upsert = useUpsertModelConfig();
  const del = useDeleteModelConfig();

  const effectiveProvider = provider as Provider;
  const picks = MODEL_PICKS[effectiveProvider] ?? [];

  const handleSave = () => {
    const finalModel = isCustom ? customModel : model;
    if (!finalModel) return;
    upsert.mutate({
      repoId,
      stage,
      provider,
      model: finalModel,
      max_tokens: maxTokens,
      temperature,
    });
  };

  const handleModelSelect = (val: string) => {
    if (val === "__custom__") {
      setIsCustom(true);
      setModel("");
    } else {
      setIsCustom(false);
      setModel(val);
      setCustomModel("");
    }
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
        {/* Provider dropdown */}
        <div>
          <label className="block text-[10px] font-mono text-slate-text mb-1">
            Provider
          </label>
          <div className="relative">
            <select
              value={provider}
              onChange={(e) => {
                setProvider(e.target.value);
                setModel("");
                setIsCustom(false);
              }}
              className="w-full appearance-none rounded border border-iron bg-background px-2 py-1.5 pr-7 text-xs font-mono text-foreground focus:border-amber focus:outline-none"
            >
              <option value="">Default</option>
              {savedProviders.map((p) => (
                <option key={p} value={p}>
                  {PROVIDER_LABELS[p as Provider] ?? p}
                </option>
              ))}
            </select>
            <ChevronDown className="pointer-events-none absolute right-2 top-1/2 h-3 w-3 -translate-y-1/2 text-slate-text" />
          </div>
        </div>

        {/* Model dropdown */}
        <div>
          <label className="block text-[10px] font-mono text-slate-text mb-1">
            Model
          </label>
          {picks.length > 0 && !isCustom ? (
            <div className="relative">
              <select
                value={model}
                onChange={(e) => handleModelSelect(e.target.value)}
                className="w-full appearance-none rounded border border-iron bg-background px-2 py-1.5 pr-7 text-xs font-mono text-foreground focus:border-amber focus:outline-none"
              >
                <option value="">Select model</option>
                {picks.map((m) => (
                  <option key={m} value={m}>{m}</option>
                ))}
                <option value="__custom__">Custom...</option>
              </select>
              <ChevronDown className="pointer-events-none absolute right-2 top-1/2 h-3 w-3 -translate-y-1/2 text-slate-text" />
            </div>
          ) : (
            <input
              type="text"
              value={isCustom ? customModel : model}
              onChange={(e) => isCustom ? setCustomModel(e.target.value) : setModel(e.target.value)}
              placeholder="model-name"
              className="w-full rounded border border-iron bg-background px-2 py-1.5 text-xs font-mono text-foreground placeholder:text-iron focus:border-amber focus:outline-none"
            />
          )}
        </div>

        {/* Temperature slider */}
        <div>
          <label className="block text-[10px] font-mono text-slate-text mb-1">
            Temperature: {temperature.toFixed(1)}
          </label>
          <input
            type="range"
            min="0"
            max="2"
            step="0.1"
            value={temperature}
            onChange={(e) => setTemperature(Number(e.target.value))}
            className="w-full accent-amber h-1.5"
          />
        </div>

        {/* Max tokens */}
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
      </div>

      <button
        type="button"
        onClick={handleSave}
        disabled={upsert.isPending || (!model && !customModel)}
        className="flex items-center gap-2 rounded border border-amber/30 bg-amber/10 px-3 py-1 text-[11px] font-mono text-amber hover:bg-amber/20 transition-colors disabled:opacity-50"
      >
        <Save className="h-3 w-3" />
        {upsert.isPending ? "Saving..." : "Save"}
      </button>
    </div>
  );
}

/* ── Page ── */

export default function SettingsPage() {
  const { repos, activeId, setSelectedId, isLoading: reposLoading } = useActiveRepo();
  const { active } = useInstallation();

  const { data: configs, isLoading: configsLoading } = useModelConfigs(activeId);
  const { data: providerKeys, isLoading: keysLoading } = useProviderKeys();

  const loading = reposLoading || keysLoading || (activeId > 0 && configsLoading);
  const configMap = new Map(configs?.map((c) => [c.stage, c]));
  const keyMap = new Map(providerKeys?.map((k) => [k.provider, k]));
  const savedProviders = providerKeys?.map((k) => k.provider) ?? [];

  return (
    <>
      <div className="mb-8 flex items-center justify-between">
        <div>
          <h1 className="font-display text-2xl font-bold text-foreground">
            Settings
          </h1>
          <p className="text-xs font-mono text-slate-text mt-1">
            API keys and model configuration for {active?.org_login ?? "your org"}.
          </p>
        </div>
        <RepoSelect repos={repos} value={activeId} onChange={setSelectedId} />
      </div>

      {loading ? (
        <div className="flex items-center justify-center py-20">
          <Loader2 className="h-6 w-6 animate-spin text-slate-text" />
        </div>
      ) : !active ? (
        <div className="rounded-lg border border-iron bg-charcoal p-10 text-center">
          <Settings className="h-8 w-8 text-slate-text mx-auto mb-3" />
          <p className="text-xs font-mono text-slate-text">
            No installation found.
          </p>
        </div>
      ) : (
        <div className="space-y-10">
          {/* Section 1: API Keys */}
          <section>
            <div className="flex items-center gap-2 mb-4">
              <Key className="h-4 w-4 text-amber" />
              <h2 className="font-display text-lg font-semibold text-foreground">
                API Keys
              </h2>
            </div>
            <p className="text-[11px] font-mono text-slate-text mb-4">
              Bring your own API keys. Keys are encrypted at rest and scoped to your organization.
            </p>
            <div className="grid gap-4 md:grid-cols-3">
              {PROVIDERS.map((p) => (
                <ProviderKeyCard
                  key={p}
                  provider={p}
                  existing={keyMap.get(p)}
                />
              ))}
            </div>
          </section>

          {/* Section 2: Model Configuration */}
          <section>
            <div className="flex items-center gap-2 mb-4">
              <Cpu className="h-4 w-4 text-amber" />
              <h2 className="font-display text-lg font-semibold text-foreground">
                Model Configuration
              </h2>
            </div>
            <p className="text-[11px] font-mono text-slate-text mb-4">
              Override the default model for each review stage. Applies to{" "}
              <span className="text-foreground">{repos.find((r) => r.id === activeId)?.full_name ?? "selected repo"}</span>.
            </p>
            {activeId === 0 ? (
              <div className="rounded-lg border border-iron bg-charcoal p-10 text-center">
                <Settings className="h-8 w-8 text-slate-text mx-auto mb-3" />
                <p className="text-xs font-mono text-slate-text">
                  Select a repo to configure models.
                </p>
              </div>
            ) : (
              <div className="grid gap-4 md:grid-cols-3">
                {STAGES.map((stage) => (
                  <ConfigCard
                    key={stage}
                    stage={stage}
                    repoId={activeId}
                    existing={configMap.get(stage)}
                    savedProviders={savedProviders}
                  />
                ))}
              </div>
            )}
          </section>
        </div>
      )}
    </>
  );
}
