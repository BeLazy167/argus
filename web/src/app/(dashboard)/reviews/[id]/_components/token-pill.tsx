import type { StageTokens, TokenUsage } from "@/lib/types";
import { STAGE_ORDER, stageLabel } from "@/lib/stage-labels";
import { formatTokens } from "./format";

export function TokenPill({ usage }: { usage: TokenUsage }) {
  const total = usage.total;
  const label = `${formatTokens(total)} tokens${total.cost != null ? ` · $${total.cost.toFixed(3)}` : ""}`;

  // Build the stage list dynamically from STAGE_ORDER. Array-valued stages
  // (review, file_synthesis, simulation) expand into per-specialist /
  // per-file / per-scenario rows via the entry's `specialist` or `file`
  // field. The detail-page scope is one review, so cardinality stays
  // bounded; rows visible = rows useful.
  //
  // STAGE_ORDER lists every renderable key of TokenUsage; `total` and `mu`
  // are intentionally excluded. The `keyof` cast is narrow and safe — a
  // new stage added to TokenUsage but missing from STAGE_ORDER simply won't
  // render, same as before.
  // Include every stage that consumed tokens OR cost. Gating on tokens alone
  // would hide the gpt-5.x reasoning case where the provider returns cost but
  // elides token counts (see commit 1070dac) — the pill's header total would
  // still include that spend, producing a visible delta between the header
  // "$X" and the sum of tooltip rows.
  const hasWork = (s: StageTokens) => s.total_tokens > 0 || (s.cost ?? 0) > 0;
  const rows: { key: string; label: string; tokens: StageTokens }[] = [];
  for (const stage of STAGE_ORDER) {
    const v = usage[stage as keyof TokenUsage];
    if (Array.isArray(v)) {
      v.forEach((st, i) => {
        if (!hasWork(st)) return;
        const sub = st.specialist ?? st.file ?? "";
        // React-key uniqueness: multiple array entries may share the same
        // sub (skim reviews set Specialist="" on every file; simulation
        // entries never populate File). Suffix with the array index so
        // every row has a stable, collision-free key. The displayed label
        // still uses stageLabel(composite) — the index is key-only.
        const composite = sub ? `${stage}.${sub}` : stage;
        rows.push({ key: `${composite}#${i}`, label: stageLabel(composite), tokens: st });
      });
    } else if (v && hasWork(v)) {
      rows.push({ key: stage, label: stageLabel(stage), tokens: v });
    }
  }

  // Group model name — show once if all rows use the same model.
  const models = rows.flatMap(r => (r.tokens.model ? [r.tokens.model] : []));
  const uniqueModels = [...new Set(models)];
  const singleModel = uniqueModels.length === 1 ? uniqueModels[0] : null;

  return (
    <div className="group relative">
      <span className="inline-flex items-center border border-iron bg-iron/30 px-2.5 py-1 text-[11px] font-mono text-slate-text cursor-default">
        {label}
      </span>
      {rows.length > 0 && (
        <div className="absolute left-0 top-full mt-1.5 z-10 hidden group-hover:block">
          <div className="border border-iron bg-charcoal p-3 shadow-xl w-[280px] max-h-[60vh] overflow-y-auto">
            {singleModel && (
              <p className="text-[10px] font-mono text-slate-text/60 mb-2 truncate">
                {singleModel}
              </p>
            )}
            <div className="space-y-1">
              {rows.map(r => (
                <div key={r.key}>
                  <div className="flex items-center justify-between gap-2">
                    <span className="text-[11px] font-mono text-ash truncate min-w-0" title={r.label}>
                      {r.label}
                    </span>
                    <span className="text-[11px] font-mono text-foreground tabular-nums shrink-0">
                      {formatTokens(r.tokens)}
                      {r.tokens.cost != null && r.tokens.cost > 0 && (
                        <span className="text-slate-text ml-1.5">${r.tokens.cost.toFixed(3)}</span>
                      )}
                    </span>
                  </div>
                  {!singleModel && r.tokens.model && (
                    <p className="text-[9px] font-mono text-slate-text/50 truncate">{r.tokens.model}</p>
                  )}
                </div>
              ))}
            </div>
            <div className="mt-2 pt-2 border-t border-iron flex items-center justify-between sticky bottom-0 bg-charcoal">
              <span className="text-[11px] font-mono text-amber">Total</span>
              <span className="text-[11px] font-mono text-foreground tabular-nums">
                {formatTokens(total)}
                {total.cost != null && (
                  <span className="text-amber ml-1.5">${total.cost.toFixed(3)}</span>
                )}
              </span>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
