BINARY := viktor
BIN_DIR := bin
PKG := ./...
GO := go
COMPOSE := docker compose -f docker-compose.dev.yml

.PHONY: build test test-integration lint fmt vet clean run ci install dev-up dev-down dev-logs dev-reset surface-update surface-check

build:
	$(GO) build -trimpath -ldflags "-s -w" -o $(BIN_DIR)/$(BINARY) ./cmd/$(BINARY)

test:
	$(GO) test -race -count=1 $(PKG)

# Card #844: live-Qdrant integration suite against the target instance
# (no Docker spin-up; a per-run collection is created and dropped).
# Override with QDRANT_TEST_URL / QDRANT_TEST_API_KEY if it moves.
QDRANT_TEST_URL ?= http://localhost:16333
QDRANT_TEST_API_KEY ?=
test-integration:
	@set +e; VIKTOR_QDRANT_TEST_URL="$(QDRANT_TEST_URL)" VIKTOR_QDRANT_TEST_API_KEY="$(QDRANT_TEST_API_KEY)" $(GO) test -race -count=1 -run TestQdrantIntegration $(PKG); exit $$status

lint:
	golangci-lint run

fmt:
	$(GO) fmt ./...

vet:
	$(GO) vet $(PKG)

run: build
	./$(BIN_DIR)/$(BINARY)

clean:
	rm -rf $(BIN_DIR)

ci: lint vet test build

# Regenerate the tool-surface artifacts in one go: golden.json, the
# README tools table, and the hardcoded tool counters. Run after any
# change to the registered tool set; commit the result.
surface-update:
	$(GO) test ./internal/tools -run TestGoldenSnapshot -update-snapshot
	$(GO) test ./internal/tools -run TestToolsDocTable -update-doc
	python3 scripts/surface_update.py
	$(GO) test ./internal/tools -run 'TestGoldenSnapshot|TestToolsDocTable' -count=1

# CI guard: fail on any drift between the registered tool set, the
# golden snapshot, the README table, and the hardcoded counters.
surface-check:
	$(GO) test ./internal/tools -run 'TestGoldenSnapshot|TestToolsDocTable' -count=1
	$(GO) test ./internal/server -run TestBuildE2EToolsList -count=1

dev-up:
	$(COMPOSE) up -d

dev-down:
	$(COMPOSE) down

dev-logs:
	$(COMPOSE) logs -f --tail=100

dev-reset:
	$(COMPOSE) down -v

PREFIX ?= $(HOME)/.local

.PHONY: install
install: build
	install -d $(PREFIX)/bin
	install -m 0755 $(BIN_DIR)/$(BINARY) $(PREFIX)/bin/$(BINARY)
