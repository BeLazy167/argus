"use client";
import { Network, Loader2 } from "lucide-react";
import { useActiveRepo } from "@/lib/hooks/use-active-repo";
import { useGraphData } from "@/lib/queries/graph";
import { useRepos } from "@/lib/queries/repos";
import GraphCanvas from "@/components/graph/GraphCanvas";

function GraphBody() {
  const { activeId } = useActiveRepo();
  const { data: graphData, isLoading, error } = useGraphData();
  const { data: repos } = useRepos();

  if (!activeId) {
    return (
      <div className="flex items-center justify-center h-full text-xs font-mono text-slate-text">
        Select a repo to view its architecture graph.
      </div>
    );
  }

  if (isLoading) {
    return (
      <div className="flex items-center justify-center h-full">
        <Loader2 className="h-5 w-5 animate-spin text-slate-text" />
      </div>
    );
  }

  if (error) {
    return (
      <div className="flex flex-col items-center justify-center h-full gap-3 text-center px-8">
        <Network className="h-10 w-10 text-red-500/50" />
        <p className="text-xs font-mono text-red-400 max-w-sm">
          Failed to load architecture graph. Try refreshing the page.
        </p>
      </div>
    );
  }

  if (!graphData || !graphData.nodes || graphData.nodes.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center h-full gap-3 text-center px-8">
        <Network className="h-10 w-10 text-iron" />
        <p className="text-xs font-mono text-slate-text max-w-sm">
          No architecture data yet. The graph builds automatically as Argus reviews PRs for this repo.
        </p>
      </div>
    );
  }

  const activeRepo = repos?.find((r) => r.id === activeId);

  return (
    <GraphCanvas
      graphNodes={graphData.nodes}
      graphEdges={graphData.edges}
      repoFullName={activeRepo?.full_name || ""}
      defaultBranch={activeRepo?.default_branch || "main"}
    />
  );
}

export default function ArchitecturePage() {
  const { data: graphData } = useGraphData();

  return (
    <div className="flex flex-col h-[calc(100vh-4rem)]">
      <div className="flex items-center gap-2 border-b border-iron px-5 py-4 shrink-0">
        <Network className="h-4 w-4 text-slate-text" />
        <h2 className="text-xs font-mono uppercase tracking-[0.1em] text-foreground">
          Architecture Map
        </h2>
        {graphData && (
          <span className="text-[10px] font-mono text-slate-text ml-auto">
            {graphData.nodes.length} nodes · {graphData.edges.length} edges
          </span>
        )}
      </div>

      <div className="flex-1 min-h-0">
        <GraphBody />
      </div>
    </div>
  );
}
