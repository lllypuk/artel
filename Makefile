.PHONY: preflight build lint test test-race test-e2e package-check observer-build observer-check check

GOLANGCI_LINT ?= golangci-lint
export GOLANGCI_LINT

preflight:
	./scripts/preflight

build: preflight
	mkdir -p build/bin
	go build -o build/bin/arteld ./cmd/arteld
	go build -o build/bin/artelctl ./cmd/artelctl
	./desktop/bin/build
	$(MAKE) observer-build

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

test-e2e: preflight observer-build
	./scripts/e2e

# Dependencies are installed explicitly in this private workspace with npm ci
# --ignore-scripts; never install tools or alter global Pi configuration here.
observer-build:
	./scripts/preflight --observer
	npm --prefix integrations/pi-observer run build

observer-check:
	./scripts/preflight --observer
	npm --prefix integrations/pi-observer run check
	go test ./internal/api -run '^TestObserverOpenAPI$$' -count=1

package-check:
	./scripts/package-check

# Recursive calls keep Qt builds serial even when make check is invoked with -j.
check:
	$(MAKE) lint
	$(MAKE) test
	$(MAKE) test-race
	$(MAKE) build
	$(MAKE) observer-check
	$(MAKE) test-e2e
