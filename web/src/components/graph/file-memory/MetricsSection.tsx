import { Activity, Zap } from "lucide-react";
import type { ArchFile } from "@/lib/queries/architecture";
import { Section } from "./Section";
import { MetricRow } from "./primitives";
import { riskBarColor, formatPercentile } from "./constants";

type MetricsSectionProps = {
  archFile: ArchFile;
  expanded: boolean;
  onToggle: () => void;
};

export function MetricsSection({ archFile, expanded, onToggle }: MetricsSectionProps) {
  return (
    <Section
      id="metrics"
      icon={<Activity className="h-3 w-3 text-[var(--graph-text-muted)]" />}
      title="Metrics"
      expanded={expanded}
      onToggle={onToggle}
    >
      <div className="space-y-3">
        {/* Risk score bar */}
        <div>
          <div className="flex items-center justify-between mb-1">
            <span className="text-[10px] font-mono text-[var(--graph-text-muted)]">Risk Score</span>
            <span className="text-[10px] font-mono text-[var(--graph-text)] font-semibold">
              {archFile.risk_score.toFixed(1)}/10
            </span>
          </div>
          <div className="h-1.5 bg-[var(--graph-control-bg)] rounded-full overflow-hidden">
            <div
              className={`h-full ${riskBarColor(archFile.risk_score)} transition-all duration-500`}
              style={{ width: `${Math.min(archFile.risk_score * 10, 100)}%` }}
            />
          </div>
        </div>

        {/* Individual metrics */}
        <div className="space-y-1.5">
          <MetricRow
            label="fan_in"
            value={archFile.fan_in}
            pct={formatPercentile(archFile.percentiles.fan_in)}
          />
          <MetricRow label="fan_out" value={archFile.fan_out} />
          <MetricRow
            label="bugs / 100L"
            value={archFile.bug_density.toFixed(2)}
            pct={formatPercentile(archFile.percentiles.bug_density)}
          />
          <MetricRow
            label="PRs touched"
            value={archFile.change_frequency}
            pct={formatPercentile(archFile.percentiles.change_frequency)}
          />
        </div>

        {/* Insight */}
        {archFile.insight && (
          <div className="rounded border border-amber-500/30 bg-amber-500/5 px-2.5 py-2">
            <div className="flex items-start gap-1.5">
              <Zap className="h-3 w-3 text-amber-400 shrink-0 mt-0.5" />
              <p className="text-[10px] font-mono text-amber-300 leading-relaxed">
                {archFile.insight}
              </p>
            </div>
          </div>
        )}
      </div>
    </Section>
  );
}
