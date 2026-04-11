import { ChevronDown } from "lucide-react";
import type { Repo } from "@/lib/types";

export function RepoSelect({
  repos,
  value,
  onChange,
  showAll = false,
  className = "",
}: {
  repos: Repo[];
  value: number;
  onChange: (id: number) => void;
  showAll?: boolean;
  className?: string;
}) {
  if (repos.length === 0) return null;
  return (
    <div className={`relative ${className}`}>
      <select
        value={value}
        onChange={(e) => onChange(Number(e.target.value))}
        className="w-full appearance-none border border-iron bg-charcoal px-3 py-2 pr-8 text-xs font-mono text-foreground focus:border-amber focus:outline-none truncate"
      >
        {showAll && <option value={0}>All repos</option>}
        {repos.map((r) => (
          <option key={r.id} value={r.id}>
            {r.full_name.split("/").pop()}
          </option>
        ))}
      </select>
      <ChevronDown className="pointer-events-none absolute right-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-slate-text" />
    </div>
  );
}
