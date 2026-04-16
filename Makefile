SHELL := /bin/bash

# Configurable variables
DB ?= open-democracy.db
ADDR ?= :8080
CRAWLER_BIN ?= open-democracy-crawler
SERVER_BIN ?= open-democracy-server
CRAWLER_FLAGS ?= $(FLAGS)

.PHONY: help build build-crawler build-server crawler server test templ clean

help:
	@echo "Available targets:"
	@echo "  make build          Build crawler and server binaries"
	@echo "  make build-crawler  Build crawler binary"
	@echo "  make build-server   Build server binary"
	@echo "  make crawler        Run crawler against DB (DB=$(DB))"
	@echo "                      Examples:"
	@echo "                        make crawler CRAWLER_FLAGS='--provincial'"
	@echo "                        make crawler -- --provincial"
	@echo "  make server         Start web service (DB=$(DB), ADDR=$(ADDR))"
	@echo "  make test           Run all tests"
	@echo "  make templ          Regenerate templ files"
	@echo "  make clean          Remove built binaries"

build: build-crawler build-server

build-crawler:
	go build -o $(CRAWLER_BIN) ./cmd/crawler

build-server:
	go build -o $(SERVER_BIN) ./cmd/server

crawler:
	go run ./cmd/crawler --db $(DB) $(CRAWLER_FLAGS) $(filter-out $@,$(MAKECMDGOALS))

ifneq (,$(filter crawler,$(MAKECMDGOALS)))
%:
	@:
endif

server:
	go run ./cmd/server -db $(DB) -addr $(ADDR)

test:
	go test ./...

templ:
	go run github.com/a-h/templ/cmd/templ@v0.3.1001 generate

clean:
	rm -f $(CRAWLER_BIN) $(SERVER_BIN)
