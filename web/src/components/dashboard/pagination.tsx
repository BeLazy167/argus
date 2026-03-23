"use client";

import { useState } from "react";
import { ChevronLeft, ChevronRight } from "lucide-react";

const PAGE_SIZE = 15;

export function usePagination<T>(items: T[], pageSize = PAGE_SIZE) {
  const [page, setPage] = useState(0);
  const totalPages = Math.max(1, Math.ceil(items.length / pageSize));
  const safeP = Math.min(page, totalPages - 1);
  const paginated = items.slice(safeP * pageSize, (safeP + 1) * pageSize);
  return {
    page: safeP,
    setPage,
    totalPages,
    paginated,
    pageSize,
    total: items.length,
    hasNext: safeP < totalPages - 1,
    hasPrev: safeP > 0,
  };
}

export function PaginationBar({
  page,
  totalPages,
  total,
  pageSize,
  hasNext,
  hasPrev,
  onNext,
  onPrev,
}: {
  page: number;
  totalPages: number;
  total: number;
  pageSize: number;
  hasNext: boolean;
  hasPrev: boolean;
  onNext: () => void;
  onPrev: () => void;
}) {
  if (totalPages <= 1) return null;
  const start = page * pageSize + 1;
  const end = Math.min((page + 1) * pageSize, total);

  return (
    <div className="flex items-center justify-between border-t border-border px-5 py-3">
      <span className="text-[10px] font-mono text-muted-foreground">
        {start}–{end} of {total}
      </span>
      <div className="flex items-center gap-1.5">
        <button
          onClick={onPrev}
          disabled={!hasPrev}
          aria-label="Previous page"
          className="rounded border border-border p-1 text-muted-foreground hover:text-foreground disabled:opacity-30 disabled:cursor-not-allowed transition-colors"
        >
          <ChevronLeft className="h-3.5 w-3.5" />
        </button>
        <span className="text-[10px] font-mono text-muted-foreground px-2">
          {page + 1} / {totalPages}
        </span>
        <button
          onClick={onNext}
          disabled={!hasNext}
          aria-label="Next page"
          className="rounded border border-border p-1 text-muted-foreground hover:text-foreground disabled:opacity-30 disabled:cursor-not-allowed transition-colors"
        >
          <ChevronRight className="h-3.5 w-3.5" />
        </button>
      </div>
    </div>
  );
}
