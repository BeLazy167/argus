import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useAuth } from "@clerk/nextjs";
import { api } from "../api";
import type { ProviderKey } from "../types";
import { useInstallation } from "@/providers/installation-provider";

export function useProviderKeys() {
  const { getToken } = useAuth();
  const { active } = useInstallation();
  return useQuery({
    queryKey: ["provider-keys", active?.id],
    queryFn: async () => {
      const token = await getToken();
      return api.get<ProviderKey[]>(
        `/api/v1/installations/${active!.id}/provider-keys`,
        token ?? undefined,
        active!.id,
      );
    },
    enabled: !!active,
  });
}

export function useUpsertProviderKey() {
  const { getToken } = useAuth();
  const { active } = useInstallation();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (body: {
      provider: string;
      api_key: string;
      base_url?: string;
      repo_id?: number;
    }) => {
      const token = await getToken();
      return api.put<ProviderKey>(
        `/api/v1/installations/${active!.id}/provider-keys`,
        body,
        token ?? undefined,
        active!.id,
      );
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["provider-keys", active?.id] });
    },
  });
}

export function useDeleteProviderKey() {
  const { getToken } = useAuth();
  const { active } = useInstallation();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (keyId: number) => {
      const token = await getToken();
      return api.delete(
        `/api/v1/installations/${active!.id}/provider-keys/${keyId}`,
        token ?? undefined,
        active!.id,
      );
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["provider-keys", active?.id] });
    },
  });
}
