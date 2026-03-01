"use client";

import { useState } from "react";
import { Settings, Loader2, Save, Trash2, Key, Cpu, ChevronDown, Zap, Check, X, ArrowUp, Info, UserCog } from "lucide-react";
import {
  useModelConfigs,
  useUpsertModelConfig,
  useDeleteModelConfig,
  useTestConfig,
  type TestResult,
} from "@/lib/queries/model-configs";
import {
  useProviderKeys,
  useUpsertProviderKey,
  useDeleteProviderKey,
} from "@/lib/queries/provider-keys";
import { useActiveRepo } from "@/lib/hooks/use-active-repo";
import { useInstallation } from "@/providers/installation-provider";
import { useUpdateRepo } from "@/lib/queries/repos";
import { RepoSelect } from "@/components/dashboard/repo-select";
import type { ProviderKey } from "@/lib/types";

/* ── Providers & model quick-picks ── */

const PROVIDERS = ["openrouter", "openai", "anthropic", "zhipu"] as const;
type Provider = (typeof PROVIDERS)[number];

const PROVIDER_LABELS: Record<Provider, string> = {
  openrouter: "OpenRouter",
  openai: "OpenAI",
  anthropic: "Anthropic",
  zhipu: "Zhipu AI (GLM)",
};

const PROVIDER_BASE_URLS: Record<Provider, string> = {
  openrouter: "https://openrouter.ai/api/v1",
  openai: "https://api.openai.com/v1",
  anthropic: "https://api.anthropic.com/v1",
  zhipu: "https://api.z.ai/api/paas/v4",
};

const MODEL_PICKS: Record<Provider, string[]> = {
  openrouter: [
    "anthropic/claude-sonnet-4",
    "openai/gpt-4o",
    "google/gemini-2.5-pro",
  ],
  openai: ["gpt-4o", "gpt-4o-mini"],
  anthropic: ["claude-sonnet-4-20250514"],
  zhipu: ["glm-5", "glm-4-plus", "glm-4"],
};

const STAGES = ["triage", "review", "synthesis"] as const;

const STAGE_DESCRIPTIONS: Record<string, string> = {
  triage: "Decides which files need detailed review vs. can be skimmed",
  review: "Analyzes code changes and writes review comments",
  synthesis: "Combines per-file reviews into a unified summary",
};

/* ── Personas ── */

const PERSONAS = [
  { value: "default", label: "Default", description: "Balanced review across all categories" },
  { value: "security_auditor", label: "Security Auditor", description: "Prioritizes injection, auth, secrets, and input validation" },
  { value: "performance_engineer", label: "Performance Engineer", description: "Focuses on N+1 queries, allocations, caching, and complexity" },
  { value: "mentor", label: "Mentor", description: "Educational tone — explains why, suggests learning paths" },
  { value: "architect", label: "Architect", description: "Design patterns, coupling, API contracts, and module boundaries" },
  { value: "strict", label: "Strict", description: "Comments on everything — no issue too small" },
] as const;

/* ── Persona Card ── */

function PersonaCard({
  persona,
  isActive,
  onSelect,
  disabled,
}: {
  persona: (typeof PERSONAS)[number];
  isActive: boolean;
  onSelect: () => void;
  disabled: boolean;
}) {
  return (
    <button
      key={persona.value}
      type="button"
      onClick={onSelect}
      disabled={disabled}
      className={`group cursor-pointer rounded-lg border p-4 text-left transition-all ${
        isActive
          ? "border-amber/40 bg-amber/5"
          : "border-iron bg-charcoal hover:border-iron/80 hover:bg-charcoal/80"
      }`}
    >
      <div className="flex items-center justify-between mb-1.5">
        <span className={`text-xs font-mono font-medium ${isActive ? "text-amber" : "text-foreground"}`}>
          {persona.label}
        </span>
        {isActive && (
          <span className="inline-flex items-center rounded-sm border border-amber/30 bg-amber/10 px-1.5 py-0.5 text-[9px] font-mono uppercase tracking-wider text-amber">
            Active
          </span>
        )}
      </div>
      <p className="text-[11px] font-mono text-slate-text leading-relaxed">
        {persona.description}
      </p>
    </button>
  );
}

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
        {existing ? (
          <span className="inline-flex items-center rounded-sm border border-green-400/20 bg-green-400/10 px-1.5 py-0.5 text-[9px] font-mono uppercase tracking-wider text-green-400">
            Active
          </span>
        ) : (
          <span className="inline-flex items-center rounded-sm border border-iron bg-iron/30 px-1.5 py-0.5 text-[9px] font-mono uppercase tracking-wider text-slate-text/60">
            Not configured
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
            placeholder={PROVIDER_BASE_URLS[provider]}
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
  const test = useTestConfig();
  const [testResult, setTestResult] = useState<TestResult | null>(null);

  const effectiveProvider = provider as Provider;
  const picks = MODEL_PICKS[effectiveProvider] ?? [];

  const [error, setError] = useState("");

  const handleSave = () => {
    const finalModel = isCustom ? customModel : model;
    if (!provider || !finalModel) {
      setError(!provider ? "Select a provider" : "Select a model");
      return;
    }
    setError("");
    upsert.mutate(
      { repoId, stage, provider, model: finalModel, max_tokens: maxTokens, temperature },
      { onError: (err) => setError(err instanceof Error ? err.message : "Save failed") },
    );
  };

  const handleTest = () => {
    const finalModel = isCustom ? customModel : model;
    if (!provider || !finalModel) {
      setError(!provider ? "Select a provider" : "Select a model");
      return;
    }
    setError("");
    setTestResult(null);
    test.mutate(
      { provider, model: finalModel },
      {
        onSuccess: (r) => setTestResult(r),
        onError: (err) => setTestResult({ success: false, error: err instanceof Error ? err.message : "Test failed", latency_ms: 0 }),
      },
    );
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

  const hasNoKeys = savedProviders.length === 0;

  return (
    <div className="rounded-lg border border-iron bg-charcoal p-5">
      <div className="flex items-center justify-between mb-1">
        <div className="flex items-center gap-2">
          <span className="text-xs font-mono uppercase tracking-wider text-amber">
            {stage}
          </span>
          {existing ? (
            <span className="inline-flex items-center rounded-sm border border-green-400/20 bg-green-400/10 px-1.5 py-0.5 text-[9px] font-mono uppercase tracking-wider text-green-400">
              Configured
            </span>
          ) : (
            <span className="inline-flex items-center rounded-sm border border-iron bg-iron/30 px-1.5 py-0.5 text-[9px] font-mono uppercase tracking-wider text-slate-text/60">
              Not set
            </span>
          )}
        </div>
        {existing && (
          <button
            type="button"
            onClick={() => del.mutate({ repoId, stage })}
            className="text-[11px] font-mono text-slate-text hover:text-red-400 transition-colors"
          >
            Reset
          </button>
        )}
      </div>
      <p className="text-[11px] font-mono text-slate-text mb-3">
        {STAGE_DESCRIPTIONS[stage]}
      </p>

      {existing && (
        <div className="rounded border border-iron/50 bg-background/50 px-3 py-2 mb-3">
          <p className="text-[10px] font-mono text-slate-text">Active config</p>
          <p className="text-xs font-mono text-foreground mt-0.5">
            {PROVIDER_LABELS[existing.provider as Provider] ?? existing.provider}{" "}
            <span className="text-amber">{existing.model}</span>
          </p>
          <p className="text-[10px] font-mono text-slate-text mt-0.5">
            temp {existing.temperature} · {existing.max_tokens.toLocaleString()} tokens
          </p>
        </div>
      )}

      <div className="grid grid-cols-2 gap-3 mb-3">
        {/* Provider dropdown — shows all providers, disables unconfigured */}
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
              <option value="">
                {hasNoKeys ? "Add an API key first" : "Select provider"}
              </option>
              {PROVIDERS.map((p) => {
                const hasKey = savedProviders.includes(p);
                return (
                  <option key={p} value={p} disabled={!hasKey}>
                    {PROVIDER_LABELS[p]}{hasKey ? "" : " — no API key"}
                  </option>
                );
              })}
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

      {error && (
        <p className="text-[10px] font-mono text-red-400 mb-2">{error}</p>
      )}

      {testResult && (
        <div className={`rounded border px-3 py-2 mb-2 text-[10px] font-mono ${testResult.success ? "border-green-400/30 bg-green-400/5 text-green-400" : "border-red-400/30 bg-red-400/5 text-red-400"}`}>
          <div className="flex items-center gap-1.5">
            {testResult.success ? <Check className="h-3 w-3" /> : <X className="h-3 w-3" />}
            <span className="font-medium">{testResult.success ? "Connection OK" : "Connection failed"}</span>
            <span className="text-slate-text ml-auto">{testResult.latency_ms}ms</span>
          </div>
          {testResult.error && <p className="mt-1">{testResult.error}</p>}
          {testResult.success && testResult.tokens != null && (
            <p className="mt-1 text-slate-text">{testResult.tokens} tokens used</p>
          )}
        </div>
      )}

      <div className="flex items-center gap-2">
        <button
          type="button"
          onClick={handleSave}
          disabled={upsert.isPending || (!model && !customModel)}
          className="flex items-center gap-2 rounded border border-amber/30 bg-amber/10 px-3 py-1 text-[11px] font-mono text-amber hover:bg-amber/20 transition-colors disabled:opacity-50"
        >
          <Save className="h-3 w-3" />
          {upsert.isPending ? "Saving..." : "Save"}
        </button>
        <button
          type="button"
          onClick={handleTest}
          disabled={test.isPending || (!model && !customModel)}
          className="flex items-center gap-2 rounded border border-iron px-3 py-1 text-[11px] font-mono text-slate-text hover:text-foreground hover:border-foreground/30 transition-colors disabled:opacity-50"
        >
          {test.isPending ? <Loader2 className="h-3 w-3 animate-spin" /> : <Zap className="h-3 w-3" />}
          {test.isPending ? "Testing..." : "Test"}
        </button>
      </div>
    </div>
  );
}

/* ── Page ── */

export default function SettingsPage() {
  const { repos, activeId, setSelectedId, isLoading: reposLoading } = useActiveRepo();
  const { active } = useInstallation();

  const { data: configs, isLoading: configsLoading } = useModelConfigs(activeId);
  const { data: providerKeys, isLoading: keysLoading } = useProviderKeys();
  const updateRepo = useUpdateRepo();

  const [personaError, setPersonaError] = useState("");

  const activeRepo = repos.find((r) => r.id === activeId);
  const currentPersona = (activeRepo?.settings_json?.persona as string) || "default";

  const loading = reposLoading || keysLoading || (activeId > 0 && configsLoading);
  const configMap = new Map(configs?.map((c) => [c.stage, c]));
  const keyMap = new Map(providerKeys?.map((k) => [k.provider, k]));
  const savedProviders = providerKeys?.map((k) => k.provider) ?? [];
  const configuredCount = savedProviders.length;

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
          <section id="api-keys">
            <div className="flex items-center gap-3 mb-4">
              <span className="inline-flex items-center justify-center h-6 w-6 rounded-full border border-amber/30 bg-amber/10 text-[11px] font-mono font-bold text-amber">
                1
              </span>
              <div className="flex items-center gap-2">
                <Key className="h-4 w-4 text-amber" />
                <h2 className="font-display text-lg font-semibold text-foreground">
                  API Keys
                </h2>
              </div>
              <span className="text-[10px] font-mono text-slate-text ml-auto">
                {configuredCount}/{PROVIDERS.length} configured
              </span>
            </div>
            <p className="text-[11px] font-mono text-slate-text mb-4">
              Bring your own API keys. Keys are encrypted at rest and scoped to your organization.
              Providers configured here become available for model selection below.
            </p>
            <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4">
              {PROVIDERS.map((p) => (
                <ProviderKeyCard
                  key={p}
                  provider={p}
                  existing={keyMap.get(p)}
                />
              ))}
            </div>
          </section>

          {/* Connector */}
          <div className="flex items-center gap-3 px-4">
            <div className="h-px flex-1 bg-iron/50" />
            <span className="text-[9px] font-mono uppercase tracking-widest text-slate-text/50">
              providers flow into model config
            </span>
            <div className="h-px flex-1 bg-iron/50" />
          </div>

          {/* Section 2: Model Configuration */}
          <section>
            <div className="flex items-center gap-3 mb-4">
              <span className="inline-flex items-center justify-center h-6 w-6 rounded-full border border-amber/30 bg-amber/10 text-[11px] font-mono font-bold text-amber">
                2
              </span>
              <div className="flex items-center gap-2">
                <Cpu className="h-4 w-4 text-amber" />
                <h2 className="font-display text-lg font-semibold text-foreground">
                  Model Configuration
                </h2>
              </div>
            </div>

            {configuredCount === 0 ? (
              <div className="rounded-lg border border-amber/20 bg-amber/5 px-4 py-3 mb-4 flex items-start gap-2.5">
                <Info className="h-3.5 w-3.5 text-amber mt-0.5 shrink-0" />
                <p className="text-[11px] font-mono text-amber/80">
                  No API keys configured yet. <a href="#api-keys" className="text-amber underline underline-offset-2 hover:text-foreground transition-colors">Add an API key above</a> to unlock provider selection.
                </p>
              </div>
            ) : (
              <p className="text-[11px] font-mono text-slate-text mb-1">
                Configure the model for each review stage. Applies to{" "}
                <span className="text-foreground">{repos.find((r) => r.id === activeId)?.full_name ?? "selected repo"}</span>.
              </p>
            )}

            {configuredCount > 0 && (
              <p className="text-[10px] font-mono text-slate-text/60 mb-4 flex items-center gap-1.5">
                <ArrowUp className="h-3 w-3" />
                Only providers with saved API keys appear as selectable options.
              </p>
            )}

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

          {/* Connector */}
          <div className="flex items-center gap-3 px-4">
            <div className="h-px flex-1 bg-iron/50" />
            <span className="text-[9px] font-mono uppercase tracking-widest text-slate-text/50">
              persona shapes review style
            </span>
            <div className="h-px flex-1 bg-iron/50" />
          </div>

          {/* Section 3: Review Persona */}
          <section>
            <div className="flex items-center gap-3 mb-4">
              <span className="inline-flex items-center justify-center h-6 w-6 rounded-full border border-amber/30 bg-amber/10 text-[11px] font-mono font-bold text-amber">
                3
              </span>
              <div className="flex items-center gap-2">
                <UserCog className="h-4 w-4 text-amber" />
                <h2 className="font-display text-lg font-semibold text-foreground">
                  Review Persona
                </h2>
              </div>
            </div>
            <p className="text-[11px] font-mono text-slate-text mb-4">
              Choose a review style for{" "}
              <span className="text-foreground">{activeRepo?.full_name ?? "selected repo"}</span>.
              Personas adjust the reviewer&apos;s focus and tone. Override per-PR with{" "}
              <code className="rounded bg-iron/50 px-1 py-0.5 text-amber">@argus-eye review --persona &lt;name&gt;</code>.
            </p>

            {activeId === 0 ? (
              <div className="rounded-lg border border-iron bg-charcoal p-10 text-center">
                <UserCog className="h-8 w-8 text-slate-text mx-auto mb-3" />
                <p className="text-xs font-mono text-slate-text">
                  Select a repo to configure persona.
                </p>
              </div>
            ) : (
              <>
                <div className="grid gap-3 md:grid-cols-2 lg:grid-cols-3">
                  {PERSONAS.map((p) => (
                    <PersonaCard
                      key={p.value}
                      persona={p}
                      isActive={currentPersona === p.value}
                      onSelect={() => {
                        setPersonaError("");
                        updateRepo.mutate(
                          {
                            id: activeId,
                            settings_json: { ...activeRepo?.settings_json, persona: p.value },
                          },
                          {
                            onError: (err) =>
                              setPersonaError(err instanceof Error ? err.message : "Failed to save persona"),
                          },
                        );
                      }}
                      disabled={updateRepo.isPending}
                    />
                  ))}
                </div>
                {personaError && (
                  <p className="text-[10px] font-mono text-red-400 mt-2">{personaError}</p>
                )}
              </>
            )}
          </section>
        </div>
      )}
    </>
  );
}
