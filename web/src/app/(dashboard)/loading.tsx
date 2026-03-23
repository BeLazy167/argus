export default function DashboardLoading() {
  return (
    <div className="flex-1 space-y-6 p-6">
      <div className="h-8 w-48 animate-pulse rounded bg-zinc-800" />
      <div className="grid grid-cols-3 gap-4">
        {[...Array(3)].map((_, i) => (
          <div
            key={i}
            className="h-24 animate-pulse rounded-lg border border-zinc-800 bg-zinc-900"
          />
        ))}
      </div>
      <div className="h-64 animate-pulse rounded-lg border border-zinc-800 bg-zinc-900" />
    </div>
  );
}
