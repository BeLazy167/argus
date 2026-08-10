import { Check, ChevronDown, Loader2, Save, X, Zap } from "lucide-react";
import { useState } from "react";
import { IntegrationStatusBadge } from "@/components/dashboard/integration-status-badge";
import {
  type TestResult,
  useDeleteModelConfig,
  useTestConfig,
  useUpsertModelConfig,
} from "@/lib/queries/model-configs";
import { useOpenRouterModels } from "@/lib/queries/openrouter-models";
import {
  MODEL_PICKS,
  PROVIDER_LABELS,
  PROVIDERS,
  type Provider,
  STAGE_DESCRIPTIONS,
} from "./constants";
import { ModelSelect } from "./model-select";

type ConfigData = {
  stage: string;
  provider: string;
  model: string;
  base_url?: string;
  max_tokens: number;
  temperature: number;
};

/**
 * Cloud-provider endpoint override fields (Azure / GCP Vertex / Bedrock).
 *
 * Extracted from {@link ConfigCard} to keep that component's render flat. Only
 * the three providers that require a custom OpenAI-compatible endpoint show
 * this block; everything else renders nothing.
 */
function ProviderEndpointFields({
  provider,
  baseURL,
  setBaseURL,
}: {
  provider: string;
  baseURL: string;
  setBaseURL: (v: string) => void;
}) {
  if (provider !== "azure" && provider !== "gcp_vertex" && provider !== "aws_bedrock") {
    return null;
  }
  return (
    <div className="col-span-2">
      <label className="block text-[10px] font-mono text-slate-text mb-1">
        Endpoint URL <span className="text-amber">*</span>
      </label>
      <input
        type="text"
        value={baseURL}
        onChange={(e) => setBaseURL(e.target.value)}
        placeholder={
          provider === "azure"
            ? "https://{resource}.openai.azure.com/openai"
            : provider === "gcp_vertex"
              ? "https://{region}-aiplatform.googleapis.com/v1/projects/{project}/locations/{region}/endpoints/openapi"
              : "https://bedrock-runtime.{region}.amazonaws.com"
        }
        className="w-full border border-iron bg-background px-2 py-1.5 text-xs font-mono text-foreground placeholder:text-iron/50 focus:border-amber focus:outline-none"
      />
      <p className="text-[9px] font-mono text-slate-text/50 mt-1">
        {provider === "azure" && "Azure OpenAI resource endpoint"}
        {provider === "gcp_vertex" && "Vertex AI OpenAI-compatible endpoint"}
        {provider === "aws_bedrock" && "Bedrock runtime endpoint"}
      </p>
    </div>
  );
}

export function ConfigCard({
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
  existing?: {
    provider: string;
    model: string;
    base_url?: string;
    max_tokens: number;
    temperature: number;
  };
  savedProviders: string[];
  installationId?: number;
  onSave?: (data: ConfigData) => void;
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
  const { data: orModels } = useOpenRouterModels({
    variables: { installationId: isOpenRouter ? installationId : undefined },
    enabled: isOpenRouter && !!installationId,
  });

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
        onError: (err) =>
          setTestResult({
            success: false,
            error: err instanceof Error ? err.message : "Test failed",
            latency_ms: 0,
          }),
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
          <span className="text-xs font-mono uppercase tracking-wider text-amber">{stage}</span>
          <IntegrationStatusBadge
            variant={existing ? "configured" : "inactive"}
            label={existing ? "Configured" : "Not set"}
          />
        </div>
        {existing && (
          <button
            type="button"
            onClick={() => (onDelete ? onDelete(stage) : del.mutate({ repoId, stage }))}
            className="text-[11px] font-mono text-slate-text hover:text-red-400 transition-colors cursor-pointer"
          >
            Reset
          </button>
        )}
      </div>
      <p className="text-[11px] font-mono text-slate-text mb-3">{STAGE_DESCRIPTIONS[stage]}</p>

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
          <label className="block text-[10px] font-mono text-slate-text mb-1">Provider</label>
          <div className="relative">
            <select
              value={provider}
              onChange={(e) => {
                setProvider(e.target.value);
                setModel("");
                setIsCustom(false);
              }}
              style={{ backgroundColor: "var(--background)", color: "var(--foreground)" }}
              className="w-full appearance-none border border-iron bg-background px-2 py-1.5 pr-7 text-xs font-mono text-foreground focus:border-amber focus:outline-none"
            >
              <option value="">{hasNoKeys ? "Add an API key first" : "Select provider"}</option>
              {PROVIDERS.map((p) => {
                const hasKey = savedProviders.includes(p);
                return (
                  <option key={p} value={p} disabled={!hasKey}>
                    {PROVIDER_LABELS[p]}
                    {hasKey ? "" : " — no API key"}
                  </option>
                );
              })}
            </select>
            <ChevronDown className="pointer-events-none absolute right-2 top-1/2 h-3 w-3 -translate-y-1/2 text-slate-text" />
          </div>
        </div>

        {/* Model dropdown */}
        <div>
          <label className="block text-[10px] font-mono text-slate-text mb-1">Model</label>
          <ModelSelect
            provider={provider}
            model={model}
            customModel={customModel}
            isCustom={isCustom}
            modelSearch={modelSearch}
            orModels={orModels}
            picks={picks}
            setModel={setModel}
            setCustomModel={setCustomModel}
            setModelSearch={setModelSearch}
            onModelSelect={handleModelSelect}
          />
        </div>
        {provider === "azure" && (
          <p className="text-[9px] font-mono text-slate-text/70 mt-1">
            {"Enter your Azure deployment name (must match exactly)"}
          </p>
        )}

        {/* Base URL override — shown for providers that need custom endpoints */}
        <ProviderEndpointFields provider={provider} baseURL={baseURL} setBaseURL={setBaseURL} />

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
          <label className="block text-[10px] font-mono text-slate-text mb-1">Max tokens</label>
          <input
            type="number"
            value={maxTokens}
            onChange={(e) => setMaxTokens(Number(e.target.value))}
            className="w-full border border-iron bg-background px-2 py-1.5 text-xs font-mono text-foreground focus:border-amber focus:outline-none"
          />
        </div>
      </div>

      {error && <p className="text-[10px] font-mono text-red-400 mb-2">{error}</p>}

      {testResult && (
        <div
          className={`rounded border px-3 py-2 mb-2 text-[10px] font-mono ${testResult.success ? "border-green-400/30 bg-green-400/5 text-green-400" : "border-red-400/30 bg-red-400/5 text-red-400"}`}
        >
          <div className="flex items-center gap-1.5">
            {testResult.success ? <Check className="h-3 w-3" /> : <X className="h-3 w-3" />}
            <span className="font-medium">
              {testResult.success ? "Connection OK" : "Connection failed"}
            </span>
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
          {test.isPending ? (
            <Loader2 className="h-3 w-3 animate-spin" />
          ) : (
            <Zap className="h-3 w-3" />
          )}
          {test.isPending ? "Testing..." : "Test"}
        </button>
      </div>
    </div>
  );
}
