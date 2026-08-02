export type BadgeVariant = "active" | "inactive";

const BADGE_STYLES: Record<BadgeVariant, string> = {
	active: "border-green-400/20 bg-green-400/10 text-green-400",
	inactive: "border-iron bg-iron/30 text-slate-text/60",
};

/** Providers-page config badge, shared by the LLM key cards and the
 * embeddings card. (Distinct from the dashboard review StatusBadge.) */
export function StatusBadge({ variant, label }: { variant: BadgeVariant; label: string }) {
	return (
		<span
			className={`inline-flex items-center rounded-sm border px-1.5 py-0.5 text-[9px] font-mono uppercase tracking-wider whitespace-nowrap ${BADGE_STYLES[variant]}`}
		>
			{label}
		</span>
	);
}
