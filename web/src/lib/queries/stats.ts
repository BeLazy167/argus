import { useQuery } from "@tanstack/react-query";
import { useAuth } from "@clerk/nextjs";
import { api } from "../api";
import type { Stats, ActivityLog } from "../types";

export function useStats() {
  const { getToken } = useAuth();
  return useQuery({
    queryKey: ["stats"],
    queryFn: async () => {
      const token = await getToken();
      return api.get<Stats>("/api/v1/stats", token ?? undefined);
    },
  });
}

export function useActivity(limit = 50) {
  const { getToken } = useAuth();
  return useQuery({
    queryKey: ["activity", limit],
    queryFn: async () => {
      const token = await getToken();
      return api.get<ActivityLog[]>(
        `/api/v1/activity?limit=${limit}`,
        token ?? undefined,
      );
    },
  });
}
