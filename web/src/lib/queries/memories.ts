import { createAuthQuery, getApi } from "@/lib/query-kit";

export type MemoryScope = "all" | "shared" | "repo";

export type MemoryRecord = {
  id: number;
  installation_id: number;
  container_tag: string;
  custom_id: string;
  type: string;
  content: string;
  metadata: unknown;
  review_id: string | null;
  created_at: string;
  updated_at: string;
};

export type MemoriesResponse = {
  memories: MemoryRecord[];
  total: number;
  limit: number;
  offset: number;
};

export type MemoriesVariables = {
  installationId: number;
  repoId?: number;
  scope: MemoryScope;
  type: string;
  query: string;
  limit: number;
  offset: number;
};

export function buildMemoriesPath(variables: MemoriesVariables): string {
  const params = new URLSearchParams();
  params.set("installation_id", String(variables.installationId));
  if (variables.repoId && variables.repoId > 0) params.set("repo_id", String(variables.repoId));
  params.set("scope", variables.scope);
  if (variables.type) params.set("type", variables.type);
  const query = variables.query.trim();
  if (query) params.set("q", query);
  params.set("limit", String(variables.limit));
  params.set("offset", String(variables.offset));
  return `/api/v1/memories?${params.toString()}`;
}

export const useMemories = createAuthQuery<MemoriesResponse, MemoriesVariables>({
  queryKey: ["memories"],
  fetcher: (variables, ctx) =>
    getApi(ctx).get<MemoriesResponse>(buildMemoriesPath(variables)),
  staleTime: 30_000,
});
