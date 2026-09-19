"use client";

import { Loader2, Trash2, Zap } from "lucide-react";
import { useState } from "react";
import {
	useDeleteProviderKey,
	useProviderKeys,
	useUpsertProviderKey,
} from "@/lib/queries/provider-keys";
import { useRepos } from "@/lib/queries/repos";
import type { ProviderKey } from "@/lib/types";
import { StatusBadge } from "./status-badge";

// The provider_keys slot the backend's Jev resolver reads (repo-level first,
// then org-level); a stored row IS the installation's consent to egress, so
// BYOK needs no feature flag. A stored base_url overrides the API root.
const JEV_PROVIDER = "typesafe";
const JEV_DEFAULT_ENDPOINT = "https://api.typesafe.ai";

/**
 * TypeSafe Jev BYOK card. Jev is a typed classifier — state plus typed
 * questions in, probabilities out, never prose — that Argus runs ahead of the
 * review LLM on narrow decisions: auto-resolve verification, convention
 * relations, intent checks, false-positive filtering, and the triage shadow.
 * Confident answers skip the LLM call; the uncertain band always escalates.
 * Lives in the integrations section, not the LLM provider grid — Jev is a
 * pre-filter, not a review provider.
 */
export function JevCard() {
	const { data: keys, isLoading } = useProviderKeys();
	const { data: repos } = useRepos();

	if (isLoading) {
		return (
			<div
				role="status"
				aria-live="polite"
				className="border border-iron bg-charcoal p-5 flex items-center gap-2 text-[11px] font-mono text-slate-text"
			>
				<Loader2 className="h-3 w-3 animate-spin" /> Loading Jev config...
			</div>
		);
	}
	// This card manages the installation-scoped row only. Repo-scoped
	// typesafe keys resolve FIRST for their repos — surface them read-only so
	// a repo key is never mistaken for (or mutated as) the org-wide config.
	const typed = keys?.filter((k) => k.provider === JEV_PROVIDER) ?? [];
	const repoScoped = typed.filter((k) => k.repo_id != null);
	return (
		<JevForm
			current={typed.find((k) => k.repo_id == null)}
			repoScoped={repoScoped}
			repoNames={new Map(repos?.map((r) => [r.id, r.full_name]) ?? [])}
		/>
	);
}

function JevForm({
	current,
	repoScoped,
	repoNames,
}: {
	current: ProviderKey | undefined;
	repoScoped: ProviderKey[];
	repoNames: Map<number, string>;
}) {
	const upsert = useUpsertProviderKey();
	const del = useDeleteProviderKey();

	// Seed the endpoint from the stored row so rotating a key never silently
	// re-types the endpoint. Initializers run once; the keys query above has
	// already settled (same convention as the embeddings card).
	const [baseURL, setBaseURL] = useState(() => current?.base_url ?? "");
	const [apiKey, setApiKey] = useState("");
	const [error, setError] = useState("");

	const configured = current != null;
	const busy = upsert.isPending || del.isPending;

	const handleSave = () => {
		const key = apiKey.trim();
		setError("");
		// Omitting base_url PRESERVES the stored endpoint (store PATCH
		// semantics), so send it only when the field carries a value; a blank
		// field on a configured row keeps the stored endpoint by design.
		const endpoint = baseURL.trim();
		upsert.mutate(
			{ provider: JEV_PROVIDER, api_key: key, ...(endpoint ? { base_url: endpoint } : {}) },
			{
				onSuccess: () => {
					setApiKey("");
					setError("");
				},
				onError: (err) => setError(err instanceof Error ? err.message : "Save failed"),
			},
		);
	};

	const handleDelete = () => {
		if (!current) return;
		setError("");
		del.mutate(current.id, {
			onSuccess: () => setBaseURL(""),
			onError: (err) => setError(err instanceof Error ? err.message : "Remove failed"),
		});
	};

	return (
		<div className="border border-iron bg-charcoal p-5">
			<div className="flex items-center justify-between mb-3">
				<div className="flex items-center gap-2">
					<Zap className="h-3.5 w-3.5 text-amber" />
					<span className="text-xs font-mono font-medium text-foreground">TypeSafe Jev</span>
				</div>
				<StatusBadge
					variant={configured ? "active" : "inactive"}
					label={configured ? "Configured" : "Server default"}
				/>
			</div>

			{current && (
				<p className="text-[11px] font-mono text-slate-text mb-3">
					Key: {current.api_key_masked}
					{current.base_url ? ` — via ${current.base_url}` : ""}
				</p>
			)}

			{repoScoped.length > 0 && (
				<div className="border border-iron/60 bg-background/60 px-3 py-2 mb-3">
					<p className="text-[10px] font-mono text-slate-text mb-1 leading-relaxed">
						Repo-scoped keys — take precedence over this key for their repos:
					</p>
					<ul className="space-y-0.5">
						{repoScoped.map((k) => (
							<li key={k.id} className="text-[10px] font-mono text-slate-text/80">
								{(k.repo_id != null && repoNames.get(k.repo_id)) || `repo #${k.repo_id}`}:{" "}
								{k.api_key_masked}
								{k.base_url ? ` — via ${k.base_url}` : ""}
							</li>
						))}
					</ul>
				</div>
			)}

			<p className="text-[10px] font-mono text-slate-text mb-3 leading-relaxed">
				System One typed classifier: probabilities out, no prose. Pre-filters narrow decisions —
				auto-resolve verification, convention relations, intent checks, false-positive filtering,
				triage shadow; anything uncertain escalates to the review LLM.
				{configured
					? ""
					: " No key stored: the deployment's shared Jev config applies when enabled."}
			</p>

			<div className="space-y-2 mb-3">
				<div>
					<label
						htmlFor="jev-api-key"
						className="block text-[10px] font-mono text-slate-text mb-1"
					>
						{configured ? "Replace API key" : "API key"}
					</label>
					<input
						id="jev-api-key"
						type="password"
						autoComplete="off"
						value={apiKey}
						onChange={(e) => setApiKey(e.target.value)}
						placeholder={configured ? "Enter new key to replace" : "Paste your TypeSafe API key"}
						className="w-full border border-iron bg-background px-2 py-1.5 text-xs font-mono text-foreground placeholder:text-slate-text/50 focus:border-amber focus:outline-none"
					/>
				</div>
				<div>
					<label
						htmlFor="jev-base-url"
						className="block text-[10px] font-mono text-slate-text mb-1"
					>
						Custom endpoint (optional)
					</label>
					<input
						id="jev-base-url"
						type="text"
						value={baseURL}
						onChange={(e) => setBaseURL(e.target.value)}
						placeholder={JEV_DEFAULT_ENDPOINT}
						className="w-full border border-iron bg-background px-2 py-1.5 text-xs font-mono text-foreground placeholder:text-slate-text/50 focus:border-amber focus:outline-none"
					/>
					{current?.base_url && (
						<p className="mt-1 text-[10px] font-mono text-slate-text/70 leading-relaxed">
							A blank field keeps the stored endpoint — remove the key to reset to{" "}
							{JEV_DEFAULT_ENDPOINT}.
						</p>
					)}
				</div>
			</div>

			<p role="note" className="text-[10px] font-mono text-amber/80 mb-3 leading-relaxed">
				Saving a key sends sanitized PR and finding content to TypeSafe under your account — the
				save is the installation's consent to egress.
			</p>

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
							"Save"
						)}
					</span>
				</button>
				{current && (
					<button
						type="button"
						onClick={handleDelete}
						disabled={busy}
						aria-label="Remove TypeSafe Jev key (revert to server default)"
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
}
