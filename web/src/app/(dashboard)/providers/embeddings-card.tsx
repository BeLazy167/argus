"use client";

import { Loader2, Sparkles, Trash2 } from "lucide-react";
import { useState } from "react";
import type { EmbedProviderOption } from "@/lib/queries/embeddings";
import { embeddingsRowOf, useEmbeddingsCatalog } from "@/lib/queries/embeddings";
import {
	useDeleteProviderKey,
	useProviderKeys,
	useUpsertProviderKey,
} from "@/lib/queries/provider-keys";
import type { ProviderKey } from "@/lib/types";
import { StatusBadge } from "./status-badge";

/**
 * Memory-embeddings configuration: pick a provider + model from the
 * backend-served catalog (Voyage default, OpenAI, or a custom
 * OpenAI-compatible endpoint), installation-wide. This is the memory
 * backend's settings surface.
 */
export function EmbeddingsCard() {
	const {
		data: catalog,
		isLoading: catalogLoading,
		isError: catalogError,
	} = useEmbeddingsCatalog();
	const { data: keys, isLoading: keysLoading } = useProviderKeys();

	if (catalogLoading || keysLoading) {
		return (
			<div
				role="status"
				aria-live="polite"
				className="border border-iron bg-charcoal p-5 flex items-center gap-2 text-[11px] font-mono text-slate-text"
			>
				<Loader2 className="h-3 w-3 animate-spin" /> Loading embeddings config...
			</div>
		);
	}
	if (catalogError || !catalog) {
		return (
			<div
				role="alert"
				className="border border-iron bg-charcoal p-5 text-[11px] font-mono text-red-400"
			>
				Could not load the embeddings catalog. Memory keeps its current configuration; reload to
				retry.
			</div>
		);
	}
	return <EmbeddingsForm providers={catalog.providers} current={embeddingsRowOf(keys)} />;
}

/**
 * Infers which catalog provider a stored row belongs to (by base_url).
 * A row without base_url is the legacy "platform space, my key" shape: the
 * key targets the platform default endpoint, which the catalog lists first —
 * not a custom endpoint.
 */
function providerKeyForRow(providers: EmbedProviderOption[], row: ProviderKey): string {
	if (!row.base_url) return providers[0]?.key ?? "";
	const hosted = providers.find((p) => p.base_url && p.base_url === row.base_url);
	if (hosted) return hosted.key;
	const custom = providers.find((p) => p.custom_model);
	return custom?.key ?? "";
}

function EmbeddingsForm({
	providers,
	current,
}: {
	providers: EmbedProviderOption[];
	current: ProviderKey | undefined;
}) {
	const upsert = useUpsertProviderKey();
	const del = useDeleteProviderKey();

	// Seed the form from the stored row so editing never forces full re-entry
	// (re-typing the custom model risks silently re-stamping the embedding
	// space on a typo). Initializers run once; queries above have settled.
	const [providerKey, setProviderKey] = useState<string>(() =>
		current ? providerKeyForRow(providers, current) : "",
	);
	const [model, setModel] = useState(() => current?.model ?? "");
	const [baseURL, setBaseURL] = useState(() => current?.base_url ?? "");
	const [apiKey, setApiKey] = useState("");
	const [error, setError] = useState("");

	const selected = providers.find((p) => p.key === providerKey);
	const isCustom = selected?.custom_model === true;
	const configured = current != null;
	// Blank key keeps the stored key ONLY when the endpoint is unchanged
	// (backend enforces this); a provider/endpoint switch requires the key.
	// A stored row without base_url targets the platform default endpoint
	// (first catalog provider) — same rule the backend applies.
	const storedBase = current?.base_url ?? (configured ? (providers[0]?.base_url ?? "") : "");
	const endpointUnchanged =
		configured && (isCustom ? baseURL.trim() === storedBase : selected?.base_url === storedBase);
	const busy = upsert.isPending || del.isPending;

	const keyLabel = () => {
		if (!selected) return "API key";
		if (!selected.requires_key) return "API key (optional for self-hosted endpoints)";
		if (endpointUnchanged) return "API key (leave blank to keep the stored key)";
		return "API key";
	};

	const handleSave = () => {
		if (!selected) {
			setError("Pick a provider");
			return;
		}
		if (!model) {
			setError("Pick a model");
			return;
		}
		if (isCustom && !baseURL.trim()) {
			setError("Custom endpoints need a base URL");
			return;
		}
		if (selected.requires_key && !apiKey && !endpointUnchanged) {
			setError("API key required (a stored key only carries over on the same endpoint)");
			return;
		}
		setError("");
		// Hosted rows carry the catalog base + model; custom rows carry the
		// user's endpoint + declared model. The backend requires model whenever
		// base_url is present.
		const base = isCustom ? baseURL.trim() : selected.base_url;
		upsert.mutate(
			{ provider: "embeddings", api_key: apiKey, ...(base ? { base_url: base, model } : {}) },
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
					<Sparkles className="h-3.5 w-3.5 text-amber" />
					<span className="text-xs font-mono font-medium text-foreground">Memory embeddings</span>
				</div>
				<StatusBadge
					variant={configured ? "active" : "inactive"}
					label={configured ? (current?.model ?? "Platform model, your key") : "Platform default"}
				/>
			</div>

			<p className="text-[10px] font-mono text-slate-text mb-3 leading-relaxed">
				{configured
					? `Using ${current?.model ?? "the platform model (voyage-4) with your key"}${current?.base_url ? ` via ${current.base_url}` : ""}. Memory search, pattern learning, and dismissal suppression run in this embedding space.`
					: "Memory uses the platform embedding model (voyage-4). Bring your own key or point at a self-hosted endpoint to own the embedding space."}
			</p>

			<div className="space-y-2 mb-3">
				<div>
					<label
						htmlFor="embed-provider"
						className="block text-[10px] font-mono text-slate-text mb-1"
					>
						Provider
					</label>
					<select
						id="embed-provider"
						value={providerKey}
						onChange={(e) => {
							setProviderKey(e.target.value);
							setModel("");
							setError("");
						}}
						className="w-full border border-iron bg-background px-2 py-1.5 text-xs font-mono text-foreground focus:border-amber focus:outline-none"
					>
						<option value="">Select provider...</option>
						{providers.map((p) => (
							<option key={p.key} value={p.key}>
								{p.label}
							</option>
						))}
					</select>
					{selected?.notes && (
						<p className="mt-1 text-[10px] font-mono text-slate-text/70 leading-relaxed">
							{selected.notes}
						</p>
					)}
				</div>

				{selected && !isCustom && (
					<div>
						<label
							htmlFor="embed-model"
							className="block text-[10px] font-mono text-slate-text mb-1"
						>
							Model
						</label>
						<select
							id="embed-model"
							value={model}
							onChange={(e) => setModel(e.target.value)}
							className="w-full border border-iron bg-background px-2 py-1.5 text-xs font-mono text-foreground focus:border-amber focus:outline-none"
						>
							<option value="">Select model...</option>
							{(selected.models ?? []).map((m) => (
								<option key={m.model} value={m.model}>
									{m.model}
									{m.notes ? ` — ${m.notes}` : ""}
								</option>
							))}
						</select>
					</div>
				)}

				{isCustom && (
					<>
						<div>
							<label
								htmlFor="embed-base-url"
								className="block text-[10px] font-mono text-slate-text mb-1"
							>
								Base URL (OpenAI-compatible, without /embeddings)
							</label>
							<input
								id="embed-base-url"
								type="text"
								value={baseURL}
								onChange={(e) => setBaseURL(e.target.value)}
								placeholder="http://tei.internal:8080/v1"
								className="w-full border border-iron bg-background px-2 py-1.5 text-xs font-mono text-foreground placeholder:text-slate-text/50 focus:border-amber focus:outline-none"
							/>
						</div>
						<div>
							<label
								htmlFor="embed-custom-model"
								className="block text-[10px] font-mono text-slate-text mb-1"
							>
								Model served by the endpoint (must be 1024-dim)
							</label>
							<input
								id="embed-custom-model"
								type="text"
								value={model}
								onChange={(e) => setModel(e.target.value)}
								placeholder="bge-m3"
								className="w-full border border-iron bg-background px-2 py-1.5 text-xs font-mono text-foreground placeholder:text-slate-text/50 focus:border-amber focus:outline-none"
							/>
						</div>
					</>
				)}

				{selected && (
					<div>
						<label
							htmlFor="embed-api-key"
							className="block text-[10px] font-mono text-slate-text mb-1"
						>
							{keyLabel()}
						</label>
						<input
							id="embed-api-key"
							type="password"
							autoComplete="off"
							value={apiKey}
							onChange={(e) => setApiKey(e.target.value)}
							placeholder={selected.requires_key ? "sk-..." : "leave blank if unauthenticated"}
							className="w-full border border-iron bg-background px-2 py-1.5 text-xs font-mono text-foreground placeholder:text-slate-text/50 focus:border-amber focus:outline-none"
						/>
					</div>
				)}
			</div>

			{selected && (
				<p role="note" className="text-[10px] font-mono text-amber/80 mb-3 leading-relaxed">
					Changing the embedding model changes the embedding space: existing memories are
					re-embedded by the backfill process and similarity thresholds re-calibrate. Retrieval
					quality may shift until that completes.
				</p>
			)}

			{error && (
				<p role="alert" className="text-[10px] font-mono text-red-400 mb-2">
					{error}
				</p>
			)}

			<div className="flex items-center gap-2">
				<button
					type="button"
					onClick={handleSave}
					disabled={busy || !selected}
					className="flex items-center gap-2 border border-amber/30 bg-amber/10 px-3 py-1 text-[11px] font-mono text-amber hover:bg-amber/20 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
				>
					<span role="status" aria-live="polite" className="inline-flex items-center gap-1.5">
						{upsert.isPending ? (
							<>
								<Loader2 className="h-3 w-3 animate-spin" /> Saving...
							</>
						) : (
							"Save"
						)}
					</span>
				</button>
				{configured && (
					<button
						type="button"
						onClick={() => del.mutate(current.id)}
						disabled={busy}
						aria-label="Remove embeddings configuration (revert to platform default)"
						className="flex items-center gap-1.5 border border-iron px-3 py-1 text-[11px] font-mono text-slate-text hover:text-red-400 hover:border-red-400/40 transition-colors disabled:opacity-50"
					>
						<span role="status" aria-live="polite" className="inline-flex items-center gap-1.5">
							{del.isPending ? (
								<>
									<Loader2 className="h-3 w-3 animate-spin" /> Removing...
								</>
							) : (
								<>
									<Trash2 className="h-3 w-3" /> Revert to default
								</>
							)}
						</span>
					</button>
				)}
			</div>
		</div>
	);
}
