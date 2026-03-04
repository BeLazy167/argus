"use client";

import { useMemo, useState } from "react";
import { Plus, Trash2, Loader2, Brain, Filter } from "lucide-react";
import { usePatterns, useCreatePattern, useDeletePattern } from "@/lib/queries/patterns";
import { useRepos } from "@/lib/queries/repos";
import { formatDistanceToNow } from "@/lib/time";

export default function PatternsPage() {
  const { data: patterns, isLoading } = usePatterns();
  const { data: repos } = useRepos();
  const createPattern = useCreatePattern();
  const deletePattern = useDeletePattern();
  const [content, setContent] = useState("");
  const [selectedRepoId, setSelectedRepoId] = useState<number | undefined>();
  const [filterRepo, setFilterRepo] = useState<string>("all");

  const repoMap = useMemo(() => {
    const m = new Map<number, string>();
    for (const r of repos ?? []) {
      m.set(r.id, r.full_name);
    }
    return m;
  }, [repos]);

  const filtered = useMemo(() => {
    if (!patterns) return [];
    if (filterRepo === "all") return patterns;
    if (filterRepo === "org") return patterns.filter((p) => !p.repo_id);
    const repoId = Number(filterRepo);
    return patterns.filter((p) => p.repo_id === repoId);
  }, [patterns, filterRepo]);

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!content.trim()) return;
    createPattern.mutate({ content: content.trim(), repo_id: selectedRepoId });
    setContent("");
  };

  return (
    <>
      <div className="mb-8">
        <h1 className="font-display text-2xl font-bold text-foreground">
          Patterns
        </h1>
        <p className="text-xs font-mono text-slate-text mt-1">
          Remembered patterns shape future reviews. Add via dashboard or{" "}
          <code className="text-amber">@argus-eye remember</code>.
        </p>
      </div>

      {/* Add Pattern Form */}
      <form onSubmit={handleSubmit} className="mb-8">
        <div className="flex gap-3">
          <input
            type="text"
            value={content}
            onChange={(e) => setContent(e.target.value)}
            placeholder="e.g. Always use guard clauses instead of nested if statements"
            className="flex-1 rounded-lg border border-iron bg-charcoal px-4 py-2.5 text-xs font-mono text-foreground placeholder:text-slate-text/50 focus:outline-none focus:border-amber/50 transition-colors"
          />
          <select
            value={selectedRepoId ?? ""}
            onChange={(e) =>
              setSelectedRepoId(e.target.value ? Number(e.target.value) : undefined)
            }
            className="rounded-lg border border-iron bg-charcoal px-3 py-2.5 text-xs font-mono text-foreground focus:outline-none focus:border-amber/50 transition-colors"
          >
            <option value="">Org-wide</option>
            {(repos ?? []).map((r) => (
              <option key={r.id} value={r.id}>
                {r.full_name.split("/").pop()}
              </option>
            ))}
          </select>
          <button
            type="submit"
            disabled={!content.trim() || createPattern.isPending}
            className="flex items-center gap-2 rounded-lg border border-amber/30 bg-amber/10 px-4 py-2.5 text-xs font-mono font-medium text-amber hover:bg-amber/20 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
          >
            {createPattern.isPending ? (
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
            ) : (
              <Plus className="h-3.5 w-3.5" />
            )}
            Add
          </button>
        </div>
      </form>

      {/* Filter */}
      <div className="flex items-center gap-3 mb-4">
        <Filter className="h-3.5 w-3.5 text-slate-text" />
        <div className="flex gap-1.5 flex-wrap">
          <button
            onClick={() => setFilterRepo("all")}
            className={`rounded border px-2.5 py-1 text-[10px] font-mono transition-colors ${
              filterRepo === "all"
                ? "border-amber/40 bg-amber/10 text-amber"
                : "border-iron text-slate-text hover:text-foreground"
            }`}
          >
            All ({patterns?.length ?? 0})
          </button>
          <button
            onClick={() => setFilterRepo("org")}
            className={`rounded border px-2.5 py-1 text-[10px] font-mono transition-colors ${
              filterRepo === "org"
                ? "border-purple-500/40 bg-purple-500/10 text-purple-400"
                : "border-iron text-slate-text hover:text-foreground"
            }`}
          >
            Org-wide ({patterns?.filter((p) => !p.repo_id).length ?? 0})
          </button>
          {(repos ?? []).map((r) => {
            const count = patterns?.filter((p) => p.repo_id === r.id).length ?? 0;
            if (count === 0) return null;
            return (
              <button
                key={r.id}
                onClick={() => setFilterRepo(String(r.id))}
                className={`rounded border px-2.5 py-1 text-[10px] font-mono transition-colors ${
                  filterRepo === String(r.id)
                    ? "border-blue-500/40 bg-blue-500/10 text-blue-400"
                    : "border-iron text-slate-text hover:text-foreground"
                }`}
              >
                {r.full_name.split("/").pop()} ({count})
              </button>
            );
          })}
        </div>
      </div>

      {/* Patterns Table */}
      <div className="rounded-lg border border-iron bg-charcoal overflow-hidden">
        <div className="flex items-center gap-2 border-b border-iron px-5 py-4">
          <Brain className="h-4 w-4 text-slate-text" />
          <h2 className="text-xs font-mono uppercase tracking-[0.1em] text-foreground">
            Active Patterns
          </h2>
          <span className="text-[10px] font-mono text-slate-text ml-auto">
            {filtered.length} patterns
          </span>
        </div>

        {isLoading ? (
          <div className="flex items-center justify-center py-10">
            <Loader2 className="h-5 w-5 animate-spin text-slate-text" />
          </div>
        ) : filtered.length === 0 ? (
          <div className="py-10 text-center text-xs font-mono text-slate-text">
            No patterns yet. Add one above or use{" "}
            <code className="text-amber">@argus-eye remember</code> in a PR comment.
          </div>
        ) : (
          <table className="w-full">
            <thead>
              <tr className="border-b border-iron/50 text-[10px] font-mono uppercase tracking-wider text-slate-text">
                <th className="text-left px-5 py-2.5 font-medium">Content</th>
                <th className="text-left px-3 py-2.5 font-medium">Scope</th>
                <th className="text-left px-3 py-2.5 font-medium">Added by</th>
                <th className="text-left px-3 py-2.5 font-medium">Created</th>
                <th className="text-right px-5 py-2.5 font-medium" />
              </tr>
            </thead>
            <tbody>
              {filtered.map((pattern) => (
                <tr
                  key={pattern.id}
                  className="border-b border-iron/30 last:border-0 hover:bg-iron/10 transition-colors"
                >
                  <td className="px-5 py-3 max-w-md">
                    <p className="text-xs font-mono text-foreground truncate">
                      {pattern.content}
                    </p>
                  </td>
                  <td className="px-3 py-3">
                    <span
                      className={`inline-block rounded border px-2 py-0.5 text-[10px] font-mono ${
                        pattern.repo_id
                          ? "border-blue-500/30 bg-blue-500/10 text-blue-400"
                          : "border-purple-500/30 bg-purple-500/10 text-purple-400"
                      }`}
                    >
                      {pattern.repo_id
                        ? repoMap.get(pattern.repo_id)?.split("/").pop() ?? "repo"
                        : "org"}
                    </span>
                  </td>
                  <td className="px-3 py-3">
                    <span className="text-[11px] font-mono text-slate-text">
                      {pattern.created_by ?? "system"}
                    </span>
                  </td>
                  <td className="px-3 py-3">
                    <span className="text-[10px] font-mono text-slate-text">
                      {formatDistanceToNow(pattern.created_at)}
                    </span>
                  </td>
                  <td className="px-5 py-3 text-right">
                    <button
                      onClick={() => deletePattern.mutate(pattern.id)}
                      disabled={deletePattern.isPending}
                      className="text-slate-text hover:text-red-400 transition-colors disabled:opacity-50"
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </>
  );
}
