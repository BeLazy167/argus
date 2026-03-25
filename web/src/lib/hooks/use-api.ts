import { useAuth } from "@clerk/nextjs";
import { useInstallation } from "@/providers/installation-provider";
import { api } from "@/lib/api";

/** Pre-bound API methods with auth token + active installation. */
export function useApi() {
  const { getToken } = useAuth();
  const { active } = useInstallation();

  const authGet = async <T>(path: string, installationId?: number): Promise<T> => {
    const token = await getToken();
    return api.get<T>(path, token ?? undefined, installationId ?? active?.id);
  };

  const authPost = async <T>(path: string, body?: unknown, installationId?: number): Promise<T> => {
    const token = await getToken();
    return api.post<T>(path, body, token ?? undefined, installationId ?? active?.id);
  };

  const authPut = async <T>(path: string, body?: unknown, installationId?: number): Promise<T> => {
    const token = await getToken();
    return api.put<T>(path, body, token ?? undefined, installationId ?? active?.id);
  };

  const authPatch = async <T>(path: string, body?: unknown, installationId?: number): Promise<T> => {
    const token = await getToken();
    return api.patch<T>(path, body, token ?? undefined, installationId ?? active?.id);
  };

  const authDelete = async <T>(path: string): Promise<T> => {
    const token = await getToken();
    return api.delete<T>(path, token ?? undefined, active?.id);
  };

  return { get: authGet, post: authPost, put: authPut, patch: authPatch, delete: authDelete, active };
}
