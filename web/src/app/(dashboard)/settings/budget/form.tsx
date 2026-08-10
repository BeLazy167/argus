"use client";

import { Gauge, Info, Loader2, Save } from "lucide-react";
import { useMemo, useState } from "react";
import { BUDGET_DEFAULTS, type BudgetKey, readLimit } from "@/lib/queries/budget-settings";
import { useMemorySettings, useSaveMemorySettings } from "@/lib/queries/memory-settings";
import { useInstallation } from "@/providers/installation-provider";

/**
 * Draft holds every limit as `number | null`. Null means "inherit the
 * default"; a number is an explicit override, and 0 is a real override that
 * turns one measure off. Collapsing the two would make "unset" and "disabled"
 * indistinguishable, which is the difference an operator most needs to see.
 */
type Draft = Record<BudgetKey, number | null>;

const EMPTY_DRAFT: Draft = {
	budget_soft_files: null,
	budget_hard_files: null,
	budget_soft_lines: null,
	budget_hard_lines: null,
	budget_soft_tokens: null,
	budget_hard_tokens: null,
	budget_reduced_max_files: null,
};

function resolveDraft(data: Record<string, unknown>): Draft {
	return {
		budget_soft_files: readLimit(data, "budget_soft_files"),
		budget_hard_files: readLimit(data, "budget_hard_files"),
		budget_soft_lines: readLimit(data, "budget_soft_lines"),
		budget_hard_lines: readLimit(data, "budget_hard_lines"),
		budget_soft_tokens: readLimit(data, "budget_soft_tokens"),
		budget_hard_tokens: readLimit(data, "budget_hard_tokens"),
		budget_reduced_max_files: readLimit(data, "budget_reduced_max_files"),
	};
}

/** One row of the form: label, help text, and the field it edits. */
const FIELDS: { key: BudgetKey; label: string; help: string }[] = [
	{
		key: "budget_soft_files",
		label: "Reduce above (files)",
		help: "Past this many changed files, the review runs shallower and over fewer files.",
	},
	{
		key: "budget_hard_files",
		label: "Refuse above (files)",
		help: "Past this many, no review runs and the pull request is told why.",
	},
	{
		key: "budget_soft_lines",
		label: "Reduce above (lines)",
		help: "Changed lines, added plus removed.",
	},
	{ key: "budget_hard_lines", label: "Refuse above (lines)", help: "Changed lines, added plus removed." },
	{
		key: "budget_soft_tokens",
		label: "Reduce above (avg tokens)",
		help: "Based on this repo's last 20 completed reviews. Ignored until the repo has history.",
	},
	{
		key: "budget_hard_tokens",
		label: "Refuse above (avg tokens)",
		help: "Based on this repo's last 20 completed reviews. Ignored until the repo has history.",
	},
	{
		key: "budget_reduced_max_files",
		label: "Files a reduced review reads",
		help: "The highest-risk files are kept. Raise this alongside the reduce limits.",
	},
];

/**
 * BudgetSettingsSection renders the review cost limits, styled to match the
 * sibling Memory tab. It reuses the org-defaults fetch and save, because these
 * limits live in the same blob and a separate writer would clobber the other
 * keys on save.
 */
export function BudgetSettingsSection() {
	const { active } = useInstallation();
	const installationId = active?.id;
	const { data, isLoading } = useMemorySettings({ variables: { installationId } });
	const save = useSaveMemorySettings();

	// Seeded inline, never synced through an effect — a background refetch must
	// not clobber unsaved edits. Switching orgs remounts the form via `key`.
	const seed = useMemo(() => (data ? resolveDraft(data) : EMPTY_DRAFT), [data]);

	const onSave = async (draft: Draft) => {
		// The blob carries unrelated keys — personas, thresholds, feature
		// toggles. Spread it so a budget save preserves them.
		if (!data) return;
		await save.mutateAsync({ ...data, ...draft } as Record<string, unknown>);
	};

	if (isLoading || !active) {
		return (
			<div className="flex items-center justify-center py-20">
				<Loader2 className="h-6 w-6 animate-spin text-slate-text" />
			</div>
		);
	}

	if (!data) {
		return (
			<p className="text-xs font-mono text-red-400">
				Settings failed to load. Check your connection, then reload the page.
			</p>
		);
	}

	return (
		<div className="space-y-10">
			<div className="border border-amber/20 bg-amber/5 px-4 py-3 flex items-start gap-2.5">
				<Info className="h-3.5 w-3.5 text-amber mt-0.5 shrink-0" />
				<p className="text-[11px] font-mono text-amber/80">
					What one review may cost. A large pull request is reviewed shallowly rather than refused,
					and only refused past the hard limits. Leave a field empty to inherit the default. Applies
					to the next review in <span className="text-amber font-medium">{active.org_login}</span>.
				</p>
			</div>

			<BudgetForm
				key={installationId}
				initialValues={seed}
				onSave={onSave}
				isSaving={save.isPending}
				isError={save.isError}
				isSuccess={save.isSuccess}
			/>
		</div>
	);
}

function BudgetForm({
	initialValues,
	onSave,
	isSaving,
	isError,
	isSuccess,
}: {
	initialValues: Draft;
	onSave: (draft: Draft) => Promise<void>;
	isSaving: boolean;
	isError: boolean;
	isSuccess: boolean;
}) {
	const [draft, setDraft] = useState<Draft>(initialValues);

	const set = (key: BudgetKey, raw: string) => {
		const trimmed = raw.trim();
		// Empty clears the override back to the inherited default. A typed 0
		// survives, because 0 disables that measure.
		const next = trimmed === "" ? null : Number(trimmed);
		// Whole numbers only. These map to Go int/int64 fields, so a fractional
		// value is rejected by the API as invalid JSON before validation ever
		// runs — the save would fail with no usable message.
		if (next !== null && (!Number.isInteger(next) || next < 0)) return;
		setDraft((d) => ({ ...d, [key]: next }));
	};

	return (
		<section className="space-y-5">
			<div className="flex items-center gap-2">
				<Gauge className="h-4 w-4 text-amber" />
				<h3 className="font-mono text-sm font-semibold text-foreground">Review cost limits</h3>
			</div>

			<div className="grid gap-4 sm:grid-cols-2">
				{FIELDS.map((f) => (
					<div key={f.key} className="border border-iron bg-charcoal p-3 space-y-1.5">
						<label
							htmlFor={f.key}
							className="block text-[11px] font-mono font-medium text-foreground"
						>
							{f.label}
						</label>
						<input
							id={f.key}
							type="number"
							min={0}
							step={1}
							inputMode="numeric"
							value={draft[f.key] ?? ""}
							placeholder={String(BUDGET_DEFAULTS[f.key])}
							onChange={(e) => set(f.key, e.target.value)}
							className="w-full bg-background border border-iron px-2 py-1.5 text-xs font-mono text-foreground focus:border-amber focus:outline-none"
						/>
						<p className="text-[10px] font-mono text-slate-text/70 leading-relaxed">{f.help}</p>
					</div>
				))}
			</div>

			<div className="flex items-center gap-3">
				<button
					type="button"
					onClick={() => onSave(draft)}
					disabled={isSaving}
					className="inline-flex items-center gap-1.5 border border-amber/40 bg-amber/10 px-3 py-1.5 text-[11px] font-mono text-amber hover:bg-amber/20 disabled:opacity-50"
				>
					{isSaving ? <Loader2 className="h-3 w-3 animate-spin" /> : <Save className="h-3 w-3" />}
					Save limits
				</button>
				{isSuccess && !isSaving && <span className="text-[11px] font-mono text-green-400">Saved</span>}
				{isError && <span className="text-[11px] font-mono text-red-400">Save failed</span>}
			</div>
		</section>
	);
}
