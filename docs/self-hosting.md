# Self-Hosting Argus

Everything needed to run your own Argus: a GitHub App, Postgres, the Go backend, the Next.js dashboard, and a Clerk instance for dashboard auth. LLM keys are BYOK — added at runtime via the dashboard, never via env.

Set `SELF_HOSTED=true` on the backend to disable plan gating (self-hosts have no billing; every installation gets pro features). The API reports the effective tier, so the dashboard reflects pro automatically — no web-side configuration needed. `SELF_HOSTED=true` also makes reviews **auto-run unconditionally**: with no billing to gate, a self-host reviews on every opened/pushed/reopened PR regardless of the stored `auto_run` setting (see [Auto-run & re-review](#auto-run--re-review)).

## 1. Create the GitHub App

Go to **GitHub → Settings → Developer settings → GitHub Apps → New GitHub App** (use an org account if the app should live under an org).

### Basics

- **App name / slug**: anything you like — set the slug as `GITHUB_APP_SLUG` on the backend and `NEXT_PUBLIC_GITHUB_APP_SLUG` on the web app. The slug drives the bot's identity end to end: commands are triggered by mentioning `@<your-slug>`, posted copy uses that mention, and the backend recognizes its own comments as `<your-slug>[bot]`.
- **Homepage URL**: your dashboard URL (`DASHBOARD_BASE_URL`).

### Webhook

- **Webhook URL**: `https://<your-backend-host>/webhooks/github`
- **Webhook secret**: generate one (`openssl rand -hex 32`) and set the same value as `GITHUB_WEBHOOK_SECRET`.

### Repository permissions

These match the API calls the backend actually makes:

| Permission | Access | Used for |
|------------|--------|----------|
| Pull requests | Read & write | Posting reviews and inline comments, resolving review threads, editing PR descriptions (enrichment) |
| Issues | Read & write | Posting/editing issue comments (`@argus-eye` command replies, status comments), reactions |
| Contents | Read & write | Reading files/diffs for review context; write is needed only by `@argus-eye fix`, which commits suggestion blocks via the Git Data API — use Read-only if you don't want the fix command |
| Metadata | Read | Mandatory baseline for all Apps |

### Subscribe to events

- `pull_request` — triggers reviews
- `pull_request_review_comment` — reply analysis on inline comment threads
- `issue_comment` — `@argus-eye` commands
- `installation` — tracks installs/uninstalls

### Private key

Generate a private key on the App page, download the `.pem`, and point `GITHUB_PRIVATE_KEY_PATH` at it (or paste the PEM into `GITHUB_PRIVATE_KEY`). Set `GITHUB_APP_ID` from the App's **App ID** field.

## 2. Local webhook relay (smee)

GitHub can't reach localhost. For local dev, create a channel at [smee.io](https://smee.io), set it as the App's webhook URL, and relay:

```bash
export SMEE_URL=https://smee.io/your-channel
cd backend && make smee
# or: docker compose --profile dev up
```

## 3. Backend

```bash
cd backend
cp .env.example .env   # fill in the REQUIRED section
go run ./cmd/migrate   # apply DB migrations
go run ./cmd/argus     # start the server
```

Or `docker compose up` from the repo root (Postgres + migrations + server). Compose mounts `backend/secrets/` into the container read-only and expects the GitHub App PEM at `backend/secrets/github-app.pem` — `GITHUB_PRIVATE_KEY_PATH` from `backend/.env` is overridden inside the container.

Deploying on Fly.io: change the `app` name in `backend/fly.toml`, then `fly deploy` from `backend/`.

## 4. Clerk (dashboard auth)

Argus uses [Clerk](https://clerk.com) for dashboard sign-in and API auth.

1. Create a Clerk application (enable GitHub as a social provider for the smoothest install flow).
2. Web (`web/.env.local`):
   - `NEXT_PUBLIC_CLERK_PUBLISHABLE_KEY`
   - `CLERK_SECRET_KEY`
   - `NEXT_PUBLIC_CLERK_SIGN_IN_URL=/sign-in`, `NEXT_PUBLIC_CLERK_SIGN_UP_URL=/sign-up`
3. Backend: set `CLERK_JWKS_URL` to your instance's JWKS endpoint, e.g. `https://<your-subdomain>.clerk.accounts.dev/.well-known/jwks.json`.

When `CLERK_JWKS_URL` is unset the backend cannot verify JWTs and every authenticated API route returns `503 authentication not configured` — webhooks and reviews still work, but the dashboard API is unusable. There is no auth bypass mode.

## 5. LLM provider (BYOK)

1. Set `ENCRYPTION_KEY` on the backend (`openssl rand -hex 32`) — provider keys are encrypted at rest with it.
2. Open the dashboard **Providers** page and add your key: OpenRouter or any OpenAI-compatible endpoint (OpenAI, Azure, Groq, Together, Ollama, vLLM, ...).
3. Pick models per pipeline stage on the **Settings** page.

Reviews fail with an onboarding comment ("configure your API key") until a provider key and model config exist.

## 6. Web dashboard

```bash
cd web
cp .env.local.example .env.local   # Clerk keys + NEXT_PUBLIC_API_URL
pnpm install
pnpm dev
```

Point `NEXT_PUBLIC_API_URL` at your backend and set `CORS_ALLOW_ORIGIN` on the backend to the dashboard origin.

## Auto-run & re-review

**Auto-run is on by default.** Every PR that opens, gets pushed to, or is
reopened is reviewed automatically — no checkbox, no manual trigger. A push to an
open PR re-reviews only the commits added since the last completed review (the
inter-diff), carrying prior findings forward.

Precedence: an explicit per-repo setting wins, else the org default, else on. To
turn auto-run **off**, set `auto_run: false` — per repo on the dashboard
**Settings** page, or org-wide as an org default. When off, a push does not review
silently: Argus posts a one-shot "Trigger Argus review" checkbox comment (with a
token/cost estimate) once per PR; reviewers tick it to run on demand, or use
`@<your-slug> review`.

`SELF_HOSTED=true` overrides all of this: a self-host always auto-runs, regardless
of the stored `auto_run` flag (there is no billing to gate).

**Auto-resolve** is a separate toggle (`auto_resolve_enabled`, also on by default).
On every push it closes stale finding threads whose flagged lines the push
modified — but only after an LLM judge confirms the change actually fixed the
finding (proximity alone never resolves). It runs independently of auto-run, so a
manual-review repo still gets stale comments cleared when a fix lands. Set
`auto_resolve_enabled: false` for manual-only thread control.

## Reference

- `backend/.env.example` — every env var, annotated
- [docs/architecture.md](architecture.md) — pipeline internals
- [docs/CONTRIBUTING.md](CONTRIBUTING.md) — development workflow
