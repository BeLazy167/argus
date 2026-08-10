import { Shield } from "lucide-react";
import { formatDistanceToNow } from "@/lib/time";
import { Section } from "./Section";
import { EmptyState } from "./primitives";
import { riskColor } from "./constants";

type RiskData = {
  trace_count: number;
  last_trace: string;
};

type RiskSectionProps = {
  riskScore: RiskData | null | undefined;
  expanded: boolean;
  onToggle: () => void;
};

export function RiskSection({ riskScore, expanded, onToggle }: RiskSectionProps) {
  return (
    <Section
      id="risk"
      icon={<Shield className="h-3 w-3 text-[var(--graph-text-muted)]" />}
      title="Trace Risk"
      expanded={expanded}
      onToggle={onToggle}
    >
      {riskScore ? (
        <div className="space-y-2">
          <div className="flex items-center gap-2">
            <span
              className={`inline-block rounded border px-2 py-0.5 text-[10px] font-mono ${riskColor(
                riskScore.trace_count
              )}`}
            >
              {riskScore.trace_count} traces
            </span>
          </div>
          {riskScore.last_trace && (
            <p className="text-[10px] font-mono text-[var(--graph-text-muted)]">
              Last trace: {formatDistanceToNow(riskScore.last_trace)}
            </p>
          )}
        </div>
      ) : (
        <EmptyState text="No risk data yet." />
      )}
    </Section>
  );
}
