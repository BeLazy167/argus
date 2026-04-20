/**
 * PostHog event helpers.
 *
 * Thin wrappers over `posthog.capture`. `posthog-js` no-ops when not
 * initialized (e.g. POSTHOG_KEY unset) so callers never need to gate.
 */
import posthog from "posthog-js";

export const track = (event: string, props?: Record<string, unknown>): void => {
  posthog.capture(event, props);
};

export const trackClick = (name: string, props?: Record<string, unknown>): void => {
  track("ui.click", { name, ...props });
};
