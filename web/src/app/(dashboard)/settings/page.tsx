"use client";

import { useEffect, useRef, useState } from "react";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { Settings, Loader2, Save, Key, Cpu, ChevronDown, Zap, Check, X, ArrowUp, Info, UserCog, Lock, FileText, RotateCw, Search, Sliders } from "lucide-react";
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
import { ProGate } from "@/components/dashboard/pro-gate";
import type { PromptTemplate } from "@/lib/types";
import { useOrgDefaults, useSaveOrgDefaults } from "@/lib/queries/org-defaults";
import { useFeatureFlags, useSaveFeatureFlags, type FeatureFlags } from "@/lib/queries/features";

/* ── Providers & model quick-picks ── */

const PROVIDERS = ["openrouter", "openai", "anthropic", "fireworks", "groq", "together", "deepseek", "azure", "gcp_vertex", "aws_bedrock", "zhipu"] as const;
type Provider = (typeof PROVIDERS)[number];

const PROVIDER_LABELS: Record<Provider, string> = {
  openrouter: "OpenRouter",
  openai: "OpenAI",
  anthropic: "Anthropic",
  fireworks: "Fireworks AI",
  groq: "Groq",
  together: "Together AI",
  deepseek: "DeepSeek",
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
  fireworks: ["accounts/fireworks/models/glm-5p1", "accounts/fireworks/models/deepseek-r1", "accounts/fireworks/models/llama-v3p3-70b-instruct"],
  groq: ["llama-3.3-70b-versatile", "mixtral-8x7b-32768", "gemma2-9b-it"],
  together: ["deepseek-ai/DeepSeek-V3.1", "meta-llama/Llama-3.3-70B-Instruct-Turbo", "Qwen/Qwen2.5-72B-Instruct-Turbo"],
  deepseek: ["deepseek-chat", "deepseek-reasoner"],
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
      className={`group cursor-pointer border p-4 text-left transition-all ${
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
    <div className="border border-iron bg-charcoal p-5">
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
            className="text-[11px] font-mono text-slate-text hover:text-red-400 transition-colors cursor-pointer"
          >
            Reset
          </button>
        )}
      </div>
      <p className="text-[11px] font-mono text-slate-text mb-3">
        {STAGE_DESCRIPTIONS[stage]}
      </p>

      {existing && (
        <div className="border border-iron/50 bg-background/50 px-3 py-2 mb-3">
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

      <div className="grid grid-cols-1 sm:grid-cols-2 gap-3 mb-3">
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
              style={{ backgroundColor: 'var(--background)', color: 'var(--foreground)' }}
              className="w-full appearance-none border border-iron bg-background px-2 py-1.5 pr-7 text-xs font-mono text-foreground focus:border-amber focus:outline-none"
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
                  placeholder="Search models…"
                  autoComplete="off"
                  className="w-full border border-iron bg-background pl-7 pr-8 py-1.5 text-xs font-mono text-foreground placeholder:text-iron focus:border-amber focus:outline-none"
                />
                <ChevronDown className="pointer-events-none absolute right-2 top-1/2 h-3 w-3 -translate-y-1/2 text-slate-text" />
              </div>
              {modelSearch && !model && (
                <div className="absolute z-20 mt-1 w-full max-h-48 overflow-y-auto border border-iron bg-charcoal shadow-lg">
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
                placeholder="Type or select a model…"
                autoComplete="off"
                className="w-full border border-iron bg-background px-2 py-1.5 pr-7 text-xs font-mono text-foreground placeholder:text-iron focus:border-amber focus:outline-none"
              />
              <ChevronDown className="pointer-events-none absolute right-2 top-1/2 h-3 w-3 -translate-y-1/2 text-slate-text" />
              {modelSearch === "__show__" && picks.length > 0 && (
                <div className="absolute z-20 mt-1 w-full max-h-48 overflow-y-auto border border-iron bg-charcoal shadow-lg">
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
        {provider === "azure" && (
          <p className="text-[9px] font-mono text-slate-text/70 mt-1">
            {"Enter your Azure deployment name (must match exactly)"}
          </p>
        )}

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
              className="w-full border border-iron bg-background px-2 py-1.5 text-xs font-mono text-foreground placeholder:text-iron/50 focus:border-amber focus:outline-none"
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
            className="w-full border border-iron bg-background px-2 py-1.5 text-xs font-mono text-foreground focus:border-amber focus:outline-none"
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
          className="flex items-center gap-2 rounded border border-amber/30 bg-amber/10 px-3 py-1 text-[11px] font-mono text-amber hover:bg-amber/20 transition-colors disabled:opacity-50 cursor-pointer disabled:cursor-not-allowed"
        >
          <Save className="h-3 w-3" />
          {upsert.isPending ? "Saving..." : "Save"}
        </button>
        <button
          type="button"
          onClick={handleTest}
          disabled={test.isPending || !finalModel}
          className="flex items-center gap-2 bg-charcoal border border-iron px-3 py-1 text-[11px] font-mono text-slate-text hover:text-foreground hover:border-foreground/30 transition-colors disabled:opacity-50 cursor-pointer disabled:cursor-not-allowed"
        >
          {test.isPending ? <Loader2 className="h-3 w-3 animate-spin" /> : <Zap className="h-3 w-3" />}
          {test.isPending ? "Testing..." : "Test"}
        </button>
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
    <div className="border border-iron bg-charcoal">
      <button
        type="button"
        onClick={() => {
          if (!open && !draft) setDraft(displayText);
          setOpen(!open);
        }}
        className="flex w-full items-center justify-between p-4 text-left cursor-pointer"
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
            className="w-full border border-iron bg-background px-3 py-2 text-xs font-mono text-foreground placeholder:text-iron/60 focus:border-amber focus:outline-none resize-y leading-relaxed"
          />
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={handleSave}
              disabled={upsert.isPending || !draft.trim()}
              className="flex items-center gap-2 rounded border border-amber/30 bg-amber/10 px-3 py-1 text-[11px] font-mono text-amber hover:bg-amber/20 transition-colors disabled:opacity-50 cursor-pointer disabled:cursor-not-allowed"
            >
              <Save className="h-3 w-3" />
              {upsert.isPending ? "Saving..." : "Save"}
            </button>
            {isCustom && (
              <button
                type="button"
                onClick={handleReset}
                disabled={del.isPending}
                className="flex items-center gap-2 bg-charcoal border border-iron px-3 py-1 text-[11px] font-mono text-slate-text hover:text-foreground hover:border-foreground/30 transition-colors disabled:opacity-50 cursor-pointer disabled:cursor-not-allowed"
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

/* ── Auto-review Toggle ─────────────────────────────────────────────
 *
 * Controls whether PR open/push events automatically kick off a review.
 * Off by default at both org and repo level (nil in settings JSON => false).
 * When off, the backend posts a one-shot "Trigger Argus review" checkbox
 * comment on opened PRs, with a token/cost estimate from history + live
 * diff. Clicking the checkbox fires an edited webhook and runs the review
 * under the 3/hr force-cap path.
 *
 * Not under ProGate: this is a cost/behavior control, not a premium feature.
 */
const AUTO_RUN_TOGGLE = {
  key: "auto_run",
  label: "Auto-review every PR",
  hint: "Run a review automatically on PR open and on every new commit",
  description: "When off, Argus posts a task-list checkbox on opened PRs with an estimated token / cost preview. Ticking the box runs the review on demand.",
  defaultValue: false,
} as const;

/* ── Pipeline Feature Toggles ── */

const PIPELINE_FEATURES = [
  {
    key: "deep_review",
    label: "Deep Review",
    hint: "4 specialist agents review each file in parallel",
    description: "Run 4 specialist reviewers (Bug Hunter, Security, Architecture, Regression) with lead agent coordination",
    defaultValue: false,
  },
  {
    key: "cross_file_context",
    label: "Cross-File Context",
    hint: "Traces callers, imports, and shared types across files",
    description: "Fetch related files for richer review context",
    defaultValue: true,
  },
  {
    key: "blast_radius",
    label: "Blast Radius Analysis",
    hint: "Maps downstream dependents affected by changes",
    description: "Trace dependency impact via code graph. Finds how changes affect downstream callers.",
    defaultValue: true,
  },
  {
    key: "simulation",
    label: "Simulation & Scenarios",
    hint: "Tests known risk scenarios against the PR",
    description: "Run stored scenarios against the diff to predict breakage. Requires Deep Review.",
    defaultValue: false,
    requiresDeepReview: true,
  },
  {
    key: "pr_enrichment",
    label: "PR Enrichment",
    hint: "Auto-enriches PR descriptions with missing context and diagrams",
    description: "Auto-enriches PR descriptions with missing context and architecture diagrams (sequence, data flow, dependency).",
    defaultValue: true,
  },
  {
    key: "learn_patterns",
    label: "Pattern Learning",
    hint: "Learns reusable patterns from high-confidence findings",
    description: "Learn recurring code patterns from review feedback to improve future reviews.",
    defaultValue: true,
  },
  {
    key: "learn_conventions",
    label: "Convention Learning",
    hint: "Extracts codebase conventions from code diffs",
    description: "Learn team coding conventions from approved PRs and apply them in reviews.",
    defaultValue: true,
  },
  {
    key: "file_synthesis",
    label: "File Synthesis",
    hint: "Creates per-file institutional memory summaries",
    description: "Combine per-file reviews into a unified PR summary with cross-cutting insights.",
    defaultValue: true,
  },
  {
    key: "architecture_graph",
    label: "Architecture Graph",
    hint: "Extracts dependency graph from code changes",
    description: "Build and maintain a dependency graph from reviewed code for blast radius analysis.",
    defaultValue: true,
  },
] as const;

type ToggleDef = {
  key: string;
  label: string;
  hint: string;
  description: string;
  defaultValue: boolean;
  requiresDeepReview?: boolean;
};

function PipelineFeatureCard({
  toggle,
  enabled,
  onToggle,
  pending,
  disabled,
}: {
  toggle: ToggleDef;
  enabled: boolean;
  onToggle: () => void;
  pending: boolean;
  disabled?: boolean;
}) {
  const isDisabled = disabled || pending;
  return (
    <div className={`border p-4 transition-colors ${
      disabled ? "border-iron/50 bg-charcoal/50 opacity-60" : enabled ? "border-amber/30 bg-amber/5" : "border-iron bg-charcoal"
    }`}>
      <div className="flex items-center justify-between mb-1.5">
        <div className="flex flex-col gap-0.5">
          <span className={`text-xs font-mono font-medium ${disabled ? "text-slate-text/60" : enabled ? "text-amber" : "text-foreground"}`}>
            {toggle.label}
          </span>
          <span className="text-[11px] font-mono text-slate-text">{toggle.hint}</span>
        </div>
        <button
          type="button"
          onClick={onToggle}
          disabled={isDisabled}
          aria-label={enabled ? `Disable ${toggle.label}` : `Enable ${toggle.label}`}
          className={`relative inline-flex h-6 w-11 shrink-0 cursor-pointer rounded-full border-2 transition-colors duration-200 ease-in-out focus:outline-none focus-visible:ring-2 focus-visible:ring-amber/50 focus-visible:ring-offset-2 focus-visible:ring-offset-background disabled:cursor-not-allowed ${
            enabled && !disabled ? "border-amber bg-amber" : "border-iron bg-iron/50"
          }`}
        >
          <span
            className={`pointer-events-none inline-block h-5 w-5 rounded-full bg-foreground shadow-lg ring-0 transition-transform duration-200 ease-in-out ${
              enabled && !disabled ? "translate-x-5" : "translate-x-0"
            }`}
          />
        </button>
      </div>
      <p className="text-[11px] font-mono text-slate-text leading-relaxed">
        {toggle.description}
      </p>
      {disabled && "requiresDeepReview" in toggle && toggle.requiresDeepReview && (
        <p className="text-[9px] font-mono text-amber/60 mt-1.5 flex items-center gap-1">
          <Lock className="h-2.5 w-2.5" />
          Requires Deep Review to be enabled
        </p>
      )}
    </div>
  );
}

/**
 * Toggle card for feature-flag-backed verification workers (issue acceptance,
 * cross-PR compatibility). Mirrors PipelineFeatureCard's visual language so
 * users perceive them as part of the same control panel — but this one wires
 * to the feature_flags JSONB column instead of default_settings.
 */
function VerificationToggle({
  label,
  hint,
  description,
  cost,
  enabled,
  pending,
  onToggle,
}: {
  label: string;
  hint: string;
  description: string;
  cost: string;
  enabled: boolean;
  pending: boolean;
  onToggle: () => void;
}) {
  return (
    <div className={`border p-4 transition-colors ${
      enabled ? "border-amber/30 bg-amber/5" : "border-iron bg-charcoal"
    }`}>
      <div className="flex items-center justify-between mb-1.5">
        <div className="flex flex-col gap-0.5">
          <span className={`text-xs font-mono font-medium ${enabled ? "text-amber" : "text-foreground"}`}>
            {label}
          </span>
          <span className="text-[11px] font-mono text-slate-text">{hint}</span>
        </div>
        <button
          type="button"
          onClick={onToggle}
          disabled={pending}
          aria-label={enabled ? `Disable ${label}` : `Enable ${label}`}
          className={`relative inline-flex h-6 w-11 shrink-0 cursor-pointer rounded-full border-2 transition-colors duration-200 ease-in-out focus:outline-none focus-visible:ring-2 focus-visible:ring-amber/50 focus-visible:ring-offset-2 focus-visible:ring-offset-background disabled:cursor-not-allowed ${
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
      <p className="text-[11px] font-mono text-slate-text leading-relaxed mb-2">
        {description}
      </p>
      <p className="text-[10px] font-mono text-slate-text/60 uppercase tracking-wide">
        {cost}
      </p>
    </div>
  );
}

/* ── Page ── */

export default function SettingsPage() {
  const { repos, activeId, setSelectedId, isLoading: reposLoading } = useActiveRepo();
  const { active } = useInstallation();

  // Honor `?repo=<id>` deep links so the onboarding comment posted on a PR lands
  // the user on the right repo row. Falls through to the localStorage-backed
  // `useActiveRepo()` default when the param is absent or invalid.
  //
  // Apply exactly once per mount via a ref guard: `setSelectedId` from the
  // provider is not memoized, so including it in deps would re-fire the effect
  // on every render and override manual dropdown selections. After applying,
  // strip the `repo` param from the URL so refreshes and later dropdown picks
  // don't get reverted back to the deep-linked id.
  const searchParams = useSearchParams();
  const router = useRouter();
  const pathname = usePathname();
  const deepLinkApplied = useRef(false);
  useEffect(() => {
    if (deepLinkApplied.current) return;
    const raw = searchParams.get("repo");
    if (!raw) return;
    const id = Number(raw);
    if (!Number.isFinite(id) || id <= 0) return;
    deepLinkApplied.current = true;
    setSelectedId(id);
    const next = new URLSearchParams(searchParams.toString());
    next.delete("repo");
    const qs = next.toString();
    router.replace(qs ? `${pathname}?${qs}` : pathname, { scroll: false });
  }, [searchParams, setSelectedId, router, pathname]);

  const { data: configs, isLoading: configsLoading } = useModelConfigs(activeId);
  const { data: providerKeys, isLoading: keysLoading } = useProviderKeys();
  const { data: customPrompts } = usePrompts(activeId);
  const { data: defaultPrompts } = useDefaultPrompts();
  const updateRepo = useUpdateRepo();

  const [personaError, setPersonaError] = useState("");
  const [settingsScope, setSettingsScope] = useState<"org" | "repo" | "branches">("repo");

  // Org defaults
  const { data: orgDefaults, isLoading: orgDefaultsLoading } = useOrgDefaults();
  const saveOrgDefaults = useSaveOrgDefaults();
  const { data: featureFlags } = useFeatureFlags();
  const saveFeatureFlags = useSaveFeatureFlags();

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
  const [orgCustomPromptDraft, setOrgCustomPromptDraft] = useState(orgCustomPrompt);

  const loading = reposLoading || keysLoading || (activeId > 0 && configsLoading);
  const configMap = new Map(configs?.map((c) => [c.stage, c]));
  const savedProviders = providerKeys?.map((k) => k.provider) ?? [];
  const configuredCount = savedProviders.length;

  return (
    <>
      <div className="mb-6 flex items-center justify-between">
        <div>
          <h1 className="font-mono text-2xl font-bold text-foreground">
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
          className={`px-4 py-3 text-xs font-mono transition-colors border-b-2 -mb-px cursor-pointer ${
            settingsScope === "org"
              ? "border-amber text-amber"
              : "border-transparent text-slate-text hover:text-foreground"
          }`}
        >
          Org Defaults
        </button>
        <button
          onClick={() => setSettingsScope("repo")}
          className={`px-4 py-3 text-xs font-mono transition-colors border-b-2 -mb-px cursor-pointer ${
            settingsScope === "repo"
              ? "border-amber text-amber"
              : "border-transparent text-slate-text hover:text-foreground"
          }`}
        >
          Repo Overrides
        </button>
        <button
          type="button"
          onClick={() => setSettingsScope("branches")}
          className={`px-4 py-3 text-xs font-mono transition-colors border-b-2 -mb-px cursor-pointer ${
            settingsScope === "branches"
              ? "border-amber text-amber"
              : "border-transparent text-slate-text hover:text-foreground"
          }`}
        >
          Branch Filters
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
              <div className="border border-amber/20 bg-amber/5 px-4 py-3 flex items-start gap-2.5">
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
                    <h2 className="font-mono text-lg font-semibold text-foreground">
                      Default Persona
                    </h2>
                  </div>
                </div>
                <div className="grid gap-3 grid-cols-1 sm:grid-cols-2 lg:grid-cols-3">
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
                  <div className="mt-4 border border-iron bg-charcoal p-4">
                    <label className="block text-[11px] font-mono text-slate-text mb-2">
                      Custom persona prompt (org default)
                    </label>
                    <textarea
                      value={orgCustomPromptDraft}
                      onChange={(e) => setOrgCustomPromptDraft(e.target.value)}
                      placeholder="e.g. You are a reviewer focused on accessibility and i18n…"
                      rows={5}
                      className="w-full border border-iron bg-void px-4 py-3 text-xs font-mono text-foreground placeholder:text-slate-text/40 focus:outline-none focus:border-amber/50 transition-colors resize-y"
                    />
                    <button
                      type="button"
                      disabled={saveOrgDefaults.isPending || orgCustomPromptDraft === orgCustomPrompt}
                      onClick={() => {
                        saveOrgDefaults.mutate({ ...orgDefaults, persona: "custom", custom_persona_prompt: orgCustomPromptDraft });
                      }}
                      className="mt-2 rounded border border-amber/30 bg-amber/10 px-3 py-1.5 text-[11px] font-mono font-medium text-amber hover:bg-amber/20 disabled:opacity-50 disabled:cursor-not-allowed transition-colors cursor-pointer"
                    >
                      {saveOrgDefaults.isPending ? "Saving..." : "Save Custom Persona"}
                    </button>
                  </div>
                )}
              </section>

              {/* Org: Review Pipeline */}
              <section>
                <div className="flex items-center gap-3 mb-4">
                  <div className="flex items-center gap-2">
                    <Cpu className="h-4 w-4 text-amber" />
                    <h2 className="font-mono text-lg font-semibold text-foreground">
                      Review Pipeline
                    </h2>
                  </div>
                </div>
                <p className="text-xs font-mono text-slate-text mb-4">
                  Configure the default model for each review stage. Applies to all repos unless overridden.
                </p>

                {configuredCount === 0 ? (
                  <div className="border border-iron/50 bg-iron/10 px-4 py-3 flex items-start gap-2.5">
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

              {/* Org: Auto-review (not pro-gated — cost control) */}
              <section>
                <div className="flex items-center gap-3 mb-4">
                  <div className="flex items-center gap-2">
                    <Zap className="h-4 w-4 text-amber" />
                    <h2 className="font-mono text-lg font-semibold text-foreground">
                      Auto-review
                    </h2>
                  </div>
                </div>
                <p className="text-xs font-mono text-slate-text mb-4">
                  Default for all repos in {active?.org_login}. Turn off to require opt-in per PR via a checkbox comment with cost preview.
                </p>
                <PipelineFeatureCard
                  toggle={AUTO_RUN_TOGGLE}
                  enabled={typeof orgDefaults?.auto_run === "boolean" ? orgDefaults.auto_run : AUTO_RUN_TOGGLE.defaultValue}
                  pending={saveOrgDefaults.isPending}
                  onToggle={() => {
                    const current = typeof orgDefaults?.auto_run === "boolean" ? orgDefaults.auto_run : AUTO_RUN_TOGGLE.defaultValue;
                    saveOrgDefaults.mutate({ ...orgDefaults, auto_run: !current });
                  }}
                />
              </section>

              {/* Org: Pipeline Features */}
              <ProGate feature="Pipeline features">
                <section>
                  <div className="flex items-center gap-3 mb-4">
                    <div className="flex items-center gap-2">
                      <Sliders className="h-4 w-4 text-amber" />
                      <h2 className="font-mono text-lg font-semibold text-foreground">
                        Pipeline Features
                      </h2>
                    </div>
                  </div>
                  <div className="grid gap-3 md:grid-cols-2">
                    {PIPELINE_FEATURES.map((toggle) => {
                      const isSimulation = toggle.key === "simulation";
                      const orgDrVal = orgDefaults?.["deep_review"];
                      const orgDrEnabled = typeof orgDrVal === "boolean" ? orgDrVal : false;

                      if (isSimulation) {
                        const smVal = orgDefaults?.["scenario_memory"];
                        const csVal = orgDefaults?.["code_simulation"];
                        const enabled = (typeof smVal === "boolean" ? smVal : false) && (typeof csVal === "boolean" ? csVal : false);
                        return (
                          <PipelineFeatureCard
                            key={toggle.key}
                            toggle={toggle}
                            enabled={enabled}
                            disabled={!orgDrEnabled}
                            pending={saveOrgDefaults.isPending}
                            onToggle={() => {
                              saveOrgDefaults.mutate({ ...orgDefaults, scenario_memory: !enabled, code_simulation: !enabled });
                            }}
                          />
                        );
                      }

                      const val = orgDefaults?.[toggle.key];
                      const enabled = typeof val === "boolean" ? val : toggle.defaultValue;
                      return (
                        <PipelineFeatureCard
                          key={toggle.key}
                          toggle={toggle}
                          enabled={enabled}
                          pending={saveOrgDefaults.isPending}
                          onToggle={() => {
                            const updates: Record<string, unknown> = { ...orgDefaults, [toggle.key]: !enabled };
                            if (toggle.key === "deep_review" && enabled) {
                              updates.scenario_memory = false;
                              updates.code_simulation = false;
                            }
                            saveOrgDefaults.mutate(updates);
                          }}
                        />
                      );
                    })}
                  </div>
                </section>
              </ProGate>

              {/* Org: Verification Features (issue acceptance + cross-repo PR) */}
              <ProGate feature="Verification features">
                <section>
                  <div className="flex items-center gap-3 mb-4">
                    <div className="flex items-center gap-2">
                      <Sliders className="h-4 w-4 text-amber" />
                      <h2 className="font-mono text-lg font-semibold text-foreground">
                        Verification Features
                      </h2>
                    </div>
                    <span className="text-[11px] font-mono text-slate-text">
                      Per-installation toggles for optional review passes
                    </span>
                  </div>
                  <div className="grid gap-3 md:grid-cols-2">
                    <VerificationToggle
                      label="Issue acceptance check"
                      hint="Verify PRs against linked issue acceptance criteria"
                      description="Auto-detects linked issues via GitHub's closingIssuesReferences (PR body `Closes #N` OR the Development UI panel). Extracts criteria from issue body and uses an LLM judge to classify each criterion as addressed/partial/unaddressed/ambiguous."
                      cost="+1 LLM call per linked issue (~1-2k tokens)"
                      enabled={featureFlags?.issue_acceptance ?? true}
                      pending={saveFeatureFlags.isPending}
                      onToggle={() => {
                        const next: FeatureFlags = {
                          issue_acceptance: !(featureFlags?.issue_acceptance ?? true),
                          cross_pr_checks: featureFlags?.cross_pr_checks ?? false,
                          max_linked_prs: featureFlags?.max_linked_prs ?? 5,
                        };
                        saveFeatureFlags.mutate(next);
                      }}
                    />
                    <VerificationToggle
                      label="Cross-repo PR checks"
                      hint="Check compatibility with linked PRs from other repos"
                      description="Auto-detects GitHub PR URLs in your PR body (up to max_linked_prs). Fetches accessible peer PR diffs and uses an LLM to check for API contract / shared type incompatibilities. Inaccessible repos are noted as 'partial coverage' without severity impact."
                      cost="+1 LLM call per review (skipped if no linked PRs)"
                      enabled={featureFlags?.cross_pr_checks ?? false}
                      pending={saveFeatureFlags.isPending}
                      onToggle={() => {
                        const next: FeatureFlags = {
                          issue_acceptance: featureFlags?.issue_acceptance ?? true,
                          cross_pr_checks: !(featureFlags?.cross_pr_checks ?? false),
                          max_linked_prs: featureFlags?.max_linked_prs ?? 5,
                        };
                        saveFeatureFlags.mutate(next);
                      }}
                    />
                  </div>
                  <div className="mt-3 flex items-center gap-3 text-[11px] font-mono text-slate-text">
                    <label htmlFor="max-linked-prs">Max linked PRs per review:</label>
                    <input
                      id="max-linked-prs"
                      type="number"
                      min={1}
                      max={20}
                      value={featureFlags?.max_linked_prs ?? 5}
                      onChange={(e) => {
                        const n = parseInt(e.target.value, 10);
                        if (isNaN(n) || n < 1 || n > 20) return;
                        saveFeatureFlags.mutate({
                          issue_acceptance: featureFlags?.issue_acceptance ?? true,
                          cross_pr_checks: featureFlags?.cross_pr_checks ?? false,
                          max_linked_prs: n,
                        });
                      }}
                      className="w-16 bg-charcoal border border-iron px-2 py-1 text-center text-foreground focus:border-amber focus:outline-none"
                    />
                    <span className="text-slate-text/70">bounded 1–20</span>
                  </div>
                </section>
              </ProGate>
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
        <div className="border border-iron bg-charcoal p-10 text-center">
          <Settings className="h-8 w-8 text-slate-text mx-auto mb-3" />
          <p className="text-xs font-mono text-slate-text">
            No installation found.
          </p>
        </div>
      ) : (
        <div className="space-y-10">
          <div className="border border-iron/50 bg-iron/10 px-4 py-3 flex items-start gap-2.5">
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
                <h2 className="font-mono text-lg font-semibold text-foreground">
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
                <h2 className="font-mono text-lg font-semibold text-foreground">
                  Review Pipeline
                </h2>
              </div>
            </div>

            {configuredCount === 0 ? (
              <div className="border border-amber/20 bg-amber/5 px-4 py-3 mb-4 flex items-start gap-2.5">
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
              <div className="border border-iron bg-charcoal p-10 text-center">
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
                <h2 className="font-mono text-lg font-semibold text-foreground">
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
              <div className="border border-iron bg-charcoal p-10 text-center">
                <UserCog className="h-8 w-8 text-slate-text mx-auto mb-3" />
                <p className="text-xs font-mono text-slate-text">
                  Select a repo to configure persona.
                </p>
              </div>
            ) : (
              <>
                <div className="grid gap-3 grid-cols-1 sm:grid-cols-2 lg:grid-cols-3">
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
                  <ProGate feature="Custom persona">
                    <div className="mt-4 border border-iron bg-charcoal p-4">
                      <label className="block text-[11px] font-mono text-slate-text mb-2">
                        Custom persona prompt — define how Argus should review code
                      </label>
                      <textarea
                        value={customPromptDraft}
                        onChange={(e) => setCustomPromptDraft(e.target.value)}
                        placeholder="e.g. You are a reviewer focused on accessibility and i18n. Flag any hardcoded strings, missing aria labels, or RTL layout issues…"
                        rows={5}
                        className="w-full border border-iron bg-void px-4 py-3 text-xs font-mono text-foreground placeholder:text-slate-text/40 focus:outline-none focus:border-amber/50 transition-colors resize-y"
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
                        className="mt-2 rounded border border-amber/30 bg-amber/10 px-3 py-1.5 text-[11px] font-mono font-medium text-amber hover:bg-amber/20 disabled:opacity-50 disabled:cursor-not-allowed transition-colors cursor-pointer"
                      >
                        {updateRepo.isPending ? "Saving..." : "Save Custom Persona"}
                      </button>
                    </div>
                  </ProGate>
                )}
                {personaError && (
                  <p className="text-[10px] font-mono text-red-400 mt-2">{personaError}</p>
                )}
              </>
            )}
          </section>

          {/* Section 3b: Auto-review (not pro-gated — cost control) */}
          <section>
            <div className="flex items-center gap-3 mb-4">
              <div className="flex items-center gap-2">
                <Zap className="h-4 w-4 text-amber" />
                <h2 className="font-mono text-lg font-semibold text-foreground">
                  Auto-review
                </h2>
              </div>
            </div>
            <p className="text-[11px] font-mono text-slate-text mb-4">
              Repo override for{" "}
              <span className="text-foreground">{activeRepo?.full_name ?? "selected repo"}</span>.
              When off, Argus posts a trigger checkbox on opened PRs instead of running automatically.
            </p>

            {activeId === 0 ? (
              <div className="border border-iron bg-charcoal p-10 text-center">
                <Zap className="h-8 w-8 text-slate-text mx-auto mb-3" />
                <p className="text-xs font-mono text-slate-text">
                  Select a repo to configure auto-review.
                </p>
              </div>
            ) : (
              <PipelineFeatureCard
                toggle={AUTO_RUN_TOGGLE}
                enabled={typeof activeRepo?.settings_json?.auto_run === "boolean"
                  ? (activeRepo.settings_json.auto_run as boolean)
                  : (typeof orgDefaults?.auto_run === "boolean" ? orgDefaults.auto_run : AUTO_RUN_TOGGLE.defaultValue)}
                pending={updateRepo.isPending}
                onToggle={() => {
                  const repoVal = typeof activeRepo?.settings_json?.auto_run === "boolean"
                    ? (activeRepo.settings_json.auto_run as boolean)
                    : (typeof orgDefaults?.auto_run === "boolean" ? orgDefaults.auto_run : AUTO_RUN_TOGGLE.defaultValue);
                  updateRepo.mutate({
                    id: activeId,
                    settings_json: { ...activeRepo?.settings_json, auto_run: !repoVal },
                  });
                }}
              />
            )}
          </section>

          {/* Section 4: Review Prompts (Pro only) */}
          <ProGate feature="Custom prompts">
            <section>
              <div className="flex items-center gap-3 mb-4">
                <span className="inline-flex items-center justify-center h-6 w-6 rounded-full border border-amber/30 bg-amber/10 text-[11px] font-mono font-bold text-amber">
                  4
                </span>
                <div className="flex items-center gap-2">
                  <FileText className="h-4 w-4 text-amber" />
                  <h2 className="font-mono text-lg font-semibold text-foreground">
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
                <div className="border border-iron bg-charcoal p-10 text-center">
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
          </ProGate>

          {/* Section 5: Pipeline Features (Pro only) */}
          <ProGate feature="Pipeline features (deep review, cross-file, blast radius, simulation)">
            <section>
              <div className="flex items-center gap-3 mb-4">
                <span className="inline-flex items-center justify-center h-6 w-6 rounded-full border border-amber/30 bg-amber/10 text-[11px] font-mono font-bold text-amber">
                  5
                </span>
                <div className="flex items-center gap-2">
                  <Sliders className="h-4 w-4 text-amber" />
                  <h2 className="font-mono text-lg font-semibold text-foreground">
                    Pipeline Features
                  </h2>
                </div>
              </div>
              <p className="text-[11px] font-mono text-slate-text mb-4">
                Toggle pipeline capabilities for{" "}
                <span className="text-foreground">{activeRepo?.full_name ?? "selected repo"}</span>.
                These control which analysis stages run during review.
              </p>

              {activeId === 0 ? (
                <div className="border border-iron bg-charcoal p-10 text-center">
                  <Sliders className="h-8 w-8 text-slate-text mx-auto mb-3" />
                  <p className="text-xs font-mono text-slate-text">
                    Select a repo to configure features.
                  </p>
                </div>
              ) : (
                <div className="grid gap-3 md:grid-cols-2">
                  {PIPELINE_FEATURES.map((toggle) => {
                    const isSimulation = toggle.key === "simulation";
                    const drEnabled = deepReview;

                    if (isSimulation) {
                      const smVal = activeRepo?.settings_json?.["scenario_memory"];
                      const csVal = activeRepo?.settings_json?.["code_simulation"];
                      const enabled = (typeof smVal === "boolean" ? smVal : false) && (typeof csVal === "boolean" ? csVal : false);
                      return (
                        <PipelineFeatureCard
                          key={toggle.key}
                          toggle={toggle}
                          enabled={enabled}
                          disabled={!drEnabled}
                          pending={updateRepo.isPending}
                          onToggle={() => {
                            updateRepo.mutate({
                              id: activeId,
                              settings_json: { ...activeRepo?.settings_json, scenario_memory: !enabled, code_simulation: !enabled },
                            });
                          }}
                        />
                      );
                    }

                    const val = activeRepo?.settings_json?.[toggle.key];
                    const enabled = typeof val === "boolean" ? val : toggle.defaultValue;
                    return (
                      <PipelineFeatureCard
                        key={toggle.key}
                        toggle={toggle}
                        enabled={enabled}
                        pending={updateRepo.isPending}
                        onToggle={() => {
                          const newSettings: Record<string, unknown> = { ...activeRepo?.settings_json, [toggle.key]: !enabled };
                          if (toggle.key === "deep_review" && enabled) {
                            newSettings.scenario_memory = false;
                            newSettings.code_simulation = false;
                          }
                          updateRepo.mutate({ id: activeId, settings_json: newSettings });
                        }}
                      />
                    );
                  })}
                </div>
              )}
            </section>
          </ProGate>

        </div>
      ))}

      {/* Branch Filters Tab */}
      {settingsScope === "branches" && (
        <div className="space-y-6">
          <div className="border border-amber/20 bg-amber/5 px-4 py-3 flex items-start gap-2.5">
            <Info className="h-3.5 w-3.5 text-amber mt-0.5 shrink-0" />
            <p className="text-[11px] font-mono text-amber/80">
              Skip reviews for PRs targeting these base branches. Supports glob patterns (e.g. <code className="text-amber">release/*</code>).
            </p>
          </div>

          {repos.length === 0 ? (
            <div className="py-10 text-center">
              <p className="text-sm font-mono text-slate-text">No repos yet. Sync repos first.</p>
            </div>
          ) : (
            repos.filter(r => r.enabled).map(repo => {
              const settings = (repo.settings_json ?? {}) as Record<string, unknown>;
              const raw = settings.skip_base_branches;
              const skipBranches = Array.isArray(raw) ? raw.filter((v): v is string => typeof v === "string") : [];

              return (
                <div key={repo.id} className="border border-iron p-4">
                  <div className="flex items-center justify-between mb-3">
                    <span className="text-xs font-mono text-foreground">{repo.full_name}</span>
                    <span className="text-[10px] font-mono text-slate-text">{repo.default_branch}</span>
                  </div>
                  <div className="flex flex-wrap gap-1.5 items-center">
                    {skipBranches.map((branch, i) => (
                      <span
                        key={i}
                        className="inline-flex items-center gap-1 bg-charcoal border border-iron px-2 py-0.5 text-[11px] font-mono text-slate-text"
                      >
                        {branch}
                        <button
                          type="button"
                          disabled={updateRepo.isPending}
                          onClick={() => {
                            const updated = skipBranches.filter((_, j) => j !== i);
                            updateRepo.mutate({
                              id: repo.id,
                              settings_json: { ...settings, skip_base_branches: updated },
                            });
                          }}
                          className="text-slate-text/50 hover:text-red-400 ml-0.5 cursor-pointer disabled:opacity-30"
                        >
                          &times;
                        </button>
                      </span>
                    ))}
                    <form
                      className="inline-flex"
                      onSubmit={(e) => {
                        e.preventDefault();
                        const input = e.currentTarget.querySelector("input") as HTMLInputElement;
                        const val = input.value.trim();
                        if (!val || skipBranches.includes(val)) return;
                        updateRepo.mutate({
                          id: repo.id,
                          settings_json: { ...settings, skip_base_branches: [...skipBranches, val] },
                        });
                        input.value = "";
                      }}
                    >
                      <input
                        type="text"
                        placeholder="+ add branch"
                        className="bg-transparent border border-dashed border-iron/50 px-2 py-0.5 text-[11px] font-mono text-foreground placeholder:text-slate-text/30 w-28 focus:border-amber focus:outline-none"
                      />
                    </form>
                  </div>
                  {skipBranches.length === 0 && (
                    <p className="text-[10px] font-mono text-slate-text/50 mt-2">All branches reviewed</p>
                  )}
                </div>
              );
            })
          )}
        </div>
      )}
    </>
  );
}
