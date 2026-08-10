"use client";

import {
  TrendingUp,
  DollarSign,
  Target,
  Clock,
  Zap,
  AlertTriangle,
  Shield,
  Timer,
  Check,
  Network,
  Brain,
  FlaskConical,
  History,
  ThumbsUp,
} from "lucide-react";
import { StatCard, SectionHeader, Loading, Err } from "./stat-cards";
import { fmt$, fmtTok, fmtSecs } from "./formatters";

interface StatsOverview {
  total_reviews: number;
  total_cost: number;
  avg_score: number;
  avg_review_secs: number;
  total_tokens: number;
  critical_finds: number;
  catch_rate: number;
  auto_resolve_events: number;
  auto_resolves: number;
  auto_resolve_attempts: number;
  auto_resolve_api_calls: number;
  patterns_learned: number;
  scenarios_stored: number;
  decision_traces: number;
  feedback_indexed: number;
}

interface ReviewTimesData {
  count: number;
  p50: number;
  p75: number;
  p95: number;
}

interface OverviewSectionProps {
  overviewLoading: boolean;
  overviewError: boolean;
  overviewData: StatsOverview | undefined;
  reviewTimesData: ReviewTimesData | undefined;
  costPerFinding: number;
}

export function OverviewSection({
  overviewLoading,
  overviewError,
  overviewData,
  reviewTimesData,
  costPerFinding,
}: OverviewSectionProps) {
  return (
    <>
      {/* Overview cards */}
      {overviewLoading ? (
        <Loading />
      ) : overviewError ? (
        <Err label="overview" />
      ) : (
        overviewData && (
          <>
            <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mb-4">
              <StatCard
                label="Reviews"
                value={String(overviewData.total_reviews)}
                icon={TrendingUp}
                tip="Total completed/failed/cancelled reviews in period"
              />
              <StatCard
                label="Cost"
                value={fmt$(overviewData.total_cost)}
                icon={DollarSign}
                tip="Sum of LLM API costs across all stages"
              />
              <StatCard
                label="Score"
                value={`${overviewData.avg_score.toFixed(1)}/10`}
                icon={Target}
                tip="Mean review score (1=critical, 10=clean)"
                valueColor="text-primary"
              />
              <StatCard
                label="Time"
                value={fmtSecs(overviewData.avg_review_secs)}
                icon={Clock}
                tip="Average wall-clock time per review"
              />
            </div>
            <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mb-4">
              <StatCard
                label="Tokens"
                value={fmtTok(overviewData.total_tokens)}
                icon={Zap}
                tip="Total LLM tokens consumed (input + output)"
              />
              <StatCard
                label="Critical"
                value={String(overviewData.critical_finds)}
                icon={AlertTriangle}
                tip="Review comments with severity=critical"
                valueColor="text-destructive"
                iconColor="text-destructive"
              />
              <StatCard
                label="Detection"
                value={`${overviewData.catch_rate}%`}
                icon={Shield}
                tip="% of reviews where issues were found (score < 10)"
                valueColor="text-green-500"
                iconColor="text-green-500"
              />
              <StatCard
                label="Cost/Finding"
                value={costPerFinding > 0 ? fmt$(costPerFinding) : "—"}
                icon={DollarSign}
                tip="Total cost ÷ total findings"
              />
            </div>
          </>
        )
      )}

      {/* Review time percentiles */}
      {reviewTimesData && reviewTimesData.count > 0 && (
        <div className="grid grid-cols-3 gap-4 mb-10">
          <StatCard
            label="p50"
            value={fmtSecs(reviewTimesData.p50)}
            icon={Timer}
            tip="Median review duration"
          />
          <StatCard
            label="p75"
            value={fmtSecs(reviewTimesData.p75)}
            icon={Timer}
            tip="75th percentile"
          />
          <StatCard
            label="p95"
            value={fmtSecs(reviewTimesData.p95)}
            icon={Timer}
            tip="95th percentile"
            valueColor="text-destructive"
            iconColor="text-destructive"
          />
        </div>
      )}

      {/* Automated hygiene — diff-only operations, zero LLM cost.
          Surfacing these separately so users don't confuse them with the
          LLM-paid review activity above. */}
      {overviewData && (
        <section className="pt-4 mb-10">
          <SectionHeader
            title="Automated hygiene"
            tip="Free operations we ran on your behalf — no LLM tokens consumed"
          />
          <div className="grid grid-cols-2 md:grid-cols-3 gap-4">
            <StatCard
              label="Auto-resolves"
              value={String(overviewData.auto_resolves)}
              icon={Check}
              tip="Stale review threads we closed automatically when your push modified the flagged lines. Diff-based, no LLM call."
              valueColor="text-green-500"
              iconColor="text-green-500"
            />
            <StatCard
              label="Attempts"
              value={String(overviewData.auto_resolve_attempts)}
              icon={Target}
              tip="Thread-close attempts, including any that GitHub rejected. Resolved ≤ Attempts because GitHub may already have marked a thread resolved."
            />
            <StatCard
              label="GitHub API"
              value={String(overviewData.auto_resolve_api_calls)}
              icon={Network}
              tip="GitHub API calls auto-resolve issued — visible for rate-limit accounting on your installation token."
            />
          </div>
          {overviewData.auto_resolve_events === 0 && (
            <p className="mt-3 text-[10px] font-mono text-muted-foreground">
              No auto-resolves yet. Push a commit that changes a line Argus flagged to see this in
              action.
            </p>
          )}
        </section>
      )}

      {/* Learn layer — BYOK-paid side effects of the memory pipeline.
          Shown separately from the review cards because the cost lives
          in your Supermemory bill, not the LLM bill. */}
      {overviewData && (
        <section className="pt-4 mb-10">
          <SectionHeader
            title="Learn layer"
            tip="Memory activity for this period. Each counter maps to a row your BYOK Supermemory account is storing."
          />
          <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
            <StatCard
              label="Patterns"
              value={String(overviewData.patterns_learned)}
              icon={Brain}
              tip="New cross-repo patterns the pipeline learned this period."
            />
            <StatCard
              label="Scenarios"
              value={String(overviewData.scenarios_stored)}
              icon={FlaskConical}
              tip="New failure scenarios stored. Drive the code-simulation pass on future reviews."
            />
            <StatCard
              label="Decisions"
              value={String(overviewData.decision_traces)}
              icon={History}
              tip="Decision-trace rows — Argus findings linked to dev agrees/dismisses/fixes."
            />
            <StatCard
              label="Feedback"
              value={String(overviewData.feedback_indexed)}
              icon={ThumbsUp}
              tip="Reactions/outcomes captured from reviewer feedback on posted comments."
            />
          </div>
        </section>
      )}
    </>
  );
}
