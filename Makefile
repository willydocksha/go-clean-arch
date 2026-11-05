# load .env when present so every target sees the same config
ifneq (,$(wildcard .env))
include .env
export
endif

# variables
BIN_DIR := bin

.PHONY: help setup run build test cover lint tidy migrate-up migrate-down migrate-status migrate-new seed seed-fresh up down logs

# list targets
help:
	@grep -E '^[a-z-]+:' Makefile | cut -d: -f1

# first-time setup: env file, migrations, sample data
setup:
	@[ -f .env ] || (cp .env.example .env && echo "created .env from .env.example")
	@$(MAKE) --no-print-directory migrate-up seed

# run the api
run:
	go run ./cmd/api

# build all binaries into bin/
build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BIN_DIR)/api ./cmd/api
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BIN_DIR)/migrate ./cmd/migrate
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BIN_DIR)/seed ./cmd/seed

# run tests with race detector
test:
	go test -race -count=1 ./...

# coverage report
cover:
	go test -coverprofile=coverage.out ./... && go tool cover -func=coverage.out

# static checks
lint:
	go vet ./...

# tidy modules
tidy:
	go mod tidy

# apply pending migrations
migrate-up:
	go run ./cmd/migrate up

# roll back migrations, usage: make migrate-down n=1
migrate-down:
	go run ./cmd/migrate down $(or $(n),1)

# show migration status
migrate-status:
	go run ./cmd/migrate status

# create migration files, usage: make migrate-new name=create_orders_table
migrate-new:
	@[ -n "$(name)" ] || (echo "usage: make migrate-new name=create_orders_table" && exit 1)
	@last=$$(ls migrations/*.up.sql 2>/dev/null | sed -E 's#.*/0*([0-9]+)_.*#\1#' | sort -n | tail -1); \
	next=$$(printf "%06d" $$(( $${last:-0} + 1 ))); \
	touch migrations/$${next}_$(name).up.sql migrations/$${next}_$(name).down.sql; \
	echo "created migrations/$${next}_$(name).{up,down}.sql"

# insert sample data (idempotent)
seed:
	go run ./cmd/seed

# wipe users & products then seed again
seed-fresh:
	go run ./cmd/seed -fresh

# docker compose stack
up:
	docker compose up -d --build

down:
	docker compose down

logs:
	docker compose logs -f api
