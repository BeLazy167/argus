import { createQuery } from "react-query-kit";

/**
 * Public OpenRouter queries for the marketing BYOK estimator.
 *
 * Deliberately NOT `createAuthQuery`, unlike every other module here: these hit
 * the Next.js proxy routes from a marketing page, where there is no signed-in
 * user and no installation to scope by. `useOpenRouterModels` in
 * `openrouter-models.ts` is the authenticated, installation-scoped sibling that
 * talks to the backend — same vendor, different endpoint and audience.
 */

export type PublicModel = {
  id: string;
  name: string;
  prompt: number;
  completion: number;
};

export type PublicProvider = {
  provider: string;
  prompt: number;
  completion: number;
  quantization: string | null;
};

/**
 * The model catalogue.
 *
 * Returns an empty array rather than throwing when the upstream list is empty,
 * so the caller can keep its static fallback and never render a blank or $0
 * estimate during an OpenRouter outage.
 */
export const useOpenRouterCatalog = createQuery<PublicModel[]>({
  queryKey: ["openrouter", "public", "models"],
  fetcher: async (): Promise<PublicModel[]> => {
    const res = await fetch("/api/openrouter/models");
    if (!res.ok) throw new Error(`openrouter models: ${res.status}`);
    const body = (await res.json()) as { models?: PublicModel[] };
    return body.models ?? [];
  },
  // Prices move slowly and this renders on a public page that can be reloaded
  // hard; a long stale window keeps a burst of visitors off the proxy route.
  staleTime: 5 * 60 * 1000,
  retry: 1,
});

/**
 * Per-provider pricing for one model.
 *
 * Keyed by model id, so switching models yields `undefined` data until the new
 * request settles. That is the behaviour the estimator wants: it must not show
 * the previous provider's price under the new model's name.
 */
export const useOpenRouterEndpoints = createQuery<PublicProvider[], { modelId: string }>({
  queryKey: ["openrouter", "public", "endpoints"],
  fetcher: async ({ modelId }): Promise<PublicProvider[]> => {
    const res = await fetch(`/api/openrouter/endpoints?model=${encodeURIComponent(modelId)}`);
    if (!res.ok) throw new Error(`openrouter endpoints: ${res.status}`);
    const body = (await res.json()) as { providers?: PublicProvider[] };
    return body.providers ?? [];
  },
  staleTime: 5 * 60 * 1000,
  retry: 1,
});
