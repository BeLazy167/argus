import { ChevronDown, Search } from "lucide-react";
import type { OpenRouterModel } from "@/lib/types";

/**
 * Model picker subsection of {@link ConfigCard}.
 *
 * Renders one of two mutually-exclusive inputs, preserving the original
 * ConfigCard logic verbatim:
 *  - For OpenRouter with a fetched model list, a searchable dropdown filtered
 *    against the live catalog (id/name substring), plus a "Custom model…" escape.
 *  - Otherwise, a free-text combo input whose focus reveals the provider's
 *    static quick-pick suggestions.
 *
 * All state is lifted to the parent; this component is purely presentational
 * over the handful of setters it receives.
 */
export function ModelSelect({
  provider,
  model,
  customModel,
  isCustom,
  modelSearch,
  orModels,
  picks,
  setModel,
  setCustomModel,
  setModelSearch,
  onModelSelect,
}: {
  provider: string;
  model: string;
  customModel: string;
  isCustom: boolean;
  modelSearch: string;
  orModels: OpenRouterModel[] | undefined;
  picks: string[];
  setModel: (v: string) => void;
  setCustomModel: (v: string) => void;
  setModelSearch: (v: string) => void;
  onModelSelect: (v: string) => void;
}) {
  const isOpenRouter = provider === "openrouter";

  if (isOpenRouter && orModels && orModels.length > 0 && !isCustom) {
    return (
      <div className="relative">
        <div className="relative">
          <Search className="pointer-events-none absolute left-2 top-1/2 h-3 w-3 -translate-y-1/2 text-slate-text" />
          <input
            type="text"
            value={modelSearch || model}
            onChange={(e) => {
              setModelSearch(e.target.value);
              setModel("");
            }}
            onFocus={() => {
              if (model) {
                setModelSearch(model);
                setModel("");
              }
            }}
            placeholder="Search models…"
            autoComplete="off"
            className="w-full border border-iron bg-background pl-7 pr-8 py-1.5 text-xs font-mono text-foreground placeholder:text-iron focus:border-amber focus:outline-none"
          />
          <ChevronDown className="pointer-events-none absolute right-2 top-1/2 h-3 w-3 -translate-y-1/2 text-slate-text" />
        </div>
        {modelSearch && !model && (
          <div className="absolute z-20 mt-1 w-full max-h-48 overflow-y-auto border border-iron bg-charcoal shadow-lg">
            {orModels
              .filter(
                (m) =>
                  m.id.toLowerCase().includes(modelSearch.toLowerCase()) ||
                  m.name.toLowerCase().includes(modelSearch.toLowerCase()),
              )
              .slice(0, 20)
              .map((m) => (
                <button
                  key={m.id}
                  type="button"
                  onClick={() => {
                    setModel(m.id);
                    setModelSearch("");
                  }}
                  className="w-full text-left px-3 py-1.5 text-xs font-mono hover:bg-amber/10 transition-colors"
                >
                  <span className="text-foreground">{m.id}</span>
                  <span className="text-slate-text ml-2">
                    {(m.context_length / 1000).toFixed(0)}k ctx
                  </span>
                </button>
              ))}
            <button
              type="button"
              onClick={() => onModelSelect("__custom__")}
              className="w-full text-left px-3 py-1.5 text-xs font-mono text-amber hover:bg-amber/10 transition-colors border-t border-iron"
            >
              Custom model...
            </button>
          </div>
        )}
      </div>
    );
  }

  return (
    <div className="relative">
      <input
        type="text"
        value={isCustom ? customModel : model}
        onChange={(e) => {
          const v = e.target.value;
          if (isCustom) {
            setCustomModel(v);
          } else {
            setModel(v);
          }
        }}
        onFocus={() => setModelSearch("__show__")}
        onBlur={() => setTimeout(() => setModelSearch(""), 150)}
        placeholder="Type or select a model…"
        autoComplete="off"
        className="w-full border border-iron bg-background px-2 py-1.5 pr-7 text-xs font-mono text-foreground placeholder:text-iron focus:border-amber focus:outline-none"
      />
      <ChevronDown className="pointer-events-none absolute right-2 top-1/2 h-3 w-3 -translate-y-1/2 text-slate-text" />
      {modelSearch === "__show__" && picks.length > 0 && (
        <div className="absolute z-20 mt-1 w-full max-h-48 overflow-y-auto border border-iron bg-charcoal shadow-lg">
          {picks.map((m) => (
            <button
              key={m}
              type="button"
              onMouseDown={(e) => {
                e.preventDefault();
                setModel(m);
                setModelSearch("");
              }}
              className="w-full text-left px-3 py-1.5 text-xs font-mono hover:bg-amber/10 transition-colors text-foreground"
            >
              {m}
            </button>
          ))}
          <div className="px-3 py-1 text-[9px] font-mono text-slate-text/50 border-t border-iron">
            Or type any model name above
          </div>
        </div>
      )}
    </div>
  );
}
