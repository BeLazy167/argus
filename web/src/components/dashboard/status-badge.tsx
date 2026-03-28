import { Loader2 } from "lucide-react";
import type { Review } from "@/lib/types";

const styles: Record<Review["status"], string> = {
  completed: "bg-green-400/10 text-green-400 border-green-400/30",
  in_progress: "bg-amber/10 text-amber border-amber/30",
  pending: "bg-blue-400/10 text-blue-400 border-blue-400/30",
  failed: "bg-red-400/10 text-red-400 border-red-400/30",
};

const labels: Record<Review["status"], string> = {
  completed: "COMPLETED",
  in_progress: "IN PROGRESS",
  pending: "PENDING",
  failed: "FAILED",
};

export function StatusBadge({ status }: { status: Review["status"] }) {
  return (
    <span
      className={`inline-flex items-center gap-1 rounded-sm border px-2 py-0.5 text-[10px] font-mono uppercase tracking-wider ${styles[status]}`}
    >
      {status === "in_progress" && (
        <Loader2 className="h-2.5 w-2.5 animate-spin" />
      )}
      {labels[status]}
    </span>
  );
}
