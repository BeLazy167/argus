.PHONY: build run dev test lint tygo tygo-check sqlc sqlc-check migrate-up migrate-down docker-up docker-down deploy secrets

TYGO_VERSION := v0.2.21
SQLC_VERSION := 1.30.0

build:
	go build -o ./bin/argus ./cmd/argus

# tygo regenerates web/src/lib/generated/*.ts from the Go wire structs.
tygo:
	go run github.com/gzuidhof/tygo@$(TYGO_VERSION) generate

# tygo-check regenerates and fails if the committed output drifts — either a
# tracked file changed (git diff) or a new generated file is untracked
# (git status --porcelain, which git diff alone would miss). CI gate.
tygo-check: tygo
	git diff --exit-code -- ../web/src/lib/generated
	@if [ -n "$$(git status --porcelain -- ../web/src/lib/generated)" ]; then \
		echo "error: untracked/modified generated files — commit them:"; \
		git status --porcelain -- ../web/src/lib/generated; \
		exit 1; \
	fi

# sqlc regenerates internal/store/db/*.go from the SQL queries + migrations.
# Requires sqlc $(SQLC_VERSION) on PATH; a mismatched version drifts the
# output, so the target enforces the pin instead of trusting the comment.
# Keep SQLC_VERSION in lockstep with .github/workflows/ci.yml's setup-sqlc pin.
sqlc:
	@sqlc version | grep -q "v$(SQLC_VERSION)" || { \
		echo "error: sqlc v$(SQLC_VERSION) required, found: $$(sqlc version)"; \
		exit 1; \
	}
	sqlc generate

# sqlc-check regenerates and fails if the committed output drifts — either a
# tracked file changed (git diff) or a new generated file is untracked
# (git status --porcelain, which git diff alone would miss). CI gate.
sqlc-check: sqlc
	git diff --exit-code -- internal/store/db
	@if [ -n "$$(git status --porcelain -- internal/store/db)" ]; then \
		echo "error: untracked/modified generated files — run 'make sqlc' and commit:"; \
		git status --porcelain -- internal/store/db; \
		exit 1; \
	fi

run: build
	./bin/argus

dev:
	go run ./cmd/argus

test:
	go test ./... -v -count=1

lint:
	golangci-lint run

migrate-up:
	migrate -path internal/store/migrations -database "$(DATABASE_URL)" up

migrate-down:
	migrate -path internal/store/migrations -database "$(DATABASE_URL)" down 1

docker-up:
	docker compose up --build -d

docker-down:
	docker compose down

docker-dev:
	docker compose --profile dev up --build -d

smee:
	npx smee-client --url $(SMEE_URL) --target http://localhost:8080/webhooks/github

deploy:
	fly deploy

# LLM provider keys are BYOK — added via the dashboard Providers page and
# stored encrypted in the DB, never pushed as deploy secrets.
secrets:
	fly secrets set \
		DATABASE_URL="$(DATABASE_URL)" \
		GITHUB_APP_ID="$(GITHUB_APP_ID)" \
		GITHUB_PRIVATE_KEY="$$(cat $(GITHUB_PRIVATE_KEY_PATH))" \
		GITHUB_WEBHOOK_SECRET="$(GITHUB_WEBHOOK_SECRET)" \
		ENCRYPTION_KEY="$(ENCRYPTION_KEY)"
