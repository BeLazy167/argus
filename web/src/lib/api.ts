const API_URL = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8080";

type FetchOptions = RequestInit & { token?: string };

async function apiFetch<T>(path: string, opts: FetchOptions = {}): Promise<T> {
  const { token, headers, ...rest } = opts;
  const res = await fetch(`${API_URL}${path}`, {
    headers: {
      "Content-Type": "application/json",
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
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
  get: <T>(path: string, token?: string) =>
    apiFetch<T>(path, { token }),

  post: <T>(path: string, body: unknown, token?: string) =>
    apiFetch<T>(path, { method: "POST", body: JSON.stringify(body), token }),

  put: <T>(path: string, body: unknown, token?: string) =>
    apiFetch<T>(path, { method: "PUT", body: JSON.stringify(body), token }),

  patch: <T>(path: string, body: unknown, token?: string) =>
    apiFetch<T>(path, { method: "PATCH", body: JSON.stringify(body), token }),

  delete: <T>(path: string, token?: string) =>
    apiFetch<T>(path, { method: "DELETE", token }),
};
