"use client";
import { useMemo, useState } from "react";
import { Network, Loader2, GitBranch, AlertTriangle } from "lucide-react";
import { useActiveRepo } from "@/lib/hooks/use-active-repo";
import { useArchitectureData } from "@/lib/queries/architecture";
import ArchitectureCanvas, { type Lens } from "@/components/graph/ArchitectureCanvas";
import FileMemorySidebar from "@/components/graph/FileMemorySidebar";
import LensBar from "@/components/graph/LensBar";

export default function ArchitecturePage() {
  const { activeId } = useActiveRepo();
  const { data: archData, isLoading, error } = useArchitectureData();
  const [selectedFilePath, setSelectedFilePath] = useState<string | null>(null);
  const [lens, setLens] = useState<Lens>("risk");

  const selectedFile = useMemo(
    () => archData?.files.find((f) => f.path === selectedFilePath),
    [archData, selectedFilePath]
  );

  // Top risk file for header display
  const topRiskFile = useMemo(() => {
    if (!archData || archData.files.length === 0) return null;
    return [...archData.files].sort((a, b) => b.risk_score - a.risk_score)[0];
  }, [archData]);

  return (
    <div className="flex flex-col h-[calc(100vh-4rem)] bg-[#0a0a12]">
      {/* Page heading */}
      <div className="px-5 pt-5 pb-2 shrink-0 bg-[#0a0a12]">
        <h1 className="font-mono text-2xl font-bold text-foreground mb-1">Architecture</h1>
        <p className="text-sm text-slate-text mb-6">
          Dependency analysis — identify choke points, hotspots, and coupling risks.
        </p>
      </div>

      {/* Toolbar: lens switcher + stats */}
      <div className="flex items-center gap-4 border-b border-slate-800/50 px-5 py-3 shrink-0 bg-[#0a0a12]">
        <LensBar active={lens} onChange={(l) => setLens(l as Lens)} />

        <div className="flex-1" />

        {archData && archData.files.length > 0 && (
          <div className="flex items-center gap-4">
            {topRiskFile && topRiskFile.risk_score >= 7 && (
              <button
                onClick={() => setSelectedFilePath(topRiskFile.path)}
                className="flex items-center gap-1.5 text-[11px] font-mono text-amber-400 hover:text-amber-300 transition-colors group"
                title={`Inspect ${topRiskFile.path}`}
              >
                <AlertTriangle className="h-3 w-3" />
                <span>
                  Top risk:{" "}
                  <span className="text-amber-300 font-semibold group-hover:underline">
                    {topRiskFile.path.split("/").pop()}
                  </span>{" "}
                  <span className="text-amber-500/70">({topRiskFile.risk_score.toFixed(1)})</span>
                </span>
              </button>
            )}
            <div className="flex items-center gap-3 text-[11px] font-mono text-slate-600 tabular-nums">
              <span>{archData.files.length} files</span>
              <span className="text-slate-800">·</span>
              <span>{archData.edges.length} deps</span>
              <span className="text-slate-800">·</span>
              <span>{archData.summary.choke_points.length} chokepoints</span>
            </div>
          </div>
        )}
      </div>

      <div className="flex-1 min-h-0 flex flex-col md:flex-row">
        <div className="flex-1 relative">
          {!activeId ? (
            <div className="flex items-center justify-center h-full">
              <div className="text-center">
                <GitBranch className="h-8 w-8 text-slate-700 mx-auto mb-3" />
                <p className="text-xs font-mono text-slate-500">
                  Select a repo to view its architecture.
                </p>
              </div>
            </div>
          ) : isLoading ? (
            <div className="flex items-center justify-center h-full">
              <div className="flex flex-col items-center gap-3">
                <Loader2 className="h-4 w-4 animate-spin text-slate-600" />
                <p className="text-[11px] font-mono text-slate-500">
                  Computing architecture metrics...
                </p>
              </div>
            </div>
          ) : error ? (
            <div className="flex items-center justify-center h-full">
              <div className="text-center">
                <Network className="h-8 w-8 text-red-900 mx-auto mb-3" />
                <p className="text-[11px] font-mono text-red-400/70">Failed to load architecture.</p>
              </div>
            </div>
          ) : !archData || archData.files.length === 0 ? (
            <div className="flex items-center justify-center h-full">
              <div className="text-center max-w-sm">
                <div className="w-14 h-14 rounded-full border border-slate-800/50 bg-slate-800/30 flex items-center justify-center mx-auto mb-5">
                  <Network className="h-6 w-6 text-slate-500" />
                </div>
                <h3 className="text-sm font-mono font-medium text-slate-400 mb-2">
                  No architecture data yet
                </h3>
                <p className="text-[11px] font-mono text-slate-500 leading-relaxed">
                  Architecture metrics are computed from reviewed code. Trigger a review to start
                  building the dependency graph.
                </p>
              </div>
            </div>
          ) : (
            <ArchitectureCanvas
              files={archData.files}
              edges={archData.edges}
              lens={lens}
              onSelectFile={setSelectedFilePath}
            />
          )}
        </div>

        {selectedFilePath && (
          <div className="border-t md:border-t-0 md:border-l border-slate-800/50 bg-[#0a0a12] md:w-[320px] md:shrink-0 max-h-[40vh] md:max-h-none overflow-y-auto">
            <FileMemorySidebar
              filePath={selectedFilePath}
              archFile={selectedFile}
              allFiles={archData?.files}
              onClose={() => setSelectedFilePath(null)}
            />
          </div>
        )}
      </div>
    </div>
  );
}
