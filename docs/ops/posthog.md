# PostHog Telemetry Runbook

Argus ships end-to-end PostHog observability: every pipeline stage, LLM call, webhook, settings change, and frontend click lands in one project. This runbook covers setup, dashboards, alerts, and failure modes.

## Architecture at a glance

```
┌─ Frontend (posthog-js) ──────────┐   ┌─ Backend (slog → obs.Handler) ─┐
│  pageviews, clicks, ui.* events  │   │  Every slog record with        │
│  session recording (masked)      │   │  `event=` attr → PostHog.       │
│  captureException on errors      │   │  Warn/Error without event →     │
│  alias github:<login> → user.id  │   │  log.warn / log.error.          │
└──────────────────┬───────────────┘   │  Non-blocking enqueue + circuit │
                   │                   │  breaker → never stalls prod.   │
                   └───┬───────────────┴─────────────┬───────────────────┘
                       ▼                             ▼
                 ┌──────────────────────────────────────┐
                 │  PostHog US Cloud                    │
                 │  Events · Recordings · Funnels       │
                 └──────────────────────────────────────┘
```

Full trace correlation: every request gets `X-Argus-Trace-Id` at the middleware layer; the ID is persisted on `reviews.trace_id` so cross-PR and sweeper goroutines keep emitting events under the same trace ID.

## First-time setup

### 1. Create a PostHog project

1. Visit https://us.i.posthog.com, create a new project called `argus-prod` (and `argus-dev` for local).
2. Copy the **Project API key** (starts with `phc_...`). This is the client-side token — safe to ship in the frontend bundle.
3. Generate a **Personal API key** under Settings → Personal API keys. This is required for the backend's server-side event capture. It stays secret.

### 2. Backend secrets

```bash
fly secrets set -a argus-ai POSTHOG_API_KEY=phx_...
```

`POSTHOG_API_KEY` unset = kill switch. The obs handler falls through to plain `slog.TextHandler` and nothing is forwarded.

### 3. Frontend env

On Vercel (both preview + prod):

```
NEXT_PUBLIC_POSTHOG_PROJECT_TOKEN=phc_...
NEXT_PUBLIC_POSTHOG_HOST=https://us.i.posthog.com
```

`NEXT_PUBLIC_POSTHOG_PROJECT_TOKEN` unset = frontend telemetry + session recording disabled entirely. The provider degrades gracefully — no crash, just no events.

## Dashboards

Create these in PostHog → Dashboards:

### Review Ops
- **Tile: Failures by repo** — `review.failed` count, grouped by `repo`, last 7 days
- **Tile: P95 duration by stage** — `stage.completed` P95 of `duration_ms`, grouped by `stage`
- **Tile: Cost by stage** — `llm.call.completed` sum of `cost_usd`, grouped by `stage` + `model`
- **Tile: Completed / day** — `review.completed` trend, bar chart
- **Tile: Auto-resolve rate** — `auto_resolve.evaluated` sum of `threads_resolved` / sum of `threads_checked`

### User Funnel
Steps (drop to next on first completion per user):
1. `onboarding.install_clicked`
2. `onboarding.first_review_seen`
3. `review.completed` (count ≥ 2 to filter bots / one-off tryouts)
4. `billing.upgraded` (once wired)

Filter: `$groups.installation` != `null` (only installed users count).

### Errors
- **Trend: log.error by stage** — `log.error` count, grouped by `stage`, last 48h
- **Trend: ui.exception by route** — `ui.exception` count, grouped by `route`
- **Table: Recent `review.failed`** — event list last 24h showing `repo`, `pr_number`, `error_class`, `trace_id`

### Cross-PR / Joint Acceptance
- `cross_pr.stage.completed` count over time, grouped by `risks_found` bucketed
- `cross_pr.stage.skipped` grouped by `reason`
- `rate_limit.hit` count, grouped by `kind`

## Alerts

### PostHog-native billing alert
Settings → Billing → Usage alerts. Set at **$50/month**. Email trigger.

### `log.error` spike
Insights → new trend → `log.error` count, last 1h. Save as alert: **notify when > 20 in 1h**, email channel.

### Backend handler drop counter
Expose via `/healthz`:
```json
{
  "posthog": {
    "sent": 12405,
    "dropped_buffer": 0,
    "dropped_unattributed": 3,
    "breaker_open": false
  }
}
```
Fly alerting rule (optional): page on `dropped_buffer > 0 for > 5m` — signals PostHog outage exceeding our buffer.

## Session recording

Default sample rate: **0.1** (10%) for dashboard routes, **0** on marketing.

Masking selectors (see `web/src/providers/posthog-provider.tsx`):
- `.ph-mask` — applied to settings pages + review comment bodies
- `pre`, `code` — masks all syntax-highlighted diff / code blocks globally
- `[data-phx-mask]` — escape hatch for custom components (diff viewers, etc.)

**Post-merge TODO:** flip `session_recording.sampleRate` from `0.1 → 1.0` after 2-week mask verification window. Tracked in a GitHub issue at merge time.

## Identity model

| Surface | `distinct_id` | `$groups` |
|---|---|---|
| Dashboard (Clerk user) | `user.id` | `installation: <id>`, `org: <clerk_org_id>` |
| Webhook events (PR author) | `github:<pr_author_login>` | `installation: <id>`, `repo: <full_name>` |

Merge via `posthog.alias("github:<login>", user.id)` — fired on dashboard load when the Clerk user has a GitHub OAuth external account. Users who signed up with email stay unaliased (fallback: `GET /api/me` can enrich once Clerk SDK is imported server-side; currently returns empty github_login).

## Event taxonomy (v1, 30 events)

See `/home/user/.claude/plans/nah-investigate-the-memory-scalable-meteor.md` "Event taxonomy (all 30, v1)" section. Allowlist of attribute keys is enforced by `backend/internal/obs/allowlist_test.go#TestSlogAttrsOnAllowlist` — a new slog call with an unlisted key fails CI.

## Handler failure modes

| Failure | Behavior |
|---|---|
| PostHog unreachable (DNS / 5xx) | Circuit breaker opens after 10 consecutive failures. Subsequent events dropped silently for 60s, then one half-open probe. `breaker_open` counter reflects state. |
| Event buffer full | Non-blocking enqueue drops record, increments `dropped_buffer`. Request path never blocks. |
| Missing ctx attribution | Record dropped at handler (no `distinct_id` resolvable). Increments `dropped_unattributed`. Still logged to stdout via inner handler. |
| SIGTERM / Fly machine kill | `phClient.Close()` in `app.Run()` defer flushes pending; `fly.shutdown_signal_received` event fired just before shutdown. |

## Troubleshooting

**"My event isn't showing up in PostHog Live Events"**
1. Confirm `POSTHOG_API_KEY` is set on Fly: `fly secrets list -a argus-ai | grep POSTHOG`
2. Confirm the event has an `event=` slog attr: `grep -n 'event=".event_name"' backend/`
3. Check `/healthz` — `dropped_unattributed > 0` means ctx is missing user/installation attribution
4. Check for allowlist drop: run `go test -run TestSlogAttrsOnAllowlist -v ./internal/obs/...`

**"Session recording shows `*****` where content should be visible"**
Expected for settings pages, review comment bodies, diff viewers. If an unmasked element needs visibility, remove `.ph-mask` from its wrapper. Never remove masking from settings routes.

**"PostHog Live Events shows events but dashboards are empty"**
Dashboards use `$groups` for org-level queries. Confirm `posthog.group("installation", ...)` fires by filtering a single event — the properties panel should show `$groups.installation = <id>`.

## Cost expectations (current scale)

- ~3.5k events/day from pipeline + frontend combined = **~105k/month** → 10% of the 1M free tier
- 0.1 sample × 5 sessions/day × 1 active user = **~15 recordings/month** → 0.3% of 5k free tier

Scaling projection: a 10× bump in active users (10 devs using the dashboard daily + 10× review volume) remains under PostHog free tier for events. Recording minutes are the first line item to blow through once flipped to 1.0 sample — budget `~150 recordings/month/active user` at 1.0.
