import type { FindingState } from "../types";
import { createAuthQuery, getApi } from "@/lib/query-kit";

/** One recent review finding on a file, as GetFileMemory returns it. */
export type FileMemoryComment = {
  severity: string;
  category: string;
  body: string;
  created_at: string;
  state: FindingState;
  suppressed_reason?: string;
};

type FileMemoryPayload = {
  file_path: string;
  risk_score: { trace_count: number; last_trace: string };
  patterns: { content: string; source: string }[];
  /** GetFileMemory returns every lifecycle state, including 'suppressed' —
   *  findings the suppression pass dropped before they ever reached the PR.
   *  `state` and `suppressed_reason` are what let the sidebar tell those apart
   *  from findings a developer actually saw on their pull request. */
  recent_comments: FileMemoryComment[];
  traces: { trace_type: string; content: string; pr_number: number; created_at: string }[];
};

const useFileMemoryQuery = createAuthQuery<FileMemoryPayload, { repoId: number; filePath: string }>({
  queryKey: ["file-memory"],
  fetcher: ({ repoId, filePath }, ctx) =>
    getApi(ctx).get<FileMemoryPayload>(`/api/v1/repos/${repoId}/files/${filePath}`),
  staleTime: 2 * 60 * 1000,
});

export const useFileMemory = (repoId: number | undefined, filePath: string | null) =>
  useFileMemoryQuery({
    variables: { repoId: repoId ?? 0, filePath: filePath ?? "" },
    enabled: !!repoId && !!filePath,
  });
