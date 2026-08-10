import { Info } from "lucide-react";
import { useUpdateRepo } from "@/lib/queries/repos";
import type { Repo } from "@/lib/types";

/**
 * "Branch Filters" settings tab: per-repo glob list of base branches to skip
 * during review.
 *
 * Self-contained — it owns its own {@link useUpdateRepo} mutation rather than
 * receiving it, so the parent page only needs to pass the repo list.
 */
export function BranchFiltersTab({ repos }: { repos: Repo[] }) {
  const updateRepo = useUpdateRepo();

  return (
    <div className="space-y-6">
      <div className="border border-amber/20 bg-amber/5 px-4 py-3 flex items-start gap-2.5">
        <Info className="h-3.5 w-3.5 text-amber mt-0.5 shrink-0" />
        <p className="text-[11px] font-mono text-amber/80">
          Skip reviews for PRs targeting these base branches. Supports glob patterns (e.g.{" "}
          <code className="text-amber">release/*</code>).
        </p>
      </div>

      {repos.length === 0 ? (
        <div className="py-10 text-center">
          <p className="text-sm font-mono text-slate-text">No repos yet. Sync repos first.</p>
        </div>
      ) : (
        repos
          .filter((r) => r.enabled)
          .map((repo) => {
            const settings = (repo.settings_json ?? {}) as Record<string, unknown>;
            const raw = settings.skip_base_branches;
            const skipBranches = Array.isArray(raw)
              ? raw.filter((v): v is string => typeof v === "string")
              : [];

            return (
              <div key={repo.id} className="border border-iron p-4">
                <div className="flex items-center justify-between mb-3">
                  <span className="text-xs font-mono text-foreground">{repo.full_name}</span>
                  <span className="text-[10px] font-mono text-slate-text">
                    {repo.default_branch}
                  </span>
                </div>
                <div className="flex flex-wrap gap-1.5 items-center">
                  {skipBranches.map((branch, i) => (
                    <span
                      key={branch}
                      className="inline-flex items-center gap-1 bg-charcoal border border-iron px-2 py-0.5 text-[11px] font-mono text-slate-text"
                    >
                      {branch}
                      <button
                        type="button"
                        disabled={updateRepo.isPending}
                        onClick={() => {
                          const updated = skipBranches.filter((_, j) => j !== i);
                          updateRepo.mutate({
                            id: repo.id,
                            settings_json: { ...settings, skip_base_branches: updated },
                          });
                        }}
                        className="text-slate-text/50 hover:text-red-400 ml-0.5 cursor-pointer disabled:opacity-30"
                      >
                        &times;
                      </button>
                    </span>
                  ))}
                  <form
                    className="inline-flex"
                    onSubmit={(e) => {
                      e.preventDefault();
                      const input = e.currentTarget.querySelector("input") as HTMLInputElement;
                      const val = input.value.trim();
                      if (!val || skipBranches.includes(val)) return;
                      updateRepo.mutate({
                        id: repo.id,
                        settings_json: { ...settings, skip_base_branches: [...skipBranches, val] },
                      });
                      input.value = "";
                    }}
                  >
                    <input
                      type="text"
                      placeholder="+ add branch"
                      className="bg-transparent border border-dashed border-iron/50 px-2 py-0.5 text-[11px] font-mono text-foreground placeholder:text-slate-text/30 w-28 focus:border-amber focus:outline-none"
                    />
                  </form>
                </div>
                {skipBranches.length === 0 && (
                  <p className="text-[10px] font-mono text-slate-text/50 mt-2">
                    All branches reviewed
                  </p>
                )}
              </div>
            );
          })
      )}
    </div>
  );
}
