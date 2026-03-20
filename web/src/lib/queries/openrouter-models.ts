import { useQuery } from "@tanstack/react-query";
import { useAuth } from "@clerk/nextjs";
import { api } from "../api";
import type { OpenRouterModel } from "../types";
import { useInstallation } from "@/providers/installation-provider";

export function useOpenRouterModels(installationId: number | undefined) {
  const { getToken } = useAuth();
  const { active } = useInstallation();
  return useQuery({
    queryKey: ["openrouter-models", installationId],
    queryFn: async () => {
      const token = await getToken();
      return api.get<OpenRouterModel[]>(
        `/api/v1/openrouter-models?installation_id=${installationId}`,
        token ?? undefined,
        active?.id,
      );
    },
    enabled: !!installationId && !!active,
    staleTime: 5 * 60 * 1000, // 5 min — models don't change often
  });
}
