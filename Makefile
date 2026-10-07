SHELL := bash
.DEFAULT_GOAL := help

# Without provenance attestations a cached rebuild keeps the same image ID, so
# Terraform does not replace every node on the next apply.
BUILD_FLAGS := --provenance=false

.PHONY: help
help: ## Show available targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-12s %s\n", $$1, $$2}'

.PHONY: minio-image
minio-image: ## Build MinIO and mc from pinned sources
	docker build $(BUILD_FLAGS) -t disavery/minio:local images/minio

.PHONY: test
test: minio-image ## Run all Go tests (integration tests need Docker)
	go test ./...

.PHONY: test-short
test-short: ## Run unit tests only
	go test -short ./...

.PHONY: lint
lint: ## Run golangci-lint
	golangci-lint run

TB := docker compose exec -T toolbox

.PHONY: toolbox
toolbox: minio-image ## Build and start the toolbox container
	docker compose up -d --build toolbox

.PHONY: images
images: minio-image ## Build the node and MinIO images
	docker build $(BUILD_FLAGS) -t disavery/node:local images/node

.PHONY: secrets
secrets: toolbox ## Generate local secrets once (SOPS + age, escrow copy)
	$(TB) bash scripts/init-secrets.sh

TF_DIR := infra/terraform/envs/local

.PHONY: infra
infra: toolbox ## Create networks and containers with Terraform
	$(TB) bash -c 'cd $(TF_DIR) && terraform init -input=false && terraform workspace select -or-create drill && terraform apply -input=false -auto-approve'

.PHONY: down
down: ## Destroy Terraform-managed containers (keeps secrets and toolbox)
	$(TB) bash -c 'cd $(TF_DIR) && terraform workspace select drill && terraform destroy -input=false -auto-approve'

.PHONY: configure
configure: toolbox ## Configure all nodes with Ansible
	$(TB) ansible-playbook infra/ansible/playbooks/site.yml

.PHONY: build
build: toolbox ## Build Linux binaries into build/
	$(TB) bash -c 'CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o build/ ./cmd/docsvc ./cmd/webhookmock'

.PHONY: up
up: images secrets build infra configure ## Bring the whole lab up (idempotent)

.PHONY: smoke
smoke: ## Run the end-to-end smoke test
	$(TB) bash scripts/smoke.sh

.PHONY: destroy
destroy: down ## Remove everything, including secrets, escrow and the toolbox
	docker compose down -v
