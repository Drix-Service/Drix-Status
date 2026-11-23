SHELL := /bin/bash
COMPOSE := docker compose -f deploy/docker-compose.yml
-include .env
export

.PHONY: help infra infra-down server agent web-install status admin test build docker clean

help: ## Show targets
	@grep -E '^[a-z-]+:.*##' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  \033[1m%-12s\033[0m %s\n", $$1, $$2}'

infra: ## Start Postgres, Redis and Mailpit in Docker
	$(COMPOSE) up -d postgres redis mailpit

infra-down: ## Stop Postgres + Redis
	$(COMPOSE) down

server: ## Run the main server (needs `make infra`)
	go run ./server/cmd/drix-server

agent: ## Run an agent: make agent DRIX_AGENT_TOKEN=drx_...
	DRIX_SERVER_URL=$${DRIX_SERVER_URL:-http://localhost:8080} go run ./agent/cmd/drix-agent

web-install: ## Install frontend dependencies
	cd web && pnpm install

status: ## Status page dev server on :3000
	cd web && pnpm dev:status

admin: ## Admin panel dev server on :3001
	cd web && pnpm dev:admin

test: ## Run Go tests and typecheck the frontends
	go test ./shared/... ./server/... ./agent/...
	go vet ./shared/... ./server/... ./agent/...
	cd web && pnpm typecheck

build: ## Build binaries into ./bin and both websites into web/dist
	mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/drix-server ./server/cmd/drix-server
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/drix-agent ./agent/cmd/drix-agent
	cd web && pnpm build

docker: ## Build and start the full stack in Docker
	$(COMPOSE) --profile full up -d --build

clean: ## Remove build output
	rm -rf bin web/dist
