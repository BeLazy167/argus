"use client";

import { Brain, Loader2, RotateCw, Save, Undo2 } from "lucide-react";
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
					<p className="text-sm font-mono text-slate-500 leading-relaxed">
						How strictly Argus matches past knowledge to new code, and how long org-wide patterns
						keep their influence. Changes apply from the next review.
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
						Settings failed to load. Check your connection, then reload the page.
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

	const decayOn = !(draft.disable_decay ?? MEMORY_DEFAULTS.disable_shared_decay);

	return (
		<>
			<Section
				title="Pattern matching"
				subtitle="How similar a remembered pattern must be to new code before Argus uses it. Slide right for fewer, more-certain matches; left for more context."
			>
				<ThresholdField
					title="Comment enrichment"
					keyChip="finding_enrich"
					description="A saved pattern is attached to a review comment only when it matches this closely."
					lowLabel="more context"
					highLabel="stricter"
					value={draft.finding_enrich}
					defaultValue={MEMORY_DEFAULTS.threshold_finding_enrich}
					onChange={(v) => setDraft((d) => ({ ...d, finding_enrich: v }))}
				/>
				<ThresholdField
					title="Reviewer briefing"
					keyChip="specialist_min"
					description="Repo and org memory must be at least this relevant to appear in a reviewer's per-file briefing."
					lowLabel="more context"
					highLabel="stricter"
					value={draft.specialist_min}
					defaultValue={MEMORY_DEFAULTS.threshold_specialist_min}
					onChange={(v) => setDraft((d) => ({ ...d, specialist_min: v }))}
				/>
			</Section>

			<Section
				title="Scenario engine"
				subtitle="Scenarios are failure risks Argus watches for on every PR. These two gates control how they're counted and created."
			>
				<ThresholdField
					title="Failure recognition"
					keyChip="scenario_trigger"
					description="A simulation failure counts toward an existing scenario only when it matches this closely."
					lowLabel="count loosely"
					highLabel="count strictly"
					value={draft.scenario_trigger}
					defaultValue={MEMORY_DEFAULTS.threshold_scenario_trigger}
					onChange={(v) => setDraft((d) => ({ ...d, scenario_trigger: v }))}
				/>
				<ThresholdField
					title="Duplicate detection"
					keyChip="scenario_dedupe"
					description="A proposed scenario this similar to an existing one is treated as a duplicate and skipped."
					lowLabel="merge more"
					highLabel="keep more"
					value={draft.scenario_dedupe}
					defaultValue={MEMORY_DEFAULTS.threshold_scenario_dedupe}
					onChange={(v) => setDraft((d) => ({ ...d, scenario_dedupe: v }))}
				/>
			</Section>

			<Section
				title="Org-wide pattern lifetime"
				subtitle="Org-wide patterns start at full confidence. Left dormant, they fade on a nightly schedule; re-confirming one restores it to full strength."
			>
				<div className="border border-iron bg-charcoal/60 p-4 space-y-4">
					<div className="flex items-start justify-between gap-4">
						<div className="flex-1 min-w-0">
							<div className="flex items-baseline gap-3 mb-1">
								<span className="text-sm font-mono text-slate-100">Confidence decay</span>
								<KeyChip name="disable_shared_decay" />
								{draft.disable_decay !== null && <OverrideChip />}
							</div>
							<p className="text-xs font-mono text-slate-500 leading-relaxed">
								{decayOn
									? "Dormant patterns fade after 30 days, stop influencing reviews below 0.30, and retire at 0.20 — about 5 months without a re-confirmation."
									: "Decay is off. Patterns keep full influence forever until deleted by hand."}
							</p>
						</div>
						<div className="flex items-center gap-3 shrink-0">
							<button
								type="button"
								onClick={() =>
									setDraft((d) => ({
										...d,
										// UI is framed positively (decay ON/OFF); storage keeps the
										// backend's disable_shared_decay flag, so toggling decay off
										// stores disable=true and vice versa.
										disable_decay: decayOn,
									}))
								}
								aria-checked={decayOn}
								role="switch"
								aria-label="Confidence decay"
								className={`relative h-6 w-11 shrink-0 transition-colors ${
									decayOn ? "bg-amber" : "bg-iron"
								}`}
							>
								<span
									className={`absolute top-0.5 h-5 w-5 bg-slate-100 transition-transform ${
										decayOn ? "translate-x-5" : "translate-x-0.5"
									}`}
								/>
							</button>
							<button
								type="button"
								onClick={() => setDraft((d) => ({ ...d, disable_decay: null }))}
								disabled={draft.disable_decay === null}
								aria-label="Reset confidence decay to default"
								title="Reset to default (decay on)"
								className="p-1 text-slate-600 hover:text-slate-300 disabled:opacity-30 disabled:hover:text-slate-600 transition-colors"
							>
								<RotateCw className="h-3.5 w-3.5" aria-hidden />
							</button>
						</div>
					</div>

					<DecayTimeline active={decayOn} />
				</div>
			</Section>

			<footer className="flex items-center justify-between border-t border-iron pt-6">
				<p className="text-xs font-mono text-slate-600" aria-live="polite">
					{dirty ? "Unsaved changes" : isSuccess ? "Saved" : " "}
				</p>
				<div className="flex items-center gap-2">
					{dirty && (
						<button
							type="button"
							onClick={() => setDraft(initial)}
							className="flex items-center gap-2 px-3 py-2 text-xs font-mono text-slate-500 hover:text-slate-200 transition-colors"
						>
							<Undo2 className="h-3.5 w-3.5" aria-hidden />
							Discard
						</button>
					)}
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
						Save changes
					</button>
				</div>
			</footer>

			{isError && (
				<p className="text-xs font-mono text-red-400" role="alert">
					Save failed. Your edits are still here — check your connection and save again.
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
				<p className="text-xs font-mono text-slate-500 leading-relaxed max-w-xl">{subtitle}</p>
			</div>
			<div className="space-y-3">{children}</div>
		</section>
	);
}

/** The backend config key, kept visible so settings map 1:1 to the docs. */
function KeyChip({ name }: { name: string }) {
	return (
		<code className="text-[10px] font-mono text-slate-600 border border-iron/60 px-1.5 py-0.5 tracking-tight">
			{name}
		</code>
	);
}

function OverrideChip({ delta }: { delta?: number }) {
	return (
		<span className="text-[10px] font-mono text-amber border border-amber/40 bg-amber/10 px-1.5 py-0.5 tabular-nums">
			{delta === undefined || delta === 0
				? "override"
				: `${delta > 0 ? "+" : "−"}${Math.abs(delta).toFixed(2)}`}
		</span>
	);
}

/**
 * ThresholdField renders one [0, 1] similarity control. The numeric input is
 * primary; the slider is a live scrub with the default marked by a notch on
 * the track. While the field is focused it holds the raw typed string so
 * intermediate states like "0." or "" survive keystrokes; the value is
 * parsed/clamped/rounded on blur or Enter. Empty commits to null (inherit
 * default); an explicit 0 is a valid override, not coerced away. Stepping the
 * slider writes an explicit override (not null) even if the user lands on the
 * default. Reset re-inherits (null).
 */
function ThresholdField({
	title,
	keyChip,
	description,
	lowLabel,
	highLabel,
	value,
	defaultValue,
	onChange,
}: {
	title: string;
	keyChip: string;
	description: string;
	lowLabel: string;
	highLabel: string;
	value: number | null;
	defaultValue: number;
	onChange: (v: number | null) => void;
}) {
	const effective = value ?? defaultValue;
	const overridden = value !== null;
	const delta = overridden ? Number((effective - defaultValue).toFixed(2)) : 0;
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
		<div
			className={`border bg-charcoal/60 p-4 space-y-3 transition-colors ${
				overridden ? "border-amber/40" : "border-iron"
			}`}
		>
			<div className="flex items-start justify-between gap-4">
				<div className="flex-1 min-w-0">
					<div className="flex items-baseline gap-3 mb-1 flex-wrap">
						<span className="text-sm font-mono text-slate-100">{title}</span>
						<KeyChip name={keyChip} />
						{overridden && <OverrideChip delta={delta} />}
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
						aria-label={`${title} value`}
						className="w-20 bg-[#0a0a12] border border-iron px-2 py-1 text-sm font-mono text-slate-200 text-right focus:border-amber focus:outline-none tabular-nums"
					/>
					<button
						type="button"
						onClick={() => onChange(null)}
						disabled={!overridden}
						aria-label={`Reset ${title} to default`}
						title={`Reset to default (${defaultValue.toFixed(2)})`}
						className="p-1 text-slate-600 hover:text-slate-300 disabled:opacity-30 disabled:hover:text-slate-600 transition-colors"
					>
						<RotateCw className="h-3.5 w-3.5" aria-hidden />
					</button>
				</div>
			</div>

			<div className="space-y-1">
				<div className="relative">
					<input
						type="range"
						min={0}
						max={1}
						step={0.01}
						value={effective}
						onChange={(e) => scrub(e.target.value)}
						aria-label={`${title} slider`}
						className="threshold-range w-full"
						style={
							{
								"--fill": `${effective * 100}%`,
							} as React.CSSProperties
						}
					/>
					{/* Default notch: a fixed tick on the track so the factory value
					 * stays visible while scrubbing away from it. */}
					<span
						aria-hidden
						className="absolute top-1/2 -translate-y-1/2 h-3 w-px bg-slate-500 pointer-events-none"
						style={{ left: `${defaultValue * 100}%` }}
					/>
				</div>
				<div className="flex justify-between text-[10px] font-mono text-slate-600 tabular-nums">
					<span>&larr; {lowLabel}</span>
					<span>default {defaultValue.toFixed(2)}</span>
					<span>{highLabel} &rarr;</span>
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
          display: block;
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
        @media (prefers-reduced-motion: reduce) {
          .threshold-range::-webkit-slider-thumb {
            transition: none;
          }
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

/**
 * DecayTimeline draws the actual retirement policy as a confidence-over-time
 * curve: full strength through the 30-day grace window, a weekly fade, the
 * 0.30 line where a pattern stops influencing reviews, and retirement at
 * 0.20 (~5 months dormant). With decay off it flattens to a constant line.
 * Pure SVG derived from the real policy constants — no animation, so it is
 * identical under prefers-reduced-motion.
 */
function DecayTimeline({ active }: { active: boolean }) {
	// Policy constants mirrored from the backend reconciler: 30d grace, then
	// -0.05/week from 1.00; influence floor 0.30 (~4.2 mo); retire 0.20 (~4.7 mo).
	const W = 600;
	const H = 96;
	const PAD_X = 8;
	const MONTHS = 5.5;
	const x = (months: number) => PAD_X + (months / MONTHS) * (W - 2 * PAD_X);
	const y = (conf: number) => 10 + (1 - conf) * (H - 34);
	const graceEnd = 1; // ~30 days
	const floorAt = graceEnd + 14 / 4.345; // 14 weekly steps to 0.30
	const retireAt = graceEnd + 16 / 4.345; // 16 weekly steps to 0.20

	return (
		<figure className="m-0">
			<svg
				viewBox={`0 0 ${W} ${H}`}
				className="w-full h-auto"
				role="img"
				aria-label={
					active
						? "Confidence timeline: full strength for 30 days, fading weekly, below 0.30 a pattern stops influencing reviews at about four months, retired at 0.20 after about five months dormant."
						: "Decay disabled: confidence stays at full strength indefinitely."
				}
			>
				{/* influence floor */}
				<line
					x1={PAD_X}
					x2={W - PAD_X}
					y1={y(0.3)}
					y2={y(0.3)}
					stroke="oklch(1 0 0 / 12%)"
					strokeDasharray="3 4"
				/>
				{active ? (
					<>
						{/* grace + fade curve */}
						<path
							d={`M ${x(0)} ${y(1)} L ${x(graceEnd)} ${y(1)} L ${x(floorAt)} ${y(0.3)} L ${x(retireAt)} ${y(0.2)}`}
							fill="none"
							stroke="var(--color-amber, oklch(0.47 0.157 37.304))"
							strokeWidth="2"
						/>
						{/* retirement mark */}
						<line
							x1={x(retireAt)}
							x2={x(retireAt)}
							y1={y(0.2) - 6}
							y2={y(0.2) + 6}
							stroke="oklch(0.637 0.237 25.331)"
							strokeWidth="2"
						/>
						<text
							x={x(graceEnd / 2)}
							y={y(1) - 6}
							textAnchor="middle"
							className="fill-slate-500"
							fontSize="9"
							fontFamily="monospace"
						>
							30d grace
						</text>
						<text
							x={x(floorAt)}
							y={y(0.3) + 14}
							textAnchor="middle"
							className="fill-slate-500"
							fontSize="9"
							fontFamily="monospace"
						>
							0.30 stops influencing
						</text>
						<text
							x={x(retireAt)}
							y={y(0.2) - 10}
							textAnchor="end"
							className="fill-slate-500"
							fontSize="9"
							fontFamily="monospace"
						>
							0.20 retired ~5mo
						</text>
					</>
				) : (
					<>
						<line
							x1={x(0)}
							x2={W - PAD_X}
							y1={y(1)}
							y2={y(1)}
							stroke="oklch(1 0 0 / 25%)"
							strokeWidth="2"
						/>
						<text
							x={W / 2}
							y={y(1) + 16}
							textAnchor="middle"
							className="fill-slate-600"
							fontSize="9"
							fontFamily="monospace"
						>
							decay off — full strength forever
						</text>
					</>
				)}
				{/* x-axis month ticks */}
				{[0, 1, 2, 3, 4, 5].map((m) => (
					<text
						key={m}
						x={x(m)}
						y={H - 4}
						textAnchor="middle"
						className="fill-slate-700"
						fontSize="8"
						fontFamily="monospace"
					>
						{m}mo
					</text>
				))}
			</svg>
			<figcaption className="sr-only">
				Org-wide pattern confidence over months of dormancy.
			</figcaption>
		</figure>
	);
}
