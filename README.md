<div align="center">

# Argus

**A self-hostable AI code reviewer that behaves like a senior engineer — not a comment bot.**

Argus sizes up each pull request before it reviews it — a docs typo and a database migration do not get the same scrutiny — then runs a multi-pass specialist review that has to back every comment with a concrete failure scenario and a fix. No praise spam, no style nitpicks, and it shows its work on every review.

[![CI](https://github.com/BeLazy167/argus/actions/workflows/ci.yml/badge.svg)](https://github.com/BeLazy167/argus/actions/workflows/ci.yml)
![Go](https://img.shields.io/badge/Go-1.24-00ADD8)
![Next.js](https://img.shields.io/badge/Next.js-16-black)
![License](https://img.shields.io/badge/License-AGPL--3.0-green)

</div>

---

## What a review looks like

Argus posts one PR review: a summary at the top, findings inline on the exact lines, and a "Glass Box" footer that shows what it did. Every finding carries a priority (**P0/P1/P2**), a confidence score, the failure it would cause, and — where it can — a committable fix. This is the real template, with a fictional example:

> ### 🔎 Argus · 6/10 — Clean session refactor, but an unchecked nil will panic the auth handler
>
> This PR moves session lookup behind a `SessionStore` interface and adds Redis-backed caching. The abstraction is sound and the cache-key derivation is correct, but `GetSession` now returns `(nil, nil)` on a cache miss while callers still assume a non-nil session — one path dereferences it directly. Coverage for the miss/expiry path is missing.
>
> *2 findings suppressed by team feedback ([audit](#))*
>
> **3 findings** · 3 inline · 0 folded
>
> ---
> <sub>Contract: production/full · checked: bug_hunter, security, architecture, regression · 2 suppressed by team feedback · review took 1m42s</sub>

And one of the inline comments it left, anchored to `internal/auth/handler.go:88`:

> 🔴 **P0 (9/10) · Bug:** Nil pointer dereference when the session cache misses
>
> `store.GetSession` returns `(nil, nil)` on a cache miss or expired key, but `handleRequest` dereferences `session.UserID` without a nil check. Any request carrying an evicted or expired session cookie panics the handler goroutine and returns a 500 — an unauthenticated caller can trigger it on demand with a stale cookie.
>
> ```suggestion
> 	session, err := store.GetSession(ctx, cookie)
> 	if err != nil {
> 		return nil, err
> 	}
> 	if session == nil {
> 		return nil, ErrSessionNotFound
> 	}
> ```
>
> *— Matches a prior fix in PR #418 (@dev-sarah, 3 months ago).*

That last line is Argus recognizing a shape it has seen before. It remembers.

---

## Why Argus is different

Most AI reviewers run one prompt over the diff and post whatever comes back. Argus is built around a few opinionated decisions that make its reviews worth reading:

- **It calibrates before it reviews.** Every PR gets a **Review Contract** — `{change_class, evidence_bar, depth}` — computed from deterministic signals first (draft status, labels, branch prefix, path globs, size), with an LLM filling the class only when the metadata is silent. A one-time script gets a single balanced reviewer; a migration or an auth-file change gets a maxed-out evidence bar that never relaxes. You don't get a 40-comment pile-on for a config bump.

- **It reviews in passes, not one shot.** Production PRs go through four specialists in parallel — **bug hunter, security, architecture, regression** — coordinated by a lead-agent brief, then deduplicated, corroborated against static analysis, judged, and (on deep reviews) given a second look at the hottest and coldest files.

- **Every comment obeys the same laws.** One rubric ships in every prompt: a finding needs a concrete failure scenario *and* a fix, praise comments are banned, style is the linter's job, and **silence is a valid review**. If there's nothing worth saying, Argus says nothing.

- **It learns from your team.** Thumbs-down a finding and Argus stops posting semantically similar ones for that category — except security findings and permanent checks, which are never muted. Reply to a comment and it factors that in. Prior fixes and repo rules surface on future PRs.

- **Every review shows its work.** The Glass Box footer states the contract class and depth, which reviewers ran, how many findings were suppressed by team feedback, and how long it took. A token/cost breakdown is one click away. Nothing is a black box.

- **You run it.** Self-hostable, bring-your-own-key (LLM keys live encrypted in your own database, never in env), AGPL-3.0. No per-seat SaaS, no code leaving your infrastructure.

---

## How it works

A Review Contract is computed the moment the webhook arrives, then gates a nine-stage pipeline:

```
Webhook → Contract → Triage → Briefing → Review → Dedup → Validate → Scoring → Pass2 → Synthesis → Post
```

| Stage | What it does |
|-------|-------------|
| **Contract** | Pre-pipeline. Change class, evidence bar, and depth from PR metadata + paths; an LLM fills the class only when deterministic signals are silent. |
| **Triage** | Heuristic + LLM file classification into skip / skim / security-skim / deep, then contract overrides. |
| **Briefing** | A lead agent produces per-file focus and a cross-cutting brief for the specialists. |
| **Review** | Four specialists in parallel (or a single balanced reviewer for one-time scripts). |
| **Dedup** | SmartDedup: three deterministic layers — canonical vuln type, TF-IDF cosine, line proximity — plus cross-specialist corroboration. |
| **Validate** | SAST corroboration, blast-radius analysis, and execution simulation, in parallel. |
| **Scoring** | An LLM judge with class-conditioned thresholds and deterministic per-category caps. |
| **Pass2** | Re-review of the hottest and coldest files (deep reviews only; skipped for script/docs/generated classes). |
| **Synthesis** | Summary, brief, score (1–10), and Mermaid diagrams. |
| **Post** | Up to 10 inline findings, severity-first; overflow folded into "…plus N similar"; near-misses in a collapsed *Minor notes* section; Glass Box footer; pattern learning. |

> Briefing, Dedup, Validate, and Pass2 are deep-review stages — shallow and skim reviews take a faster path. See [docs/architecture.md](docs/architecture.md) for the full design.

---

## Capabilities

**Calibrated review**
- Per-PR Review Contracts (`change_class`, `evidence_bar`, `depth`, `signals`) — deterministic-first, LLM-fill-when-silent
- Class-aware routing: scripts get one reviewer, docs/generated skip the second pass, security files and migrations get a maxed-out evidence bar that never relaxes
- Review Laws: failure-scenario-plus-fix or nothing; no praise, no style nits; silence is valid

**Multi-pass analysis**
- Four specialists (bug hunter, security, architecture, regression) coordinated by a lead-agent brief
- SmartDedup — 3-layer deterministic dedup with cross-specialist corroboration
- Cross-file dedup — at most two findings per vulnerability type across the whole PR
- SAST integration — staticcheck (Go), ESLint (TS/JS), semgrep — findings fed to the reviewers as verification targets
- Code graph over ~15 languages (Go AST, pure-Go tree-sitter, regex fallback) driving blast-radius analysis (depth-2 transitive dependencies)
- Simulation mode — execution-scenario testing with confidence scores

**Signal, not noise**
- P0/P1/P2 priorities with 1–10 confidence scores and class-aware severity thresholds
- 10-finding inline cap, severity-first, with graceful overflow and a collapsed *Minor notes* section
- Glass Box footer on every review, with an optional token/cost breakdown
- PR-description enrichment with Mermaid diagrams (sequence, dataflow, dependency)

**Memory & feedback**
- Suppression memory — semantic dismissal matching per category (security and permanent checks are never muted)
- Pattern learning from reactions and reply analysis; prior fixes and repo rules surface inline
- Incremental re-review on push, with a 1–10 score
- Review gauge — post-close telemetry on which comments actually got addressed, per category per change class (`GET /api/v1/stats/gauge`)
- Custom rules in natural language

---

## Quick Start

Running Argus against your own repos means creating a GitHub App and pointing a self-hosted backend at it. The [self-hosting guide](docs/self-hosting.md) walks through all of it — GitHub App creation, webhook relay, Clerk auth, and `SELF_HOSTED=true` to unlock every feature.

### Prerequisites

- Go 1.24+
- PostgreSQL (or a serverless Postgres like [Neon](https://neon.tech))
- A [GitHub App](docs/self-hosting.md) — Argus receives its webhooks and posts as it

### External services

| Service | Required? | Purpose |
|---------|-----------|---------|
| GitHub App | **Required** | Receives PR webhooks, posts reviews |
| Clerk | **Required for the dashboard** | Dashboard auth + backend JWT verification |
| LLM provider (BYOK) | **Required for reviews** | OpenRouter or any OpenAI-compatible API, added via the dashboard |
| Supermemory | Optional | RAG memory for patterns and rules |
| PostHog | Optional | Analytics; disabled when unset |

### Run the backend

```bash
git clone https://github.com/BeLazy167/argus.git
cd argus/backend
cp .env.example .env
# Edit .env with your database URL and GitHub App credentials.
# NOTE: make targets read your shell environment, not .env — export the vars
# (`set -a; source .env; set +a`) or use docker compose, which loads it for you.

go run ./cmd/migrate   # apply migrations — the migrator ships in the repo, no extra tooling
go run ./cmd/argus     # start the server
```

Or bring up the whole stack (Postgres + migrations + server) with Docker:

```bash
docker compose up
```

### Configure an LLM provider

Argus is bring-your-own-key: LLM keys live **encrypted in your database**, not in env vars. Set `ENCRYPTION_KEY` first (`openssl rand -hex 32`), start the dashboard, and add your key on the **Providers** page. OpenRouter and any OpenAI-compatible endpoint work.

### Run the dashboard

```bash
cd web
pnpm install
pnpm dev
```

---

## Configuration

Key environment variables — see [`backend/.env.example`](backend/.env.example) for the fully annotated list:

| Variable | Description |
|----------|-------------|
| `DATABASE_URL` | PostgreSQL connection string |
| `GITHUB_APP_ID` | GitHub App ID |
| `GITHUB_PRIVATE_KEY_PATH` | GitHub App private-key path (or `GITHUB_PRIVATE_KEY` inline PEM) |
| `GITHUB_WEBHOOK_SECRET` | Webhook signature secret |
| `GITHUB_APP_SLUG` | Your app's slug — drives install URLs and the `@mention` Argus answers to |
| `ENCRYPTION_KEY` | AES key encrypting BYOK provider keys at rest (required for reviews) |
| `CLERK_JWKS_URL` | Clerk JWKS endpoint; when unset, authed API routes fail closed (503) |
| `DASHBOARD_BASE_URL` | Dashboard URL linked from GitHub comments |
| `SELF_HOSTED` | `true` unlocks all features (disables plan gating) |
| `SUPERMEMORY_API_KEY` | Supermemory key for pattern learning (optional) |

---

## Development

All backend commands run from `backend/`:

```bash
make dev          # run the server
make test         # run all tests (CI runs the same suite with -race)
make lint         # golangci-lint
make build        # build the binary
make sqlc-check   # verify generated DB code is in sync
make tygo-check   # verify generated Go→TS wire types are in sync
```

Frontend, from `web/`: `pnpm lint`, `pnpm typecheck`, `pnpm build`.

See [docs/CONTRIBUTING.md](docs/CONTRIBUTING.md) for the full setup, code style, and PR process.

---

## Deployment

```bash
cd backend && fly deploy      # backend → Fly.io
cd web && vercel --prod       # dashboard → Vercel
```

Set the app name in `backend/fly.toml` and your host URLs in the backend config before deploying.

---

## Project Structure

```
backend/
  cmd/argus/        # entry point
  cmd/migrate/      # standalone DB migrator (embedded SQL)
  internal/
    api/            # HTTP handlers, routes, middleware
    app/            # application bootstrap
    config/         # configuration loading
    crypto/         # AES encryption for stored keys
    github/         # GitHub API client
    graph/          # code graph (AST, tree-sitter, regex, indexer)
    llm/            # LLM provider abstraction (OpenAI-compatible)
    memory/         # Supermemory integration (patterns, rules)
    pipeline/       # review pipeline (orchestrator, stages, dedup, scoring)
    sast/           # SAST runners (staticcheck, eslint, semgrep)
    store/          # database layer (pgx, sqlc)
  pkg/diff/         # diff parser
web/                # Next.js dashboard
docs/               # architecture, self-hosting, contributing
```

---

## Roadmap

- Custom-rules dashboard UI (the backend API already exists)
- GitLab and Bitbucket support
- Migrate the tree-sitter engine to a WASM build for broader language coverage without CGO
- Review profiles / tone control
- MCP server for IDE integration

## Contributing

Issues and pull requests are welcome — start with [docs/CONTRIBUTING.md](docs/CONTRIBUTING.md). Security reports go through [GitHub private advisories](docs/SECURITY.md).

## License

[AGPL-3.0](LICENSE) — if you run a modified Argus as a network service, you must share your changes.
