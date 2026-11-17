.PHONY: preflight build lint test test-race test-e2e package-check check

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
	python3 -B -m unittest discover -s tests -p 'test_*.py'
	go test ./...
	./desktop/bin/test

test-race: preflight
	go test -race ./...

test-e2e: preflight
	./scripts/e2e

package-check:
	./scripts/package-check

# Recursive calls keep Qt builds serial even when make check is invoked with -j.
check:
	$(MAKE) lint
	$(MAKE) test
	$(MAKE) test-race
	$(MAKE) build
	$(MAKE) test-e2e
