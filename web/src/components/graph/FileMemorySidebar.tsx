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
} from "lucide-react";
import { useFileMemory } from "@/lib/queries/graph";
import { useActiveRepo } from "@/lib/hooks/use-active-repo";
import { formatDistanceToNow } from "@/lib/time";

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

type SectionKey = "risk" | "patterns" | "findings" | "traces";

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

type FileMemorySidebarProps = {
  filePath: string;
  onClose: () => void;
};

export default function FileMemorySidebar({ filePath, onClose }: FileMemorySidebarProps) {
  const { activeId } = useActiveRepo();
  const { data, isLoading } = useFileMemory(activeId ?? undefined, filePath);

  const [expanded, setExpanded] = useState<Record<SectionKey, boolean>>({
    risk: true,
    patterns: false,
    findings: false,
    traces: false,
  });

  const toggle = (key: SectionKey) =>
    setExpanded((prev) => ({ ...prev, [key]: !prev[key] }));

  const fileName = filePath.split("/").pop() ?? filePath;

  return (
    <div className="w-[320px] shrink-0 h-full bg-[#0e0e18] border-l border-slate-800/50 flex flex-col overflow-hidden animate-slide-in-right">
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
            {/* Risk Section */}
            <Section
              id="risk"
              icon={<Shield className="h-3 w-3 text-slate-500" />}
              title="Risk"
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
