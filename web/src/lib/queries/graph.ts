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
