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

export type ArchResponse = {
  files: ArchFile[];
  edges: ArchEdge[];
  summary: ArchSummary;
};

const useArchitectureQuery = createAuthQuery<ArchResponse, { repoId: number }>({
  queryKey: ["architecture"],
  fetcher: ({ repoId }, ctx) => getApi(ctx).get<ArchResponse>(`/api/v1/repos/${repoId}/architecture`),
  staleTime: 5 * 60 * 1000,
});

export const useArchitectureData = () => {
  const { activeId } = useActiveRepo();
  return useArchitectureQuery({
    variables: { repoId: activeId ?? 0 },
    enabled: !!activeId,
  });
};
