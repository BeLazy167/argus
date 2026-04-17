export default function DashboardLoading() {
  return (
    <div className="flex-1 space-y-6 p-6">
      <div className="h-8 w-48 animate-pulse rounded bg-zinc-800" />
      <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
        {["a", "b", "c"].map((id) => (
          <div
            key={id}
            className="h-24 animate-pulse border border-zinc-800 bg-zinc-900"
          />
        ))}
      </div>
      <div className="h-64 animate-pulse border border-zinc-800 bg-zinc-900" />
    </div>
  );
}
