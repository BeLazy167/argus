import { ChevronDown } from "lucide-react";
import type { Repo } from "@/lib/types";

export function RepoSelect({
  repos,
  value,
  onChange,
  className = "",
}: {
  repos: Repo[];
  value: number;
  onChange: (id: number) => void;
  className?: string;
}) {
  if (repos.length === 0) return null;
  return (
    <div className={`relative ${className}`}>
      <select
        value={value}
        onChange={(e) => onChange(Number(e.target.value))}
        className="appearance-none rounded-md border border-iron bg-charcoal px-4 py-2 pr-8 text-xs font-mono text-foreground focus:border-amber focus:outline-none"
      >
        {repos.map((r) => (
          <option key={r.id} value={r.id}>
            {r.full_name}
          </option>
        ))}
      </select>
      <ChevronDown className="pointer-events-none absolute right-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-slate-text" />
    </div>
  );
}
