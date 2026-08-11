import { type EffectCallback, useEffect } from "react";

/** Run a true external-system setup once when a keyed command component mounts. */
export function useMountEffect(effect: EffectCallback): void {
	// The caller owns remounting through its key; dependency changes are intentionally ignored.
	// biome-ignore lint/correctness/useExhaustiveDependencies: mount-only external synchronization
	useEffect(effect, []);
}
