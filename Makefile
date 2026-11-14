# CareerPilot AI developer commands. Run `make help`.
SHELL := /bin/bash
.DEFAULT_GOAL := help

API_DIR := services/api
AI_DIR  := services/ai
WEB_DIR := apps/web
PY      := $(AI_DIR)/.venv/bin/python

.PHONY: help
help: ## List commands
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

.PHONY: env
env: ## Create .env from .env.example with generated secrets (refuses to overwrite)
	@test ! -f .env || (echo ".env already exists; not overwriting" && exit 1)
	@pw=$$(openssl rand -hex 16); \
	sed -e "s/change-me/$$pw/g" \
	    -e "s|^SESSION_SECRET=.*|SESSION_SECRET=$$(openssl rand -hex 32)|" \
	    -e "s|^DATA_ENCRYPTION_KEY=.*|DATA_ENCRYPTION_KEY=$$(openssl rand -base64 32)|" \
	    -e "s|^AI_SERVICE_TOKEN=.*|AI_SERVICE_TOKEN=$$(openssl rand -hex 32)|" \
	    .env.example > .env && chmod 600 .env && echo "Wrote .env"

.PHONY: setup
setup: ## Install all toolchains' dependencies
	cd $(API_DIR) && go mod download
	test -d $(AI_DIR)/.venv || python3 -m venv $(AI_DIR)/.venv
	$(PY) -m pip install -q -r $(AI_DIR)/requirements-dev.txt
	cd $(WEB_DIR) && pnpm install --frozen-lockfile

# ---------- Stack ----------
.PHONY: infra up down logs ps
infra: ## Start Postgres + Redis only
	docker compose up -d postgres redis
up: ## Build and start the full stack (web on :8088)
	docker compose up -d --build
down: ## Stop the stack (data volume kept)
	docker compose down
logs: ## Tail stack logs
	docker compose logs -f --tail=100
ps: ## Show stack status
	docker compose ps

# ---------- Run services on the host ----------
.PHONY: dev-api dev-ai dev-web migrate
dev-api: ## Run the Go API on the host (needs `make infra`)
	set -a; source .env; set +a; cd $(API_DIR) && go run ./cmd/careerpilot serve
dev-ai: ## Run the AI service on the host
	set -a; source .env; set +a; cd $(AI_DIR) && .venv/bin/uvicorn app.main:create_app --factory --reload --port 8090
dev-web: ## Run the Vite dev server (proxies /api to :8080)
	cd $(WEB_DIR) && pnpm dev
migrate: ## Apply database migrations
	set -a; source .env; set +a; cd $(API_DIR) && go run ./cmd/careerpilot migrate up

# ---------- Quality ----------
.PHONY: test test-api test-ai test-web lint lint-api lint-ai lint-web build
test: test-api test-ai test-web ## Run all unit tests
test-api:
	cd $(API_DIR) && go test -race ./...
test-ai:
	cd $(AI_DIR) && .venv/bin/pytest
test-web:
	cd $(WEB_DIR) && pnpm test
lint: lint-api lint-ai lint-web ## Run all linters / type checks
lint-api:
	cd $(API_DIR) && test -z "$$(gofmt -l .)" && go vet ./...
lint-ai:
	cd $(AI_DIR) && .venv/bin/ruff check . && .venv/bin/ruff format --check .
lint-web:
	cd $(WEB_DIR) && pnpm typecheck && pnpm lint
build: ## Build all artefacts
	cd $(API_DIR) && CGO_ENABLED=0 go build -o bin/careerpilot ./cmd/careerpilot
	cd $(WEB_DIR) && pnpm build
