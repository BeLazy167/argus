"use client";

import { Brain, Loader2, RotateCw, Save } from "lucide-react";
import { useMemo, useState } from "react";
import {
	MEMORY_DEFAULTS,
	readBool,
	readThreshold,
	useMemorySettings,
	useSaveMemorySettings,
} from "@/lib/queries/memory-settings";
import { useInstallation } from "@/providers/installation-provider";

/** Shape of the draft held in local state — all fields nullable. Null means
 * "inherit the default"; a number or boolean is an explicit override. */
type Draft = {
	finding_enrich: number | null;
	specialist_min: number | null;
	scenario_trigger: number | null;
	scenario_dedupe: number | null;
	disable_decay: boolean | null;
};

const EMPTY_DRAFT: Draft = {
	finding_enrich: null,
	specialist_min: null,
	scenario_trigger: null,
	scenario_dedupe: null,
	disable_decay: null,
};

/** Extracts the memory draft from the raw org-defaults blob. */
function resolveDraft(data: Record<string, unknown>): Draft {
	return {
		finding_enrich: readThreshold(data, "threshold_finding_enrich"),
		specialist_min: readThreshold(data, "threshold_specialist_min"),
		scenario_trigger: readThreshold(data, "threshold_scenario_trigger"),
		scenario_dedupe: readThreshold(data, "threshold_scenario_dedupe"),
		disable_decay: readBool(data, "disable_shared_decay"),
	};
}

export default function MemorySettingsPage() {
	const { active } = useInstallation();
	const installationId = active?.id;
	const { data, isLoading } = useMemorySettings({ variables: { installationId } });
	const save = useSaveMemorySettings();

	// Seed derived inline from the fetched blob — never synced into state via
	// an effect. The form (below) owns its draft after mount, so a background
	// refetch (window refocus, sibling invalidation) can't clobber unsaved
	// edits. Switching orgs changes `installationId`, remounting the form with
	// the new org's snapshot via the `key` prop.
	const seed = useMemo(() => (data ? resolveDraft(data) : EMPTY_DRAFT), [data]);

	const onSave = async (draft: Draft) => {
		// The `default_settings` JSONB holds unrelated keys (feature flags,
		// personas, model configs) — spread the fetched blob so we preserve
		// them and only overwrite the five memory fields.
		if (!data) return;
		const payload: Record<string, unknown> = { ...data };
		payload.threshold_finding_enrich = draft.finding_enrich;
		payload.threshold_specialist_min = draft.specialist_min;
		payload.threshold_scenario_trigger = draft.scenario_trigger;
		payload.threshold_scenario_dedupe = draft.scenario_dedupe;
		payload.disable_shared_decay = draft.disable_decay;
		await save.mutateAsync(payload);
	};

	return (
		<div className="min-h-screen bg-[#0a0a12] text-slate-200">
			<div className="max-w-3xl mx-auto p-8 space-y-10">
				<header>
					<div className="flex items-center gap-2 mb-2">
						<Brain className="h-5 w-5 text-slate-500" aria-hidden />
						<h1 className="text-2xl font-mono text-slate-100">Memory</h1>
					</div>
					<p className="text-sm text-slate-500 font-mono leading-relaxed">
						Tune the similarity gates and retirement policy for this installation's memory system.
						Changes apply to the next review.
					</p>
				</header>

				{isLoading ? (
					<div className="flex items-center gap-2 text-slate-500 font-mono text-sm">
						<Loader2 className="h-4 w-4 animate-spin" aria-hidden />
						Loading settings...
					</div>
				) : data ? (
					<MemoryForm
						key={installationId}
						initialValues={seed}
						onSave={onSave}
						isSaving={save.isPending}
						isError={save.isError}
						isSuccess={save.isSuccess}
					/>
				) : (
					<p className="text-xs font-mono text-red-400">
						{"// Failed to load settings — check connection and retry."}
					</p>
				)}
			</div>
		</div>
	);
}

/**
 * MemoryForm owns the editable draft. It is keyed by installation in the
 * parent, so it remounts (re-seeding from `initialValues`) only when the org
 * changes — a plain refetch of the same org leaves the draft untouched. The
 * useState initializer reads the snapshot once; after a successful save the
 * baseline advances via the save handler (an event, not an effect).
 */
function MemoryForm({
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
	const [initial, setInitial] = useState<Draft>(initialValues);
	const dirty = !draftEquals(draft, initial);

	const handleSave = async () => {
		await onSave(draft);
		// Advance the baseline so the form reads "Saved"/clean without waiting
		// on the background refetch — event-handler reset, not effect-on-data.
		setInitial(draft);
	};

	return (
		<>
			<Section
				title="Similarity thresholds"
				subtitle="How confident the memory system must be before a match influences a review. Raise to reduce false matches; lower to surface more context."
			>
				<ThresholdField
					label="finding_enrich"
					description="Threshold below which a pattern match will not enrich a review comment."
					value={draft.finding_enrich}
					defaultValue={MEMORY_DEFAULTS.threshold_finding_enrich}
					onChange={(v) => setDraft((d) => ({ ...d, finding_enrich: v }))}
				/>
				<ThresholdField
					label="specialist_min"
					description="Server-side similarity cutoff for repo + shared reads in the specialist memory block."
					value={draft.specialist_min}
					defaultValue={MEMORY_DEFAULTS.threshold_specialist_min}
					onChange={(v) => setDraft((d) => ({ ...d, specialist_min: v }))}
				/>
				<ThresholdField
					label="scenario_trigger"
					description="Minimum similarity for a simulation failure to increment an existing scenario's trigger count."
					value={draft.scenario_trigger}
					defaultValue={MEMORY_DEFAULTS.threshold_scenario_trigger}
					onChange={(v) => setDraft((d) => ({ ...d, scenario_trigger: v }))}
				/>
				<ThresholdField
					label="scenario_dedupe"
					description="Above this similarity, a candidate scenario is treated as a duplicate of an existing one and skipped."
					value={draft.scenario_dedupe}
					defaultValue={MEMORY_DEFAULTS.threshold_scenario_dedupe}
					onChange={(v) => setDraft((d) => ({ ...d, scenario_dedupe: v }))}
				/>
			</Section>

			<Section
				title="Shared container retirement"
				subtitle="Org-wide patterns decay and retire over ~6 months of dormancy. Disable to keep everything forever."
			>
				<ToggleField
					label="disable_shared_decay"
					description="When ON, the nightly reconciler skips the _shared container decay phase. Patterns remain until manually deleted."
					value={draft.disable_decay ?? MEMORY_DEFAULTS.disable_shared_decay}
					defaultValue={MEMORY_DEFAULTS.disable_shared_decay}
					overridden={draft.disable_decay !== null}
					onChange={(v) => setDraft((d) => ({ ...d, disable_decay: v }))}
					onReset={() => setDraft((d) => ({ ...d, disable_decay: null }))}
				/>
			</Section>

			<footer className="flex items-center justify-between border-t border-iron pt-6">
				<p className="text-xs font-mono text-slate-600">
					{dirty ? "// Unsaved changes" : isSuccess ? "// Saved" : "// Clean"}
				</p>
				<button
					type="button"
					onClick={handleSave}
					disabled={!dirty || isSaving}
					className="flex items-center gap-2 px-4 py-2 bg-slate-800 border border-iron text-sm font-mono text-slate-200 hover:bg-slate-700 disabled:opacity-40 disabled:cursor-not-allowed transition-colors"
				>
					{isSaving ? (
						<Loader2 className="h-4 w-4 animate-spin" aria-hidden />
					) : (
						<Save className="h-4 w-4" aria-hidden />
					)}
					Save
				</button>
			</footer>

			{isError && (
				<p className="text-xs font-mono text-red-400">
					{"// Failed to save — check connection and retry."}
				</p>
			)}
		</>
	);
}

function draftEquals(a: Draft, b: Draft): boolean {
	return (
		a.finding_enrich === b.finding_enrich &&
		a.specialist_min === b.specialist_min &&
		a.scenario_trigger === b.scenario_trigger &&
		a.scenario_dedupe === b.scenario_dedupe &&
		a.disable_decay === b.disable_decay
	);
}

function Section({
	title,
	subtitle,
	children,
}: {
	title: string;
	subtitle: string;
	children: React.ReactNode;
}) {
	return (
		<section className="space-y-4">
			<div className="space-y-1">
				<h2 className="text-sm font-mono text-slate-300 uppercase tracking-wider">{title}</h2>
				<p className="text-xs font-mono text-slate-500 leading-relaxed">{subtitle}</p>
			</div>
			<div className="space-y-3">{children}</div>
		</section>
	);
}

/**
 * ThresholdField renders one [0, 1] similarity control as:
 *   label  [0.75]  (default: 0.75)  [reset]
 *   [░░░░░░░░░░░░░░░░░░▓▓▓▓▓▓▓▓▓▓▓▓] 0.00 — 1.00
 *
 * The numeric input is primary; the slider is a live scrub. While the field
 * is focused it holds the raw typed string so intermediate states like "0."
 * or "" survive keystrokes; the value is parsed/clamped/rounded on blur or
 * Enter. Empty commits to null (inherit default); an explicit 0 is a valid
 * override, not coerced away. Stepping the slider writes an explicit override
 * (not null) even if the user lands on the default. Reset re-inherits (null).
 */
function ThresholdField({
	label,
	description,
	value,
	defaultValue,
	onChange,
}: {
	label: string;
	description: string;
	value: number | null;
	defaultValue: number;
	onChange: (v: number | null) => void;
}) {
	const effective = value ?? defaultValue;
	const overridden = value !== null;
	const [editing, setEditing] = useState(false);
	const [text, setText] = useState("");

	// Commit the raw draft string. Empty -> inherit default (null). Otherwise
	// parse, clamp to [0, 1], round to 2dp; explicit 0 stays 0. Non-numeric
	// garbage is dropped, keeping the prior value.
	const commit = (raw: string) => {
		setEditing(false);
		const trimmed = raw.trim();
		if (trimmed === "") {
			onChange(null);
			return;
		}
		const n = Number.parseFloat(trimmed);
		if (Number.isNaN(n)) return;
		const clamped = Math.max(0, Math.min(1, n));
		onChange(Number(clamped.toFixed(2)));
	};

	// Slider is a live scrub — no intermediate invalid states to preserve.
	const scrub = (raw: string) => {
		const n = Number.parseFloat(raw);
		if (Number.isNaN(n)) return;
		onChange(Number(Math.max(0, Math.min(1, n)).toFixed(2)));
	};

	const display = editing ? text : effective.toFixed(2);

	return (
		<div className="border border-iron bg-charcoal/60 p-4 space-y-3">
			<div className="flex items-start justify-between gap-4">
				<div className="flex-1 min-w-0">
					<div className="flex items-baseline gap-3 mb-1">
						<span className="text-sm font-mono text-slate-200">{label}</span>
						<span
							className={`text-xs font-mono ${overridden ? "text-amber" : "text-slate-600"}`}
							title={overridden ? "overridden" : "using default"}
						>
							{overridden ? "// overridden" : "// default"}
						</span>
					</div>
					<p className="text-xs font-mono text-slate-500 leading-relaxed">{description}</p>
				</div>
				<div className="flex items-center gap-2 shrink-0">
					<input
						type="text"
						inputMode="decimal"
						value={display}
						onFocus={() => {
							setText(effective.toFixed(2));
							setEditing(true);
						}}
						onChange={(e) => setText(e.target.value)}
						onBlur={(e) => commit(e.target.value)}
						onKeyDown={(e) => {
							if (e.key === "Enter") e.currentTarget.blur();
						}}
						aria-label={`${label} value`}
						className="w-20 bg-[#0a0a12] border border-iron px-2 py-1 text-sm font-mono text-slate-200 text-right focus:border-amber focus:outline-none tabular-nums"
					/>
					<button
						type="button"
						onClick={() => onChange(null)}
						disabled={!overridden}
						aria-label={`Reset ${label} to default`}
						title={`Reset to ${defaultValue}`}
						className="p-1 text-slate-600 hover:text-slate-300 disabled:opacity-30 disabled:hover:text-slate-600 transition-colors"
					>
						<RotateCw className="h-3.5 w-3.5" aria-hidden />
					</button>
				</div>
			</div>

			<div className="space-y-1">
				<input
					type="range"
					min={0}
					max={1}
					step={0.01}
					value={effective}
					onChange={(e) => scrub(e.target.value)}
					aria-label={`${label} slider`}
					className="threshold-range w-full"
					style={
						{
							"--fill": `${effective * 100}%`,
						} as React.CSSProperties
					}
				/>
				<div className="flex justify-between text-[10px] font-mono text-slate-600 tabular-nums">
					<span>0.00</span>
					<span>default: {defaultValue.toFixed(2)}</span>
					<span>1.00</span>
				</div>
			</div>

			{/* Tactical-terminal range styling. Scoped per-field to keep the CSS
			 * adjacent to the component it drives. No rounding, amber fill, iron
			 * track — consistent with the rest of the dashboard. */}
			<style jsx>{`
        .threshold-range {
          -webkit-appearance: none;
          appearance: none;
          height: 4px;
          background: linear-gradient(
            to right,
            var(--color-amber, oklch(0.47 0.157 37.304)) 0%,
            var(--color-amber, oklch(0.47 0.157 37.304)) var(--fill),
            oklch(1 0 0 / 10%) var(--fill),
            oklch(1 0 0 / 10%) 100%
          );
          outline: none;
          cursor: pointer;
        }
        .threshold-range::-webkit-slider-thumb {
          -webkit-appearance: none;
          appearance: none;
          width: 12px;
          height: 12px;
          background: oklch(0.986 0.002 67.8);
          border: 1px solid oklch(0.47 0.157 37.304);
          cursor: grab;
          transition: transform 120ms ease-out;
        }
        .threshold-range::-webkit-slider-thumb:active {
          cursor: grabbing;
          transform: scale(1.15);
        }
        .threshold-range::-moz-range-thumb {
          width: 12px;
          height: 12px;
          background: oklch(0.986 0.002 67.8);
          border: 1px solid oklch(0.47 0.157 37.304);
          cursor: grab;
        }
        .threshold-range:focus-visible {
          outline: 1px solid oklch(0.47 0.157 37.304);
          outline-offset: 2px;
        }
      `}</style>
		</div>
	);
}

/** ToggleField is the boolean analog of ThresholdField. Same overridden
 * indicator + reset control. Styled identically to the feature-flag toggles
 * already in use at /settings/features for visual consistency. */
function ToggleField({
	label,
	description,
	value,
	defaultValue,
	overridden,
	onChange,
	onReset,
}: {
	label: string;
	description: string;
	value: boolean;
	defaultValue: boolean;
	overridden: boolean;
	onChange: (v: boolean) => void;
	onReset: () => void;
}) {
	return (
		<div className="border border-iron bg-charcoal/60 p-4">
			<div className="flex items-start justify-between gap-4">
				<div className="flex-1 min-w-0">
					<div className="flex items-baseline gap-3 mb-1">
						<span className="text-sm font-mono text-slate-200">{label}</span>
						<span className={`text-xs font-mono ${overridden ? "text-amber" : "text-slate-600"}`}>
							{overridden ? "// overridden" : "// default"}
						</span>
					</div>
					<p className="text-xs font-mono text-slate-500 leading-relaxed mb-2">{description}</p>
					<p className="text-[10px] font-mono text-slate-600 uppercase tracking-wide">
						default: {String(defaultValue)}
					</p>
				</div>
				<div className="flex items-center gap-3 shrink-0">
					<button
						type="button"
						onClick={() => onChange(!value)}
						aria-checked={value}
						role="switch"
						className={`relative h-6 w-11 shrink-0 transition-colors ${
							value ? "bg-amber" : "bg-iron"
						}`}
					>
						<span
							className={`absolute top-0.5 h-5 w-5 bg-slate-100 transition-transform ${
								value ? "translate-x-5" : "translate-x-0.5"
							}`}
						/>
					</button>
					<button
						type="button"
						onClick={onReset}
						disabled={!overridden}
						aria-label={`Reset ${label} to default`}
						title={`Reset to ${String(defaultValue)}`}
						className="p-1 text-slate-600 hover:text-slate-300 disabled:opacity-30 disabled:hover:text-slate-600 transition-colors"
					>
						<RotateCw className="h-3.5 w-3.5" aria-hidden />
					</button>
				</div>
			</div>
		</div>
	);
}
