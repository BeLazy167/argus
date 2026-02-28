import type { Review } from "@/lib/types";

const styles: Record<Review["status"], string> = {
  completed: "bg-green-400/10 text-green-400 border-green-400/20",
  in_progress: "bg-amber/10 text-amber border-amber/20",
  pending: "bg-blue-400/10 text-blue-400 border-blue-400/20",
  failed: "bg-red-400/10 text-red-400 border-red-400/20",
};

export function StatusBadge({ status }: { status: Review["status"] }) {
  return (
    <span
      className={`inline-flex items-center rounded-sm border px-2 py-0.5 text-[10px] font-mono uppercase tracking-wider ${styles[status]}`}
    >
      {status.replace("_", " ")}
    </span>
  );
}
