import { useQueryClient } from "@tanstack/react-query";
import { createAuthMutation, createAuthQuery, getApi } from "@/lib/query-kit";

/**
 * Org-level memory tuning. Lives inside `installations.default_settings`
 * (same JSONB column as feature flags), so read/write goes through the
 * existing /defaults endpoints — no new API route needed.
 *
 * Every field is nullable. `null` means "inherit the hardcoded default
 * on the backend." Zero and 0.5 are legitimate user values, which is
 * why we can't treat zero as "unset."
 */
export type MemorySettings = {
	threshold_finding_enrich: number | null;
	threshold_specialist_min: number | null;
	threshold_scenario_trigger: number | null;
	threshold_scenario_dedupe: number | null;
	disable_shared_decay: boolean | null;
};

/** Hardcoded defaults mirrored from backend/internal/memory/thresholds.go. */
export const MEMORY_DEFAULTS = {
	threshold_finding_enrich: 0.5,
	threshold_specialist_min: 0.6,
	threshold_scenario_trigger: 0.75,
	threshold_scenario_dedupe: 0.85,
	disable_shared_decay: false,
} as const;

/**
 * Pulls the full org defaults blob and extracts just the memory fields.
 * Unknown keys pass through untouched on save — we preserve whatever
 * other settings (feature flags, personas) live in the same JSONB.
 */
/** Keyed by installation so switching orgs never serves another org's blob
 * from cache. Callers pass `active?.id`; mirror `useOrgDefaults` exactly so
 * the two hooks share one cache entry per installation (reads/writes stay in
 * sync, features page included). */
type MemorySettingsVars = { installationId?: number };

/**
 * Reuses the `org-defaults` cache key (now scoped by installation) so reads
 * and writes share state with `useOrgDefaults` consumers elsewhere in the
 * app. Saving memory settings invalidates the org-defaults queries too — no
 * stale reads on the features page or anywhere else the same JSONB blob is
 * surfaced.
 */
export const useMemorySettings = createAuthQuery<Record<string, unknown>, MemorySettingsVars>({
	queryKey: ["org-defaults"],
	fetcher: (_vars, ctx) => {
		const api = getApi(ctx);
		return api.get<Record<string, unknown>>(`/api/v1/installations/${api.active?.id}/defaults`);
	},
	staleTime: 5 * 60 * 1000,
});

const useSaveMemorySettingsMutation = createAuthMutation<
	{ status: string },
	Record<string, unknown>
>({
	mutationFn: (settings, ctx) => {
		const api = getApi(ctx);
		return api.put<{ status: string }>(
			`/api/v1/installations/${api.active?.id}/defaults`,
			settings,
		);
	},
});

export const useSaveMemorySettings = () => {
	const qc = useQueryClient();
	return useSaveMemorySettingsMutation({
		onSuccess: () => qc.invalidateQueries({ queryKey: useMemorySettings.getKey() }),
		onError: (err) => console.error("[save-memory-settings] failed:", err.message),
	});
};

/**
 * Safely extracts a memory field from the raw org-defaults JSON blob.
 * Returns null for out-of-range floats so the UI can fall back to
 * defaults — backend validates again, but we fail closed here too.
 */
export function readThreshold(
	blob: Record<string, unknown> | undefined,
	key: keyof Pick<
		MemorySettings,
		| "threshold_finding_enrich"
		| "threshold_specialist_min"
		| "threshold_scenario_trigger"
		| "threshold_scenario_dedupe"
	>,
): number | null {
	const v = blob?.[key];
	if (typeof v !== "number") return null;
	if (v < 0 || v > 1 || Number.isNaN(v)) return null;
	return v;
}

export function readBool(
	blob: Record<string, unknown> | undefined,
	key: "disable_shared_decay",
): boolean | null {
	const v = blob?.[key];
	return typeof v === "boolean" ? v : null;
}
