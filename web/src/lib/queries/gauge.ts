import { createAuthQuery, getApi } from "@/lib/query-kit";
import type { GaugeRow } from "@/lib/types";

export type { GaugeRow };

/**
 * Review Gauge — installation-scoped outcome telemetry. Wraps
 * GET /api/v1/stats/gauge, which returns `{ gauge: GaugeRow[] }`.
 */
export const useReviewGauge = createAuthQuery<GaugeRow[]>({
  queryKey: ["stats", "gauge"],
  fetcher: (_vars, ctx) =>
    getApi(ctx)
      .get<{ gauge: GaugeRow[] }>("/api/v1/stats/gauge")
      .then((r) => r.gauge ?? []),
  staleTime: 60_000,
});
