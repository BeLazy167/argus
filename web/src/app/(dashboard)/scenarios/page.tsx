"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { usePagination, PaginationBar } from "@/components/dashboard/pagination";
import { Shield, Plus, Trash2, Loader2, AlertTriangle, AlertCircle, ChevronDown, ExternalLink, Loader } from "lucide-react";
import { ProGate } from "@/components/dashboard/pro-gate";
import {
  useScenarios,
  useCreateScenario,
  useDeleteScenario,
  useScenarioKPIs,
  useScenarioRuns,
} from "@/lib/queries/scenarios";
import { useActiveRepo } from "@/lib/hooks/use-active-repo";
import { formatDistanceToNow } from "@/lib/time";
import type { Scenario, ScenarioVerdict } from "@/lib/types";

type SeverityFilter = "all" | "critical" | "high" | "medium" | "low";

/* ── style maps ─────────────────────────────────────────────────────── */

const SEVERITY_DOT: Record<string, string> = {
  critical: "bg-red-500",
  high: "bg-orange-500",
  medium: "bg-amber",
  low: "bg-blue-500",
};

const SEVERITY_BADGE: Record<string, string> = {
  critical: "border-red-500/30 bg-red-500/10 text-red-400",
  high: "border-orange-500/30 bg-orange-500/10 text-orange-400",
  medium: "border-amber/30 bg-amber/10 text-amber",
  low: "border-blue-500/30 bg-blue-500/10 text-blue-400",
};

const VERDICT_BADGE: Record<ScenarioVerdict, string> = {
  broken: "border-red-500/40 bg-red-500/10 text-red-400",
  partial: "border-amber/40 bg-amber/10 text-amber",
  fixed: "border-emerald-500/40 bg-emerald-500/10 text-emerald-400",
  unclear: "border-slate-500/40 bg-slate-500/10 text-slate-400",
};

const VERDICT_LABEL: Record<ScenarioVerdict, string> = {
  broken: "Broken",
  partial: "Partial fix",
  fixed: "Fixed",
  unclear: "Unclear",
};

const SOURCE_BADGE: Record<string, string> = {
  manual: "border-slate-500/30 bg-slate-500/10 text-slate-400",
  review: "border-amber/30 bg-amber/10 text-amber",
  auto: "border-purple-500/30 bg-purple-500/10 text-purple-400",
  issue: "border-blue-500/30 bg-blue-500/10 text-blue-400",
};

const SEVERITY_FILTER_ACTIVE: Record<SeverityFilter, string> = {
  all: "border-amber/40 bg-amber/10 text-amber",
  critical: "border-red-500/40 bg-red-500/10 text-red-400",
  high: "border-orange-500/40 bg-orange-500/10 text-orange-400",
  medium: "border-amber/40 bg-amber/10 text-amber",
  low: "border-blue-500/40 bg-blue-500/10 text-blue-400",
};

/* ── subcomponents ──────────────────────────────────────────────────── */

function VerdictBadge({ verdict }: { verdict: ScenarioVerdict }) {
  return (
    <span className={`inline-block rounded border px-2 py-0.5 text-[11px] font-mono ${VERDICT_BADGE[verdict]}`}>
      {VERDICT_LABEL[verdict]}
    </span>
  );
}

/**
 * KpiCard — one of the four summary tiles at the top of the page. Clicking filters the
 * underlying list when `onSelect` is provided.
 */
function KpiCard({
  label,
  count,
  tone,
  active,
  onSelect,
  unavailable,
}: {
  label: string;
  count: number;
  tone: "neutral" | "amber" | "red" | "emerald" | "yellow";
  active?: boolean;
  onSelect?: () => void;
  unavailable?: boolean;
}) {
  const toneClass = {
    neutral: "text-foreground",
    amber: "text-amber",
    red: "text-red-400",
    emerald: "text-emerald-400",
    yellow: "text-yellow-400",
  }[tone];
  const Wrapper: "button" | "div" = onSelect ? "button" : "div";
  return (
    <Wrapper
      type={onSelect ? "button" : undefined}
      onClick={onSelect}
      className={`flex flex-col gap-1 border px-4 py-3 text-left transition-colors ${
        active ? "border-amber/50 bg-amber/5" : "border-iron bg-charcoal hover:border-iron/80"
      } ${onSelect ? "cursor-pointer" : ""}`}
    >
      <span className="text-[10px] font-mono uppercase tracking-[0.12em] text-slate-text">
        {label}
      </span>
      <span className={`font-mono text-2xl ${toneClass}`} title={unavailable ? "KPI fetch failed" : undefined}>
        {unavailable ? "—" : count}
      </span>
    </Wrapper>
  );
}

/**
 * ScenarioDrawer — the expanded "full detail" view shown when a row is clicked.
 * Shows the current verdict + Why/Fix + run history timeline + telemetry.
 */
function ScenarioDrawer({ scenario }: { scenario: Scenario }) {
  const { data: runs, isLoading, isError: runsError } = useScenarioRuns(scenario.id);
  const hasVerdict = !!scenario.last_verdict;

  return (
    <div className="mt-4 border-t border-iron/30 pt-4 space-y-4">
      {hasVerdict && (
        <div className="space-y-2">
          <div className="flex items-center gap-2 flex-wrap text-[11px] font-mono">
            <VerdictBadge verdict={scenario.last_verdict!} />
            <span className="text-slate-text">
              {scenario.last_pr_number != null ? (
                <>on PR #{scenario.last_pr_number}</>
              ) : (
                "on last review"
              )}
              {scenario.last_confidence != null && (
                <> · {Math.round(scenario.last_confidence * 100)}% sure</>
              )}
            </span>
            {scenario.last_run_at && (
              <span className="text-iron">· {formatDistanceToNow(scenario.last_run_at)}</span>
            )}
          </div>
          {scenario.last_why && (
            <p className="text-[12px] font-mono text-foreground leading-relaxed">
              <span className="text-slate-text">Why: </span>
              {scenario.last_why}
            </p>
          )}
          {scenario.last_fix && (
            <p className="text-[12px] font-mono text-foreground leading-relaxed">
              <span className="text-slate-text">Fix: </span>
              {scenario.last_fix}
            </p>
          )}
        </div>
      )}

      {scenario.files?.length > 0 && (
        <div className="flex gap-1.5 flex-wrap">
          {scenario.files.map((f) => (
            <span
              key={f}
              className="inline-block border border-iron/60 bg-iron/10 px-2.5 py-1 text-xs font-mono text-slate-text"
            >
              {f}
            </span>
          ))}
        </div>
      )}

      {/* Telemetry */}
      <div className="flex items-center gap-3 text-[11px] font-mono text-slate-text flex-wrap">
        <span>
          Triggered{" "}
          <span className="text-foreground">{scenario.trigger_count ?? 0}</span> times
        </span>
        <span className="text-iron">·</span>
        <span>First seen {formatDistanceToNow(scenario.created_at)}</span>
        {scenario.source_ref && (
          <>
            <span className="text-iron">·</span>
            <span>
              Learned from review{" "}
              <a
                href={`/reviews/${scenario.source_ref}`}
                className="text-amber hover:text-amber-glow inline-flex items-center gap-1"
              >
                #{scenario.source_ref.slice(0, 8)}
                <ExternalLink className="h-3 w-3" />
              </a>
            </span>
          </>
        )}
      </div>

      {/* Run history */}
      <div className="space-y-2">
        <h3 className="text-[10px] font-mono uppercase tracking-[0.12em] text-slate-text">
          Run history
        </h3>
        {isLoading ? (
          <div className="flex items-center gap-2 text-[11px] font-mono text-slate-text">
            <Loader className="h-3 w-3 animate-spin" />
            Loading runs…
          </div>
        ) : runsError ? (
          <p className="flex items-center gap-1.5 text-[11px] font-mono text-red-400">
            <AlertCircle className="h-3 w-3" />
            Could not load run history.
          </p>
        ) : !runs || runs.length === 0 ? (
          <p className="text-[11px] font-mono text-slate-text">No runs yet.</p>
        ) : (
          <ul className="space-y-1.5">
            {runs.map((run) => (
              <li
                key={run.id}
                className="border border-iron/40 bg-iron/5 px-3 py-2 space-y-1"
              >
                <div className="flex items-center gap-2 flex-wrap text-[11px] font-mono">
                  <VerdictBadge verdict={run.verdict} />
                  <span className="text-slate-text">
                    PR #{run.pr_number} · {Math.round(run.confidence * 100)}% sure
                  </span>
                  <span className="text-iron ml-auto">{formatDistanceToNow(run.created_at)}</span>
                </div>
                {run.why && (
                  <p className="text-[11px] font-mono text-foreground leading-relaxed">
                    {run.why}
                  </p>
                )}
                {run.fix && (
                  <p className="text-[11px] font-mono text-slate-text leading-relaxed">
                    <span className="text-iron">Fix: </span>
                    {run.fix}
                  </p>
                )}
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}

/* ── page ───────────────────────────────────────────────────────────── */

export default function ScenariosPage() {
  const { activeId } = useActiveRepo();
  const { data: scenarios, isLoading, isError: scenariosError, error: scenariosErrMsg } = useScenarios();
  const { data: kpis, isError: kpisError } = useScenarioKPIs();
  const createScenario = useCreateScenario();
  const deleteScenario = useDeleteScenario();
  const [banner, setBanner] = useState<{ tone: "error" | "success"; message: string } | null>(null);

  const [severityFilter, setSeverityFilter] = useState<SeverityFilter>("all");
  const [showForm, setShowForm] = useState(false);
  const [description, setDescription] = useState("");
  const [severity, setSeverity] = useState("medium");
  const [filesInput, setFilesInput] = useState("");
  const [expandedId, setExpandedId] = useState<number | null>(null);
  const [confirmDeleteId, setConfirmDeleteId] = useState<number | null>(null);
  const deleteTimerRef = useRef<ReturnType<typeof setTimeout>>(undefined);

  useEffect(() => () => clearTimeout(deleteTimerRef.current), []);

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
    const files = filesInput.split(",").flatMap((f) => {
      const trimmed = f.trim();
      return trimmed ? [trimmed] : [];
    });
    createScenario.mutate(
      { description: description.trim(), severity, files },
      {
        onSuccess: () => {
          setDescription("");
          setSeverity("medium");
          setFilesInput("");
          setShowForm(false);
          setBanner(null);
        },
        onError: (err: Error) =>
          setBanner({ tone: "error", message: `Could not create scenario — ${err.message}` }),
      },
    );
  };

  const handleDelete = (e: React.MouseEvent, scenarioId: number) => {
    e.stopPropagation();
    if (confirmDeleteId === scenarioId) {
      deleteScenario.mutate(scenarioId, {
        onError: (err: Error) =>
          setBanner({ tone: "error", message: `Could not delete scenario — ${err.message}` }),
      });
      setConfirmDeleteId(null);
    } else {
      setConfirmDeleteId(scenarioId);
      clearTimeout(deleteTimerRef.current);
      deleteTimerRef.current = setTimeout(() => setConfirmDeleteId(null), 3000);
    }
  };

  return (
    <ProGate feature="Scenario memory">
      <div className="mb-6 flex items-center justify-between gap-4">
        <div>
          <h1 className="font-mono text-2xl font-bold text-foreground">Scenarios</h1>
          <p className="text-xs font-mono text-slate-text mt-1 max-w-xl">
            Risks Argus learned from past reviews and watches for on every new PR.
          </p>
        </div>
        <button
          type="button"
          onClick={() => setShowForm(!showForm)}
          className="flex items-center gap-2 border border-amber/30 bg-amber/10 px-3 py-1.5 text-xs font-mono text-amber hover:bg-amber/20 transition-colors cursor-pointer"
        >
          <Plus className="h-3.5 w-3.5" />
          Add scenario
        </button>
      </div>

      {/* Inline banner for mutation errors (create / delete). Auto-cleared on next success. */}
      {banner && (
        <div
          className={`mb-4 flex items-start gap-2 border px-3 py-2 text-[12px] font-mono ${
            banner.tone === "error"
              ? "border-red-500/40 bg-red-500/10 text-red-400"
              : "border-emerald-500/40 bg-emerald-500/10 text-emerald-400"
          }`}
        >
          <AlertCircle className="h-3.5 w-3.5 mt-0.5 shrink-0" />
          <span className="flex-1">{banner.message}</span>
          <button
            type="button"
            onClick={() => setBanner(null)}
            className="text-slate-text hover:text-foreground"
            aria-label="Dismiss"
          >
            ×
          </button>
        </div>
      )}

      {/* KPI cards — render a muted "—" when the KPI fetch fails so the page doesn't pretend
          the counts are zero. The scenario list still renders independently. */}
      <div className="mb-4 grid grid-cols-2 md:grid-cols-4 gap-3">
        <KpiCard label="Active" count={kpis?.active ?? 0} tone="neutral" unavailable={kpisError} />
        <KpiCard label="Broken this week" count={kpis?.broken_this_week ?? 0} tone="red" unavailable={kpisError} />
        <KpiCard label="Fixed this week" count={kpis?.fixed_this_week ?? 0} tone="emerald" unavailable={kpisError} />
        <KpiCard label="Outdated" count={kpis?.outdated ?? 0} tone="yellow" unavailable={kpisError} />
      </div>

      {/* Create form */}
      <div
        className={`overflow-hidden transition-[max-height,opacity] duration-250 ease-out ${
          showForm ? "max-h-[500px] opacity-100 mb-6" : "max-h-0 opacity-0"
        }`}
      >
        <div className="border border-amber/30 bg-charcoal p-5 space-y-5">
          <form onSubmit={handleCreate} className="space-y-5">
            <div>
              <label
                htmlFor="scenario-description"
                className="block text-[11px] font-mono uppercase tracking-wider text-slate-text mb-1.5"
              >
                Description
              </label>
              <textarea
                id="scenario-description"
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                onKeyDown={(e) => {
                  if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
                    e.preventDefault();
                    handleCreate(e as React.FormEvent);
                  }
                }}
                rows={3}
                placeholder="Describe the risk scenario\u2026"
                className="w-full border border-iron bg-background px-3 py-2 text-xs font-mono text-foreground placeholder:text-iron focus:border-amber focus:outline-none resize-none"
              />
            </div>
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-5">
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
                <label
                  htmlFor="scenario-files"
                  className="block text-[11px] font-mono uppercase tracking-wider text-slate-text mb-1.5"
                >
                  Related files (comma-separated)
                </label>
                <input
                  id="scenario-files"
                  type="text"
                  value={filesInput}
                  onChange={(e) => setFilesInput(e.target.value)}
                  placeholder="src/auth.ts, lib/db.ts"
                  className="w-full border border-iron bg-background px-3 py-2 text-xs font-mono text-foreground placeholder:text-iron focus:border-amber focus:outline-none"
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
                className="border border-amber bg-amber/10 px-4 py-1.5 text-xs font-mono text-amber hover:bg-amber/20 transition-colors disabled:opacity-50 cursor-pointer disabled:cursor-not-allowed"
              >
                {createScenario.isPending ? "Creating..." : "Create scenario"}
              </button>
            </div>
          </form>
        </div>
      </div>

      {/* Severity filter tabs */}
      <div className="flex items-center gap-1.5 mb-3">
        {(["all", "critical", "high", "medium", "low"] as const).map((tab) => {
          const label = tab === "all" ? "All" : tab.charAt(0).toUpperCase() + tab.slice(1);
          const count = severityCounts[tab];
          const isActive = severityFilter === tab;
          return (
            <button
              key={tab}
              onClick={() => {
                setSeverityFilter(tab);
                setPage(0);
              }}
              className={`rounded border px-2.5 py-1 text-[11px] font-mono transition-colors cursor-pointer ${
                isActive ? SEVERITY_FILTER_ACTIVE[tab] : "border-iron text-slate-text hover:text-foreground"
              }`}
            >
              {label} ({count})
            </button>
          );
        })}
      </div>

      {/* Scenarios list */}
      <div className="border border-iron bg-charcoal overflow-hidden">
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
        ) : scenariosError ? (
          // Distinct from "no scenarios yet" — a fetch error shouldn't be misread as empty state.
          <div className="py-10 flex flex-col items-center gap-2 text-center">
            <AlertCircle className="h-7 w-7 text-red-400" />
            <p className="text-sm font-mono text-foreground">Could not load scenarios</p>
            <p className="text-[11px] font-mono text-slate-text max-w-md">
              {scenariosErrMsg?.message ?? "Network error"}
            </p>
          </div>
        ) : filtered.length === 0 ? (
          <div className="py-16 flex flex-col items-center gap-4">
            <div className="rounded-full border border-iron/50 bg-iron/10 p-4">
              <Shield className="h-7 w-7 text-slate-text" />
            </div>
            <div className="text-center">
              <p className="text-sm font-mono text-foreground mb-1">No scenarios yet</p>
              <p className="text-xs font-mono text-slate-text max-w-xs">
                Argus auto-creates scenarios from critical findings in reviews, or you can add one manually.
              </p>
            </div>
            <button
              type="button"
              onClick={() => setShowForm(true)}
              className="mt-1 flex items-center gap-2 border border-amber/30 bg-amber/10 px-4 py-2 text-xs font-mono text-amber hover:bg-amber/20 transition-colors cursor-pointer"
            >
              <Plus className="h-3.5 w-3.5" />
              Add scenario
            </button>
          </div>
        ) : (
          <div className="divide-y divide-iron/30">
            {paginated.map((scenario) => {
              const isExpanded = expandedId === scenario.id;
              return (
                <div
                  key={scenario.id}
                  className="flex hover:bg-iron/10 transition-colors cursor-pointer"
                  onClick={() => setExpandedId(isExpanded ? null : scenario.id)}
                >
                  {/* Severity bar */}
                  <div className={`w-1 shrink-0 ${SEVERITY_DOT[scenario.severity] ?? SEVERITY_DOT.medium}`} />

                  <div className="flex-1 min-w-0 px-5 py-4">
                    <div className="flex items-start justify-between gap-4">
                      <div className="flex-1 min-w-0">
                        {/* Row 1: title */}
                        <div className="flex items-start gap-1.5">
                          <ChevronDown
                            className={`h-3.5 w-3.5 shrink-0 mt-0.5 text-slate-text transition-transform ${
                              isExpanded ? "rotate-0" : "-rotate-90"
                            }`}
                          />
                          <p
                            className={`text-sm font-mono text-foreground leading-snug ${
                              isExpanded ? "" : "line-clamp-2"
                            }`}
                          >
                            {scenario.description}
                          </p>
                        </div>

                        {/* Row 2: verdict + why (new) */}
                        {scenario.last_verdict && (
                          <div className="mt-2 flex items-start gap-2 flex-wrap text-[12px] font-mono">
                            <VerdictBadge verdict={scenario.last_verdict} />
                            <span className="text-slate-text">
                              {scenario.last_pr_number != null ? (
                                <>on PR #{scenario.last_pr_number}</>
                              ) : (
                                "on last review"
                              )}
                            </span>
                            {scenario.last_why && !isExpanded && (
                              <span className="text-slate-text truncate max-w-[60ch]">
                                · {scenario.last_why}
                              </span>
                            )}
                          </div>
                        )}

                        {/* Row 3: metadata badges */}
                        <div className="mt-2 flex items-center gap-2 flex-wrap">
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
                          <span className="text-[11px] font-mono text-slate-text">
                            {formatDistanceToNow(scenario.created_at)}
                          </span>
                        </div>

                        {isExpanded && <ScenarioDrawer scenario={scenario} />}
                      </div>
                      <button
                        type="button"
                        aria-label="Delete scenario"
                        onClick={(e) => handleDelete(e, scenario.id)}
                        disabled={deleteScenario.isPending}
                        className={`flex items-center gap-1.5 shrink-0 transition-colors disabled:opacity-50 cursor-pointer ${
                          confirmDeleteId === scenario.id
                            ? "text-red-400 animate-pulse"
                            : "text-slate-text hover:text-red-400"
                        }`}
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                        {confirmDeleteId === scenario.id && (
                          <span className="text-[11px] font-mono">confirm</span>
                        )}
                      </button>
                    </div>
                  </div>
                </div>
              );
            })}
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
    </ProGate>
  );
}
