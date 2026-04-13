# Contributing to Argus

## Development Setup

1. Install Go 1.24+ and PostgreSQL (or use [Neon](https://neon.tech))
2. Clone the repo and copy the env file:
   ```bash
   git clone https://github.com/BeLazy167/argus.git
   cd argus
   cp .env.example .env
   ```
3. Edit `.env` with your database URL and credentials
4. Run migrations: `make migrate-up`
5. Start the server: `make dev`

## Running Tests

```bash
make test    # go test -race -count=1 ./...
make lint    # golangci-lint run
```

All tests must pass with the race detector enabled before submitting a PR.

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
