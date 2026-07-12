import { createAuthQuery, getApi } from "@/lib/query-kit";

/**
 * One vw_review_gauge cell: address-rate telemetry for a (category,
 * change_class) pair within an installation. Rates are null until enough PRs
 * have closed to compute them.
 */
export interface GaugeRow {
  installation_id: number;
  category: string;
  change_class: string;
  posted_findings: number;
  addressed_human: number;
  addressed_agent: number;
  dismissed: number;
  ignored: number;
  deferred: number;
  /** Human-weighted address rate 0..1 (agent fixes count half). Null pre-outcomes. */
  address_rate: number | null;
  /** Dismiss rate 0..1. Null pre-outcomes. */
  dismiss_rate: number | null;
  /** Median seconds from finding posted to PR merge. Null until PRs merge. */
  median_seconds_to_merge: number | null;
}

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
