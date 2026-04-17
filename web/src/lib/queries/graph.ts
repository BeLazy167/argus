import type { GraphNode, GraphEdge } from "../types";
import { useActiveRepo } from "@/lib/hooks/use-active-repo";
import { createAuthQuery, getApi } from "@/lib/query-kit";

type GraphPayload = { nodes: GraphNode[]; edges: GraphEdge[] };

const useGraphQuery = createAuthQuery<GraphPayload, { repoId: number }>({
  queryKey: ["graph"],
  fetcher: ({ repoId }, ctx) => getApi(ctx).get<GraphPayload>(`/api/v1/repos/${repoId}/graph`),
  staleTime: 2 * 60 * 1000,
});

export const useGraphData = () => {
  const { activeId } = useActiveRepo();
  return useGraphQuery({
    variables: { repoId: activeId ?? 0 },
    enabled: !!activeId,
  });
};

type FileMemoryPayload = {
  file_path: string;
  risk_score: { trace_count: number; last_trace: string };
  patterns: { content: string; source: string }[];
  recent_comments: { severity: string; category: string; body: string; created_at: string }[];
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
