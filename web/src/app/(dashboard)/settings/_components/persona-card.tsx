export const PERSONAS = [
  { value: "default", label: "Default", description: "Balanced review across all categories" },
  {
    value: "security_auditor",
    label: "Security Auditor",
    description: "Prioritizes injection, auth, secrets, and input validation",
  },
  {
    value: "performance_engineer",
    label: "Performance Engineer",
    description: "Focuses on N+1 queries, allocations, caching, and complexity",
  },
  {
    value: "mentor",
    label: "Mentor",
    description: "Educational tone — explains why, suggests learning paths",
  },
  {
    value: "architect",
    label: "Architect",
    description: "Design patterns, coupling, API contracts, and module boundaries",
  },
  { value: "strict", label: "Strict", description: "Comments on everything — no issue too small" },
  {
    value: "custom",
    label: "Custom",
    description: "Write your own persona prompt — define exactly how Argus reviews",
  },
] as const;

export function PersonaCard({
  persona,
  isActive,
  onSelect,
  disabled,
}: {
  persona: (typeof PERSONAS)[number];
  isActive: boolean;
  onSelect: () => void;
  disabled: boolean;
}) {
  return (
    <button
      key={persona.value}
      type="button"
      onClick={onSelect}
      disabled={disabled}
      className={`group cursor-pointer border p-4 text-left transition-all ${
        isActive
          ? "border-amber/40 bg-amber/5"
          : "border-iron bg-charcoal hover:border-iron/80 hover:bg-charcoal/80"
      }`}
    >
      <div className="flex items-center justify-between mb-1.5">
        <span
          className={`text-xs font-mono font-medium ${isActive ? "text-amber" : "text-foreground"}`}
        >
          {persona.label}
        </span>
        {isActive && (
          <span className="inline-flex items-center rounded-sm border border-amber/30 bg-amber/10 px-1.5 py-0.5 text-[9px] font-mono uppercase tracking-wider text-amber">
            active
          </span>
        )}
      </div>
      <p className="text-[11px] font-mono text-slate-text leading-relaxed">{persona.description}</p>
    </button>
  );
}
