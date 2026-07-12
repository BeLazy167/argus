.PHONY: build run dev test lint tygo tygo-check migrate-up migrate-down docker-up docker-down deploy secrets

TYGO_VERSION := v0.2.21

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

secrets:
	fly secrets set \
		DATABASE_URL="$(DATABASE_URL)" \
		GITHUB_APP_ID="$(GITHUB_APP_ID)" \
		GITHUB_PRIVATE_KEY="$$(cat $(GITHUB_PRIVATE_KEY_PATH))" \
		GITHUB_WEBHOOK_SECRET="$(GITHUB_WEBHOOK_SECRET)" \
		LLM_API_KEY="$(LLM_API_KEY)" \
		LLM_BASE_URL="$(LLM_BASE_URL)" \
		DEFAULT_REVIEW_MODEL="$(DEFAULT_REVIEW_MODEL)" \
		DEFAULT_TRIAGE_MODEL="$(DEFAULT_TRIAGE_MODEL)"
