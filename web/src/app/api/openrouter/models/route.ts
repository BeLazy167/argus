import { NextResponse } from "next/server";

/**
 * Live model list from OpenRouter, trimmed to what the BYOK estimator needs.
 * Cached for an hour — the catalog changes slowly and this keeps the estimator
 * fast without hammering OpenRouter. Server-side so there is no CORS or key
 * exposure. Prices are USD per token (OpenRouter's native unit).
 */
export const revalidate = 3600;

type OpenRouterModel = {
  id: string;
  name: string;
  pricing?: { prompt?: string; completion?: string };
  context_length?: number;
};

export async function GET() {
  const res = await fetch("https://openrouter.ai/api/v1/models", {
    next: { revalidate: 3600 },
    headers: { accept: "application/json" },
  });
  if (!res.ok) {
    return NextResponse.json({ models: [] }, { status: 502 });
  }
  const json = (await res.json()) as { data?: OpenRouterModel[] };
  const models = (json.data ?? []).flatMap((m) => {
    const p = m.pricing;
    // Skip models without straightforward per-token pricing (free/variable).
    if (!p?.prompt || !p?.completion) return [];
    const prompt = Number(p.prompt);
    const completion = Number(p.completion);
    if (!Number.isFinite(prompt) || !Number.isFinite(completion)) return [];
    if (prompt <= 0 && completion <= 0) return [];
    return [
      { id: m.id, name: m.name, prompt, completion, context: m.context_length ?? null },
    ];
  });
  models.sort((a, b) => a.name.localeCompare(b.name));
  return NextResponse.json({ models });
}
