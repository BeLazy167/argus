"use client";

import { SectionHeader, Loading, Err } from "./stat-cards";
import { AdoptionBar } from "./adoption-bar";
import { fmt$, SEV_COLORS } from "./formatters";
import { stageColor, stageLabel } from "@/lib/stage-labels";

interface StageCost {
  stage: string;
  total_tokens: number;
  total_cost: number;
}

interface FindingsData {
  by_severity: { severity: string; count: number }[];
  new_findings: number;
  pattern_matches: number;
}

interface AdoptionData {
  deep_review_pct: number;
  incremental_pct: number;
  avg_files_per_review: number;
  active_repos: number;
  total_repos: number;
}

interface StageFindingsSectionProps {
  costPerStageLoading: boolean;
  costPerStageError: boolean;
  stageCostData: StageCost[];
  findingsLoading: boolean;
  findingsError: boolean;
  findingsData:
    | (FindingsData & { by_severity: { severity: string; count: number }[] })
    | undefined;
  sevData: { severity: string; count: number }[];
  adoptionLoading: boolean;
  adoptionError: boolean;
  adoptionData: AdoptionData | undefined;
}

export function StageFindingsSection({
  costPerStageLoading,
  costPerStageError,
  stageCostData,
  findingsLoading,
  findingsError,
  findingsData,
  sevData,
  adoptionLoading,
  adoptionError,
  adoptionData,
}: StageFindingsSectionProps) {
  return (
    <>
      {/* Cost by Stage + Findings */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4 pt-10 mb-10">
        <section>
          <SectionHeader title="Cost by Stage" tip="LLM cost breakdown by pipeline stage" />
          {costPerStageLoading ? (
            <Loading />
          ) : costPerStageError ? (
            <Err label="cost per stage" />
          ) : (
            <div className="border border-border bg-card p-5">
              {stageCostData.length > 0 ? (
                <div className="space-y-3">
                  {(() => {
                    const stageMax = Math.max(...stageCostData.map((x) => x.total_cost), 0.01);
                    return stageCostData.map((s) => (
                      <div key={s.stage} className="flex items-center gap-2 text-[10px] font-mono">
                        <span
                          className="w-28 text-muted-foreground shrink-0 truncate"
                          title={stageLabel(s.stage)}
                        >
                          {stageLabel(s.stage)}
                        </span>
                        <div
                          className="flex-1 h-4 bg-border/30 overflow-hidden"
                          style={{ borderRadius: 2 }}
                        >
                          <div
                            className="h-full"
                            style={{
                              width: `${(s.total_cost / stageMax) * 100}%`,
                              background: stageColor(s.stage),
                              borderRadius: 2,
                            }}
                          />
                        </div>
                        <span className="w-14 text-right text-muted-foreground shrink-0">
                          {fmt$(s.total_cost)}
                        </span>
                      </div>
                    ));
                  })()}
                </div>
              ) : (
                <span className="text-xs font-mono text-muted-foreground">No data</span>
              )}
            </div>
          )}
        </section>

        <section>
          <SectionHeader title="Findings" tip="Review comments by severity" />
          {findingsLoading ? (
            <Loading />
          ) : findingsError ? (
            <Err label="findings" />
          ) : (
            findingsData && (
              <div className="border border-border bg-card p-5">
                <div className="space-y-2.5 mb-5">
                  {(() => {
                    const sevMax = Math.max(...sevData.map((x) => x.count), 1);
                    return sevData.map((s) => (
                      <div
                        key={s.severity}
                        className="flex items-center gap-2 text-[10px] font-mono"
                      >
                        <span
                          className="w-[70px] shrink-0 font-medium"
                          style={{ color: SEV_COLORS[s.severity] ?? "#6b7280" }}
                        >
                          {s.severity}
                        </span>
                        <div
                          className="flex-1 h-3 bg-border/30 overflow-hidden"
                          style={{ borderRadius: 2 }}
                        >
                          <div
                            className="h-full"
                            style={{
                              width: `${(s.count / sevMax) * 100}%`,
                              background: SEV_COLORS[s.severity] ?? "#6b7280",
                              borderRadius: 2,
                            }}
                          />
                        </div>
                        <span className="w-8 text-right text-muted-foreground shrink-0">
                          {s.count}
                        </span>
                      </div>
                    ));
                  })()}
                </div>
                <div className="flex gap-3 pt-3 border-t border-border/30">
                  <div className="flex-1 border border-border bg-background p-3 text-center">
                    <span className="text-[10px] font-mono text-muted-foreground uppercase block mb-1">
                      New
                    </span>
                    <span className="text-lg font-mono font-bold text-foreground">
                      {findingsData.new_findings}
                    </span>
                  </div>
                  <div className="flex-1 border border-border bg-background p-3 text-center">
                    <span className="text-[10px] font-mono text-muted-foreground uppercase block mb-1">
                      Pattern
                    </span>
                    <span className="text-lg font-mono font-bold text-foreground">
                      {findingsData.pattern_matches}
                    </span>
                  </div>
                </div>
              </div>
            )
          )}
        </section>
      </div>

      {/* Adoption */}
      <section className="pt-10 mb-10">
        <SectionHeader title="Adoption" tip="Feature usage rates across all reviews" />
        {adoptionLoading ? (
          <Loading />
        ) : adoptionError ? (
          <Err label="adoption" />
        ) : (
          adoptionData && (
            <div className="border border-border bg-card p-5 space-y-5 max-w-[640px]">
              <AdoptionBar label="Deep Review" value={adoptionData.deep_review_pct} />
              <AdoptionBar label="Incremental" value={adoptionData.incremental_pct} />
              <div className="grid grid-cols-2 gap-3 pt-3 border-t border-border/30">
                <div className="border border-border bg-background p-3">
                  <span className="text-[10px] font-mono text-muted-foreground uppercase block mb-1">
                    Active Repos
                  </span>
                  <span className="text-2xl font-mono font-bold text-foreground">
                    {adoptionData.active_repos}/{adoptionData.total_repos}
                  </span>
                </div>
                <div className="border border-border bg-background p-3">
                  <span className="text-[10px] font-mono text-muted-foreground uppercase block mb-1">
                    Avg Files
                  </span>
                  <span className="text-2xl font-mono font-bold text-foreground">
                    {adoptionData.avg_files_per_review.toFixed(1)}
                  </span>
                </div>
              </div>
            </div>
          )
        )}
      </section>
    </>
  );
}
