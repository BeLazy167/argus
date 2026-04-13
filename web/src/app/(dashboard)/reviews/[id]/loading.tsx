import { Loader2 } from "lucide-react";

export default function ReviewLoading() {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-4">
      <Loader2 className="h-6 w-6 animate-spin text-amber-500" />
      <p className="font-mono text-xs text-zinc-500">Loading review...</p>
    </div>
  );
}
