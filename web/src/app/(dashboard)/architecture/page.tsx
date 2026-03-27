"use client";
import { Network, Loader2, GitBranch } from "lucide-react";
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
      <div className="flex items-center justify-center h-full">
        <div className="text-center">
          <GitBranch className="h-8 w-8 text-slate-700 mx-auto mb-3" />
          <p className="text-xs font-mono text-slate-500">Select a repo to view its architecture.</p>
        </div>
      </div>
    );
  }

  if (isLoading) {
    return (
      <div className="flex items-center justify-center h-full">
        <Loader2 className="h-4 w-4 animate-spin text-slate-600" />
      </div>
    );
  }

  if (error) {
    return (
      <div className="flex items-center justify-center h-full">
        <div className="text-center">
          <Network className="h-8 w-8 text-red-900 mx-auto mb-3" />
          <p className="text-[11px] font-mono text-red-400/70">Failed to load graph.</p>
        </div>
      </div>
    );
  }

  if (!graphData || !graphData.nodes || graphData.nodes.length === 0) {
    return (
      <div className="flex items-center justify-center h-full">
        <div className="text-center max-w-xs">
          <div className="w-12 h-12 rounded-full bg-slate-800/50 flex items-center justify-center mx-auto mb-4">
            <Network className="h-5 w-5 text-slate-600" />
          </div>
          <p className="text-[11px] font-mono text-slate-500 leading-relaxed">
            No architecture data yet. The graph builds automatically as Argus reviews PRs.
          </p>
        </div>
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
    <div className="flex flex-col h-[calc(100vh-4rem)] bg-[#0a0a12]">
      {/* Minimal header — the graph is the star */}
      <div className="flex items-center gap-3 border-b border-slate-800/50 px-5 py-3 shrink-0 bg-[#0a0a12]">
        <Network className="h-3.5 w-3.5 text-slate-600" />
        <span className="text-[10px] font-mono uppercase tracking-[0.15em] text-slate-500">
          Architecture
        </span>
        {graphData && graphData.nodes && graphData.nodes.length > 0 && (
          <span className="text-[10px] font-mono text-slate-700 ml-auto">
            {graphData.nodes.length} components · {graphData.edges.length} dependencies
          </span>
        )}
      </div>

      <div className="flex-1 min-h-0">
        <GraphBody />
      </div>
    </div>
  );
}
