import { ChevronDown, RotateCw, Save } from "lucide-react";
import { useState } from "react";
import { useDeletePrompt, useUpsertPrompt } from "@/lib/queries/prompts";
import type { PromptTemplate } from "@/lib/types";
import { STAGE_LABELS } from "./constants";

export function PromptCard({
  stage,
  repoId,
  custom,
  defaultText,
}: {
  stage: string;
  repoId: number;
  custom?: PromptTemplate;
  defaultText: string;
}) {
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState(custom?.prompt_text ?? "");
  const upsert = useUpsertPrompt();
  const del = useDeletePrompt();

  const isCustom = custom?.is_custom ?? false;
  const displayText = isCustom ? custom!.prompt_text : defaultText;

  const handleSave = () => {
    if (!draft.trim()) return;
    upsert.mutate({ repoId, stage, prompt_text: draft });
  };

  const handleReset = () => {
    del.mutate({ repoId, stage }, { onSuccess: () => setDraft("") });
  };

  return (
    <div className="border border-iron bg-charcoal">
      <button
        type="button"
        onClick={() => {
          if (!open && !draft) setDraft(displayText);
          setOpen(!open);
        }}
        className="flex w-full items-center justify-between p-4 text-left cursor-pointer"
      >
        <div className="flex items-center gap-2">
          <span className="text-xs font-mono font-medium text-foreground">
            {STAGE_LABELS[stage]}
          </span>
          {isCustom && (
            <span className="inline-flex items-center rounded-sm border border-amber/30 bg-amber/10 px-1.5 py-0.5 text-[9px] font-mono uppercase tracking-wider text-amber">
              Custom
            </span>
          )}
        </div>
        <ChevronDown
          className={`h-3.5 w-3.5 text-slate-text transition-transform ${open ? "rotate-180" : ""}`}
        />
      </button>

      {open && (
        <div className="border-t border-iron px-4 pb-4 pt-3 space-y-3">
          <textarea
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            placeholder={defaultText}
            rows={10}
            className="w-full border border-iron bg-background px-3 py-2 text-xs font-mono text-foreground placeholder:text-iron/60 focus:border-amber focus:outline-none resize-y leading-relaxed"
          />
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={handleSave}
              disabled={upsert.isPending || !draft.trim()}
              className="flex items-center gap-2 rounded border border-amber/30 bg-amber/10 px-3 py-1 text-[11px] font-mono text-amber hover:bg-amber/20 transition-colors disabled:opacity-50 cursor-pointer disabled:cursor-not-allowed"
            >
              <Save className="h-3 w-3" />
              {upsert.isPending ? "Saving..." : "Save"}
            </button>
            {isCustom && (
              <button
                type="button"
                onClick={handleReset}
                disabled={del.isPending}
                className="flex items-center gap-2 bg-charcoal border border-iron px-3 py-1 text-[11px] font-mono text-slate-text hover:text-foreground hover:border-foreground/30 transition-colors disabled:opacity-50 cursor-pointer disabled:cursor-not-allowed"
              >
                <RotateCw className="h-3 w-3" />
                {del.isPending ? "Resetting..." : "Reset to default"}
              </button>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
