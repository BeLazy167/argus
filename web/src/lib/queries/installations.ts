import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useAuth } from "@clerk/nextjs";
import { api } from "../api";

export function useLinkInstallation() {
  const { getToken } = useAuth();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (params: { installationId: number; clerkOrgId?: string }) => {
      const token = await getToken();
      return api.post(
        "/api/v1/installations/link",
        { installation_id: params.installationId, clerk_org_id: params.clerkOrgId },
        token ?? undefined,
      );
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ["my-installations"] }),
    onError: (err: Error) => {
      console.error("[link-installation] failed:", err.message);
    },
  });
}
