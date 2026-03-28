"use client";

import { useMemo, useState } from "react";
import { usePagination, PaginationBar } from "@/components/dashboard/pagination";
import { Shield, Plus, Trash2, Loader2, X, AlertTriangle, ChevronDown } from "lucide-react";
import { Protect } from "@clerk/nextjs";
import {
  useScenarios,
  useCreateScenario,
  useDeleteScenario,
} from "@/lib/queries/scenarios";
import { UpgradePrompt } from "@/components/dashboard/upgrade-prompt";
import { useActiveRepo } from "@/lib/hooks/use-active-repo";
import { RepoSelect } from "@/components/dashboard/repo-select";
import { formatDistanceToNow } from "@/lib/time";

type SeverityFilter = "all" | "critical" | "high" | "medium" | "low";

const SEVERITY_BADGE: Record<string, string> = {
  critical: "border-red-500/30 bg-red-500/10 text-red-400",
  high: "border-orange-500/30 bg-orange-500/10 text-orange-400",
  medium: "border-amber/30 bg-amber/10 text-amber",
  low: "border-blue-500/30 bg-blue-500/10 text-blue-400",
};

const SEVERITY_BAR: Record<string, string> = {
  critical: "bg-red-500",
  high: "bg-orange-500",
  medium: "bg-amber",
  low: "bg-blue-500",
};

const SOURCE_BADGE: Record<string, string> = {
  manual: "border-slate-500/30 bg-slate-500/10 text-slate-400",
  review: "border-amber/30 bg-amber/10 text-amber",
  auto: "border-purple-500/30 bg-purple-500/10 text-purple-400",
};

export default function ScenariosPage() {
  const { repos, activeId, setSelectedId } = useActiveRepo();
  const { data: scenarios, isLoading } = useScenarios();
  const createScenario = useCreateScenario();
  const deleteScenario = useDeleteScenario();

  const [severityFilter, setSeverityFilter] = useState<SeverityFilter>("all");
  const [showForm, setShowForm] = useState(false);
  const [description, setDescription] = useState("");
  const [severity, setSeverity] = useState("medium");
  const [filesInput, setFilesInput] = useState("");
  const [expandedId, setExpandedId] = useState<number | null>(null);
  const [confirmDeleteId, setConfirmDeleteId] = useState<number | null>(null);

  const filtered = useMemo(() => {
    if (!scenarios) return [];
    if (severityFilter === "all") return scenarios;
    return scenarios.filter((s) => s.severity === severityFilter);
  }, [scenarios, severityFilter]);

  const severityCounts = useMemo(() => {
    if (!scenarios) return { all: 0, critical: 0, high: 0, medium: 0, low: 0 };
    return {
      all: scenarios.length,
      critical: scenarios.filter((s) => s.severity === "critical").length,
      high: scenarios.filter((s) => s.severity === "high").length,
      medium: scenarios.filter((s) => s.severity === "medium").length,
      low: scenarios.filter((s) => s.severity === "low").length,
    };
  }, [scenarios]);

  const stats = useMemo(() => {
    if (!scenarios) return { active: 0, triggered: 0, outdated: 0 };
    return {
      active: scenarios.filter((s) => s.active !== false).length,
      triggered: scenarios.filter((s) => s.last_run_at).length,
      outdated: scenarios.filter((s) => s.is_outdated).length,
    };
  }, [scenarios]);

  const { page, setPage, totalPages, paginated, pageSize, total, hasNext, hasPrev } =
    usePagination(filtered);

  const handleCreate = (e: React.FormEvent) => {
    e.preventDefault();
    if (!description.trim()) return;
    const files = filesInput
      .split(",")
      .map((f) => f.trim())
      .filter(Boolean);
    createScenario.mutate(
      { description: description.trim(), severity, files },
      {
        onSuccess: () => {
          setDescription("");
          setSeverity("medium");
          setFilesInput("");
          setShowForm(false);
        },
      },
    );
  };

  const handleDelete = (e: React.MouseEvent, scenarioId: number) => {
    e.stopPropagation();
    if (confirmDeleteId === scenarioId) {
      deleteScenario.mutate(scenarioId);
      setConfirmDeleteId(null);
    } else {
      setConfirmDeleteId(scenarioId);
      setTimeout(() => setConfirmDeleteId((prev) => (prev === scenarioId ? null : prev)), 3000);
    }
  };

  return (
    <Protect plan="org:pro" fallback={<UpgradePrompt feature="Scenario memory" />}>
      <div className="mb-8 flex items-center justify-between">
        <div>
          <h1 className="font-display text-2xl font-bold text-foreground">
            Scenarios
          </h1>
          <p className="text-xs font-mono text-slate-text mt-1">
            Known risk scenarios Argus watches for in every review.
          </p>
        </div>
        <div className="flex items-center gap-3">
          <button
            type="button"
            onClick={() => setShowForm(!showForm)}
            className="flex items-center gap-2 rounded-md border border-amber/30 bg-amber/10 px-3 py-1.5 text-xs font-mono text-amber hover:bg-amber/20 transition-colors cursor-pointer"
          >
            <Plus className="h-3.5 w-3.5" />
            Add scenario
          </button>
        </div>
      </div>

      {/* Create form */}
      <div
        className={`overflow-hidden transition-all duration-300 ease-out ${
          showForm ? "max-h-[500px] opacity-100 mb-6" : "max-h-0 opacity-0"
        }`}
      >
        <div className="rounded-lg border border-amber/30 bg-charcoal p-5 space-y-5">
          <form onSubmit={handleCreate} className="space-y-5">
            <div>
              <label className="block text-[11px] font-mono uppercase tracking-wider text-slate-text mb-1.5">
                Description
              </label>
              <textarea
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                rows={3}
                placeholder="Describe the risk scenario..."
                className="w-full rounded-md border border-iron bg-background px-3 py-2 text-xs font-mono text-foreground placeholder:text-iron focus:border-amber focus:outline-none resize-none"
              />
            </div>
            <div className="grid grid-cols-2 gap-5">
              <div>
                <label className="block text-[11px] font-mono uppercase tracking-wider text-slate-text mb-1.5">
                  Severity
                </label>
                <div className="flex">
                  {(["critical", "high", "medium", "low"] as const).map((s) => (
                    <button
                      key={s}
                      type="button"
                      onClick={() => setSeverity(s)}
                      className={`flex-1 border px-2 py-2 text-xs font-mono transition-colors cursor-pointer first:rounded-l-md last:rounded-r-md capitalize ${
                        severity === s
                          ? SEVERITY_BADGE[s]
                          : "bg-background text-slate-text border-iron hover:text-foreground"
                      }`}
                    >
                      {s}
                    </button>
                  ))}
                </div>
              </div>
              <div>
                <label className="block text-[11px] font-mono uppercase tracking-wider text-slate-text mb-1.5">
                  Related files (comma-separated)
                </label>
                <input
                  type="text"
                  value={filesInput}
                  onChange={(e) => setFilesInput(e.target.value)}
                  placeholder="src/auth.ts, lib/db.ts"
                  className="w-full rounded-md border border-iron bg-background px-3 py-2 text-xs font-mono text-foreground placeholder:text-iron focus:border-amber focus:outline-none"
                />
              </div>
            </div>
            <div className="flex justify-end gap-3 pt-1">
              <button
                type="button"
                onClick={() => setShowForm(false)}
                className="rounded-md px-3 py-1.5 text-xs font-mono text-slate-text hover:text-foreground transition-colors cursor-pointer"
              >
                Cancel
              </button>
              <button
                type="submit"
                disabled={createScenario.isPending || !description.trim()}
                className="rounded-md border border-amber bg-amber/10 px-4 py-1.5 text-xs font-mono text-amber hover:bg-amber/20 transition-colors disabled:opacity-50 cursor-pointer disabled:cursor-not-allowed"
              >
                {createScenario.isPending ? "Creating..." : "Create scenario"}
              </button>
            </div>
          </form>
        </div>
      </div>

      {/* Severity Filters */}
      <div className="flex items-center gap-3 mb-3">
        <div className="flex gap-1.5">
          {(["all", "critical", "high", "medium", "low"] as const).map((tab) => {
            const label = tab === "all" ? "All" : tab.charAt(0).toUpperCase() + tab.slice(1);
            const count = severityCounts[tab];
            const isActive = severityFilter === tab;
            const activeStyles: Record<SeverityFilter, string> = {
              all: "border-amber/40 bg-amber/10 text-amber",
              critical: "border-red-500/40 bg-red-500/10 text-red-400",
              high: "border-orange-500/40 bg-orange-500/10 text-orange-400",
              medium: "border-amber/40 bg-amber/10 text-amber",
              low: "border-blue-500/40 bg-blue-500/10 text-blue-400",
            };
            return (
              <button
                key={tab}
                onClick={() => { setSeverityFilter(tab); setPage(0); }}
                className={`rounded border px-2.5 py-1 text-[11px] font-mono transition-colors cursor-pointer ${
                  isActive ? activeStyles[tab] : "border-iron text-slate-text hover:text-foreground"
                }`}
              >
                {label} ({count})
              </button>
            );
          })}
        </div>
      </div>

      {/* Stats row */}
      {scenarios && scenarios.length > 0 && (
        <div className="mb-4 text-[11px] font-mono text-slate-text flex items-center gap-1.5">
          <span className="text-foreground">{stats.active}</span> active
          <span className="text-iron mx-1">&middot;</span>
          <span className="text-foreground">{stats.triggered}</span> triggered
          {stats.outdated > 0 && (
            <>
              <span className="text-iron mx-1">&middot;</span>
              <span className="text-yellow-400">{stats.outdated}</span>
              <span className="text-yellow-400">outdated</span>
            </>
          )}
        </div>
      )}

      {/* Scenarios List */}
      <div className="rounded-lg border border-iron bg-charcoal overflow-hidden">
        <div className="flex items-center gap-2 border-b border-iron px-5 py-4">
          <Shield className="h-4 w-4 text-slate-text" />
          <h2 className="text-xs font-mono uppercase tracking-[0.1em] text-foreground">
            Active Scenarios
          </h2>
          <span className="text-[11px] font-mono text-slate-text ml-auto">
            {filtered.length} scenarios
          </span>
        </div>

        {isLoading ? (
          <div className="flex items-center justify-center py-10">
            <Loader2 className="h-5 w-5 animate-spin text-slate-text" />
          </div>
        ) : !activeId ? (
          <div className="py-10 text-center text-xs font-mono text-slate-text">
            Select a repo to view scenarios.
          </div>
        ) : filtered.length === 0 ? (
          <div className="py-16 flex flex-col items-center gap-4">
            <div className="rounded-full border border-iron/50 bg-iron/10 p-4">
              <Shield className="h-7 w-7 text-slate-text" />
            </div>
            <div className="text-center">
              <p className="text-sm font-mono text-foreground mb-1">No scenarios yet</p>
              <p className="text-xs font-mono text-slate-text max-w-xs">
                Scenarios are auto-generated from reviews or create one manually.
              </p>
            </div>
            <button
              type="button"
              onClick={() => setShowForm(true)}
              className="mt-1 flex items-center gap-2 rounded-md border border-amber/30 bg-amber/10 px-4 py-2 text-xs font-mono text-amber hover:bg-amber/20 transition-colors cursor-pointer"
            >
              <Plus className="h-3.5 w-3.5" />
              Add scenario
            </button>
          </div>
        ) : (
          <div className="divide-y divide-iron/30">
            {paginated.map((scenario) => (
              <div
                key={scenario.id}
                className="flex hover:bg-iron/10 transition-colors cursor-pointer"
                onClick={() => setExpandedId(expandedId === scenario.id ? null : scenario.id)}
              >
                {/* Severity indicator bar */}
                <div
                  className={`w-1 shrink-0 ${
                    SEVERITY_BAR[scenario.severity] ?? SEVERITY_BAR.medium
                  }`}
                />

                <div className="flex-1 min-w-0 px-5 py-4">
                  <div className="flex items-start justify-between gap-4">
                    <div className="flex-1 min-w-0">
                      <div className="flex items-start gap-1.5 mb-2.5">
                        <ChevronDown
                          className={`h-3.5 w-3.5 shrink-0 mt-0.5 text-slate-text transition-transform ${
                            expandedId === scenario.id ? "rotate-0" : "-rotate-90"
                          }`}
                        />
                        <p className={`text-sm font-mono text-foreground leading-snug ${
                          expandedId === scenario.id ? "" : "line-clamp-2"
                        }`}>
                          {scenario.description}
                        </p>
                      </div>
                      <div className="flex items-center gap-2.5 flex-wrap">
                        {!scenario.active && (
                          <span className="inline-block rounded border border-zinc-500/30 bg-zinc-500/10 px-2 py-0.5 text-[11px] font-mono text-zinc-400">
                            pending
                          </span>
                        )}
                        {scenario.is_outdated && (
                          <span className="inline-flex items-center gap-1 rounded border border-yellow-500/30 bg-yellow-500/10 px-2 py-0.5 text-[11px] font-mono text-yellow-400">
                            <AlertTriangle className="h-2.5 w-2.5" />
                            outdated
                          </span>
                        )}
                        <span
                          className={`inline-block rounded border px-2 py-0.5 text-[11px] font-mono capitalize ${
                            SEVERITY_BADGE[scenario.severity] ?? SEVERITY_BADGE.medium
                          }`}
                        >
                          {scenario.severity}
                        </span>
                        <span
                          className={`inline-block rounded border px-2 py-0.5 text-[11px] font-mono ${
                            SOURCE_BADGE[scenario.source] ?? SOURCE_BADGE.manual
                          }`}
                        >
                          {scenario.source || "manual"}
                        </span>
                        {scenario.files?.length > 0 && (
                          <span className="text-[11px] font-mono text-slate-text">
                            {scenario.files.length} file{scenario.files.length !== 1 ? "s" : ""}
                          </span>
                        )}
                        <span className="text-[11px] font-mono text-slate-text">
                          {formatDistanceToNow(scenario.created_at)}
                        </span>
                      </div>
                      {scenario.files?.length > 0 && (
                        <div className="flex gap-1.5 mt-2.5 flex-wrap">
                          {scenario.files.map((f) => (
                            <span
                              key={f}
                              className="inline-block rounded border border-iron/60 bg-iron/10 px-2 py-0.5 text-[11px] font-mono text-slate-text"
                            >
                              {f}
                            </span>
                          ))}
                        </div>
                      )}
                      {scenario.steps?.length > 0 && (
                        <ol className="mt-2.5 ml-4 list-decimal space-y-0.5">
                          {scenario.steps.map((step, i) => (
                            <li key={i} className="text-[11px] font-mono text-slate-text">
                              <span className="text-foreground">{step.action}</span>
                              {step.hint && (
                                <span className="text-iron ml-1">({step.hint})</span>
                              )}
                            </li>
                          ))}
                        </ol>
                      )}
                      {scenario.expected_outcome && (
                        <p className="mt-2 text-[11px] font-mono text-slate-text">
                          <span className="text-iron">Expected:</span>{" "}
                          {scenario.expected_outcome}
                        </p>
                      )}
                      {expandedId === scenario.id && (
                        <div className="mt-4 pt-4 border-t border-iron/30 space-y-3">
                          {scenario.source_ref && (
                            <div className="text-[11px] font-mono">
                              <span className="text-slate-text mr-2">Source ref</span>
                              <span className="text-foreground">{scenario.source_ref}</span>
                            </div>
                          )}
                          {scenario.initial_state && (
                            <div className="text-[11px] font-mono">
                              <span className="text-slate-text mr-2">Initial state</span>
                              <span className="text-foreground">{scenario.initial_state}</span>
                            </div>
                          )}
                          {scenario.modules?.length > 0 && (
                            <div className="text-[11px] font-mono">
                              <span className="text-slate-text mr-2">Modules</span>
                              <span className="text-foreground">{scenario.modules.join(", ")}</span>
                            </div>
                          )}
                          {scenario.trigger_count != null && scenario.trigger_count > 0 && (
                            <div className="text-[11px] font-mono">
                              <span className="text-slate-text mr-2">Triggered</span>
                              <span className="text-foreground">{scenario.trigger_count} time{scenario.trigger_count !== 1 ? "s" : ""}</span>
                            </div>
                          )}
                          {scenario.last_run_at && (
                            <div className="text-[11px] font-mono">
                              <span className="text-slate-text mr-2">Last triggered</span>
                              <span className="text-foreground">{formatDistanceToNow(scenario.last_run_at)}</span>
                            </div>
                          )}
                        </div>
                      )}
                    </div>
                    <button
                      onClick={(e) => handleDelete(e, scenario.id)}
                      disabled={deleteScenario.isPending}
                      className={`flex items-center gap-1.5 shrink-0 transition-colors disabled:opacity-50 cursor-pointer ${
                        confirmDeleteId === scenario.id
                          ? "text-red-400"
                          : "text-slate-text hover:text-red-400"
                      }`}
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                      {confirmDeleteId === scenario.id && (
                        <span className="text-[10px] font-mono">confirm</span>
                      )}
                    </button>
                  </div>
                </div>
              </div>
            ))}
          </div>
        )}
        <PaginationBar
          page={page}
          totalPages={totalPages}
          total={total}
          pageSize={pageSize}
          hasNext={hasNext}
          hasPrev={hasPrev}
          onNext={() => setPage(page + 1)}
          onPrev={() => setPage(page - 1)}
        />
      </div>
    </Protect>
  );
}
