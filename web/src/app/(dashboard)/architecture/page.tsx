"use client";
import { useMemo, useState } from "react";
import { Network, Loader2, GitBranch, AlertTriangle, Search, Info, X } from "lucide-react";
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
  const [searchQuery, setSearchQuery] = useState("");
  const [showGuide, setShowGuide] = useState(() =>
    typeof window !== "undefined" ? localStorage.getItem("argus-arch-guide-dismissed") !== "1" : true
  );

  const selectedFile = useMemo(
    () => archData?.files.find((f) => f.path === selectedFilePath),
    [archData, selectedFilePath]
  );

  // Top risk file for header display
  const topRiskFile = useMemo(() => {
    if (!archData || archData.files.length === 0) return null;
    return [...archData.files].sort((a, b) => b.risk_score - a.risk_score)[0];
  }, [archData]);

  const lensCounts = useMemo(() => {
    if (!archData) return undefined;
    const f = archData.files;
    return {
      risk: f.length,
      choke: f.filter((x) => x.fan_in >= 3).length,
      hotspot: f.filter((x) => x.bug_density > 0).length,
      coupling: f.filter((x) => x.coupling.length > 0).length,
    };
  }, [archData]);

  return (
    <div className="flex flex-col h-[calc(100vh-4rem)] bg-[var(--graph-bg)]">
      {/* Page heading */}
      <div className="px-5 pt-5 pb-2 shrink-0 bg-[var(--graph-bg)]">
        <h1 className="font-mono text-2xl font-bold text-foreground mb-1">Architecture</h1>
        <p className="text-sm text-slate-text mb-6">
          Dependency analysis — identify choke points, hotspots, and coupling risks.
        </p>
      </div>

      {/* Toolbar: lens switcher + search + stats */}
      <div className="flex flex-col sm:flex-row items-start sm:items-center gap-3 border-b border-slate-800/50 px-5 py-3 shrink-0 bg-[var(--graph-bg)]">
        <LensBar active={lens} onChange={(l) => setLens(l as Lens)} fileCounts={lensCounts} />

        {/* Search */}
        <div className="relative">
          <Search className="absolute left-2 top-1/2 -translate-y-1/2 h-3 w-3 text-slate-600" />
          <input
            type="text"
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            placeholder="Find file..."
            className="pl-7 pr-2 py-1.5 w-44 text-[11px] font-mono bg-[var(--graph-surface)] border border-slate-800 text-slate-300 placeholder:text-slate-600 focus:border-amber-500/50 focus:outline-none transition-colors"
          />
        </div>

        <div className="flex-1" />

        {archData && archData.files.length > 0 && (
          <div className="flex items-center gap-4 flex-wrap">
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
            <div className="flex items-center gap-3 text-[11px] font-mono text-slate-500 tabular-nums">
              <span>{archData.files.length} files</span>
              <span className="text-slate-700">·</span>
              <span>{archData.edges.length} deps</span>
              <span className="text-slate-700">·</span>
              <span className={archData.summary.choke_points.length > 0 ? "text-amber-500" : ""}>
                {archData.summary.choke_points.length} chokepoints
              </span>
              <span className="text-slate-700">·</span>
              <span className={archData.summary.hotspots.length > 0 ? "text-amber-500" : ""}>
                {archData.summary.hotspots.length} hotspots
              </span>
            </div>
          </div>
        )}

        {/* Guide toggle */}
        {!showGuide && (
          <button
            onClick={() => setShowGuide(true)}
            className="p-1 text-slate-600 hover:text-slate-400 transition-colors shrink-0"
            title="Show guide"
          >
            <Info className="h-3.5 w-3.5" />
          </button>
        )}
      </div>

      {/* Onboarding guide */}
      {showGuide && archData && archData.files.length > 0 && (
        <div className="px-5 py-2 border-b border-slate-800/50 bg-[var(--graph-surface)]/50 flex items-start gap-3 shrink-0">
          <Info className="h-3 w-3 text-amber-500 mt-0.5 shrink-0" />
          <div className="text-[10px] font-mono text-slate-400 leading-relaxed space-y-0.5">
            <p>
              Node size = risk score. Border color = bug density (
              <span className="text-emerald-400">green</span> →{" "}
              <span className="text-red-400">red</span>). Badges:{" "}
              <span className="text-amber-400">choke</span> = high fan-in,{" "}
              <span className="text-red-400">hot</span> = high bug density.
            </p>
            <p>Click a node to inspect. Switch lenses above to highlight choke points, hotspots, or coupling.</p>
          </div>
          <button
            onClick={() => { setShowGuide(false); localStorage.setItem("argus-arch-guide-dismissed", "1"); }}
            className="text-slate-600 hover:text-slate-400 shrink-0 ml-auto"
          >
            <X className="h-3 w-3" />
          </button>
        </div>
      )}

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
              searchQuery={searchQuery}
              onSelectFile={setSelectedFilePath}
            />
          )}
        </div>

        {selectedFilePath && (
          <div className="border-t md:border-t-0 md:border-l border-slate-800/50 bg-[var(--graph-bg)] md:w-[320px] md:shrink-0 h-[50vh] md:h-auto overflow-hidden">
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
