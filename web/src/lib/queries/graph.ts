import { useQuery } from "@tanstack/react-query";
import type { GraphNode, GraphEdge } from "../types";
import { useApi } from "@/lib/hooks/use-api";
import { useActiveRepo } from "@/lib/hooks/use-active-repo";

export function useGraphData() {
  const api = useApi();
  const { activeId } = useActiveRepo();
  return useQuery({
    queryKey: ["graph", api.active?.id, activeId],
    queryFn: () =>
      api.get<{ nodes: GraphNode[]; edges: GraphEdge[] }>(
        `/api/v1/repos/${activeId}/graph`
      ),
    enabled: !!activeId && !!api.active,
  });
}

export function useFileMemory(repoId: number | undefined, filePath: string | null) {
  const api = useApi();
  return useQuery({
    queryKey: ["file-memory", api.active?.id, repoId, filePath],
    queryFn: () =>
      api.get<{
        file_path: string;
        risk_score: { trace_count: number; last_trace: string };
        patterns: { content: string; source: string }[];
        recent_comments: { severity: string; category: string; body: string; created_at: string }[];
        traces: { trace_type: string; content: string; pr_number: number; created_at: string }[];
      }>(`/api/v1/repos/${repoId}/files/${filePath}`),
    enabled: !!repoId && !!filePath && !!api.active,
  });
}
