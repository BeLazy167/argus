type BadgeVariant = "active" | "configured" | "inactive";

const BADGE_STYLES: Record<BadgeVariant, string> = {
  active: "border-green-400/20 bg-green-400/10 text-green-400",
  configured: "border-green-400/20 bg-green-400/10 text-green-400",
  inactive: "border-iron bg-iron/30 text-slate-text/60",
};

/**
 * Small pill badge for integration/config state (active vs. not configured).
 *
 * Shared by the Settings and Providers pages, which previously each declared an
 * identical copy. `className` lets a call site append layout-only utilities
 * (e.g. `whitespace-nowrap`) without forking the component.
 */
export function IntegrationStatusBadge({
  variant,
  label,
  className = "",
}: {
  variant: BadgeVariant;
  label: string;
  className?: string;
}) {
  return (
    <span
      className={`inline-flex items-center rounded-sm border px-1.5 py-0.5 text-[9px] font-mono uppercase tracking-wider ${className} ${BADGE_STYLES[variant]}`}
    >
      {label}
    </span>
  );
}
