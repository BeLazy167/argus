/* ── Friendly Errors ─────────────────────────── */

const HTTP_502_RE = /\b502\b/;
const HTTP_503_RE = /\b503\b/;

export function friendlyError(raw: string): {
  title: string;
  detail: string;
  action: string;
} {
  if (raw.includes("secondary rate limit")) {
    return {
      title: "GitHub rate limit reached",
      detail:
        "GitHub temporarily blocked posting due to too many requests. The review is complete — findings are shown below.",
      action: "Wait a few minutes and click Retry to post to GitHub.",
    };
  }
  if (raw.includes("submitted too quickly")) {
    return {
      title: "GitHub hasn’t processed the diff yet",
      detail:
        "The review was submitted before GitHub finished computing the PR diff.",
      action: "Click Retry — it usually works on the second attempt.",
    };
  }
  if (HTTP_502_RE.test(raw) || HTTP_503_RE.test(raw)) {
    return {
      title: "GitHub server error",
      detail:
        "GitHub returned a server error but may have posted the review anyway.",
      action: "Check the PR on GitHub. If no review appears, click Retry.",
    };
  }
  if (raw.includes("422")) {
    return {
      title: "GitHub rejected the review",
      detail:
        "GitHub could not process the review, likely due to line positions that no longer match the diff.",
      action: "Click Retry to re-run the review against the latest diff.",
    };
  }
  if (raw.includes("stage") && raw.includes("failed")) {
    return {
      title: "Review pipeline error",
      detail: raw.length > 200 ? raw.slice(0, 200) + "…" : raw,
      action: "Click Retry to re-run the review.",
    };
  }
  // Server-restart recovery: backend writes this exact phrase when marking
  // stale in-progress/pending reviews as failed at startup. Nothing the user
  // can fix by checking provider keys.
  if (raw.includes("server restarted") || raw.includes("timed out")) {
    return {
      title: "Review interrupted by server restart",
      detail:
        "A backend restart ended this review mid-pipeline. No charge — nothing ran to completion.",
      action: "Click Retry to start fresh.",
    };
  }
  return {
    title: "Review failed to post",
    detail: raw.length > 200 ? raw.slice(0, 200) + "…" : raw,
    action: "Check your API key and provider settings, then click Retry.",
  };
}
