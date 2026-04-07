# Argus

AI-powered code review that posts inline comments on GitHub pull requests.

![Go](https://img.shields.io/badge/Go-1.24-blue)
![License](https://img.shields.io/badge/License-AGPL--3.0-green)
![Tests](https://img.shields.io/badge/Tests-12%20packages-brightgreen)

## What is Argus?

Argus reviews pull requests using a multi-pass AI pipeline with 4 specialist agents (bug hunter, security, architecture, regression). It posts inline comments on GitHub with P0/P1/P2 priorities, confidence scores, and structured output for AI agent consumption.

## Features

- **Multi-pass AI review** with 4 specialists (bug hunter, security, architecture, regression)
- **SmartDedup** -- 4-layer deduplication (canonical type, TF-IDF cosine, line proximity, LLM judge)
- **40+ language support** via Go AST, universal ctags, and regex parsers
- **SAST integration** -- staticcheck (Go), eslint (TS/JS), semgrep (30+ languages)
- **SAST-driven review hints** -- static analysis findings fed to LLM as verification targets
- **Pattern learning** from user feedback (thumbs-up/down reactions, reply analysis)
- **Incremental reviews** -- re-review on push with score progression (1-10)
- **Simulation mode** -- code execution scenario testing with confidence scores
- **PR description enrichment** with Mermaid diagrams (sequence, dataflow, dependency)
- **Blast radius analysis** via code graph (recursive CTE, depth-2 transitive deps)
- **P0/P1/P2 priority** with confidence scores and severity-based thresholds
- **40-comment GitHub cap** with file-diversity round-robin selection
- **Custom rules** (natural language, API-ready)
- **XML structured output** for AI agent consumption (Cursor, Copilot, Claude Code)
- **Cross-file dedup** -- max 2 per vulnerability type across all files
- **Cold file pass** -- under-reviewed files get a second Security specialist review

## Quick Start

### Prerequisites

- Go 1.24+
- PostgreSQL (or [Neon](https://neon.tech) serverless)
- A [GitHub App](https://docs.github.com/en/apps/creating-github-apps)

### Setup

```bash
git clone https://github.com/BeLazy167/argus.git
cd argus
cp .env.example .env
# Edit .env with your database URL, GitHub App credentials, and LLM API key
make run
```

### Frontend (optional)

```bash
cd web
pnpm install
pnpm dev
```

## Architecture

9-stage pipeline:

```
Triage -> Briefing -> Review -> Dedup -> Validate -> Scoring -> Pass2 -> Synthesis -> Post
```

| Stage | What it does |
|-------|-------------|
| Triage | Heuristic + LLM file classification (deep vs shallow) |
| Briefing | Lead agent produces cross-cutting brief for specialists |
| Review | 4 specialists review files in parallel |
| Dedup | SmartDedup: canonical type + TF-IDF + proximity + LLM judge |
| Validate | SAST corroboration, blast radius, simulation |
| Scoring | LLM judge scores findings, deterministic FP caps |
| Pass2 | Re-review hot files + cold files with fresh specialist |
| Synthesis | Generate summary, brief, diagrams, score |
| Post | 40-comment cap with diversity, enrichment, pattern learning |

See [docs/architecture.md](docs/architecture.md) for the full architecture document.

## Configuration

Key environment variables (see `.env.example`):

| Variable | Description |
|----------|-------------|
| `DATABASE_URL` | PostgreSQL connection string |
| `GITHUB_APP_ID` | GitHub App ID |
| `GITHUB_PRIVATE_KEY` | GitHub App private key (PEM) |
| `GITHUB_WEBHOOK_SECRET` | Webhook signature secret |
| `LLM_API_KEY` | OpenAI-compatible API key |
| `LLM_BASE_URL` | LLM API base URL |
| `CLERK_JWKS_URL` | Clerk auth JWKS endpoint |
| `SUPERMEMORY_API_KEY` | Supermemory API key (pattern learning) |
| `ENCRYPTION_KEY` | AES key for encrypting stored API keys |

## Development

```bash
make dev          # Run with hot reload
make test         # Run all tests with race detector
make lint         # Run golangci-lint
make build        # Build binary
make migrate-up   # Run database migrations
```

## Deployment

### Backend (Fly.io)

```bash
fly deploy
```

### Frontend (Vercel)

```bash
cd web
vercel --prod
```

## Project Structure

```
cmd/argus/          # Entry point
internal/
  api/              # HTTP handlers, routes, middleware
  app/              # Application bootstrap
  config/           # Configuration loading
  crypto/           # AES encryption for stored keys
  github/           # GitHub API client
  graph/            # Code graph (AST parser, ctags, regex, indexer)
  llm/              # LLM provider abstraction
  memory/           # Supermemory integration (patterns, rules)
  pipeline/         # Review pipeline (orchestrator, stages, dedup, scoring)
  sast/             # SAST runners (staticcheck, eslint, semgrep)
  store/            # Database layer (pgx, sqlc)
  util/             # String utilities
pkg/diff/           # Diff parser
web/                # Next.js frontend
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup, code style, and PR process.

## License

[AGPL-3.0](LICENSE)

## Roadmap

- Custom rules UI (backend API exists)
- GitLab and Bitbucket support
- Tree-sitter WASM via malivvan/go-tree-sitter (248 languages, no CGO)
- Review profiles / tone control
- MCP server for IDE integration
