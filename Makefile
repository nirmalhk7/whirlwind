.PHONY: dev run dashboard check test test-go test-node vet build fmt-check format tools architecture-check quality

GO ?= go
NODE ?= node
PYTHON ?= python3
NODE_TESTS := $(wildcard scripts/*.test.mjs bridges/*/*.test.mjs)

dev: dashboard

run:
	$(GO) run ./cmd/whirlwind --supervisor

dashboard:
	./bin/whirlwind dashboard

check: fmt-check architecture-check test vet build

architecture-check:
	$(GO) test ./internal/architecture

test: test-go test-node

test-go:
	$(GO) test -race ./...

test-node:
	$(NODE) --test $(NODE_TESTS)

vet:
	$(GO) vet ./...

build:
	$(GO) build ./cmd/...

# Fail on formatting drift without rewriting source files.
fmt-check:
	PYTHON=$(PYTHON) $(NODE) scripts/format-source.mjs

format:
	PYTHON=$(PYTHON) $(NODE) scripts/format-source.mjs --write

tools:
	npm ci --ignore-scripts
	$(PYTHON) -m pip install -r requirements-dev.txt
	GOBIN=$(CURDIR)/.tools/bin $(GO) install golang.org/x/tools/cmd/goimports@v0.38.0

quality:
	qlty smells --all
	qlty check --all
