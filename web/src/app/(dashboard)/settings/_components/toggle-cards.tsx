import { Lock } from "lucide-react";

export type ToggleDef = {
  key: string;
  label: string;
  hint: string;
  description: string;
  defaultValue: boolean;
  requiresDeepReview?: boolean;
};

export function PipelineFeatureCard({
  toggle,
  enabled,
  onToggle,
  pending,
  disabled,
}: {
  toggle: ToggleDef;
  enabled: boolean;
  onToggle: () => void;
  pending: boolean;
  disabled?: boolean;
}) {
  const isDisabled = disabled || pending;
  return (
    <div
      className={`border p-4 transition-colors ${
        disabled
          ? "border-iron/50 bg-charcoal/50 opacity-60"
          : enabled
            ? "border-amber/30 bg-amber/5"
            : "border-iron bg-charcoal"
      }`}
    >
      <div className="flex items-center justify-between mb-1.5">
        <div className="flex flex-col gap-0.5">
          <span
            className={`text-xs font-mono font-medium ${disabled ? "text-slate-text/60" : enabled ? "text-amber" : "text-foreground"}`}
          >
            {toggle.label}
          </span>
          <span className="text-[11px] font-mono text-slate-text">{toggle.hint}</span>
        </div>
        <button
          type="button"
          onClick={onToggle}
          disabled={isDisabled}
          aria-label={enabled ? `Disable ${toggle.label}` : `Enable ${toggle.label}`}
          className={`relative inline-flex h-6 w-11 shrink-0 cursor-pointer rounded-full border-2 transition-colors duration-200 ease-in-out focus:outline-none focus-visible:ring-2 focus-visible:ring-amber/50 focus-visible:ring-offset-2 focus-visible:ring-offset-background disabled:cursor-not-allowed ${
            enabled && !disabled ? "border-amber bg-amber" : "border-iron bg-iron/50"
          }`}
        >
          <span
            className={`pointer-events-none inline-block h-5 w-5 rounded-full bg-foreground shadow-lg ring-0 transition-transform duration-200 ease-in-out ${
              enabled && !disabled ? "translate-x-5" : "translate-x-0"
            }`}
          />
        </button>
      </div>
      <p className="text-[11px] font-mono text-slate-text leading-relaxed">{toggle.description}</p>
      {disabled && "requiresDeepReview" in toggle && toggle.requiresDeepReview && (
        <p className="text-[9px] font-mono text-amber/60 mt-1.5 flex items-center gap-1">
          <Lock className="h-2.5 w-2.5" />
          Requires Deep Review to be enabled
        </p>
      )}
    </div>
  );
}

/**
 * Toggle card for feature-flag-backed verification workers (issue acceptance,
 * cross-PR compatibility). Mirrors PipelineFeatureCard's visual language so
 * users perceive them as part of the same control panel — but this one wires
 * to the feature_flags JSONB column instead of default_settings.
 */
export function VerificationToggle({
  label,
  hint,
  description,
  cost,
  enabled,
  pending,
  onToggle,
}: {
  label: string;
  hint: string;
  description: string;
  cost: string;
  enabled: boolean;
  pending: boolean;
  onToggle: () => void;
}) {
  return (
    <div
      className={`border p-4 transition-colors ${
        enabled ? "border-amber/30 bg-amber/5" : "border-iron bg-charcoal"
      }`}
    >
      <div className="flex items-center justify-between mb-1.5">
        <div className="flex flex-col gap-0.5">
          <span
            className={`text-xs font-mono font-medium ${enabled ? "text-amber" : "text-foreground"}`}
          >
            {label}
          </span>
          <span className="text-[11px] font-mono text-slate-text">{hint}</span>
        </div>
        <button
          type="button"
          onClick={onToggle}
          disabled={pending}
          aria-label={enabled ? `Disable ${label}` : `Enable ${label}`}
          className={`relative inline-flex h-6 w-11 shrink-0 cursor-pointer rounded-full border-2 transition-colors duration-200 ease-in-out focus:outline-none focus-visible:ring-2 focus-visible:ring-amber/50 focus-visible:ring-offset-2 focus-visible:ring-offset-background disabled:cursor-not-allowed ${
            enabled ? "border-amber bg-amber" : "border-iron bg-iron/50"
          }`}
        >
          <span
            className={`pointer-events-none inline-block h-5 w-5 rounded-full bg-foreground shadow-lg ring-0 transition-transform duration-200 ease-in-out ${
              enabled ? "translate-x-5" : "translate-x-0"
            }`}
          />
        </button>
      </div>
      <p className="text-[11px] font-mono text-slate-text leading-relaxed mb-2">{description}</p>
      <p className="text-[10px] font-mono text-slate-text/60 uppercase tracking-wide">{cost}</p>
    </div>
  );
}
