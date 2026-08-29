import { NextResponse } from "next/server";

/**
 * Per-provider pricing for a single OpenRouter model. OpenRouter aggregates the
 * same model across many providers (e.g. an open model served by DeepInfra,
 * Nebius, Together…), each with its own price — this returns them sorted
 * cheapest-first. Cached an hour; server-side.
 */
export const revalidate = 3600;

type OpenRouterEndpoint = {
  provider_name?: string;
  pricing?: { prompt?: string; completion?: string };
  context_length?: number;
  quantization?: string | null;
};

export async function GET(req: Request) {
  const model = new URL(req.url).searchParams.get("model");
  // Validate the proxied model id (this is an unauthenticated proxy): bounded
  // length + a conservative charset covering OpenRouter ids (author/slug[:tag]).
  if (
    !model ||
    model.length > 128 ||
    model.includes("..") ||
    !/^[\w./:-]+$/.test(model)
  ) {
    return NextResponse.json({ providers: [] }, { status: 400 });
  }
  // Bound enumeration of this unauthenticated proxy: only serve ids present in
  // the (cached) catalog. The membership check below fails closed when the
  // catalog can't be fetched.
  const catalog = (await fetch("https://openrouter.ai/api/v1/models", {
    next: { revalidate: 3600 },
    headers: { accept: "application/json" },
  })
    .then((r) => (r.ok ? r.json() : { data: [] }))
    .catch(() => ({ data: [] }))) as { data?: { id: string }[] };
  const known = new Set((catalog.data ?? []).map((m) => m.id));
  // Fail closed: if the catalog is unavailable, reject rather than proxy an
  // arbitrary id — the client keeps a working estimate via its fallback models,
  // so nothing is gained by proxying unknown ids and it would leave the
  // enumeration bound open exactly during an OpenRouter outage.
  if (!known.has(model)) {
    return NextResponse.json({ providers: [] }, { status: 404 });
  }
  const res = await fetch(
    `https://openrouter.ai/api/v1/models/${model}/endpoints`,
    { next: { revalidate: 3600 }, headers: { accept: "application/json" } },
  );
  if (!res.ok) {
    return NextResponse.json({ providers: [] }, { status: 502 });
  }
  const json = (await res.json()) as {
    data?: { endpoints?: OpenRouterEndpoint[] };
  };
  const providers = (json.data?.endpoints ?? []).flatMap((e) => {
    const p = e.pricing;
    if (!e.provider_name || !p?.prompt || !p?.completion) return [];
    const prompt = Number(p.prompt);
    const completion = Number(p.completion);
    if (!Number.isFinite(prompt) || !Number.isFinite(completion)) return [];
    if (prompt <= 0 && completion <= 0) return [];
    return [
      {
        provider: e.provider_name,
        prompt,
        completion,
        context: e.context_length ?? null,
        quantization: e.quantization ?? null,
      },
    ];
  });
  // The client sorts by the same input-weighted cost it displays, so the route
  // returns providers as OpenRouter lists them.
  return NextResponse.json({ providers });
}
