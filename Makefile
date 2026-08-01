.PHONY: build test vet check run dry-run

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
