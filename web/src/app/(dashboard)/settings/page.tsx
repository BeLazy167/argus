"use client";

import { useState } from "react";
import { Settings, Loader2, Save, Key, Cpu, ChevronDown, Zap, Check, X, ArrowUp, Info, UserCog, Lock, ShieldCheck, Bug, Blocks, RotateCcw, FileText, RotateCw, Search, Sliders, FlaskConical } from "lucide-react";
import {
  useModelConfigs,
  useUpsertModelConfig,
  useDeleteModelConfig,
  useTestConfig,
  useOrgModelConfigs,
  useUpsertOrgModelConfig,
  useDeleteOrgModelConfig,
  type TestResult,
} from "@/lib/queries/model-configs";
import { useProviderKeys } from "@/lib/queries/provider-keys";
import {
  usePrompts,
  useDefaultPrompts,
  useUpsertPrompt,
  useDeletePrompt,
} from "@/lib/queries/prompts";
import { useOpenRouterModels } from "@/lib/queries/openrouter-models";
import { useActiveRepo } from "@/lib/hooks/use-active-repo";
import { useInstallation } from "@/providers/installation-provider";
import { useUpdateRepo } from "@/lib/queries/repos";
import { RepoSelect } from "@/components/dashboard/repo-select";
import { Protect } from "@clerk/nextjs";
import { UpgradePrompt } from "@/components/dashboard/upgrade-prompt";
import type { PromptTemplate } from "@/lib/types";
import { useOrgDefaults, useSaveOrgDefaults } from "@/lib/queries/org-defaults";

/* ── Providers & model quick-picks ── */

const PROVIDERS = ["openrouter", "openai", "anthropic", "azure", "gcp_vertex", "aws_bedrock", "zhipu"] as const;
type Provider = (typeof PROVIDERS)[number];

const PROVIDER_LABELS: Record<Provider, string> = {
  openrouter: "OpenRouter",
  openai: "OpenAI",
  anthropic: "Anthropic",
  azure: "Azure OpenAI",
  gcp_vertex: "GCP Vertex AI",
  aws_bedrock: "AWS Bedrock",
  zhipu: "Zhipu AI (GLM)",
};


const MODEL_PICKS: Record<Provider, string[]> = {
  openrouter: [
    "anthropic/claude-sonnet-4",
    "openai/gpt-4o",
    "google/gemini-2.5-pro",
  ],
  openai: ["gpt-4o", "gpt-4o-mini"],
  anthropic: ["claude-sonnet-4-20250514"],
  azure: ["gpt-4o", "gpt-4o-mini"],
  gcp_vertex: ["gemini-2.5-pro", "gemini-2.5-flash"],
  aws_bedrock: ["anthropic.claude-sonnet-4", "anthropic.claude-haiku"],
  zhipu: ["glm-5", "glm-4-plus", "glm-4"],
};

const CORE_STAGES = ["triage", "review", "scoring", "synthesis"] as const;

const STAGE_DESCRIPTIONS: Record<string, string> = {
  triage: "Decides which files need detailed review vs. can be skimmed",
  review: "Analyzes code changes and writes review comments",
  scoring: "Cross-model validation — scores and deduplicates specialist comments",
  synthesis: "Combines per-file reviews into a unified summary",
};

/* ── Specialists ── */

const SPECIALISTS = [
  { key: "bug_hunter", label: "Bug Hunter", icon: Bug, color: "text-amber" },
  { key: "security", label: "Security", icon: ShieldCheck, color: "text-red-400" },
  { key: "architecture", label: "Architecture", icon: Blocks, color: "text-blue-400" },
  { key: "regression", label: "Regression", icon: RotateCcw, color: "text-purple-400" },
] as const;

/* ── Personas ── */

const PERSONAS = [
  { value: "default", label: "Default", description: "Balanced review across all categories" },
  { value: "security_auditor", label: "Security Auditor", description: "Prioritizes injection, auth, secrets, and input validation" },
  { value: "performance_engineer", label: "Performance Engineer", description: "Focuses on N+1 queries, allocations, caching, and complexity" },
  { value: "mentor", label: "Mentor", description: "Educational tone — explains why, suggests learning paths" },
  { value: "architect", label: "Architect", description: "Design patterns, coupling, API contracts, and module boundaries" },
  { value: "strict", label: "Strict", description: "Comments on everything — no issue too small" },
  { value: "custom", label: "Custom", description: "Write your own persona prompt — define exactly how Argus reviews" },
] as const;

/* ── Prompt stage labels ── */

const STAGE_LABELS: Record<string, string> = {
  triage_system: "Triage",
  review_system: "Review",
  scoring_system: "Scoring",
  specialist_bug_hunter: "Bug Hunter Specialist",
  specialist_security: "Security Specialist",
  specialist_architecture: "Architecture Specialist",
  specialist_regression: "Regression Specialist",
};

const PROMPT_STAGES = Object.keys(STAGE_LABELS);

/* ── Status Badge ── */

type BadgeVariant = "active" | "configured" | "inactive";

function StatusBadge({ variant, label }: { variant: BadgeVariant; label: string }) {
  const styles: Record<BadgeVariant, string> = {
    active: "border-green-400/20 bg-green-400/10 text-green-400",
    configured: "border-green-400/20 bg-green-400/10 text-green-400",
    inactive: "border-iron bg-iron/30 text-slate-text/60",
  };
  return (
    <span className={`inline-flex items-center rounded-sm border px-1.5 py-0.5 text-[9px] font-mono uppercase tracking-wider ${styles[variant]}`}>
      {label}
    </span>
  );
}

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
          <span className="inline-flex items-center rounded-sm border border-amber/30 bg-amber/10 px-1.5 py-0.5 text-[9px] font-mono uppercase tracking-wider text-amber">active</span>
        )}
      </div>
      <p className="text-[11px] font-mono text-slate-text leading-relaxed">
        {persona.description}
      </p>
    </button>
  );
}

/* ── Model Config Card ── */

function ConfigCard({
  stage,
  repoId,
  existing,
  savedProviders,
  installationId,
  onSave,
  onDelete,
}: {
  stage: string;
  repoId: number;
  existing?: { provider: string; model: string; base_url?: string; max_tokens: number; temperature: number };
  savedProviders: string[];
  installationId?: number;
  onSave?: (data: { stage: string; provider: string; model: string; base_url?: string; max_tokens: number; temperature: number }) => void;
  onDelete?: (stage: string) => void;
}) {
  const [provider, setProvider] = useState(existing?.provider ?? "");
  const [model, setModel] = useState(existing?.model ?? "");
  const [customModel, setCustomModel] = useState("");
  const [isCustom, setIsCustom] = useState(false);
  const [maxTokens, setMaxTokens] = useState(existing?.max_tokens ?? 4096);
  const [temperature, setTemperature] = useState(existing?.temperature ?? 0.2);
  const [baseURL, setBaseURL] = useState(existing?.base_url ?? "");
  const [modelSearch, setModelSearch] = useState("");

  const upsert = useUpsertModelConfig();
  const del = useDeleteModelConfig();
  const test = useTestConfig();
  const [testResult, setTestResult] = useState<TestResult | null>(null);

  const isOpenRouter = provider === "openrouter";
  const { data: orModels } = useOpenRouterModels(isOpenRouter ? installationId : undefined);

  const effectiveProvider = provider as Provider;
  const picks = MODEL_PICKS[effectiveProvider] ?? [];

  const [error, setError] = useState("");

  const finalModel = isCustom ? customModel : model;

  /** Returns true if provider+model are valid, sets error otherwise. */
  const validate = (): boolean => {
    if (!provider || !finalModel) {
      setError(!provider ? "Select a provider" : "Select a model");
      return false;
    }
    setError("");
    return true;
  };

  const handleSave = () => {
    if (!validate()) return;
    if (onSave) {
      onSave({ stage, provider, model: finalModel, max_tokens: maxTokens, temperature });
    } else {
      upsert.mutate(
        { repoId, stage, provider, model: finalModel, max_tokens: maxTokens, temperature },
        { onError: (err) => setError(err instanceof Error ? err.message : "Save failed") },
      );
    }
  };

  const handleTest = () => {
    if (!validate()) return;
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
          <StatusBadge variant={existing ? "configured" : "inactive"} label={existing ? "Configured" : "Not set"} />
        </div>
        {existing && (
          <button
            type="button"
            onClick={() => onDelete ? onDelete(stage) : del.mutate({ repoId, stage })}
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
          {isOpenRouter && orModels && orModels.length > 0 && !isCustom ? (
            /* Searchable OpenRouter model list */
            <div className="relative">
              <div className="relative">
                <Search className="pointer-events-none absolute left-2 top-1/2 h-3 w-3 -translate-y-1/2 text-slate-text" />
                <input
                  type="text"
                  value={modelSearch || model}
                  onChange={(e) => {
                    setModelSearch(e.target.value);
                    setModel("");
                  }}
                  onFocus={() => { if (model) { setModelSearch(model); setModel(""); } }}
                  placeholder="Search models..."
                  autoComplete="off"
                  className="w-full rounded border border-iron bg-background pl-7 pr-8 py-1.5 text-xs font-mono text-foreground placeholder:text-iron focus:border-amber focus:outline-none"
                />
                <ChevronDown className="pointer-events-none absolute right-2 top-1/2 h-3 w-3 -translate-y-1/2 text-slate-text" />
              </div>
              {modelSearch && !model && (
                <div className="absolute z-20 mt-1 w-full max-h-48 overflow-y-auto rounded border border-iron bg-charcoal shadow-lg">
                  {orModels
                    .filter((m) => m.id.toLowerCase().includes(modelSearch.toLowerCase()) || m.name.toLowerCase().includes(modelSearch.toLowerCase()))
                    .slice(0, 20)
                    .map((m) => (
                      <button
                        key={m.id}
                        type="button"
                        onClick={() => {
                          setModel(m.id);
                          setModelSearch("");
                        }}
                        className="w-full text-left px-3 py-1.5 text-xs font-mono hover:bg-amber/10 transition-colors"
                      >
                        <span className="text-foreground">{m.id}</span>
                        <span className="text-slate-text ml-2">
                          {(m.context_length / 1000).toFixed(0)}k ctx
                        </span>
                      </button>
                    ))}
                  <button
                    type="button"
                    onClick={() => handleModelSelect("__custom__")}
                    className="w-full text-left px-3 py-1.5 text-xs font-mono text-amber hover:bg-amber/10 transition-colors border-t border-iron"
                  >
                    Custom model...
                  </button>
                </div>
              )}
            </div>
          ) : (
            /* Combo input: type any model name OR click a suggestion */
            <div className="relative">
              <input
                type="text"
                value={isCustom ? customModel : model}
                onChange={(e) => {
                  const v = e.target.value;
                  if (isCustom) { setCustomModel(v); } else { setModel(v); setIsCustom(false); }
                }}
                onFocus={() => setModelSearch("__show__")}
                onBlur={() => setTimeout(() => setModelSearch(""), 150)}
                placeholder="Type or select a model..."
                autoComplete="off"
                className="w-full rounded border border-iron bg-background px-2 py-1.5 pr-7 text-xs font-mono text-foreground placeholder:text-iron focus:border-amber focus:outline-none"
              />
              <ChevronDown className="pointer-events-none absolute right-2 top-1/2 h-3 w-3 -translate-y-1/2 text-slate-text" />
              {modelSearch === "__show__" && picks.length > 0 && (
                <div className="absolute z-20 mt-1 w-full max-h-48 overflow-y-auto rounded border border-iron bg-charcoal shadow-lg">
                  {picks.map((m) => (
                    <button
                      key={m}
                      type="button"
                      onMouseDown={(e) => { e.preventDefault(); setModel(m); setIsCustom(false); setModelSearch(""); }}
                      className="w-full text-left px-3 py-1.5 text-xs font-mono hover:bg-amber/10 transition-colors text-foreground"
                    >
                      {m}
                    </button>
                  ))}
                  <div className="px-3 py-1 text-[9px] font-mono text-slate-text/50 border-t border-iron">
                    Or type any model name above
                  </div>
                </div>
              )}
            </div>
          )}
        </div>

        {/* Base URL override — shown for providers that need custom endpoints */}
        {(provider === "azure" || provider === "gcp_vertex" || provider === "aws_bedrock") && (
          <div className="col-span-2">
            <label className="block text-[10px] font-mono text-slate-text mb-1">
              Endpoint URL <span className="text-amber">*</span>
            </label>
            <input
              type="text"
              value={baseURL}
              onChange={(e) => setBaseURL(e.target.value)}
              placeholder={
                provider === "azure" ? "https://{resource}.openai.azure.com/openai" :
                provider === "gcp_vertex" ? "https://{region}-aiplatform.googleapis.com/v1/projects/{project}/locations/{region}/endpoints/openapi" :
                "https://bedrock-runtime.{region}.amazonaws.com"
              }
              className="w-full rounded border border-iron bg-background px-2 py-1.5 text-xs font-mono text-foreground placeholder:text-iron/50 focus:border-amber focus:outline-none"
            />
            <p className="text-[9px] font-mono text-slate-text/50 mt-1">
              {provider === "azure" && "Azure OpenAI resource endpoint"}
              {provider === "gcp_vertex" && "Vertex AI OpenAI-compatible endpoint"}
              {provider === "aws_bedrock" && "Bedrock runtime endpoint"}
            </p>
          </div>
        )}

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
          disabled={upsert.isPending || !finalModel}
          className="flex items-center gap-2 rounded border border-amber/30 bg-amber/10 px-3 py-1 text-[11px] font-mono text-amber hover:bg-amber/20 transition-colors disabled:opacity-50"
        >
          <Save className="h-3 w-3" />
          {upsert.isPending ? "Saving..." : "Save"}
        </button>
        <button
          type="button"
          onClick={handleTest}
          disabled={test.isPending || !finalModel}
          className="flex items-center gap-2 rounded border border-iron px-3 py-1 text-[11px] font-mono text-slate-text hover:text-foreground hover:border-foreground/30 transition-colors disabled:opacity-50"
        >
          {test.isPending ? <Loader2 className="h-3 w-3 animate-spin" /> : <Zap className="h-3 w-3" />}
          {test.isPending ? "Testing..." : "Test"}
        </button>
      </div>
    </div>
  );
}

/* ── Deep Review Toggle Card ── */

function DeepReviewCard({
  enabled,
  onToggle,
  pending,
}: {
  enabled: boolean;
  onToggle: () => void;
  pending: boolean;
}) {
  return (
    <div className={`rounded-lg border p-5 transition-colors ${
      enabled ? "border-amber/30 bg-amber/5" : "border-iron bg-charcoal"
    }`}>
      <div className="flex items-center justify-between mb-3">
        <div className="flex items-center gap-2">
          <span className="text-xs font-mono font-medium text-foreground">
            Deep Review
          </span>
          <StatusBadge variant={enabled ? "active" : "inactive"} label={enabled ? "Active" : "Off"} />
        </div>
        <button
          type="button"
          onClick={onToggle}
          disabled={pending}
          aria-label={enabled ? "Disable deep review" : "Enable deep review"}
          className={`relative inline-flex h-6 w-11 shrink-0 cursor-pointer rounded-full border-2 transition-colors duration-200 ease-in-out focus:outline-none focus-visible:ring-2 focus-visible:ring-amber/50 focus-visible:ring-offset-2 focus-visible:ring-offset-background ${
            enabled ? "border-amber bg-amber" : "border-iron bg-iron/50"
          }`}
        >
          <span
            className={`pointer-events-none inline-block h-5 w-5 rounded-full bg-foreground shadow-lg ring-0 transition-transform duration-200 ease-in-out ${
              enabled ? "translate-x-5" : "translate-x-0"
            }`}
          />
        </button>
      </div>

      <p className="text-[10px] font-mono text-slate-text mb-3">
        4 parallel specialist agents review files triaged as &quot;deep&quot;
      </p>

      {/* Specialist chips */}
      <div className="grid grid-cols-2 gap-2">
        {SPECIALISTS.map((s) => {
          const Icon = s.icon;
          return (
            <div
              key={s.key}
              className={`flex items-center gap-2 rounded border px-2.5 py-1.5 transition-colors ${
                enabled
                  ? "border-iron/50 bg-background/50"
                  : "border-iron/30 bg-iron/10"
              }`}
            >
              <Icon className={`h-3 w-3 ${enabled ? s.color : "text-slate-text/40"}`} />
              <span className={`text-[10px] font-mono ${enabled ? "text-foreground" : "text-slate-text/40"}`}>
                {s.label}
              </span>
            </div>
          );
        })}
      </div>
    </div>
  );
}

/* ── Prompt Editor Card ── */

function PromptCard({
  stage,
  repoId,
  custom,
  defaultText,
}: {
  stage: string;
  repoId: number;
  custom?: PromptTemplate;
  defaultText: string;
}) {
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState(custom?.prompt_text ?? "");
  const upsert = useUpsertPrompt();
  const del = useDeletePrompt();

  const isCustom = custom?.is_custom ?? false;
  const displayText = isCustom ? custom!.prompt_text : defaultText;

  const handleSave = () => {
    if (!draft.trim()) return;
    upsert.mutate({ repoId, stage, prompt_text: draft });
  };

  const handleReset = () => {
    del.mutate(
      { repoId, stage },
      { onSuccess: () => setDraft("") },
    );
  };

  return (
    <div className="rounded-lg border border-iron bg-charcoal">
      <button
        type="button"
        onClick={() => {
          if (!open && !draft) setDraft(displayText);
          setOpen(!open);
        }}
        className="flex w-full items-center justify-between p-4 text-left"
      >
        <div className="flex items-center gap-2">
          <span className="text-xs font-mono font-medium text-foreground">
            {STAGE_LABELS[stage]}
          </span>
          {isCustom && (
            <span className="inline-flex items-center rounded-sm border border-amber/30 bg-amber/10 px-1.5 py-0.5 text-[9px] font-mono uppercase tracking-wider text-amber">
              Custom
            </span>
          )}
        </div>
        <ChevronDown className={`h-3.5 w-3.5 text-slate-text transition-transform ${open ? "rotate-180" : ""}`} />
      </button>

      {open && (
        <div className="border-t border-iron px-4 pb-4 pt-3 space-y-3">
          <textarea
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            placeholder={defaultText}
            rows={10}
            className="w-full rounded border border-iron bg-background px-3 py-2 text-xs font-mono text-foreground placeholder:text-iron/60 focus:border-amber focus:outline-none resize-y leading-relaxed"
          />
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={handleSave}
              disabled={upsert.isPending || !draft.trim()}
              className="flex items-center gap-2 rounded border border-amber/30 bg-amber/10 px-3 py-1 text-[11px] font-mono text-amber hover:bg-amber/20 transition-colors disabled:opacity-50"
            >
              <Save className="h-3 w-3" />
              {upsert.isPending ? "Saving..." : "Save"}
            </button>
            {isCustom && (
              <button
                type="button"
                onClick={handleReset}
                disabled={del.isPending}
                className="flex items-center gap-2 rounded border border-iron px-3 py-1 text-[11px] font-mono text-slate-text hover:text-foreground hover:border-foreground/30 transition-colors disabled:opacity-50"
              >
                <RotateCw className="h-3 w-3" />
                {del.isPending ? "Resetting..." : "Reset to default"}
              </button>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

/* ── Feature Toggles ── */

const FEATURE_TOGGLES = [
  {
    key: "cross_file_context",
    label: "Cross-file context",
    description: "Include related files (callers, tests, imports) in review context",
    defaultValue: true,
  },
  {
    key: "blast_radius",
    label: "Blast radius analysis",
    description: "Show dependency impact analysis for changed code",
    defaultValue: true,
  },
  {
    key: "scenario_memory",
    label: "Scenario memory",
    description: "Auto-generate and check scenarios from past reviews",
    defaultValue: true,
  },
  {
    key: "code_simulation",
    label: "Code simulation",
    description: "Simulate execution paths to predict breakage (experimental)",
    defaultValue: false,
    experimental: true,
  },
] as const;

function FeatureToggleCard({
  toggle,
  enabled,
  onToggle,
  pending,
}: {
  toggle: (typeof FEATURE_TOGGLES)[number];
  enabled: boolean;
  onToggle: () => void;
  pending: boolean;
}) {
  return (
    <div className={`rounded-lg border p-4 transition-colors ${
      enabled ? "border-amber/30 bg-amber/5" : "border-iron bg-charcoal"
    }`}>
      <div className="flex items-center justify-between mb-1.5">
        <div className="flex items-center gap-2">
          <span className={`text-xs font-mono font-medium ${enabled ? "text-amber" : "text-foreground"}`}>
            {toggle.label}
          </span>
          {"experimental" in toggle && toggle.experimental && (
            <span className="inline-flex items-center gap-1 rounded-sm border border-purple-400/30 bg-purple-400/10 px-1.5 py-0.5 text-[9px] font-mono uppercase tracking-wider text-purple-400">
              <FlaskConical className="h-2.5 w-2.5" />
              experimental
            </span>
          )}
        </div>
        <button
          type="button"
          onClick={onToggle}
          disabled={pending}
          aria-label={enabled ? `Disable ${toggle.label}` : `Enable ${toggle.label}`}
          className={`relative inline-flex h-6 w-11 shrink-0 cursor-pointer rounded-full border-2 transition-colors duration-200 ease-in-out focus:outline-none focus-visible:ring-2 focus-visible:ring-amber/50 focus-visible:ring-offset-2 focus-visible:ring-offset-background ${
            enabled ? "border-amber bg-amber" : "border-iron bg-iron/50"
          }`}
        >
          <span
            className={`pointer-events-none inline-block h-5 w-5 rounded-full bg-foreground shadow-lg ring-0 transition-transform duration-200 ease-in-out ${
              enabled ? "translate-x-5" : "translate-x-0"
            }`}
          />
        </button>
      </div>
      <p className="text-[10px] font-mono text-slate-text leading-relaxed">
        {toggle.description}
      </p>
    </div>
  );
}

/* ── Page ── */

export default function SettingsPage() {
  const { repos, activeId, setSelectedId, isLoading: reposLoading } = useActiveRepo();
  const { active } = useInstallation();

  const { data: configs, isLoading: configsLoading } = useModelConfigs(activeId);
  const { data: providerKeys, isLoading: keysLoading } = useProviderKeys();
  const { data: customPrompts } = usePrompts(activeId);
  const { data: defaultPrompts } = useDefaultPrompts();
  const updateRepo = useUpdateRepo();

  const [personaError, setPersonaError] = useState("");
  const [settingsScope, setSettingsScope] = useState<"org" | "repo">("repo");

  // Org defaults
  const { data: orgDefaults, isLoading: orgDefaultsLoading } = useOrgDefaults();
  const saveOrgDefaults = useSaveOrgDefaults();

  // Org model configs
  const { data: orgConfigs } = useOrgModelConfigs();
  const upsertOrgConfig = useUpsertOrgModelConfig();
  const deleteOrgConfig = useDeleteOrgModelConfig();
  const orgConfigMap = new Map(orgConfigs?.map((c) => [c.stage, c]));

  const promptMap = new Map(customPrompts?.map((p) => [p.stage, p]));
  const defaultPromptMap = new Map(defaultPrompts?.map((p) => [p.stage, p]));

  const activeRepo = repos.find((r) => r.id === activeId);
  const currentPersona = (activeRepo?.settings_json?.persona as string) || "default";
  const currentCustomPrompt = (activeRepo?.settings_json?.custom_persona_prompt as string) || "";
  const deepReview = (activeRepo?.settings_json?.deep_review as boolean) ?? false;
  const [customPromptDraft, setCustomPromptDraft] = useState(currentCustomPrompt);

  // Org defaults state
  const orgPersona = (orgDefaults?.persona as string) || "default";
  const orgCustomPrompt = (orgDefaults?.custom_persona_prompt as string) || "";
  const orgDeepReview = (orgDefaults?.deep_review as boolean) ?? false;
  const [orgCustomPromptDraft, setOrgCustomPromptDraft] = useState(orgCustomPrompt);

  const loading = reposLoading || keysLoading || (activeId > 0 && configsLoading);
  const configMap = new Map(configs?.map((c) => [c.stage, c]));
  const savedProviders = providerKeys?.map((k) => k.provider) ?? [];
  const configuredCount = savedProviders.length;

  const toggleDeepReview = () => {
    updateRepo.mutate({
      id: activeId,
      settings_json: { ...activeRepo?.settings_json, deep_review: !deepReview },
    });
  };

  const toggleOrgDeepReview = () => {
    saveOrgDefaults.mutate({ ...orgDefaults, deep_review: !orgDeepReview });
  };

  return (
    <>
      <div className="mb-6 flex items-center justify-between">
        <div>
          <h1 className="font-display text-2xl font-bold text-foreground">
            Settings
          </h1>
          <p className="text-xs font-mono text-slate-text mt-1">
            API keys and model configuration for {active?.org_login ?? "your org"}.
          </p>
        </div>
        {settingsScope === "repo" && (
          <RepoSelect repos={repos} value={activeId} onChange={setSelectedId} />
        )}
      </div>

      {/* Scope tabs */}
      <div className="flex items-center gap-1 mb-8 border-b border-iron">
        <button
          type="button"
          onClick={() => setSettingsScope("org")}
          className={`px-4 py-2 text-xs font-mono transition-colors border-b-2 -mb-px ${
            settingsScope === "org"
              ? "border-amber text-amber"
              : "border-transparent text-slate-text hover:text-foreground"
          }`}
        >
          Org Defaults
        </button>
        <button
          type="button"
          onClick={() => setSettingsScope("repo")}
          className={`px-4 py-2 text-xs font-mono transition-colors border-b-2 -mb-px ${
            settingsScope === "repo"
              ? "border-amber text-amber"
              : "border-transparent text-slate-text hover:text-foreground"
          }`}
        >
          Repo Overrides
        </button>
      </div>

      {/* Org Defaults Tab */}
      {settingsScope === "org" && (
        <>
          {!active || orgDefaultsLoading ? (
            <div className="flex items-center justify-center py-20">
              <Loader2 className="h-6 w-6 animate-spin text-slate-text" />
            </div>
          ) : (
            <div className="space-y-10">
              <div className="rounded-lg border border-amber/20 bg-amber/5 px-4 py-3 flex items-start gap-2.5">
                <Info className="h-3.5 w-3.5 text-amber mt-0.5 shrink-0" />
                <p className="text-[11px] font-mono text-amber/80">
                  These defaults apply to all repos in <span className="text-amber font-medium">{active?.org_login}</span>.
                  Repos can override individual settings on the &quot;Repo Overrides&quot; tab.
                </p>
              </div>

              {/* Org: Persona */}
              <section>
                <div className="flex items-center gap-3 mb-4">
                  <div className="flex items-center gap-2">
                    <UserCog className="h-4 w-4 text-amber" />
                    <h2 className="font-display text-lg font-semibold text-foreground">
                      Default Persona
                    </h2>
                  </div>
                </div>
                <div className="grid gap-3 md:grid-cols-2 lg:grid-cols-3">
                  {PERSONAS.map((p) => (
                    <PersonaCard
                      key={p.value}
                      persona={p}
                      isActive={orgPersona === p.value}
                      onSelect={() => {
                        saveOrgDefaults.mutate({ ...orgDefaults, persona: p.value });
                      }}
                      disabled={saveOrgDefaults.isPending}
                    />
                  ))}
                </div>
                {orgPersona === "custom" && (
                  <div className="mt-4 rounded-lg border border-iron bg-charcoal p-4">
                    <label className="block text-[11px] font-mono text-slate-text mb-2">
                      Custom persona prompt (org default)
                    </label>
                    <textarea
                      value={orgCustomPromptDraft}
                      onChange={(e) => setOrgCustomPromptDraft(e.target.value)}
                      placeholder="e.g. You are a reviewer focused on accessibility and i18n..."
                      rows={5}
                      className="w-full rounded-lg border border-iron bg-void px-4 py-3 text-xs font-mono text-foreground placeholder:text-slate-text/40 focus:outline-none focus:border-amber/50 transition-colors resize-y"
                    />
                    <button
                      type="button"
                      disabled={saveOrgDefaults.isPending || orgCustomPromptDraft === orgCustomPrompt}
                      onClick={() => {
                        saveOrgDefaults.mutate({ ...orgDefaults, persona: "custom", custom_persona_prompt: orgCustomPromptDraft });
                      }}
                      className="mt-2 rounded border border-amber/30 bg-amber/10 px-3 py-1.5 text-[10px] font-mono font-medium text-amber hover:bg-amber/20 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
                    >
                      {saveOrgDefaults.isPending ? "Saving..." : "Save Custom Persona"}
                    </button>
                  </div>
                )}
              </section>

              {/* Org: Deep Review */}
              <section>
                <div className="flex items-center gap-3 mb-4">
                  <div className="flex items-center gap-2">
                    <Cpu className="h-4 w-4 text-amber" />
                    <h2 className="font-display text-lg font-semibold text-foreground">
                      Deep Review
                    </h2>
                  </div>
                </div>
                <Protect plan="org:pro" fallback={<UpgradePrompt feature="Deep review" />}>
                  <DeepReviewCard
                    enabled={orgDeepReview}
                    onToggle={toggleOrgDeepReview}
                    pending={saveOrgDefaults.isPending}
                  />
                </Protect>
              </section>

              {/* Org: Review Pipeline */}
              <section>
                <div className="flex items-center gap-3 mb-4">
                  <div className="flex items-center gap-2">
                    <Cpu className="h-4 w-4 text-amber" />
                    <h2 className="font-display text-lg font-semibold text-foreground">
                      Review Pipeline
                    </h2>
                  </div>
                </div>
                <p className="text-xs font-mono text-slate-text mb-4">
                  Configure the default model for each review stage. Applies to all repos unless overridden.
                </p>

                {configuredCount === 0 ? (
                  <div className="rounded-lg border border-iron/50 bg-iron/10 px-4 py-3 flex items-start gap-2.5">
                    <Info className="h-3.5 w-3.5 text-slate-text mt-0.5 shrink-0" />
                    <p className="text-[11px] font-mono text-slate-text">
                      No API keys configured yet. <a href="/providers" className="text-amber underline underline-offset-2 hover:text-foreground transition-colors">Add an API key</a> to unlock provider selection.
                    </p>
                  </div>
                ) : (
                  <div className="space-y-4">
                    <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4">
                      {CORE_STAGES.map((stage) => (
                        <ConfigCard
                          key={`org-${stage}`}
                          stage={stage}
                          repoId={0}
                          existing={orgConfigMap.get(stage)}
                          savedProviders={savedProviders}
                          installationId={active?.id}
                          onSave={(data) => upsertOrgConfig.mutate(data)}
                          onDelete={(s) => deleteOrgConfig.mutate({ stage: s })}
                        />
                      ))}
                    </div>
                  </div>
                )}
              </section>

              {/* Org: Feature Toggles */}
              <Protect plan="org:pro" fallback={<UpgradePrompt feature="Advanced features" />}>
                <section>
                  <div className="flex items-center gap-3 mb-4">
                    <div className="flex items-center gap-2">
                      <Sliders className="h-4 w-4 text-amber" />
                      <h2 className="font-display text-lg font-semibold text-foreground">
                        Default Feature Toggles
                      </h2>
                    </div>
                  </div>
                  <div className="grid gap-3 md:grid-cols-2">
                    {FEATURE_TOGGLES.map((toggle) => {
                      const val = orgDefaults?.[toggle.key];
                      const enabled = typeof val === "boolean" ? val : toggle.defaultValue;
                      return (
                        <FeatureToggleCard
                          key={toggle.key}
                          toggle={toggle}
                          enabled={enabled}
                          pending={saveOrgDefaults.isPending}
                          onToggle={() => {
                            saveOrgDefaults.mutate({ ...orgDefaults, [toggle.key]: !enabled });
                          }}
                        />
                      );
                    })}
                  </div>
                </section>
              </Protect>
            </div>
          )}
        </>
      )}

      {/* Repo Overrides Tab */}
      {settingsScope === "repo" && (loading ? (
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
          <div className="rounded-lg border border-iron/50 bg-iron/10 px-4 py-3 flex items-start gap-2.5">
            <Info className="h-3.5 w-3.5 text-slate-text mt-0.5 shrink-0" />
            <p className="text-[11px] font-mono text-slate-text">
              Settings not configured here fall back to org defaults.
            </p>
          </div>
          {/* API Keys link */}
          <section>
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
            <p className="text-[11px] font-mono text-slate-text">
              Manage API keys in <a href="/providers" className="text-amber underline underline-offset-2 hover:text-foreground transition-colors">Providers</a>.
            </p>
          </section>

          {/* Section 2: Review Pipeline (models + deep review merged) */}
          <section>
            <div className="flex items-center gap-3 mb-4">
              <span className="inline-flex items-center justify-center h-6 w-6 rounded-full border border-amber/30 bg-amber/10 text-[11px] font-mono font-bold text-amber">
                2
              </span>
              <div className="flex items-center gap-2">
                <Cpu className="h-4 w-4 text-amber" />
                <h2 className="font-display text-lg font-semibold text-foreground">
                  Review Pipeline
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
              <div className="space-y-4">
                {/* Row 1: Core stages */}
                <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4">
                  {CORE_STAGES.map((stage) => (
                    <ConfigCard
                      key={stage}
                      stage={stage}
                      repoId={activeId}
                      existing={configMap.get(stage)}
                      savedProviders={savedProviders}
                      installationId={active?.id}
                    />
                  ))}
                </div>

                {/* Row 2: Deep review toggle (Pro only) */}
                <Protect plan="org:pro" fallback={<UpgradePrompt feature="Deep review" />}>
                  <DeepReviewCard
                    enabled={deepReview}
                    onToggle={toggleDeepReview}
                    pending={updateRepo.isPending}
                  />
                </Protect>
              </div>
            )}
          </section>

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
                {currentPersona === "custom" && (
                  <Protect plan="org:pro" fallback={<UpgradePrompt feature="Custom persona" />}>
                    <div className="mt-4 rounded-lg border border-iron bg-charcoal p-4">
                      <label className="block text-[11px] font-mono text-slate-text mb-2">
                        Custom persona prompt — define how Argus should review code
                      </label>
                      <textarea
                        value={customPromptDraft}
                        onChange={(e) => setCustomPromptDraft(e.target.value)}
                        placeholder="e.g. You are a reviewer focused on accessibility and i18n. Flag any hardcoded strings, missing aria labels, or RTL layout issues..."
                        rows={5}
                        className="w-full rounded-lg border border-iron bg-void px-4 py-3 text-xs font-mono text-foreground placeholder:text-slate-text/40 focus:outline-none focus:border-amber/50 transition-colors resize-y"
                      />
                      <button
                        type="button"
                        disabled={updateRepo.isPending || customPromptDraft === currentCustomPrompt}
                        onClick={() => {
                          setPersonaError("");
                          updateRepo.mutate(
                            {
                              id: activeId,
                              settings_json: { ...activeRepo?.settings_json, persona: "custom", custom_persona_prompt: customPromptDraft },
                            },
                            {
                              onError: (err) =>
                                setPersonaError(err instanceof Error ? err.message : "Failed to save"),
                            },
                          );
                        }}
                        className="mt-2 rounded border border-amber/30 bg-amber/10 px-3 py-1.5 text-[10px] font-mono font-medium text-amber hover:bg-amber/20 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
                      >
                        {updateRepo.isPending ? "Saving..." : "Save Custom Persona"}
                      </button>
                    </div>
                  </Protect>
                )}
                {personaError && (
                  <p className="text-[10px] font-mono text-red-400 mt-2">{personaError}</p>
                )}
              </>
            )}
          </section>

          {/* Section 4: Review Prompts (Pro only) */}
          <Protect plan="org:pro" fallback={<UpgradePrompt feature="Custom prompts" />}>
            <section>
              <div className="flex items-center gap-3 mb-4">
                <span className="inline-flex items-center justify-center h-6 w-6 rounded-full border border-amber/30 bg-amber/10 text-[11px] font-mono font-bold text-amber">
                  4
                </span>
                <div className="flex items-center gap-2">
                  <FileText className="h-4 w-4 text-amber" />
                  <h2 className="font-display text-lg font-semibold text-foreground">
                    Review Prompts
                  </h2>
                </div>
              </div>
              <p className="text-[11px] font-mono text-slate-text mb-4">
                Customize the AI prompts used in each pipeline stage for{" "}
                <span className="text-foreground">{activeRepo?.full_name ?? "selected repo"}</span>.
                Changes override the built-in defaults.
              </p>

              {activeId === 0 ? (
                <div className="rounded-lg border border-iron bg-charcoal p-10 text-center">
                  <FileText className="h-8 w-8 text-slate-text mx-auto mb-3" />
                  <p className="text-xs font-mono text-slate-text">
                    Select a repo to customize prompts.
                  </p>
                </div>
              ) : (
                <div className="space-y-2">
                  {PROMPT_STAGES.map((stage) => (
                    <PromptCard
                      key={stage}
                      stage={stage}
                      repoId={activeId}
                      custom={promptMap.get(stage)}
                      defaultText={defaultPromptMap.get(stage)?.prompt_text ?? ""}
                    />
                  ))}
                </div>
              )}
            </section>
          </Protect>

          {/* Section 5: Advanced Features (Pro only) */}
          <Protect plan="org:pro" fallback={<UpgradePrompt feature="Advanced features (cross-file, blast radius, scenarios, simulation)" />}>
            <section>
              <div className="flex items-center gap-3 mb-4">
                <span className="inline-flex items-center justify-center h-6 w-6 rounded-full border border-amber/30 bg-amber/10 text-[11px] font-mono font-bold text-amber">
                  5
                </span>
                <div className="flex items-center gap-2">
                  <Sliders className="h-4 w-4 text-amber" />
                  <h2 className="font-display text-lg font-semibold text-foreground">
                    Advanced Features
                  </h2>
                </div>
              </div>
              <p className="text-[11px] font-mono text-slate-text mb-4">
                Toggle pipeline capabilities for{" "}
                <span className="text-foreground">{activeRepo?.full_name ?? "selected repo"}</span>.
                These control which analysis stages run during review.
              </p>

              {activeId === 0 ? (
                <div className="rounded-lg border border-iron bg-charcoal p-10 text-center">
                  <Sliders className="h-8 w-8 text-slate-text mx-auto mb-3" />
                  <p className="text-xs font-mono text-slate-text">
                    Select a repo to configure features.
                  </p>
                </div>
              ) : (
                <div className="grid gap-3 md:grid-cols-2">
                  {FEATURE_TOGGLES.map((toggle) => {
                    const val = activeRepo?.settings_json?.[toggle.key];
                    const enabled = typeof val === "boolean" ? val : toggle.defaultValue;
                    return (
                      <FeatureToggleCard
                        key={toggle.key}
                        toggle={toggle}
                        enabled={enabled}
                        pending={updateRepo.isPending}
                        onToggle={() => {
                          updateRepo.mutate({
                            id: activeId,
                            settings_json: { ...activeRepo?.settings_json, [toggle.key]: !enabled },
                          });
                        }}
                      />
                    );
                  })}
                </div>
              )}
            </section>
          </Protect>
        </div>
      ))}
    </>
  );
}
