.PHONY: build run dev test lint migrate-up migrate-down docker-up docker-down deploy secrets

build:
	go build -o ./bin/argus ./cmd/argus

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
