SHELL := bash
.DEFAULT_GOAL := help

.PHONY: help
help: ## Show available targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-12s %s\n", $$1, $$2}'

.PHONY: minio-image
minio-image: ## Build MinIO and mc from pinned sources
	docker build -t disavery/minio:local images/minio

.PHONY: test
test: minio-image ## Run all Go tests (integration tests need Docker)
	go test ./...

.PHONY: test-short
test-short: ## Run unit tests only
	go test -short ./...

.PHONY: lint
lint: ## Run golangci-lint
	golangci-lint run
