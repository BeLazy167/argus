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
            className="flex items-center gap-2 rounded-md border border-amber/30 bg-amber/10 px-3 py-1.5 text-xs font-mono text-amber hover:bg-amber/20 transition-colors"
          >
            <Plus className="h-3.5 w-3.5" />
            Add scenario
          </button>
        </div>
      </div>

      {/* Create form */}
      {showForm && (
        <div className="mb-6 rounded-lg border border-amber/30 bg-charcoal p-5 space-y-4">
          <form onSubmit={handleCreate} className="space-y-4">
            <div>
              <label className="block text-[11px] font-mono uppercase tracking-wider text-slate-text mb-1">
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
            <div className="grid grid-cols-2 gap-4">
              <div>
                <label className="block text-[11px] font-mono uppercase tracking-wider text-slate-text mb-1">
                  Severity
                </label>
                <div className="flex">
                  {(["critical", "high", "medium", "low"] as const).map((s) => (
                    <button
                      key={s}
                      type="button"
                      onClick={() => setSeverity(s)}
                      className={`flex-1 border px-2 py-2 text-xs font-mono transition-colors first:rounded-l-md last:rounded-r-md capitalize ${
                        severity === s
                          ? SEVERITY_BADGE[s]
                          : "bg-background text-slate-text border-iron"
                      }`}
                    >
                      {s}
                    </button>
                  ))}
                </div>
              </div>
              <div>
                <label className="block text-[11px] font-mono uppercase tracking-wider text-slate-text mb-1">
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
            <div className="flex justify-end gap-3">
              <button
                type="button"
                onClick={() => setShowForm(false)}
                className="rounded-md px-3 py-1.5 text-xs font-mono text-slate-text hover:text-foreground transition-colors"
              >
                Cancel
              </button>
              <button
                type="submit"
                disabled={createScenario.isPending || !description.trim()}
                className="rounded-md border border-amber bg-amber/10 px-4 py-1.5 text-xs font-mono text-amber hover:bg-amber/20 transition-colors disabled:opacity-50"
              >
                {createScenario.isPending ? "Creating..." : "Create scenario"}
              </button>
            </div>
          </form>
        </div>
      )}

      {/* Severity Filters */}
      <div className="flex items-center gap-3 mb-4">
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
                className={`rounded border px-2.5 py-1 text-[10px] font-mono transition-colors ${
                  isActive ? activeStyles[tab] : "border-iron text-slate-text hover:text-foreground"
                }`}
              >
                {label} ({count})
              </button>
            );
          })}
        </div>
      </div>

      {/* Scenarios List */}
      <div className="rounded-lg border border-iron bg-charcoal overflow-hidden">
        <div className="flex items-center gap-2 border-b border-iron px-5 py-4">
          <Shield className="h-4 w-4 text-slate-text" />
          <h2 className="text-xs font-mono uppercase tracking-[0.1em] text-foreground">
            Active Scenarios
          </h2>
          <span className="text-[10px] font-mono text-slate-text ml-auto">
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
          <div className="py-10 text-center text-xs font-mono text-slate-text">
            No scenarios yet. Scenarios are auto-generated from reviews or you can add them manually.
          </div>
        ) : (
          <div className="divide-y divide-iron/30">
            {paginated.map((scenario) => (
              <div
                key={scenario.id}
                className="px-5 py-4 hover:bg-iron/10 transition-colors cursor-pointer"
                onClick={() => setExpandedId(expandedId === scenario.id ? null : scenario.id)}
              >
                <div className="flex items-start justify-between gap-4">
                  <div className="flex-1 min-w-0">
                    <div className="flex items-start gap-1.5 mb-2">
                      <ChevronDown
                        className={`h-3.5 w-3.5 shrink-0 mt-0.5 text-slate-text transition-transform ${
                          expandedId === scenario.id ? "rotate-0" : "-rotate-90"
                        }`}
                      />
                      <p className={`text-xs font-mono text-foreground ${
                        expandedId === scenario.id ? "" : "line-clamp-2"
                      }`}>
                        {scenario.description}
                      </p>
                    </div>
                    <div className="flex items-center gap-2 flex-wrap">
                      {!scenario.active && (
                        <span className="inline-block rounded border border-zinc-500/30 bg-zinc-500/10 px-2 py-0.5 text-[10px] font-mono text-zinc-400">
                          pending
                        </span>
                      )}
                      {scenario.is_outdated && (
                        <span className="inline-flex items-center gap-1 rounded border border-yellow-500/30 bg-yellow-500/10 px-2 py-0.5 text-[10px] font-mono text-yellow-400">
                          <AlertTriangle className="h-2.5 w-2.5" />
                          outdated
                        </span>
                      )}
                      <span
                        className={`inline-block rounded border px-2 py-0.5 text-[10px] font-mono capitalize ${
                          SEVERITY_BADGE[scenario.severity] ?? SEVERITY_BADGE.medium
                        }`}
                      >
                        {scenario.severity}
                      </span>
                      <span
                        className={`inline-block rounded border px-2 py-0.5 text-[10px] font-mono ${
                          SOURCE_BADGE[scenario.source] ?? SOURCE_BADGE.manual
                        }`}
                      >
                        {scenario.source || "manual"}
                      </span>
                      {scenario.files?.length > 0 && (
                        <span className="text-[10px] font-mono text-slate-text">
                          {scenario.files.length} file{scenario.files.length !== 1 ? "s" : ""}
                        </span>
                      )}
                      <span className="text-[10px] font-mono text-slate-text">
                        {formatDistanceToNow(scenario.created_at)}
                      </span>
                    </div>
                    {scenario.files?.length > 0 && (
                      <div className="flex gap-1.5 mt-2 flex-wrap">
                        {scenario.files.map((f) => (
                          <span
                            key={f}
                            className="inline-block rounded border border-iron px-1.5 py-0.5 text-[9px] font-mono text-slate-text"
                          >
                            {f}
                          </span>
                        ))}
                      </div>
                    )}
                    {scenario.steps?.length > 0 && (
                      <ol className="mt-2 ml-4 list-decimal space-y-0.5">
                        {scenario.steps.map((step, i) => (
                          <li key={i} className="text-[10px] font-mono text-slate-text">
                            <span className="text-foreground">{step.action}</span>
                            {step.hint && (
                              <span className="text-iron ml-1">({step.hint})</span>
                            )}
                          </li>
                        ))}
                      </ol>
                    )}
                    {scenario.expected_outcome && (
                      <p className="mt-1.5 text-[10px] font-mono text-slate-text">
                        <span className="text-iron">Expected:</span>{" "}
                        {scenario.expected_outcome}
                      </p>
                    )}
                    {expandedId === scenario.id && (
                      <div className="mt-3 pt-3 border-t border-iron/30 space-y-2">
                        {scenario.source_ref && (
                          <div className="text-[10px] font-mono">
                            <span className="text-slate-text">Source ref: </span>
                            <span className="text-foreground">{scenario.source_ref}</span>
                          </div>
                        )}
                        {scenario.initial_state && (
                          <div className="text-[10px] font-mono">
                            <span className="text-slate-text">Initial state: </span>
                            <span className="text-foreground">{scenario.initial_state}</span>
                          </div>
                        )}
                        {scenario.modules?.length > 0 && (
                          <div className="text-[10px] font-mono">
                            <span className="text-slate-text">Modules: </span>
                            <span className="text-foreground">{scenario.modules.join(", ")}</span>
                          </div>
                        )}
                        {scenario.last_run_at && (
                          <div className="text-[10px] font-mono">
                            <span className="text-slate-text">Last run: </span>
                            <span className="text-foreground">{new Date(scenario.last_run_at).toLocaleString()}</span>
                          </div>
                        )}
                      </div>
                    )}
                  </div>
                  <button
                    onClick={(e) => { e.stopPropagation(); deleteScenario.mutate(scenario.id); }}
                    disabled={deleteScenario.isPending}
                    className="text-slate-text hover:text-red-400 transition-colors disabled:opacity-50 shrink-0"
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                  </button>
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
