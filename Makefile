# Tunnex.io — developer Makefile
# One-command boot lives here: `make up` / `make down`.

.DEFAULT_GOAL := help
COMPOSE := docker compose

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

.PHONY: up
up: ## Start the full stack (postgres, redis, api, web, nginx, node-agent, mailpit)
	@test -f .env || cp .env.example .env
	$(COMPOSE) up -d --build
	@echo "Tunnex is starting → http://localhost   (Mailpit → http://localhost:8025)"

.PHONY: down
down: ## Stop the stack (keep volumes)
	$(COMPOSE) down

.PHONY: reset
reset: ## Stop the stack and delete all data volumes
	$(COMPOSE) down -v

.PHONY: ps
ps: ## Show service status
	$(COMPOSE) ps

.PHONY: logs
logs: ## Tail all service logs
	$(COMPOSE) logs -f

.PHONY: migrate
migrate: ## Apply all database migrations
	$(COMPOSE) run --rm --build migrate up

.PHONY: migrate-down
migrate-down: ## Roll back one database migration
	$(COMPOSE) run --rm --build migrate down

.PHONY: migrate-version
migrate-version: ## Print the current schema version
	$(COMPOSE) run --rm --build migrate version

.PHONY: migrate-create
migrate-create: ## Scaffold a migration pair: make migrate-create name=add_widgets
	@test -n "$(name)" || { echo "usage: make migrate-create name=<snake_case>"; exit 1; }
	@dir=apps/api/db/migrations; \
	next=$$(printf "%04d" $$(( $$(ls $$dir/*.up.sql 2>/dev/null | wc -l | tr -d ' ') + 1 ))); \
	touch $$dir/$${next}_$(name).up.sql $$dir/$${next}_$(name).down.sql; \
	echo "created $$dir/$${next}_$(name).{up,down}.sql"

.PHONY: sqlc
sqlc: ## Regenerate typed query code from db/queries
	docker run --rm -v "$(PWD)/apps/api":/src -w /src sqlc/sqlc generate

.PHONY: api
api: ## Run the API locally (outside docker)
	cd apps/api && go run ./cmd/server

.PHONY: agent
agent: ## Run the node agent locally (outside docker)
	cd apps/node && go run ./cmd/agent

.PHONY: web
web: ## Run the web dev server locally
	pnpm --filter @tunnex/web dev

.PHONY: tidy
tidy: ## Tidy Go modules
	cd apps/api && go mod tidy
	cd apps/node && go mod tidy
