"use client";

import { Brain, Key, Loader2, Save, Trash2 } from "lucide-react";
import { memo, useMemo, useState } from "react";
import {
	useDeleteProviderKey,
	useProviderKeys,
	useUpsertProviderKey,
} from "@/lib/queries/provider-keys";
import type { ProviderKey } from "@/lib/types";
import { useInstallation } from "@/providers/installation-provider";
import { EmbeddingsCard } from "./embeddings-card";
import { JevCard } from "./jev-card";
import { StatusBadge } from "./status-badge";

const PROVIDERS = [
	"openrouter",
	"openai",
	"anthropic",
	"fireworks",
	"groq",
	"together",
	"deepseek",
	"azure",
	"gcp_vertex",
	"aws_bedrock",
	"zhipu",
	"vercel",
] as const;
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
	vercel: "Vercel AI Gateway",
};

const PROVIDER_BASE_URLS: Record<Provider, string> = {
	openrouter: "https://openrouter.ai/api/v1",
	openai: "https://api.openai.com/v1",
	anthropic: "https://api.anthropic.com/v1",
	fireworks: "https://api.fireworks.ai/inference/v1",
	groq: "https://api.groq.com/openai/v1",
	together: "https://api.together.xyz/v1",
	deepseek: "https://api.deepseek.com/v1",
	azure: "https://{resource}.openai.azure.com/openai",
	gcp_vertex: "",
	aws_bedrock: "",
	zhipu: "https://api.z.ai/api/paas/v4",
	vercel: "https://ai-gateway.vercel.sh/v1?only=azure",
};

const CLOUD_PROVIDERS: Set<Provider> = new Set(["azure", "gcp_vertex", "aws_bedrock"]);

const ProviderKeyCard = memo(function ProviderKeyCard({
	provider,
	existing,
}: {
	provider: Provider;
	existing?: ProviderKey;
}) {
	const [apiKey, setApiKey] = useState("");
	const [baseUrl, setBaseUrl] = useState(existing?.base_url ?? "");
	const [error, setError] = useState("");
	const upsert = useUpsertProviderKey();
	const del = useDeleteProviderKey();
	const busy = upsert.isPending || del.isPending;

	const handleSave = () => {
		// The API requires a key on every save (blank-key updates are an
		// embeddings-only path), so a stored row still needs the key re-entered.
		if (!apiKey.trim()) return;
		upsert.mutate(
			{
				provider,
				api_key: apiKey,
				base_url: baseUrl || undefined,
			},
			{
				onSuccess: () => {
					setApiKey("");
					setError("");
				},
				onError: (err) => setError(err instanceof Error ? err.message : "Save failed"),
			},
		);
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
				<StatusBadge
					variant={existing ? "active" : "inactive"}
					label={existing ? "Active" : "Not configured"}
				/>
			</div>

			{existing && (
				<p className="text-[11px] font-mono text-slate-text mb-3">Key: {existing.api_key_masked}</p>
			)}

			<div className="space-y-2 mb-3">
				<div>
					<label
						htmlFor={`provider-key-${provider}`}
						className="block text-[10px] font-mono text-slate-text mb-1"
					>
						{provider === "gcp_vertex" ? "Access token" : existing ? "Replace API key" : "API key"}
					</label>
					<input
						id={`provider-key-${provider}`}
						type="password"
						autoComplete="off"
						value={apiKey}
						onChange={(e) => setApiKey(e.target.value)}
						placeholder={existing ? "Enter new key to replace" : "sk-..."}
						className="w-full border border-iron bg-background px-2 py-1.5 text-xs font-mono text-foreground placeholder:text-slate-text/50 focus:border-amber focus:outline-none"
					/>
				</div>
				<div>
					<label
						htmlFor={`provider-baseurl-${provider}`}
						className="block text-[10px] font-mono text-slate-text mb-1"
					>
						Base URL {CLOUD_PROVIDERS.has(provider) ? "(required)" : "(optional)"}
					</label>
					<input
						id={`provider-baseurl-${provider}`}
						type="text"
						value={baseUrl}
						onChange={(e) => setBaseUrl(e.target.value)}
						placeholder={PROVIDER_BASE_URLS[provider]}
						className="w-full border border-iron bg-background px-2 py-1.5 text-xs font-mono text-foreground placeholder:text-slate-text/50 focus:border-amber focus:outline-none"
					/>
				</div>
				{provider === "azure" && (
					<p className="text-[9px] font-mono text-slate-text/70 mt-1 leading-relaxed">
						{"OpenAI: https://<resource>.openai.azure.com/openai"}
						<br />
						{"Foundry (Claude, Llama): https://<endpoint>.inference.ai.azure.com/v1"}
						<br />
						{"Model field = deployment name"}
					</p>
				)}
				{provider === "gcp_vertex" && (
					<p className="text-[9px] font-mono text-slate-text/70 mt-1 leading-relaxed">
						{"API key = GCP access token (short-lived ~1hr). Run: gcloud auth print-access-token"}
					</p>
				)}
				{provider === "aws_bedrock" && (
					<p className="text-[9px] font-mono text-slate-text/70 mt-1 leading-relaxed">
						{"Requires IAM credentials. API key = AWS session token. Limited support."}
					</p>
				)}
				{provider === "vercel" && (
					<p className="text-[9px] font-mono text-slate-text/70 mt-1 leading-relaxed">
						{"Models use creator/model form, e.g. openai/gpt-5.6-sol"}<br/>
						{"Leave base URL blank and the gateway picks the upstream, and may"}<br/>
						{"fall back off your own credentials. Pin it with ?only=<provider>,"}<br/>
						{"comma-separated for several: .../v1?only=azure"}
					</p>
				)}
			</div>

			{error && (
				<p role="alert" className="text-[10px] font-mono text-red-400 mb-2">
					{error}
				</p>
			)}

			<div className="flex items-center gap-2">
				<button
					type="button"
					onClick={handleSave}
					disabled={busy || !apiKey.trim()}
					className="flex items-center gap-2 border border-amber/30 bg-amber/10 px-3 py-1 text-[11px] font-mono text-amber hover:bg-amber/20 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
				>
					<span role="status" aria-live="polite" className="inline-flex items-center gap-1.5">
						{upsert.isPending ? (
							<>
								<Loader2 className="h-3 w-3 animate-spin" /> Saving...
							</>
						) : (
							<>
								<Save className="h-3 w-3" /> Save
							</>
						)}
					</span>
				</button>
				{existing && (
					<button
						type="button"
						onClick={() => del.mutate(existing.id)}
						disabled={busy}
						aria-label={`Remove ${PROVIDER_LABELS[provider]} key`}
						className="flex items-center gap-1.5 border border-iron px-3 py-1 text-[11px] font-mono text-slate-text hover:text-red-400 hover:border-red-400/40 transition-colors disabled:opacity-50"
					>
						<span role="status" aria-live="polite" className="inline-flex items-center gap-1.5">
							{del.isPending ? (
								<>
									<Loader2 className="h-3 w-3 animate-spin" /> Removing...
								</>
							) : (
								<>
									<Trash2 className="h-3 w-3" /> Remove key
								</>
							)}
						</span>
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
	// Count only LLM provider keys — the embeddings slot is a provider_keys row
	// but not one of the N LLM providers this card grid tracks.
	const configuredCount =
		providerKeys?.filter((k) => (PROVIDERS as readonly string[]).includes(k.provider)).length ?? 0;

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
				<h1 className="font-mono text-2xl font-bold text-foreground">Integrations</h1>
				<p className="text-xs font-mono text-slate-text mt-1">
					API keys and service connections. Keys are encrypted at rest with AES-256-GCM.
				</p>
			</div>

			<section aria-labelledby="integrations-heading" className="mb-10">
				<div className="flex items-center gap-3 mb-4">
					<div className="flex items-center gap-2">
						<Brain className="h-4 w-4 text-amber" />
						<h2 id="integrations-heading" className="font-mono text-lg font-semibold text-foreground">
							Memory &amp; classifiers
						</h2>
					</div>
				</div>
				<p className="text-[11px] font-mono text-slate-text mb-4">
					Auxiliary services, not review providers: embeddings index institutional memory for
					retrieval, and the Jev classifier pre-filters narrow typed decisions ahead of the LLM.
				</p>
				<div className="grid gap-4 grid-cols-1 lg:grid-cols-2 max-w-4xl">
					<EmbeddingsCard />
					<JevCard />
				</div>
			</section>

			<section aria-labelledby="llm-providers-heading">
				<div className="flex items-center gap-3 mb-4">
					<div className="flex items-center gap-2">
						<Key className="h-4 w-4 text-amber" />
						<h2
							id="llm-providers-heading"
							className="font-mono text-lg font-semibold text-foreground"
						>
							LLM Providers
						</h2>
					</div>
					<span className="text-[10px] font-mono text-slate-text">
						{configuredCount}/{PROVIDERS.length} configured
					</span>
				</div>
				<p className="text-[11px] font-mono text-slate-text mb-4 break-words">
					Generative providers for the review pipeline — these power every stage&apos;s model
					picker. Keys are scoped to{" "}
					<span className="text-foreground">{active?.org_login ?? "your org"}</span> and configured
					providers become selectable in Settings.
				</p>
				<div className="grid gap-4 grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
					{PROVIDERS.map((p) => (
						<ProviderKeyCard key={p} provider={p} existing={keyMap.get(p)} />
					))}
				</div>
			</section>
		</>
	);
}
