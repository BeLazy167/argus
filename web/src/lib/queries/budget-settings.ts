/**
 * Review budget limits, stored in the same org-defaults blob as the memory
 * thresholds and read through the same endpoint.
 *
 * The limits bound what one review may cost. A soft limit reduces the review —
 * shallower, over fewer files. A hard limit refuses it outright and says so on
 * the pull request. An explicit 0 turns one measure off without touching the
 * others, which is why every field is `number | null` rather than `number`:
 * null means "inherit the default", and 0 is a real value.
 */

/** Defaults the backend applies when a field is unset (admission.DefaultLimits). */
export const BUDGET_DEFAULTS = {
	budget_soft_files: 60,
	budget_hard_files: 400,
	budget_soft_lines: 1500,
	budget_hard_lines: 20000,
	budget_soft_tokens: 400000,
	budget_hard_tokens: 1500000,
	budget_reduced_max_files: 40,
} as const;

export type BudgetKey = keyof typeof BUDGET_DEFAULTS;

/**
 * Reads one limit out of the raw org-defaults blob.
 *
 * Returns null for an absent or non-numeric value so the form can show the
 * inherited default rather than a spurious 0 — the two mean different things
 * here, and rendering "0" for "unset" would tell an operator that a measure is
 * disabled when it is running at the default.
 */
export function readLimit(blob: Record<string, unknown> | undefined, key: BudgetKey): number | null {
	const raw = blob?.[key];
	return typeof raw === "number" && Number.isFinite(raw) ? raw : null;
}
