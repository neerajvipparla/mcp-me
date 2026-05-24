.PHONY: help build run dev infra-up infra-down infra-logs \
        migrate-up migrate-down migrate-status migrate-force \
        test test-verbose lint vet tidy \
        docker-build docker-run clean

# ── Config ────────────────────────────────────────────────────────────────────
-include .env
export

BINARY     := bin/server
CMD        := ./cmd/server
MIGRATIONS := migrations
DB_URL     ?= $(DATABASE_URL)

# ── Help ──────────────────────────────────────────────────────────────────────
help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'

# ── Build ─────────────────────────────────────────────────────────────────────
build: ## Compile binary → bin/server
	@mkdir -p bin
	go build -o $(BINARY) $(CMD)

run: build ## Build and run the server
	$(BINARY)

dev: ## Run with live env (no build cache) — for local iteration
	go run $(CMD)/main.go

# ── Infrastructure ────────────────────────────────────────────────────────────
infra-up: ## Start Redis only (Postgres + Qdrant managed externally)
	docker compose up -d redis

infra-down: ## Stop all Docker services
	docker compose down

infra-logs: ## Tail logs from all Docker services
	docker compose logs -f

infra-up-all: ## Start all services including local Postgres + Qdrant (dev only)
	docker compose up -d

up: ## Build and start app + nginx + redis
	docker compose up -d --build app nginx redis

down: ## Stop app + nginx
	docker compose stop app nginx

logs: ## Tail app logs
	docker compose logs -f app

# ── Migrations ────────────────────────────────────────────────────────────────
migrate-up: ## Apply all pending migrations
	@[ -n "$(DB_URL)" ] || (echo "ERROR: DATABASE_URL not set" && exit 1)
	migrate -path $(MIGRATIONS) -database "$(DB_URL)" up

migrate-down: ## Roll back the last migration
	@[ -n "$(DB_URL)" ] || (echo "ERROR: DATABASE_URL not set" && exit 1)
	migrate -path $(MIGRATIONS) -database "$(DB_URL)" down 1

migrate-status: ## Show current migration version
	@[ -n "$(DB_URL)" ] || (echo "ERROR: DATABASE_URL not set" && exit 1)
	migrate -path $(MIGRATIONS) -database "$(DB_URL)" version

migrate-force: ## Force migration to version V (usage: make migrate-force V=1)
	@[ -n "$(DB_URL)" ] || (echo "ERROR: DATABASE_URL not set" && exit 1)
	@[ -n "$(V)" ] || (echo "ERROR: V not set. Usage: make migrate-force V=1" && exit 1)
	migrate -path $(MIGRATIONS) -database "$(DB_URL)" force $(V)

migrate-drop: ## Drop everything — DESTRUCTIVE
	@[ -n "$(DB_URL)" ] || (echo "ERROR: DATABASE_URL not set" && exit 1)
	@echo "WARNING: this drops all tables. Ctrl-C to abort." && sleep 3
	migrate -path $(MIGRATIONS) -database "$(DB_URL)" drop -f

# ── Code quality ──────────────────────────────────────────────────────────────
test: ## Run all tests
	go test ./...

test-verbose: ## Run all tests with verbose output
	go test -v ./...

test-race: ## Run all tests with race detector
	go test -race ./...

vet: ## Run go vet
	go vet ./...

lint: ## Run golangci-lint (must be installed)
	golangci-lint run ./...

tidy: ## Tidy and verify go.mod / go.sum
	go mod tidy
	go mod verify

# ── Docker ────────────────────────────────────────────────────────────────────
docker-build: ## Build the production Docker image
	docker build -t mcp-me:latest .

docker-run: ## Run the production Docker image (reads .env)
	docker run --env-file .env -p 8080:8080 mcp-me:latest

# ── Shortcuts ─────────────────────────────────────────────────────────────────
setup: infra-up migrate-up ## First-time setup: start Redis + apply migrations
	@echo "Done. Run: make dev"

reset-db: migrate-drop migrate-up ## Wipe and recreate all tables — DESTRUCTIVE

# ── Skills ────────────────────────────────────────────────────────────────────
install-skill: ## Install the mcp-me Claude Code skill → ~/.claude/skills/mcp-me/
	@mkdir -p ~/.claude/skills/mcp-me
	@cp docs/skills/mcp-me/SKILL.md ~/.claude/skills/mcp-me/SKILL.md
	@echo "✓ Skill installed at ~/.claude/skills/mcp-me/SKILL.md"

# ── Clean ─────────────────────────────────────────────────────────────────────
clean: ## Remove build artifacts
	rm -rf bin/
