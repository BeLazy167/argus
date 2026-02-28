"use client";

import { GitFork, ToggleLeft, ToggleRight, Loader2 } from "lucide-react";
import { useRepos, useUpdateRepo } from "@/lib/queries/repos";
import { formatDistanceToNow } from "@/lib/time";

export default function ReposPage() {
  const { data: repos, isLoading } = useRepos();
  const updateRepo = useUpdateRepo();

  return (
    <>
      <div className="mb-8">
        <h1 className="font-display text-2xl font-bold text-foreground">
          Repositories
        </h1>
        <p className="text-xs font-mono text-slate-text mt-1">
          Repos connected via the Argus GitHub App.
        </p>
      </div>

      {isLoading ? (
        <div className="flex items-center justify-center py-20">
          <Loader2 className="h-6 w-6 animate-spin text-slate-text" />
        </div>
      ) : repos?.length === 0 ? (
        <div className="rounded-lg border border-iron bg-charcoal p-10 text-center">
          <GitFork className="h-8 w-8 text-slate-text mx-auto mb-3" />
          <p className="text-sm font-mono text-foreground mb-1">
            No repos yet
          </p>
          <p className="text-xs font-mono text-slate-text">
            Install the Argus GitHub App on your org to get started.
          </p>
        </div>
      ) : (
        <div className="space-y-3">
          {repos?.map((repo) => (
            <div
              key={repo.id}
              className="flex items-center justify-between rounded-lg border border-iron bg-charcoal px-5 py-4"
            >
              <div className="flex items-center gap-4">
                <GitFork className="h-4 w-4 text-slate-text" />
                <div>
                  <p className="text-sm font-mono text-foreground">
                    {repo.full_name}
                  </p>
                  <p className="text-[11px] font-mono text-slate-text">
                    {repo.default_branch} &middot; updated{" "}
                    {formatDistanceToNow(repo.updated_at)}
                  </p>
                </div>
              </div>
              <button
                type="button"
                onClick={() =>
                  updateRepo.mutate({ id: repo.id, enabled: !repo.enabled })
                }
                className="flex items-center gap-2 text-xs font-mono"
                disabled={updateRepo.isPending}
              >
                {repo.enabled ? (
                  <>
                    <ToggleRight className="h-5 w-5 text-green-400" />
                    <span className="text-green-400">Active</span>
                  </>
                ) : (
                  <>
                    <ToggleLeft className="h-5 w-5 text-slate-text" />
                    <span className="text-slate-text">Disabled</span>
                  </>
                )}
              </button>
            </div>
          ))}
        </div>
      )}
    </>
  );
}
