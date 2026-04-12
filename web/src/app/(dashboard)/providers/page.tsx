"use client";

import { memo, useMemo, useState } from "react";
import { Key, Save, Trash2, Loader2 } from "lucide-react";
import {
  useProviderKeys,
  useUpsertProviderKey,
  useDeleteProviderKey,
} from "@/lib/queries/provider-keys";
import { useInstallation } from "@/providers/installation-provider";
import type { ProviderKey } from "@/lib/types";

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

const PROVIDER_BASE_URLS: Record<Provider, string> = {
  openrouter: "https://openrouter.ai/api/v1",
  openai: "https://api.openai.com/v1",
  anthropic: "https://api.anthropic.com/v1",
  fireworks: "https://api.fireworks.ai/inference/v1",
  groq: "https://api.groq.com/openai/v1",
  together: "https://api.together.xyz/v1",
  deepseek: "https://api.deepseek.com/v1",
  azure: "",
  gcp_vertex: "",
  aws_bedrock: "",
  zhipu: "https://api.z.ai/api/paas/v4",
};

type BadgeVariant = "active" | "inactive";

const BADGE_STYLES: Record<BadgeVariant, string> = {
  active: "border-green-400/20 bg-green-400/10 text-green-400",
  inactive: "border-iron bg-iron/30 text-slate-text/60",
};

function StatusBadge({ variant, label }: { variant: BadgeVariant; label: string }) {
  return (
    <span className={`inline-flex items-center rounded-sm border px-1.5 py-0.5 text-[9px] font-mono uppercase tracking-wider ${BADGE_STYLES[variant]}`}>
      {label}
    </span>
  );
}

const ProviderKeyCard = memo(function ProviderKeyCard({
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
    <div className="border border-iron bg-charcoal p-5">
      <div className="flex items-center justify-between mb-3">
        <div className="flex items-center gap-2">
          <Key className="h-3.5 w-3.5 text-amber" />
          <span className="text-xs font-mono font-medium text-foreground">
            {PROVIDER_LABELS[provider]}
          </span>
        </div>
        <StatusBadge variant={existing ? "active" : "inactive"} label={existing ? "Active" : "Not configured"} />
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
            className="w-full border border-iron bg-background px-2 py-1.5 text-xs font-mono text-foreground placeholder:text-iron focus:border-amber focus:outline-none"
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
            className="w-full border border-iron bg-background px-2 py-1.5 text-xs font-mono text-foreground placeholder:text-iron focus:border-amber focus:outline-none"
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
});

export default function ProvidersPage() {
  const { active } = useInstallation();
  const { data: providerKeys, isLoading } = useProviderKeys();

  const keyMap = useMemo(
    () => new Map(providerKeys?.map((k) => [k.provider, k]) ?? []),
    [providerKeys],
  );
  const configuredCount = providerKeys?.length ?? 0;

  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-20">
        <Loader2 className="h-6 w-6 animate-spin text-slate-text" />
      </div>
    );
  }

  return (
    <>
      <div className="mb-6">
        <h1 className="font-mono text-2xl font-bold text-foreground">
          Providers
        </h1>
        <p className="text-xs font-mono text-slate-text mt-1">
          API keys for LLM providers. Keys are encrypted at rest with AES-256-GCM.
        </p>
      </div>

      <div className="flex items-center gap-3 mb-4">
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
      <p className="text-[11px] font-mono text-slate-text mb-4">
        Bring your own API keys. Keys are scoped to <span className="text-foreground">{active?.org_login ?? "your org"}</span>.
        Providers configured here become available for model selection in settings.
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
    </>
  );
}
