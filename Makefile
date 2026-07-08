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

# --- Code generation (OpenAPI-first: the spec is the single source of truth) ---
# Pin the exact Go patch so local and container builds produce identical codegen.
GO_IMAGE := golang:1.25.11-alpine
NODE_IMAGE := node:20-alpine
PW_IMAGE := mcr.microsoft.com/playwright:v1.48.2-jammy
OAPI_CODEGEN_VERSION := v2.4.1
OPENAPI_TS_VERSION := 7.4.4

# Compose network + dev DB creds (defaults match .env.example) used by seed/e2e.
NET := tunnex_default
PG_USER ?= tunnex
PG_PASS ?= tunnex_dev_password
PG_DB ?= tunnex

.PHONY: generate
generate: generate-go generate-ts generate-rbac sqlc ## Regenerate all code from openapi/openapi.yaml

.PHONY: generate-rbac
generate-rbac: ## Emit the RBAC grant table (rbac.Policy) as JSON for the web client mirror
	docker run --rm -v "$(PWD)":/repo -w /repo/apps/api -e GOFLAGS=-mod=mod $(GO_IMAGE) \
	  go run ./cmd/rbac-policy-gen /repo/apps/web/src/lib/rbac-policy.json

.PHONY: generate-go
generate-go: ## Generate the Go server (api) + Go client (cli) from the spec
	@mkdir -p apps/api/internal/api apps/cli/internal/api
	docker run --rm -v "$(PWD)":/repo -w /repo/apps/api -e GOFLAGS=-mod=mod $(GO_IMAGE) \
	  go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$(OAPI_CODEGEN_VERSION) \
	  -config oapi-codegen.yaml ../../openapi/openapi.yaml
	docker run --rm -v "$(PWD)":/repo -w /repo/apps/cli -e GOFLAGS=-mod=mod $(GO_IMAGE) \
	  go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$(OAPI_CODEGEN_VERSION) \
	  -config oapi-codegen.yaml ../../openapi/openapi.yaml

.PHONY: generate-ts
generate-ts: ## Generate the TypeScript API types from the spec
	docker run --rm -v "$(PWD)":/repo -w /repo/packages/shared $(NODE_IMAGE) \
	  npx --yes openapi-typescript@$(OPENAPI_TS_VERSION) ../../openapi/openapi.yaml -o src/api.d.ts

.PHONY: generate-check
generate-check: generate ## Fail if generated code is out of date (CI drift guard)
	@git diff --exit-code -- \
	  apps/api/internal/api apps/cli/internal/api apps/api/db/sqlc packages/shared/src/api.d.ts apps/web/src/lib/rbac-policy.json \
	  || { echo ""; echo "ERROR: generated code is stale. Run 'make generate' and commit the result."; exit 1; }
	@echo "generated code is up to date."

.PHONY: cli-dist
cli-dist: ## Cross-compile the tunnex CLI for release + SHA256SUMS (S5.1)
	@mkdir -p dist
	@for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64; do \
	  goos=$${target%/*}; goarch=$${target#*/}; ext=""; \
	  [ "$$goos" = "windows" ] && ext=".exe"; \
	  echo ">> $$goos/$$goarch"; \
	  docker run --rm -v "$(PWD)":/repo -w /repo/apps/cli -e GOFLAGS=-mod=mod \
	    -e CGO_ENABLED=0 -e GOOS=$$goos -e GOARCH=$$goarch $(GO_IMAGE) \
	    go build -trimpath -ldflags="-s -w" -o /repo/dist/tunnex-$$goos-$$goarch$$ext ./cmd/tunnex || exit 1; \
	done
	@cd dist && sha256sum tunnex-* > SHA256SUMS && cat SHA256SUMS
	@echo ">> dist/ ready — publish the binaries WITH SHA256SUMS (S5.1 release convention)."

.PHONY: build-editions
build-editions: ## Compile both open and enterprise builds (catches edition rot)
	@echo ">> open build"
	docker run --rm -v "$(PWD)/apps/api":/src -w /src -e GOFLAGS=-mod=mod $(GO_IMAGE) sh -c "apk add --no-cache git && go build ./..."
	@echo ">> enterprise build (-tags enterprise)"
	docker run --rm -v "$(PWD)/apps/api":/src -w /src -e GOFLAGS=-mod=mod $(GO_IMAGE) sh -c "apk add --no-cache git && go build -tags enterprise ./..."

.PHONY: test-editions
test-editions: ## Run the suite in BOTH editions against the live DB
	$(COMPOSE) up -d --wait postgres
	@echo ">> open edition tests"
	docker run --rm --network $(NET) -v "$(PWD)/apps/api":/src -w /src -e GOFLAGS=-mod=mod \
	  -e TUNNEX_TEST_DATABASE_URL="postgres://$(PG_USER):$(PG_PASS)@postgres:5432/$(PG_DB)?sslmode=disable" \
	  $(GO_IMAGE) sh -c "apk add --no-cache git && go test ./..."
	@echo ">> enterprise edition tests (-tags enterprise)"
	docker run --rm --network $(NET) -v "$(PWD)/apps/api":/src -w /src -e GOFLAGS=-mod=mod \
	  -e TUNNEX_TEST_DATABASE_URL="postgres://$(PG_USER):$(PG_PASS)@postgres:5432/$(PG_DB)?sslmode=disable" \
	  $(GO_IMAGE) sh -c "apk add --no-cache git && go test -tags enterprise ./..."

.PHONY: seed
seed: ## Seed the demo org/user (idempotent, non-destructive)
	$(COMPOSE) up -d --wait postgres
	docker run --rm --network $(NET) -v "$(PWD)/apps/api":/src -w /src -e GOFLAGS=-mod=mod \
	  -e DATABASE_URL="postgres://$(PG_USER):$(PG_PASS)@postgres:5432/$(PG_DB)?sslmode=disable" \
	  $(GO_IMAGE) sh -c "apk add --no-cache git && go run ./cmd/seed"

.PHONY: e2e
e2e: ## One command: bring the stack up healthy, run API integration + Playwright e2e
	$(COMPOSE) up -d --wait
	@echo ">> API integration tests (unit + trigger schema check against live DB)"
	docker run --rm --network $(NET) -v "$(PWD)/apps/api":/src -w /src -e GOFLAGS=-mod=mod \
	  -e TUNNEX_TEST_DATABASE_URL="postgres://$(PG_USER):$(PG_PASS)@postgres:5432/$(PG_DB)?sslmode=disable" \
	  $(GO_IMAGE) go test ./...
	@echo ">> Playwright browser e2e (SPA -> API correlation chain)"
	docker run --rm --network $(NET) -v "$(PWD)/e2e":/e2e -w /e2e -e E2E_BASE_URL=http://nginx:8080 \
	  $(PW_IMAGE) sh -c "npm install --no-audit --no-fund --silent && npx playwright test"
	@echo ">> e2e passed."

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
