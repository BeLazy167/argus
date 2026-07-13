# Contributing to Argus

## Development Setup

1. Install Go 1.24+ and PostgreSQL (or use [Neon](https://neon.tech))
2. Clone the repo and copy the env file:
   ```bash
   git clone https://github.com/BeLazy167/argus.git
   cd argus/backend
   cp .env.example .env
   ```
3. Edit `backend/.env` with your database URL and credentials
4. Run migrations: `go run ./cmd/migrate` (or `make migrate-up` with the golang-migrate CLI)
5. Start the server: `make dev`

### Tooling prerequisites

Only needed for the code paths you touch:

- [golang-migrate](https://github.com/golang-migrate/migrate) CLI — `make migrate-up` / `make migrate-down` (the `go run ./cmd/migrate` path needs nothing extra)
- [sqlc](https://sqlc.dev) **v1.30.0** (version-pinned) — regenerating the store layer after editing SQL queries/migrations: `make sqlc`
- [tygo](https://github.com/gzuidhof/tygo) — regenerating `web/src/lib/generated/*.ts` from Go wire structs: `make tygo` (pinned version fetched via `go run`)

### Generated-code drift gates

CI fails if generated output drifts from source. Run these locally after touching SQL queries or wire structs, and commit the regenerated files:

```bash
make sqlc-check    # internal/store/db must match the SQL
make tygo-check    # web/src/lib/generated must match the Go structs
```

## Running Tests

All backend commands run from `backend/`:

```bash
make test    # go test ./... -v -count=1 (no -race locally; CI runs with -race)
make lint    # golangci-lint run
```

All tests must pass before submitting a PR. CI runs the same suite with the race detector, so a locally-green PR can still fail on races.

## Frontend

```bash
cd web
pnpm install
pnpm dev          # local dev server
pnpm lint         # biome check src/
pnpm typecheck    # tsc --noEmit
```

Run `pnpm lint` and `pnpm typecheck` before submitting PRs that touch `web/`.

## Code Style

- Run `gofmt` on all Go code
- Use table-driven tests with subtests
- Handle all errors explicitly -- no `_` assignments without justification
- No `panic` for normal error handling
- Add `context.Context` to all blocking operations
- Propagate errors with `fmt.Errorf("...: %w", err)`

## Submitting Changes

1. Fork the repository
2. Create a feature branch: `git checkout -b feat/your-feature`
3. Make your changes
4. Run tests: `make test`
5. Run lint: `make lint`
6. Commit with a descriptive message following the convention below
7. Push and open a PR against `main`

## Commit Messages

Use conventional commit prefixes:

- `feat:` -- new feature
- `fix:` -- bug fix
- `docs:` -- documentation only
- `test:` -- adding or updating tests
- `chore:` -- maintenance, dependencies, CI

Example: `feat: add Ruby language checklist to review prompts`

## Reporting Issues

- Use the GitHub issue templates (bug report or feature request)
- Include steps to reproduce for bugs
- Include the PR URL and review ID if reporting a review quality issue

## No CLA Required

We do not require a Contributor License Agreement. By submitting a PR, you agree that your contribution is licensed under the project's AGPL-3.0 license.
