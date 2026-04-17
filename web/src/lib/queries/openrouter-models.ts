import type { OpenRouterModel } from "../types";
import { createAuthQuery, getApi } from "@/lib/query-kit";

export const useOpenRouterModels = createAuthQuery<OpenRouterModel[], { installationId?: number }>({
  queryKey: ["openrouter-models"],
  fetcher: ({ installationId }, ctx) =>
    getApi(ctx).get<OpenRouterModel[]>(`/api/v1/openrouter-models?installation_id=${installationId}`),
  staleTime: 5 * 60 * 1000, // 5 min — models don't change often
});
