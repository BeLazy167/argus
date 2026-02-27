import { Eye, GitPullRequest, AlertTriangle, CheckCircle } from "lucide-react";

function StatCard({
  label,
  value,
  icon: Icon,
  accent = false,
}: {
  label: string;
  value: string;
  icon: React.ComponentType<{ className?: string }>;
  accent?: boolean;
}) {
  return (
    <div
      className={`rounded-lg border p-5 ${
        accent
          ? "border-amber/30 bg-charcoal shadow-[0_0_16px_-6px_oklch(0.77_0.15_75/0.2)]"
          : "border-iron bg-charcoal"
      }`}
    >
      <div className="flex items-center justify-between mb-3">
        <span className="text-[11px] font-mono uppercase tracking-[0.1em] text-slate-text">
          {label}
        </span>
        <Icon
          className={`h-4 w-4 ${accent ? "text-amber" : "text-slate-text"}`}
        />
      </div>
      <p
        className={`text-2xl font-mono font-medium ${
          accent ? "text-amber" : "text-foreground"
        }`}
      >
        {value}
      </p>
    </div>
  );
}

function ReviewRow({
  repo,
  pr,
  score,
  status,
  time,
}: {
  repo: string;
  pr: string;
  score: number;
  status: "completed" | "reviewing" | "failed";
  time: string;
}) {
  const scoreColor =
    score >= 8
      ? "text-green-400"
      : score >= 5
        ? "text-amber"
        : "text-red-400";

  const statusBadge = {
    completed: "bg-green-400/10 text-green-400 border-green-400/20",
    reviewing: "bg-amber/10 text-amber border-amber/20",
    failed: "bg-red-400/10 text-red-400 border-red-400/20",
  }[status];

  return (
    <div className="flex items-center justify-between border-b border-iron/50 py-3 last:border-0">
      <div className="flex items-center gap-4">
        <span className={`font-mono text-lg font-medium ${scoreColor}`}>
          {score}
        </span>
        <div>
          <p className="text-xs font-mono text-foreground">{repo}</p>
          <p className="text-[11px] font-mono text-slate-text">{pr}</p>
        </div>
      </div>
      <div className="flex items-center gap-3">
        <span
          className={`inline-flex items-center rounded-sm border px-2 py-0.5 text-[10px] font-mono uppercase tracking-wider ${statusBadge}`}
        >
          {status}
        </span>
        <span className="text-[11px] font-mono text-iron">{time}</span>
      </div>
    </div>
  );
}

export default function DashboardPage() {
  return (
    <>
      <div className="mb-8">
        <h1 className="font-display text-2xl font-bold text-foreground">
          Mission Control
        </h1>
        <p className="text-xs font-mono text-slate-text mt-1">
          Nothing merges unseen.
        </p>
      </div>

      {/* Stats grid */}
      <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4 mb-10">
        <StatCard
          label="Reviews today"
          value="23"
          icon={Eye}
          accent
        />
        <StatCard label="Open PRs" value="7" icon={GitPullRequest} />
        <StatCard label="Critical issues" value="3" icon={AlertTriangle} />
        <StatCard label="Clean ships" value="18" icon={CheckCircle} />
      </div>

      {/* Recent reviews */}
      <div className="rounded-lg border border-iron bg-charcoal">
        <div className="flex items-center justify-between border-b border-iron px-5 py-4">
          <h2 className="text-xs font-mono uppercase tracking-[0.1em] text-foreground">
            Recent Reviews
          </h2>
          <span className="text-[10px] font-mono text-slate-text">
            Last 24h
          </span>
        </div>
        <div className="px-5">
          <ReviewRow
            repo="acmeorg/argus"
            pr="#142 — Fix auth middleware race condition"
            score={4}
            status="completed"
            time="2m ago"
          />
          <ReviewRow
            repo="acmeorg/argus"
            pr="#141 — Add webhook retry logic"
            score={8}
            status="completed"
            time="15m ago"
          />
          <ReviewRow
            repo="acme/billing"
            pr="#89 — Update Stripe integration"
            score={6}
            status="reviewing"
            time="22m ago"
          />
          <ReviewRow
            repo="acme/api"
            pr="#312 — Migrate to pgx v5"
            score={9}
            status="completed"
            time="1h ago"
          />
          <ReviewRow
            repo="acme/web"
            pr="#205 — Dark mode support"
            score={10}
            status="completed"
            time="3h ago"
          />
        </div>
      </div>
    </>
  );
}
