BINARY := viktor
BIN_DIR := bin
PKG := ./...
GO := go
COMPOSE := docker compose -f docker-compose.dev.yml

.PHONY: build test lint fmt vet clean run ci install dev-up dev-down dev-logs dev-reset

build:
	$(GO) build -trimpath -ldflags "-s -w" -o $(BIN_DIR)/$(BINARY) ./cmd/$(BINARY)

test:
	$(GO) test -race -count=1 $(PKG)

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
