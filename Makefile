DATABASE_URL ?= postgres://bealhouse:bealhouse@localhost:5432/bealhouse?sslmode=disable
MIGRATIONS   := internal/db/migrations
BINARY       := bin/bealhouse

.DEFAULT_GOAL := help

## help: list targets
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //'

## dev: build the SPA, build the binary, and run it
dev: web build run

## web: build the Vite bundle that the Go binary embeds
web:
	cd web && npm install --no-fund --no-audit && npm run build

## build: compile the single binary (SPA must be built first)
build:
	go build -o $(BINARY) ./cmd/server

## run: run the compiled binary
run:
	./$(BINARY)

## test: run the Go test suite
test:
	go test ./...

## db-up: start local Postgres
db-up:
	docker compose up -d postgres

## db-down: stop local Postgres (keeps the volume)
db-down:
	docker compose down

## db-reset: destroy the local database and its data, then re-migrate
db-reset:
	docker compose down -v && docker compose up -d postgres && sleep 3 && $(MAKE) migrate

## migrate: apply all pending migrations
migrate:
	go tool goose -dir $(MIGRATIONS) postgres "$(DATABASE_URL)" up

## migrate-down: roll back the most recent migration
migrate-down:
	go tool goose -dir $(MIGRATIONS) postgres "$(DATABASE_URL)" down

## migrate-status: show which migrations have run
migrate-status:
	go tool goose -dir $(MIGRATIONS) postgres "$(DATABASE_URL)" status

## migration: scaffold a migration, e.g. `make migration name=add_rooms`
migration:
	go tool goose -dir $(MIGRATIONS) -s create $(name) sql

## gen: regenerate type-safe query code from SQL
gen:
	go tool sqlc generate

## tidy: tidy Go modules
tidy:
	go mod tidy

.PHONY: help dev web build run test db-up db-down db-reset migrate migrate-down migrate-status migration gen tidy
