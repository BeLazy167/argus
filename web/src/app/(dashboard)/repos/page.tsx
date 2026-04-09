"use client";

import { useState } from "react";
import { usePagination, PaginationBar } from "@/components/dashboard/pagination";
import {
  GitFork,
  ToggleLeft,
  ToggleRight,
  Loader2,
  ExternalLink,
  Play,
  RefreshCw,
  AlertTriangle,
} from "lucide-react";
import { useRepos, useUpdateRepo, useSyncRepos } from "@/lib/queries/repos";
import { useReviews, useTriggerReview } from "@/lib/queries/reviews";
import { useInstallation } from "@/providers/installation-provider";
import { useApi } from "@/lib/hooks/use-api";
import { useOrganization } from "@clerk/nextjs";
import { formatDistanceToNow } from "@/lib/time";
import { scoreColor } from "@/lib/score";
import type { Repo } from "@/lib/types";

function AddReposButton() {
  const api = useApi();
  const { organization } = useOrganization();
  const [loading, setLoading] = useState(false);

  const handleClick = async () => {
    setLoading(true);
    try {
      const orgName = organization?.slug || organization?.name || "";
      const data = await api.get<{ url: string }>(`/api/v1/installations/install-url?org=${encodeURIComponent(orgName)}`);
      window.open(data.url, "_blank");
    } catch {
      // Fallback to generic URL
      window.open("https://github.com/apps/argus-eye/installations/new", "_blank");
    } finally {
      setLoading(false);
    }
  };

  return (
    <button
      onClick={handleClick}
      disabled={loading}
      className="inline-flex items-center gap-1.5 rounded-md border border-amber/30 bg-amber/10 px-4 py-2 text-xs font-mono text-amber hover:bg-amber/20 transition-colors disabled:opacity-50"
    >
      {loading ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <ExternalLink className="h-3.5 w-3.5" />}
      Add repos
    </button>
  );
}

function RepoCard({ repo, isPro }: { repo: Repo; isPro: boolean }) {
  const { data: reviews } = useReviews(repo.id, 1);
  const updateRepo = useUpdateRepo();
  const triggerReview = useTriggerReview();
  const [showTrigger, setShowTrigger] = useState(false);
  const [prNumber, setPrNumber] = useState("");
  const [repoError, setRepoError] = useState("");

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
          onClick={() => {
            setRepoError("");
            updateRepo.mutate(
              { id: repo.id, enabled: !repo.enabled },
              {
                onError: (err) => {
                  const msg = err instanceof Error ? err.message : "Failed to update";
                  setRepoError(msg.includes("Free plan") ? msg : "Failed to update repo");
                },
              },
            );
          }}
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

      {/* Repo limit error */}
      {repoError && (
        <div className="flex items-center gap-2 rounded border border-red-400/30 bg-red-400/5 px-3 py-2 mb-3">
          <AlertTriangle className="h-3 w-3 text-red-400 shrink-0" />
          <p className="text-[10px] font-mono text-red-400">{repoError}</p>
        </div>
      )}

      {/* Free plan note */}
      {!isPro && !repo.enabled && (
        <p className="text-[10px] font-mono text-slate-text/60 mb-2">Free plan: 3 repos max</p>
      )}

      {/* Middle: last review */}
      {lastReview && (
        <div className="text-[11px] font-mono text-slate-text mb-3">
          Last review:{" "}
          {lastReview.score != null && (
            <span className={scoreColor(lastReview.score)}>
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
  const syncRepos = useSyncRepos();
  const { active } = useInstallation();
  const isPro = active?.plan_tier === "pro";
  const { page, setPage, totalPages, paginated, pageSize, total, hasNext, hasPrev } = usePagination(repos ?? []);

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
        <div className="flex items-center gap-3">
          <button
            onClick={() => syncRepos.mutate()}
            disabled={syncRepos.isPending}
            className="flex items-center gap-2 rounded border border-amber/30 bg-amber/10 px-3 py-1.5 text-[10px] font-mono font-medium text-amber hover:bg-amber/20 disabled:opacity-50 transition-colors"
          >
            {syncRepos.isPending ? <Loader2 className="h-3 w-3 animate-spin" /> : <RefreshCw className="h-3 w-3" />}
            Sync from GitHub
          </button>
          <AddReposButton />
        </div>
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
        <>
          <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
            {paginated.map((repo) => (
              <RepoCard key={repo.id} repo={repo} isPro={isPro} />
            ))}
          </div>
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
        </>
      )}
    </>
  );
}
