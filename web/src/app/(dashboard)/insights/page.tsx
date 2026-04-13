"use client";

import { useMemo } from "react";
import { Activity, FileText, Search, Loader2, ExternalLink } from "lucide-react";
import { usePagination, PaginationBar, useSearchParamState } from "@/components/dashboard/pagination";
import { ProGate } from "@/components/dashboard/pro-gate";
import { useRepoRisk, useTraces } from "@/lib/queries/insights";
import { useActiveRepo } from "@/lib/hooks/use-active-repo";
import { RepoSelect } from "@/components/dashboard/repo-select";
import { formatDistanceToNow } from "@/lib/time";

const KIND_BADGE: Record<string, string> = {
  finding: "border-red-500/30 bg-red-500/10 text-red-400",
  suggestion: "border-amber/30 bg-amber/10 text-amber",
  reply: "border-blue-500/30 bg-blue-500/10 text-blue-400",
  resolution: "border-green-500/30 bg-green-500/10 text-green-400",
};

export default function InsightsPage() {
  const { repos, activeId, setSelectedId } = useActiveRepo();
  const { data: risks, isLoading: risksLoading } = useRepoRisk();
  const [fileFilter, setFileFilter] = useSearchParamState("file");
  const { data: traces, isLoading: tracesLoading } = useTraces(
    fileFilter || undefined,
  );

  const maxTraceCount = useMemo(() => {
    if (!risks || risks.length === 0) return 1;
    return Math.max(...risks.map((r) => r.trace_count), 1);
  }, [risks]);

  const filteredRisks = useMemo(() => {
    if (!risks) return [];
    if (!fileFilter) return risks;
    return risks.filter((r) =>
      r.file_path.toLowerCase().includes(fileFilter.toLowerCase()),
    );
  }, [risks, fileFilter]);

  const {
    page: riskPage,
    setPage: setRiskPage,
    totalPages: riskTotalPages,
    paginated: riskPaginated,
    pageSize: riskPageSize,
    total: riskTotal,
    hasNext: riskHasNext,
    hasPrev: riskHasPrev,
  } = usePagination(filteredRisks, undefined, "riskPage");

  const {
    page: tracePage,
    setPage: setTracePage,
    totalPages: traceTotalPages,
    paginated: tracePaginated,
    pageSize: tracePageSize,
    total: traceTotal,
    hasNext: traceHasNext,
    hasPrev: traceHasPrev,
  } = usePagination(traces ?? [], undefined, "tracePage");

  const isLoading = risksLoading || tracesLoading;

  return (
    <ProGate feature="Insights & risk analysis">
      <div className="mb-8 flex items-center justify-between">
        <div>
          <h1 className="font-mono text-2xl font-bold text-foreground">
            Insights
          </h1>
          <p className="text-xs font-mono text-slate-text mt-1">
            Code health signals — hot files, risk scores, and decision traces.
          </p>
        </div>
        <div className="flex items-center gap-3" />
      </div>

      {/* File search */}
      <div className="mb-6">
        <div className="relative">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-slate-text" />
          <input
            type="text"
            value={fileFilter}
            onChange={(e) => setFileFilter(e.target.value)}
            placeholder="Filter by file path…"
            className="w-full border border-iron bg-charcoal pl-9 pr-4 py-2.5 text-xs font-mono text-foreground placeholder:text-slate-text/50 focus:outline-none focus:border-amber/50 transition-colors"
          />
        </div>
      </div>

      {!activeId ? (
        <div className="border border-iron bg-charcoal py-10 text-center text-xs font-mono text-slate-text">
          Select a repo to view insights.
        </div>
      ) : (
        <div className="space-y-8">
          {/* Hot Files */}
          <div className="border border-iron bg-charcoal overflow-x-auto">
            <div className="flex items-center gap-2 border-b border-iron px-5 py-4">
              <FileText className="h-4 w-4 text-slate-text" />
              <h2 className="text-xs font-mono uppercase tracking-[0.1em] text-foreground">
                Hot Files
              </h2>
              <span className="text-[10px] font-mono text-slate-text ml-auto">
                {filteredRisks.length} files
              </span>
            </div>

            {risksLoading ? (
              <div className="flex items-center justify-center py-10">
                <Loader2 className="h-5 w-5 animate-spin text-slate-text" />
              </div>
            ) : filteredRisks.length === 0 ? (
              <div className="py-10 text-center text-xs font-mono text-slate-text">
                No file risk data yet. Insights are generated as reviews accumulate.
              </div>
            ) : (
              <table className="w-full">
                <thead>
                  <tr className="border-b border-iron/50 text-[10px] font-mono uppercase tracking-wider text-slate-text">
                    <th className="text-left px-5 py-2.5 font-medium">File</th>
                    <th className="text-left px-3 py-2.5 font-medium">Traces</th>
                    <th className="text-left px-3 py-2.5 font-medium min-w-[120px]">Risk</th>
                    <th className="text-left px-5 py-2.5 font-medium">Last Trace</th>
                  </tr>
                </thead>
                <tbody>
                  {riskPaginated.map((risk) => {
                    const pct = Math.round((risk.trace_count / maxTraceCount) * 100);
                    const barColor =
                      pct >= 75
                        ? "bg-red-500/60"
                        : pct >= 50
                          ? "bg-orange-500/60"
                          : pct >= 25
                            ? "bg-amber/60"
                            : "bg-blue-500/60";
                    return (
                      <tr
                        key={risk.file_path}
                        className="border-b border-iron/30 last:border-0 hover:bg-iron/10 transition-colors cursor-pointer"
                        onClick={() => setFileFilter(risk.file_path)}
                      >
                        <td className="px-5 py-3 max-w-xs">
                          <span className="text-xs font-mono text-foreground truncate block">
                            {risk.file_path}
                          </span>
                        </td>
                        <td className="px-3 py-3">
                          <span className="text-xs font-mono text-foreground">
                            {risk.trace_count}
                          </span>
                        </td>
                        <td className="px-3 py-3">
                          <div className="flex items-center gap-2">
                            <div className="flex-1 h-1.5 rounded-full bg-iron/30 overflow-hidden">
                              <div
                                 className={`h-full rounded-full ${barColor} transition-[width] duration-300`}
                                style={{ width: `${pct}%` }}
                              />
                            </div>
                            <span className="text-[10px] font-mono text-slate-text w-8 text-right">
                              {pct}%
                            </span>
                          </div>
                        </td>
                        <td className="px-5 py-3">
                          <span className="text-[10px] font-mono text-slate-text">
                            {formatDistanceToNow(risk.last_trace)}
                          </span>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            )}
            <PaginationBar
              page={riskPage}
              totalPages={riskTotalPages}
              total={riskTotal}
              pageSize={riskPageSize}
              hasNext={riskHasNext}
              hasPrev={riskHasPrev}
              onNext={() => setRiskPage(riskPage + 1)}
              onPrev={() => setRiskPage(riskPage - 1)}
            />
          </div>

          {/* Recent Activity / Decision Traces */}
          <div className="border border-iron bg-charcoal overflow-x-auto">
            <div className="flex items-center gap-2 border-b border-iron px-5 py-4">
              <Activity className="h-4 w-4 text-slate-text" />
              <h2 className="text-xs font-mono uppercase tracking-[0.1em] text-foreground">
                Recent Activity
              </h2>
              {fileFilter && (
                <span className="text-[10px] font-mono text-amber ml-2">
                  filtered: {fileFilter}
                </span>
              )}
              <span className="text-[10px] font-mono text-slate-text ml-auto">
                {(traces ?? []).length} traces
              </span>
            </div>

            {tracesLoading ? (
              <div className="flex items-center justify-center py-10">
                <Loader2 className="h-5 w-5 animate-spin text-slate-text" />
              </div>
            ) : (traces ?? []).length === 0 ? (
              <div className="py-10 text-center text-xs font-mono text-slate-text">
                No decision traces yet. Activity appears as reviews are completed.
              </div>
            ) : (
              <div className="divide-y divide-iron/30">
                {tracePaginated.map((trace) => (
                  <div
                    key={trace.id}
                    className="px-5 py-3 hover:bg-iron/10 transition-colors"
                  >
                    <div className="flex items-start gap-3">
                      <div className="mt-0.5 h-2 w-2 rounded-full shrink-0 bg-amber/60" />
                      <div className="flex-1 min-w-0">
                        <div className="flex items-center gap-2 mb-1 flex-wrap">
                          <span
                            className={`inline-block rounded border px-1.5 py-0.5 text-[9px] font-mono capitalize ${
                              KIND_BADGE[trace.kind] ?? KIND_BADGE.finding
                            }`}
                          >
                            {trace.kind}
                          </span>
                          <span className="text-[10px] font-mono text-slate-text truncate">
                            {trace.file_path}
                          </span>
                          {trace.pr_number && (
                            <span className="text-[10px] font-mono text-slate-text">
                              PR #{trace.pr_number}
                            </span>
                          )}
                          <span className="text-[10px] font-mono text-slate-text ml-auto shrink-0">
                            {formatDistanceToNow(trace.created_at)}
                          </span>
                        </div>
                        <p className="text-xs font-mono text-foreground/80 truncate">
                          {trace.summary}
                        </p>
                        {trace.review_id && (
                          <a
                            href={`/reviews/${trace.review_id}`}
                            className="inline-flex items-center gap-1 mt-1 text-[10px] font-mono text-amber hover:text-amber/80 transition-colors"
                          >
                            View review
                            <ExternalLink className="h-2.5 w-2.5" />
                          </a>
                        )}
                      </div>
                    </div>
                  </div>
                ))}
              </div>
            )}
            <PaginationBar
              page={tracePage}
              totalPages={traceTotalPages}
              total={traceTotal}
              pageSize={tracePageSize}
              hasNext={traceHasNext}
              hasPrev={traceHasPrev}
              onNext={() => setTracePage(tracePage + 1)}
              onPrev={() => setTracePage(tracePage - 1)}
            />
          </div>
        </div>
      )}
    </ProGate>
  );
}
