"use client";

import { useCallback } from "react";
import { useSearchParams, useRouter } from "next/navigation";
import { ChevronLeft, ChevronRight } from "lucide-react";

const PAGE_SIZE = 15;

/**
 * Returns a function that batch-updates multiple URL search params in one navigation.
 * Empty-string values are deleted from the URL for cleaner URLs.
 */
export function useUpdateSearchParams() {
  const searchParams = useSearchParams();
  const router = useRouter();

  return useCallback(
    (updates: Record<string, string>) => {
      const params = new URLSearchParams(searchParams.toString());
      for (const [k, v] of Object.entries(updates)) {
        if (v === "") params.delete(k);
        else params.set(k, v);
      }
      const qs = params.toString();
      router.replace(qs ? `?${qs}` : "?", { scroll: false });
    },
    [searchParams, router],
  );
}

/**
 * Read/write a single URL search-param as state.
 * Uses router.replace (no history pollution) and omits default values for clean URLs.
 */
export function useSearchParamState(key: string, defaultValue = "") {
  const searchParams = useSearchParams();
  const update = useUpdateSearchParams();
  const value = searchParams.get(key) ?? defaultValue;

  const setValue = useCallback(
    (next: string) => {
      update({ [key]: next === defaultValue ? "" : next });
    },
    [update, key, defaultValue],
  );

  return [value, setValue] as const;
}

/** Pagination over an array with page state backed by URL search param. */
export function usePagination<T>(items: T[], pageSize = PAGE_SIZE, paramKey = "page") {
  const searchParams = useSearchParams();
  const update = useUpdateSearchParams();
  const pageParam = searchParams.get(paramKey) ?? "1";
  const urlPage = Math.max(0, (Number(pageParam) || 1) - 1); // 0-indexed internally

  const totalPages = Math.max(1, Math.ceil(items.length / pageSize));
  const safeP = Math.min(urlPage, totalPages - 1);
  const paginated = items.slice(safeP * pageSize, (safeP + 1) * pageSize);

  const setPage = useCallback(
    (p: number) => {
      // Store as 1-indexed in URL; omit when page=1
      update({ [paramKey]: p <= 0 ? "" : String(p + 1) });
    },
    [update, paramKey],
  );

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
          className="border border-border p-2.5 text-muted-foreground hover:text-foreground disabled:opacity-30 disabled:cursor-not-allowed transition-colors"
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
          className="border border-border p-2.5 text-muted-foreground hover:text-foreground disabled:opacity-30 disabled:cursor-not-allowed transition-colors"
        >
          <ChevronRight className="h-3.5 w-3.5" />
        </button>
      </div>
    </div>
  );
}
