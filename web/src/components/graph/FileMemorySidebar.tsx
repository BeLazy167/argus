"use client";

import { useState } from "react";
import {
  X,
  ChevronDown,
  Shield,
  Bug,
  GitBranch,
  AlertTriangle,
  Loader2,
  Activity,
  Zap,
  Network,
} from "lucide-react";
import { useFileMemory } from "@/lib/queries/graph";
import { useActiveRepo } from "@/lib/hooks/use-active-repo";
import { formatDistanceToNow } from "@/lib/time";
import type { ArchFile } from "@/lib/queries/architecture";

const SEVERITY_STYLES: Record<string, string> = {
  critical: "border-red-500/30 bg-red-500/10 text-red-400",
  warning: "border-amber-500/30 bg-amber-500/10 text-amber-400",
  suggestion: "border-blue-500/30 bg-blue-500/10 text-blue-400",
  praise: "border-green-500/30 bg-green-500/10 text-green-400",
};

const SOURCE_BADGE_STYLES: Record<string, string> = {
  manual: "border-slate-500/30 bg-slate-500/10 text-slate-400",
  auto_learn: "border-amber-500/30 bg-amber-500/10 text-amber-400",
  convention: "border-blue-500/30 bg-blue-500/10 text-blue-400",
};

const KIND_BADGE_STYLES: Record<string, string> = {
  review: "border-blue-500/30 bg-blue-500/10 text-blue-400",
  scoring: "border-amber-500/30 bg-amber-500/10 text-amber-400",
  triage: "border-purple-500/30 bg-purple-500/10 text-purple-400",
  synthesis: "border-green-500/30 bg-green-500/10 text-green-400",
};

function riskColor(traceCount: number) {
  if (traceCount >= 10) return "border-red-500/40 bg-red-500/15 text-red-400";
  if (traceCount >= 5) return "border-amber-500/40 bg-amber-500/15 text-amber-400";
  return "border-green-500/40 bg-green-500/15 text-green-400";
}

type SectionKey = "metrics" | "deps" | "risk" | "patterns" | "findings" | "traces";

type SectionProps = {
  id: SectionKey;
  icon: React.ReactNode;
  title: string;
  count?: number;
  expanded: boolean;
  onToggle: () => void;
  children: React.ReactNode;
};

function Section({ icon, title, count, expanded, onToggle, children }: SectionProps) {
  return (
    <div className="border-b border-slate-800/50 last:border-0">
      <button
        onClick={onToggle}
        className="flex items-center gap-2 w-full px-4 py-3 hover:bg-slate-800/20 transition-colors"
      >
        {icon}
        <span className="text-[10px] font-mono uppercase tracking-[0.12em] text-slate-300">
          {title}
        </span>
        {count !== undefined && (
          <span className="text-[9px] font-mono text-slate-600 ml-1">
            ({count})
          </span>
        )}
        <ChevronDown
          className={`h-3 w-3 text-slate-600 ml-auto transition-transform duration-200 ${
            expanded ? "rotate-0" : "-rotate-90"
          }`}
        />
      </button>
      <div
        className={`overflow-hidden transition-all duration-200 ${
          expanded ? "max-h-[2000px] opacity-100" : "max-h-0 opacity-0"
        }`}
      >
        <div className="px-4 pb-3">{children}</div>
      </div>
    </div>
  );
}

function EmptyState({ text }: { text: string }) {
  return (
    <p className="text-[10px] font-mono text-slate-600 py-1">{text}</p>
  );
}

function MetricRow({ label, value, pct }: { label: string; value: number | string; pct?: string }) {
  return (
    <div className="flex items-center justify-between text-[10px] font-mono">
      <span className="text-slate-500">{label}</span>
      <div className="flex items-center gap-2">
        <span className="text-slate-300 tabular-nums">{value}</span>
        {pct && <span className="text-amber-500/80">{pct}</span>}
      </div>
    </div>
  );
}

type FileMemorySidebarProps = {
  filePath: string;
  onClose: () => void;
  archFile?: ArchFile;
  allFiles?: ArchFile[];
};

/** Risk color for the progress bar */
function riskBarColor(score: number): string {
  if (score >= 7) return "bg-red-500";
  if (score >= 4) return "bg-amber-500";
  return "bg-emerald-500";
}

/** Format percentile rank — only show callouts for unusual values, hide noise. */
function formatPercentile(pct: number): string {
  if (pct >= 95) return `top ${Math.max(100 - pct, 1)}%`;
  if (pct >= 90) return `top 10%`;
  if (pct >= 75) return `top 25%`;
  return ""; // suppress everything else — too noisy
}

export default function FileMemorySidebar({ filePath, onClose, archFile, allFiles }: FileMemorySidebarProps) {
  const { activeId } = useActiveRepo();
  const { data, isLoading } = useFileMemory(activeId ?? undefined, filePath);

  const [expanded, setExpanded] = useState<Record<SectionKey, boolean>>({
    metrics: true,
    deps: true,
    risk: false,
    patterns: false,
    findings: true,
    traces: false,
  });

  // Resolve dependents/dependencies from allFiles via coupling + edges
  // For the sidebar, show top coupled files from archFile.coupling.

  const toggle = (key: SectionKey) =>
    setExpanded((prev) => ({ ...prev, [key]: !prev[key] }));

  const fileName = filePath.split("/").pop() ?? filePath;

  return (
    <div className="h-full bg-[var(--graph-panel)] flex flex-col overflow-hidden animate-slide-in-right">
      {/* Header */}
      <div className="flex items-center gap-2 px-4 py-3 border-b border-slate-800/50 shrink-0">
        <div className="flex-1 min-w-0">
          <p className="text-[11px] font-mono text-slate-200 truncate" title={filePath}>
            {fileName}
          </p>
          <p className="text-[9px] font-mono text-slate-600 truncate" title={filePath}>
            {filePath}
          </p>
        </div>
        <button
          onClick={onClose}
          className="p-1 rounded hover:bg-slate-800/50 text-slate-600 hover:text-slate-400 transition-colors shrink-0"
        >
          <X className="h-3.5 w-3.5" />
        </button>
      </div>

      {/* Content */}
      <div className="flex-1 overflow-y-auto">
        {isLoading ? (
          <div className="flex items-center justify-center py-12">
            <Loader2 className="h-4 w-4 animate-spin text-slate-600" />
          </div>
        ) : !data ? (
          <div className="flex items-center justify-center py-12">
            <p className="text-[10px] font-mono text-slate-600">No memory data yet.</p>
          </div>
        ) : (
          <>
            {/* Architecture metrics */}
            {archFile && (
              <Section
                id="metrics"
                icon={<Activity className="h-3 w-3 text-slate-500" />}
                title="Metrics"
                expanded={expanded.metrics}
                onToggle={() => toggle("metrics")}
              >
                <div className="space-y-3">
                  {/* Risk score bar */}
                  <div>
                    <div className="flex items-center justify-between mb-1">
                      <span className="text-[10px] font-mono text-slate-500">Risk Score</span>
                      <span className="text-[10px] font-mono text-slate-300 font-semibold">
                        {archFile.risk_score.toFixed(1)}/10
                      </span>
                    </div>
                    <div className="h-1.5 bg-slate-800/50 rounded-full overflow-hidden">
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
            )}

            {/* Dependencies */}
            {archFile && (archFile.coupling.length > 0 || archFile.symbols.length > 0) && (
              <Section
                id="deps"
                icon={<Network className="h-3 w-3 text-slate-500" />}
                title="Dependencies"
                count={archFile.coupling.length}
                expanded={expanded.deps}
                onToggle={() => toggle("deps")}
              >
                {archFile.coupling.length > 0 ? (
                  <div className="space-y-1.5">
                    <p className="text-[9px] font-mono text-slate-600 uppercase tracking-wider">
                      Coupled files (co-change)
                    </p>
                    {archFile.coupling.map((c, i) => (
                      <div
                        key={i}
                        className="flex items-center justify-between text-[10px] font-mono rounded border border-slate-800/40 bg-slate-900/30 px-2 py-1.5"
                      >
                        <span className="text-slate-400 truncate" title={c.path}>
                          {c.path.split("/").pop()}
                        </span>
                        <span className="text-slate-600 ml-2 shrink-0">
                          {(c.score * 100).toFixed(0)}%
                        </span>
                      </div>
                    ))}
                  </div>
                ) : (
                  <EmptyState text="No strong couplings detected." />
                )}

                {archFile.symbols.length > 0 && (
                  <div className="mt-3 space-y-1">
                    <p className="text-[9px] font-mono text-slate-600 uppercase tracking-wider">
                      Symbols ({archFile.symbols.length})
                    </p>
                    <div className="flex flex-wrap gap-1">
                      {archFile.symbols.slice(0, 10).map((s, i) => (
                        <span
                          key={i}
                          className="text-[9px] font-mono text-slate-500 bg-slate-800/40 rounded px-1.5 py-0.5"
                        >
                          {s}
                        </span>
                      ))}
                      {archFile.symbols.length > 10 && (
                        <span className="text-[9px] font-mono text-slate-600">
                          +{archFile.symbols.length - 10} more
                        </span>
                      )}
                    </div>
                  </div>
                )}
              </Section>
            )}

            {/* Risk Section */}
            <Section
              id="risk"
              icon={<Shield className="h-3 w-3 text-slate-500" />}
              title="Trace Risk"
              expanded={expanded.risk}
              onToggle={() => toggle("risk")}
            >
              {data.risk_score ? (
                <div className="space-y-2">
                  <div className="flex items-center gap-2">
                    <span
                      className={`inline-block rounded border px-2 py-0.5 text-[10px] font-mono ${riskColor(
                        data.risk_score.trace_count
                      )}`}
                    >
                      {data.risk_score.trace_count} traces
                    </span>
                  </div>
                  {data.risk_score.last_trace && (
                    <p className="text-[10px] font-mono text-slate-500">
                      Last trace: {formatDistanceToNow(data.risk_score.last_trace)}
                    </p>
                  )}
                </div>
              ) : (
                <EmptyState text="No risk data yet." />
              )}
            </Section>

            {/* Patterns Section */}
            <Section
              id="patterns"
              icon={<AlertTriangle className="h-3 w-3 text-slate-500" />}
              title="Patterns"
              count={data.patterns?.length ?? 0}
              expanded={expanded.patterns}
              onToggle={() => toggle("patterns")}
            >
              {data.patterns && data.patterns.length > 0 ? (
                <div className="space-y-2">
                  {data.patterns.map((p, i) => (
                    <div
                      key={i}
                      className="rounded border border-slate-800/40 bg-slate-900/30 px-3 py-2"
                    >
                      <p className="text-[10px] font-mono text-slate-300 leading-relaxed">
                        {p.content}
                      </p>
                      <span
                        className={`inline-block mt-1.5 rounded border px-1.5 py-0.5 text-[9px] font-mono ${
                          SOURCE_BADGE_STYLES[p.source] ?? SOURCE_BADGE_STYLES.manual
                        }`}
                      >
                        {p.source === "auto_learn"
                          ? "AI-Learned"
                          : p.source === "convention"
                            ? "Convention"
                            : "Manual"}
                      </span>
                    </div>
                  ))}
                </div>
              ) : (
                <EmptyState text="No patterns yet." />
              )}
            </Section>

            {/* Findings Section */}
            <Section
              id="findings"
              icon={<Bug className="h-3 w-3 text-slate-500" />}
              title="Findings"
              count={data.recent_comments?.length ?? 0}
              expanded={expanded.findings}
              onToggle={() => toggle("findings")}
            >
              {data.recent_comments && data.recent_comments.length > 0 ? (
                <div className="space-y-2">
                  {data.recent_comments.slice(0, 5).map((c, i) => (
                    <div
                      key={i}
                      className="rounded border border-slate-800/40 bg-slate-900/30 px-3 py-2"
                    >
                      <div className="flex items-center gap-2 mb-1">
                        <span
                          className={`inline-block rounded border px-1.5 py-0.5 text-[9px] font-mono ${
                            SEVERITY_STYLES[c.severity] ?? SEVERITY_STYLES.suggestion
                          }`}
                        >
                          {c.severity}
                        </span>
                        {c.category && (
                          <span className="text-[9px] font-mono text-slate-600">{c.category}</span>
                        )}
                      </div>
                      <p className="text-[10px] font-mono text-slate-400 leading-relaxed line-clamp-3">
                        {c.body}
                      </p>
                    </div>
                  ))}
                </div>
              ) : (
                <EmptyState text="No findings yet." />
              )}
            </Section>

            {/* Traces Section */}
            <Section
              id="traces"
              icon={<GitBranch className="h-3 w-3 text-slate-500" />}
              title="Traces"
              count={data.traces?.length ?? 0}
              expanded={expanded.traces}
              onToggle={() => toggle("traces")}
            >
              {data.traces && data.traces.length > 0 ? (
                <div className="space-y-2">
                  {data.traces.map((t, i) => (
                    <div
                      key={i}
                      className="rounded border border-slate-800/40 bg-slate-900/30 px-3 py-2"
                    >
                      <div className="flex items-center gap-2 mb-1">
                        <span
                          className={`inline-block rounded border px-1.5 py-0.5 text-[9px] font-mono ${
                            KIND_BADGE_STYLES[t.trace_type] ?? "border-slate-500/30 bg-slate-500/10 text-slate-400"
                          }`}
                        >
                          {t.trace_type}
                        </span>
                        {t.pr_number > 0 && (
                          <span className="text-[9px] font-mono text-slate-600">PR #{t.pr_number}</span>
                        )}
                        <span className="text-[9px] font-mono text-slate-600 ml-auto">
                          {formatDistanceToNow(t.created_at)}
                        </span>
                      </div>
                      <p className="text-[10px] font-mono text-slate-400 leading-relaxed line-clamp-2">
                        {t.content}
                      </p>
                    </div>
                  ))}
                </div>
              ) : (
                <EmptyState text="No traces yet." />
              )}
            </Section>
          </>
        )}
      </div>
    </div>
  );
}
