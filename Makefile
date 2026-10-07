SHELL := bash
.DEFAULT_GOAL := help

.PHONY: help
help: ## Show available targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-12s %s\n", $$1, $$2}'

.PHONY: test
test: ## Run all Go tests (integration tests need Docker)
	go test ./...

.PHONY: test-short
test-short: ## Run unit tests only
	go test -short ./...

.PHONY: lint
lint: ## Run golangci-lint
	golangci-lint run
