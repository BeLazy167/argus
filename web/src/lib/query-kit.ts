import {
  createQuery,
  createMutation,
  createInfiniteQuery,
  type Middleware,
} from "react-query-kit";
import { useApi } from "@/lib/hooks/use-api";

export type AuthedApi = ReturnType<typeof useApi>;

/**
 * Reads the per-render `AuthedApi` that `withAuthQuery`/`withAuthMutation`
 * inject into the TanStack Query meta channel.
 *
 * Call inside a fetcher/mutationFn:
 *   fetcher: (_vars, ctx) => getApi(ctx).get("/api/v1/repos")
 */
export function getApi(ctx: { meta?: unknown }): AuthedApi {
  const api = (ctx.meta as { api?: AuthedApi } | undefined)?.api;
  if (!api) throw new Error("getApi: missing meta.api — did you forget withAuthQuery / withAuthMutation?");
  return api;
}

/**
 * Middleware that:
 *  1. Calls useApi() once per render and injects it via meta for the fetcher
 *  2. Gates the query behind `active` installation (so we don't hit the API
 *     before the user has linked a GitHub install)
 *
 * Typed as `Middleware<any>` so the same function composes with createQuery,
 * createInfiniteQuery and createMutation variants — the runtime shape is the
 * same for all three; only the inner fetcher/mutationFn signature differs.
 */
/* eslint-disable @typescript-eslint/no-explicit-any */
export const withAuthQuery: Middleware<any> = (useNext) => (options: any) => {
  const api = useApi();
  return useNext({
    ...options,
    enabled: options.enabled !== false && !!api.active,
    meta: { ...(options.meta ?? {}), api, installationId: api.active?.id },
  });
};

export const withAuthMutation: Middleware<any> = (useNext) => (options: any) => {
  const api = useApi();
  return useNext({
    ...options,
    meta: { ...(options.meta ?? {}), api, installationId: api.active?.id },
  });
};
/* eslint-enable @typescript-eslint/no-explicit-any */

/** Factory helpers — same surface as react-query-kit but with auth wired in. */
export const createAuthQuery: typeof createQuery = ((opts: Parameters<typeof createQuery>[0]) =>
  createQuery({ ...opts, use: [withAuthQuery, ...(opts.use ?? [])] })) as typeof createQuery;

export const createAuthMutation: typeof createMutation = ((opts: Parameters<typeof createMutation>[0]) =>
  createMutation({ ...opts, use: [withAuthMutation, ...(opts.use ?? [])] })) as typeof createMutation;

export const createAuthInfiniteQuery: typeof createInfiniteQuery = ((
  opts: Parameters<typeof createInfiniteQuery>[0],
) => createInfiniteQuery({ ...opts, use: [withAuthQuery, ...(opts.use ?? [])] })) as typeof createInfiniteQuery;
