import { useQuery } from "@tanstack/react-query";
import { useAuth } from "@clerk/nextjs";
import { api } from "../api";
import type { Stats, ActivityLog } from "../types";
import { useInstallation } from "@/providers/installation-provider";

export function useStats() {
  const { getToken } = useAuth();
  const { active } = useInstallation();
  return useQuery({
    queryKey: ["stats", active?.id],
    queryFn: async () => {
      const token = await getToken();
      return api.get<Stats>("/api/v1/stats", token ?? undefined, active?.id);
    },
    enabled: !!active,
  });
}

export function useActivity(limit = 50) {
  const { getToken } = useAuth();
  const { active } = useInstallation();
  return useQuery({
    queryKey: ["activity", limit, active?.id],
    queryFn: async () => {
      const token = await getToken();
      return api.get<ActivityLog[]>(
        `/api/v1/activity?limit=${limit}`,
        token ?? undefined,
        active?.id,
      );
    },
    enabled: !!active,
  });
}
