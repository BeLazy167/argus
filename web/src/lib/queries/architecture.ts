import { useActiveRepo } from "@/lib/hooks/use-active-repo";
import { createAuthQuery, getApi } from "@/lib/query-kit";

export type Language =
  | "go"
  | "typescript"
  | "javascript"
  | "python"
  | "java"
  | "rust"
  | "csharp"
  | "ruby"
  | "kotlin"
  | "swift"
  | "c"
  | "cpp"
  | "php"
  | "scala"
  | "dart";

export type ArchCoupling = { path: string; score: number };

export type ArchPercentiles = {
  fan_in: number;
  bug_density: number;
  change_frequency: number;
  coupling: number;
};

export type ArchFile = {
  path: string;
  language: Language;
  symbols: string[];
  fan_in: number;
  fan_out: number;
  bug_density: number;
  change_frequency: number;
  coupling: ArchCoupling[];
  risk_score: number;
  percentiles: ArchPercentiles;
  insight?: string;
};

export type ArchEdge = {
  source: string;
  target: string;
  kinds: string[];
  weight: number;
};

export type ArchSummary = {
  total_files: number;
  choke_points: string[];
  hotspots: string[];
  most_coupled: { file_a: string; file_b: string; score: number }[];
};

export type GraphSnapshot = {
  generation_id: number;
  commit_sha: string;
  status: string;
  tree_truncated: boolean;
  expected_files: number;
  visited_files: number;
  failed_files: number;
  skipped_files: number;
  complete: boolean;
  current: boolean;
  started_at: string;
  published_at?: string;
  topology_published_at?: string;
  published_commit_sha: string;
  default_head_sha: string;
  refresh_requested_at?: string;
};

export type ArchResponse = {
  files: ArchFile[];
  edges: ArchEdge[];
  summary: ArchSummary;
  coupling_available: boolean;
  snapshot: GraphSnapshot;
};

const useArchitectureQuery = createAuthQuery<ArchResponse, { repoId: number }>({
  queryKey: ["architecture"],
  fetcher: ({ repoId }, ctx) => getApi(ctx).get<ArchResponse>(`/api/v1/repos/${repoId}/architecture`),
  staleTime: 5 * 60 * 1000,
  refetchInterval: (query) => {
    const snapshot = query.state.data?.snapshot;
    return snapshot?.status === "building" || snapshot?.refresh_requested_at ? 5_000 : false;
  },
});

export const useArchitectureData = () => {
  const { activeId } = useActiveRepo();
  return useArchitectureQuery({
    variables: { repoId: activeId ?? 0 },
    enabled: !!activeId,
  });
};
