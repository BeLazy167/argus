/**
 * fetchWithTrace — explicit fetch wrapper that round-trips an Argus trace id
 * via the `X-Argus-Trace-Id` header so frontend events can be correlated with
 * backend pipeline stages of the same request chain.
 *
 * Behaviour:
 *  - Reads a module-local `lastTraceId`. If set, sent on the outgoing request
 *    as `X-Argus-Trace-Id` so follow-up API calls can be linked server-side.
 *  - Every response: parses `X-Argus-Trace-Id` and stores it as the new
 *    `lastTraceId` for the next request.
 *  - 4xx/5xx: fires a `ui.fetch_failed` PostHog event carrying the response
 *    trace id (if present) so failed requests show up in the error funnel.
 *
 * NOT a global fetch monkey-patch. App code opts in by importing this.
 */
import { track } from "@/lib/analytics";

const TRACE_HEADER = "X-Argus-Trace-Id";

let lastTraceId: string | null = null;

/** Exported for tests / debugging. Not used by production code paths. */
export const __getLastTraceId = (): string | null => lastTraceId;

/**
 * Returns the last trace id observed on any fetchWithTrace response.
 * Used by global error handlers (posthog-provider) to attribute crashes
 * to the most recent server round-trip. `null` before the first response.
 */
export const getLastTraceId = (): string | null => lastTraceId;

/**
 * Resolves a readable URL string from a `fetch` input for event tagging.
 * `RequestInfo` is `string | URL | Request` — we want the final string.
 */
const readUrl = (input: RequestInfo | URL): string => {
  if (typeof input === "string") return input;
  if (input instanceof URL) return input.toString();
  return input.url;
};

/**
 * Resolves a method string from a `fetch` input + init. Defaults to GET.
 */
const readMethod = (input: RequestInfo | URL, init?: RequestInit): string => {
  if (init?.method) return init.method.toUpperCase();
  if (input instanceof Request) return input.method.toUpperCase();
  return "GET";
};

/**
 * fetchWithTrace — drop-in replacement for `fetch` that participates in
 * Argus trace propagation and reports 4xx/5xx to PostHog.
 */
export async function fetchWithTrace(
  input: RequestInfo | URL,
  init?: RequestInit,
): Promise<Response> {
  const headers = new Headers(init?.headers);
  if (lastTraceId !== null && !headers.has(TRACE_HEADER)) {
    headers.set(TRACE_HEADER, lastTraceId);
  }

  let response: Response;
  try {
    response = await fetch(input, { ...init, headers });
  } catch (err) {
    // Network-level failure (DNS, offline, CORS, TLS, abort). No response,
    // so status=0 is the sentinel and error_class="network" distinguishes
    // from 4xx/5xx. trace_id falls back to the last-seen round-trip since
    // this request got no response header.
    track("ui.fetch_failed", {
      method: readMethod(input, init),
      url: readUrl(input),
      status: 0,
      error_class: "network",
      trace_id: lastTraceId,
    });
    throw err;
  }

  const responseTrace = response.headers.get(TRACE_HEADER);
  if (responseTrace) {
    lastTraceId = responseTrace;
  }

  if (!response.ok) {
    track("ui.fetch_failed", {
      method: readMethod(input, init),
      url: readUrl(input),
      status: response.status,
      trace_id: responseTrace ?? lastTraceId,
    });
  }

  return response;
}
