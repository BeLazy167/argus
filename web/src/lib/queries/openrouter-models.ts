import { useQuery } from "@tanstack/react-query";
import type { OpenRouterModel } from "../types";
import { useApi } from "@/lib/hooks/use-api";

export function useOpenRouterModels(installationId: number | undefined) {
  const api = useApi();
  return useQuery({
    queryKey: ["openrouter-models", installationId],
    queryFn: () =>
      api.get<OpenRouterModel[]>(
        `/api/v1/openrouter-models?installation_id=${installationId}`,
      ),
    enabled: !!installationId && !!api.active,
    staleTime: 5 * 60 * 1000, // 5 min — models don't change often
  });
}
