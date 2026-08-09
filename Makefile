.PHONY: build test vet check run dry-run v2-config v2-up v2-logs v2-down

GOCACHE ?= /tmp/binance-monitor-go-cache

build:
	mkdir -p bin
	GOCACHE=$(GOCACHE) go build -trimpath -o bin/binance-monitor ./cmd/binance-monitor

test:
	GOCACHE=$(GOCACHE) go test ./...

vet:
	GOCACHE=$(GOCACHE) go vet ./...

check: test vet

run:
	GOCACHE=$(GOCACHE) go run ./cmd/binance-monitor --daemon

dry-run:
	GOCACHE=$(GOCACHE) go run ./cmd/binance-monitor --dry-run

v2-config:
	docker compose --env-file .env.v2 -p binance-radar-v2 -f compose.v2.yaml config

v2-up:
	docker compose --env-file .env.v2 -p binance-radar-v2 -f compose.v2.yaml up -d --build

v2-logs:
	docker compose --env-file .env.v2 -p binance-radar-v2 -f compose.v2.yaml logs -f worker api

v2-down:
	docker compose --env-file .env.v2 -p binance-radar-v2 -f compose.v2.yaml down
