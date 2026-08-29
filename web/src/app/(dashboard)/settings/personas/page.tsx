"use client";

import { Check, Loader2, Plus, Trash2, UserCog } from "lucide-react";
import { useState } from "react";
import { useOrgDefaults, useSaveOrgDefaults } from "@/lib/queries/org-defaults";
import { useDeletePersona, usePersonas, useSavePersona } from "@/lib/queries/personas";
import type { Persona } from "@/lib/types";
import { useInstallation } from "@/providers/installation-provider";

/** Mirrors the server's bounds in api/handlers_personas.go. */
const MAX_OVERLAY = 8000;
const MAX_HINT = 600;
const MAX_NAME = 80;

type Draft = {
	slug: string;
	name: string;
	prompt_overlay: string;
	specialist_hint: string;
	/** True when editing an existing row, which freezes the slug. */
	existing: boolean;
};

const EMPTY: Draft = { slug: "", name: "", prompt_overlay: "", specialist_hint: "", existing: false };

/**
 * Derives a slug from a display name so the author never has to think about
 * identifiers. Matches the server's `^[a-z0-9][a-z0-9_-]*$`.
 */
function slugify(name: string): string {
	return name
		.toLowerCase()
		.replace(/[^a-z0-9]+/g, "_")
		.replace(/^_+|_+$/g, "")
		.slice(0, 64);
}

export default function PersonasPage() {
	const { active } = useInstallation();
	const personas = usePersonas();
	// Scoped by installation, matching the other caller. Unscoped, this would be
	// a different cache entry from the one settings/page.tsx populates, and an
	// org switch could serve another org's defaults — which this page then
	// writes straight back.
	const orgDefaults = useOrgDefaults({ variables: { installationId: active?.id } });
	const saveOrgDefaults = useSaveOrgDefaults();
	const savePersona = useSavePersona();
	const deletePersona = useDeletePersona();

	const [draft, setDraft] = useState<Draft | null>(null);

	const activeSlug = typeof orgDefaults.data?.persona === "string" ? orgDefaults.data.persona : "default";

	// Both queries must resolve before the grid is interactive. "Use as default"
	// spreads the existing blob, and SetOrgDefaults REPLACES it wholesale — so
	// firing while orgDefaults is pending or errored would persist
	// {"persona": "..."} alone and silently drop auto_run, every budget limit,
	// every memory threshold and every pipeline toggle.
	if (personas.isPending || orgDefaults.isPending) {
		return (
			<div className="flex items-center justify-center py-20">
				<Loader2 className="h-6 w-6 animate-spin text-slate-text" />
			</div>
		);
	}

	if (personas.isError || orgDefaults.isError || !orgDefaults.data) {
		return (
			<p className="text-xs font-mono text-red-400">
				Settings failed to load. Check your connection, then reload the page.
			</p>
		);
	}

	const rows = personas.data ?? [];

	const onSave = async () => {
		if (!draft) return;
		const slug = draft.existing ? draft.slug : slugify(draft.slug || draft.name);
		await savePersona.mutateAsync({
			slug,
			name: draft.name,
			prompt_overlay: draft.prompt_overlay,
			specialist_hint: draft.specialist_hint,
		});
		setDraft(null);
	};

	return (
		<div className="space-y-8">
			<header className="flex items-start justify-between gap-4">
				<div className="flex items-center gap-2">
					<UserCog className="h-4 w-4 text-amber" />
					<div>
						<h1 className="font-mono text-lg font-semibold text-foreground">Personas</h1>
						<p className="text-[11px] font-mono text-slate-text mt-1">
							A persona is a review style. Its text is appended to the review prompt, so it shapes
							what Argus looks for without replacing the rest of the instructions.
						</p>
					</div>
				</div>
				<button
					type="button"
					onClick={() => setDraft({ ...EMPTY })}
					className="inline-flex shrink-0 items-center gap-1.5 border border-amber/40 bg-amber/10 px-3 py-1.5 text-[11px] font-mono text-amber hover:bg-amber/20"
				>
					<Plus className="h-3 w-3" /> New persona
				</button>
			</header>

			<div className="grid gap-3 grid-cols-1 lg:grid-cols-2">
				{rows.map((p) => (
					<PersonaRow
						key={p.slug}
						persona={p}
						isActive={p.slug === activeSlug}
						busy={saveOrgDefaults.isPending || deletePersona.isPending}
						onUse={() => saveOrgDefaults.mutate({ ...orgDefaults.data, persona: p.slug })}
						onEdit={() =>
							setDraft({
								slug: p.slug,
								name: p.name,
								prompt_overlay: p.prompt_overlay,
								specialist_hint: p.specialist_hint,
								existing: true,
							})
						}
						onDelete={
							// Only an installation's own row can be deleted. A built-in has
							// no installation_id and is shared by everyone.
							p.installation_id ? () => deletePersona.mutate(p.slug) : undefined
						}
					/>
				))}
			</div>

			{draft && (
				<PersonaEditor
					draft={draft}
					setDraft={setDraft}
					onSave={onSave}
					onCancel={() => setDraft(null)}
					saving={savePersona.isPending}
					error={savePersona.isError}
				/>
			)}
		</div>
	);
}

function PersonaRow({
	persona,
	isActive,
	busy,
	onUse,
	onEdit,
	onDelete,
}: {
	persona: Persona;
	isActive: boolean;
	busy: boolean;
	onUse: () => void;
	onEdit: () => void;
	onDelete?: () => void;
}) {
	// Computed server-side: is_builtin is always FALSE on an installation's own
	// row, so it cannot tell a fresh persona from a retuned built-in. Saying
	// which it is matters, because deleting a shadow RESTORES the original
	// rather than removing the name their repos select.
	const isShadow = persona.shadows_builtin;

	return (
		<div
			className={`border p-4 ${isActive ? "border-amber/40 bg-amber/5" : "border-iron bg-charcoal"}`}
		>
			<div className="flex items-center justify-between gap-2 mb-1.5">
				<span
					className={`text-xs font-mono font-medium ${isActive ? "text-amber" : "text-foreground"}`}
				>
					{persona.name}
				</span>
				<div className="flex items-center gap-1.5">
					{isActive && (
						<span className="inline-flex items-center border border-amber/30 bg-amber/10 px-1.5 py-0.5 text-[9px] font-mono uppercase tracking-wider text-amber">
							default
						</span>
					)}
					{!persona.installation_id && (
						<span className="inline-flex items-center border border-iron px-1.5 py-0.5 text-[9px] font-mono uppercase tracking-wider text-slate-text">
							built-in
						</span>
					)}
					{isShadow && (
						<span className="inline-flex items-center border border-iron px-1.5 py-0.5 text-[9px] font-mono uppercase tracking-wider text-slate-text">
							retuned
						</span>
					)}
				</div>
			</div>

			<p className="text-[11px] font-mono text-slate-text leading-relaxed line-clamp-3 min-h-[2.5rem]">
				{persona.prompt_overlay.trim() || "No overlay — reviews run with the base prompt."}
			</p>

			<div className="mt-3 flex items-center gap-2">
				<button
					type="button"
					onClick={onUse}
					disabled={busy || isActive}
					className="inline-flex items-center gap-1 border border-iron px-2 py-1 text-[10px] font-mono text-foreground hover:border-amber/40 disabled:opacity-40"
				>
					<Check className="h-3 w-3" /> Use as default
				</button>
				<button
					type="button"
					onClick={onEdit}
					className="border border-iron px-2 py-1 text-[10px] font-mono text-foreground hover:border-amber/40"
				>
					{persona.installation_id ? "Edit" : "Retune"}
				</button>
				{onDelete && (
					<button
						type="button"
						onClick={onDelete}
						disabled={busy}
						className="inline-flex items-center gap-1 border border-iron px-2 py-1 text-[10px] font-mono text-red-400 hover:border-red-400/40 disabled:opacity-40"
					>
						<Trash2 className="h-3 w-3" /> Delete
					</button>
				)}
			</div>
		</div>
	);
}

function PersonaEditor({
	draft,
	setDraft,
	onSave,
	onCancel,
	saving,
	error,
}: {
	draft: Draft;
	setDraft: (d: Draft) => void;
	onSave: () => void;
	onCancel: () => void;
	saving: boolean;
	error: boolean;
}) {
	const set = <K extends keyof Draft>(key: K, value: Draft[K]) => setDraft({ ...draft, [key]: value });

	return (
		<section className="border border-amber/20 bg-charcoal p-4 space-y-4">
			<h2 className="font-mono text-sm font-semibold text-foreground">
				{draft.existing ? `Editing ${draft.name || draft.slug}` : "New persona"}
			</h2>

			<div className="space-y-1.5">
				<label htmlFor="persona-name" className="block text-[11px] font-mono text-slate-text">
					Name
				</label>
				<input
					id="persona-name"
					value={draft.name}
					maxLength={MAX_NAME}
					onChange={(e) => set("name", e.target.value)}
					placeholder="House Style"
					className="w-full bg-background border border-iron px-2 py-1.5 text-xs font-mono text-foreground focus:border-amber focus:outline-none"
				/>
				{draft.existing ? (
					<p className="text-[10px] font-mono text-slate-text/70">
						Identifier <code>{draft.slug}</code> — fixed, because repos already store it.
					</p>
				) : (
					<p className="text-[10px] font-mono text-slate-text/70">
						Saved as <code>{slugify(draft.name) || "…"}</code>. Reusing a built-in identifier
						retunes it instead of replacing it.
					</p>
				)}
			</div>

			<div className="space-y-1.5">
				<label htmlFor="persona-overlay" className="block text-[11px] font-mono text-slate-text">
					Review prompt
				</label>
				<textarea
					id="persona-overlay"
					value={draft.prompt_overlay}
					maxLength={MAX_OVERLAY}
					onChange={(e) => set("prompt_overlay", e.target.value)}
					rows={8}
					placeholder="Prioritise…"
					className="w-full bg-background border border-iron px-2 py-1.5 text-xs font-mono text-foreground focus:border-amber focus:outline-none"
				/>
				<p className="text-[10px] font-mono text-slate-text/70">
					{draft.prompt_overlay.length}/{MAX_OVERLAY}. Appended to every review this persona runs.
				</p>
			</div>

			<div className="space-y-1.5">
				<label htmlFor="persona-hint" className="block text-[11px] font-mono text-slate-text">
					Specialist hint (optional)
				</label>
				<textarea
					id="persona-hint"
					value={draft.specialist_hint}
					maxLength={MAX_HINT}
					onChange={(e) => set("specialist_hint", e.target.value)}
					rows={3}
					className="w-full bg-background border border-iron px-2 py-1.5 text-xs font-mono text-foreground focus:border-amber focus:outline-none"
				/>
				<p className="text-[10px] font-mono text-slate-text/70">
					{draft.specialist_hint.length}/{MAX_HINT}. Added once per specialist on deep reviews, so
					keep it to one line.
				</p>
			</div>

			<div className="flex items-center gap-3">
				<button
					type="button"
					onClick={onSave}
					disabled={saving || !draft.name.trim() || !draft.prompt_overlay.trim()}
					className="inline-flex items-center gap-1.5 border border-amber/40 bg-amber/10 px-3 py-1.5 text-[11px] font-mono text-amber hover:bg-amber/20 disabled:opacity-50"
				>
					{saving && <Loader2 className="h-3 w-3 animate-spin" />}
					Save persona
				</button>
				<button
					type="button"
					onClick={onCancel}
					className="border border-iron px-3 py-1.5 text-[11px] font-mono text-slate-text hover:text-foreground"
				>
					Cancel
				</button>
				{error && <span className="text-[11px] font-mono text-red-400">Save failed</span>}
			</div>
		</section>
	);
}
