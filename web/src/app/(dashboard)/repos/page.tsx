"use client";

import { useState } from "react";
import {
  GitFork,
  ToggleLeft,
  ToggleRight,
  Loader2,
  ExternalLink,
  Play,
} from "lucide-react";
import { useRepos, useUpdateRepo } from "@/lib/queries/repos";
import { useReviews, useTriggerReview } from "@/lib/queries/reviews";
import { formatDistanceToNow } from "@/lib/time";
import type { Repo } from "@/lib/types";

function RepoCard({ repo }: { repo: Repo }) {
  const { data: reviews } = useReviews(repo.id, 1);
  const updateRepo = useUpdateRepo();
  const triggerReview = useTriggerReview();
  const [showTrigger, setShowTrigger] = useState(false);
  const [prNumber, setPrNumber] = useState("");

  const lastReview = reviews?.[0];

  const handleTrigger = () => {
    if (!prNumber) return;
    triggerReview.mutate(
      { repoId: repo.id, prNumber: Number(prNumber) },
      {
        onSuccess: () => {
          setPrNumber("");
          setShowTrigger(false);
        },
      },
    );
  };

  return (
    <div
      className={`rounded-lg border p-5 ${
        repo.enabled
          ? "border-iron bg-charcoal"
          : "border-iron border-dashed bg-charcoal opacity-60"
      }`}
    >
      {/* Top row */}
      <div className="flex items-center justify-between mb-3">
        <div className="flex items-center gap-3 min-w-0">
          <GitFork className="h-4 w-4 text-slate-text shrink-0" />
          <span className="text-sm font-mono text-foreground font-bold truncate">
            {repo.full_name}
          </span>
          <span className="inline-flex items-center rounded-sm border border-iron bg-iron/50 px-2 py-0.5 text-[10px] font-mono text-slate-text shrink-0">
            {repo.default_branch}
          </span>
        </div>
        <button
          type="button"
          onClick={() =>
            updateRepo.mutate({ id: repo.id, enabled: !repo.enabled })
          }
          className="flex items-center gap-1.5 text-xs font-mono shrink-0"
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

      {/* Middle: last review */}
      {lastReview && (
        <div className="text-[11px] font-mono text-slate-text mb-3">
          Last review:{" "}
          {lastReview.score != null && (
            <span
              className={
                lastReview.score >= 8
                  ? "text-green-400"
                  : lastReview.score >= 5
                    ? "text-amber"
                    : "text-red-400"
              }
            >
              {lastReview.score}
            </span>
          )}{" "}
          &middot; {formatDistanceToNow(lastReview.created_at)}
        </div>
      )}

      {/* Bottom: trigger review */}
      {showTrigger ? (
        <div className="flex items-center gap-2">
          <input
            type="number"
            placeholder="PR #"
            value={prNumber}
            onChange={(e) => setPrNumber(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && handleTrigger()}
            className="w-24 rounded-md border border-iron bg-background px-2.5 py-1.5 text-xs font-mono text-foreground placeholder:text-slate-text focus:border-amber focus:outline-none"
          />
          <button
            type="button"
            onClick={handleTrigger}
            disabled={triggerReview.isPending || !prNumber}
            className="inline-flex items-center gap-1.5 rounded-md border border-amber/30 bg-amber/10 px-3 py-1.5 text-xs font-mono text-amber hover:bg-amber/20 transition-colors disabled:opacity-50"
          >
            {triggerReview.isPending ? (
              <Loader2 className="h-3 w-3 animate-spin" />
            ) : (
              <Play className="h-3 w-3" />
            )}
            Run
          </button>
          <button
            type="button"
            onClick={() => {
              setShowTrigger(false);
              setPrNumber("");
            }}
            className="text-[10px] font-mono text-slate-text hover:text-foreground transition-colors"
          >
            cancel
          </button>
        </div>
      ) : (
        <button
          type="button"
          onClick={() => setShowTrigger(true)}
          className="text-[11px] font-mono text-slate-text hover:text-amber transition-colors"
        >
          Trigger review
        </button>
      )}
    </div>
  );
}

export default function ReposPage() {
  const { data: repos, isLoading } = useRepos();

  return (
    <>
      <div className="mb-8 flex items-center justify-between">
        <div>
          <h1 className="font-display text-2xl font-bold text-foreground">
            Repositories
          </h1>
          <p className="text-xs font-mono text-slate-text mt-1">
            Repos connected via the Argus GitHub App.
          </p>
        </div>
        <a
          href="https://github.com/apps/argus-ai/installations/new"
          target="_blank"
          rel="noopener noreferrer"
          className="inline-flex items-center gap-1.5 rounded-md border border-amber/30 bg-amber/10 px-4 py-2 text-xs font-mono text-amber hover:bg-amber/20 transition-colors"
        >
          <ExternalLink className="h-3.5 w-3.5" />
          Add repos
        </a>
      </div>

      {isLoading ? (
        <div className="flex items-center justify-center py-20">
          <Loader2 className="h-6 w-6 animate-spin text-slate-text" />
        </div>
      ) : repos?.length === 0 ? (
        <div className="rounded-lg border border-iron bg-charcoal p-10 text-center">
          <GitFork className="h-8 w-8 text-slate-text mx-auto mb-3" />
          <p className="text-sm font-mono text-foreground mb-1">No repos yet</p>
          <p className="text-xs font-mono text-slate-text">
            Install the Argus GitHub App on your org to get started.
          </p>
        </div>
      ) : (
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
          {repos?.map((repo) => (
            <RepoCard key={repo.id} repo={repo} />
          ))}
        </div>
      )}
    </>
  );
}
