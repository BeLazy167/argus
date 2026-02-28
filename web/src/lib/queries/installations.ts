import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useAuth } from "@clerk/nextjs";
import { api } from "../api";
import type { Installation } from "../types";

export function useMyInstallations() {
  const { getToken } = useAuth();
  return useQuery({
    queryKey: ["my-installations"],
    queryFn: async () => {
      const token = await getToken();
      return api.get<Installation[]>("/api/v1/me/installations", token ?? undefined);
    },
  });
}

export function useLinkInstallation() {
  const { getToken } = useAuth();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (installationId: number) => {
      const token = await getToken();
      return api.post("/api/v1/installations/link", { installation_id: installationId }, token ?? undefined);
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ["my-installations"] }),
  });
}
