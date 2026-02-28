const API_URL = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8080";

type FetchOptions = RequestInit & { token?: string; installationId?: number };

async function apiFetch<T>(path: string, opts: FetchOptions = {}): Promise<T> {
  const { token, installationId, headers, ...rest } = opts;
  const res = await fetch(`${API_URL}${path}`, {
    headers: {
      "Content-Type": "application/json",
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
      ...(installationId ? { "X-Installation-ID": String(installationId) } : {}),
      ...headers,
    },
    ...rest,
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: res.statusText }));
    throw new Error(body.error || `API error: ${res.status}`);
  }
  return res.json();
}

export const api = {
  get: <T>(path: string, token?: string, installationId?: number) =>
    apiFetch<T>(path, { token, installationId }),

  post: <T>(path: string, body: unknown, token?: string, installationId?: number) =>
    apiFetch<T>(path, { method: "POST", body: JSON.stringify(body), token, installationId }),

  put: <T>(path: string, body: unknown, token?: string, installationId?: number) =>
    apiFetch<T>(path, { method: "PUT", body: JSON.stringify(body), token, installationId }),

  patch: <T>(path: string, body: unknown, token?: string, installationId?: number) =>
    apiFetch<T>(path, { method: "PATCH", body: JSON.stringify(body), token, installationId }),

  delete: <T>(path: string, token?: string, installationId?: number) =>
    apiFetch<T>(path, { method: "DELETE", token, installationId }),
};
