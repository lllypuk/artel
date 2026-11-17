.PHONY: preflight build lint test test-race

GOLANGCI_LINT ?= golangci-lint
export GOLANGCI_LINT

preflight:
	./scripts/preflight

build: preflight
	mkdir -p build/bin
	go build -o build/bin/arteld ./cmd/arteld
	go build -o build/bin/artelctl ./cmd/artelctl
	./desktop/bin/build

lint: preflight
	@test -z "$$(gofmt -l cmd internal)" || { gofmt -l cmd internal; exit 1; }
	$(GOLANGCI_LINT) run ./...
	./scripts/desktop-lint

test: preflight
	go test ./...
	./desktop/bin/test

test-race: preflight
	go test -race ./...
