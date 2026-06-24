## Makefile for db-auth-gateway
## Inspired by patterns from grafana/mimir and grafana/loki

SHELL = /usr/bin/env bash -o pipefail

GO_VERSION  ?= 1.26.1
GIT_REVISION := $(shell git rev-parse --short HEAD)
GIT_BRANCH   := $(shell git rev-parse --abbrev-ref HEAD)
GO_FLAGS     := -ldflags "-s -w \
    -X main.Version=$(GIT_REVISION) \
    -X main.Branch=$(GIT_BRANCH) \
    -X main.Revision=$(GIT_REVISION)"

IMAGE_PREFIX          ?= grafana/db-auth-gateway
GOLANGCI_LINT_VERSION ?= v2.11.3

DB_AUTH_GATEWAY_IMAGE ?= $(IMAGE_PREFIX):$(GIT_REVISION)
# renovate: datasource=docker depName=grafana/loki
LOKI_IMAGE  ?= grafana/loki:3.7.1
# LBAC-capable Mimir build. LBAC (the -auth.label-access-control-enabled flag and
# X-Prom-Label-Policy enforcement) is not in any stable Mimir release yet, so we pin a
# weekly r400 image that contains it (grafana/mimir#15554). Revert to a stable
# grafana/mimir:<version> tag (and restore the renovate annotation) once LBAC ships in a
# release. The whole e2e suite runs against this image.
MIMIR_IMAGE ?= grafana/mimir:r400-c18b9d72
# renovate: datasource=docker depName=grafana/tempo
TEMPO_IMAGE ?= grafana/tempo:2.10.3

.PHONY: all build test test-race test-e2e lint fmt check-fmt check mod-check tidy install-hooks docker-build clean help check-license reference-help check-reference-help

HELP_REF_DIR := cmd/db-auth-gateway

-include Makefile.local

all: build ## Default target: build the binary

build: ## Build the db-auth-gateway binary into bin/
	CGO_ENABLED=0 go build $(GO_FLAGS) -o bin/db-auth-gateway ./cmd/db-auth-gateway

test: ## Run all tests with coverage
	@pkgs=$$(go list ./... 2>/dev/null); \
	if [ -z "$$pkgs" ]; then echo "No packages found, skipping tests."; \
	else go test -timeout 30m -coverprofile=coverage.txt $$pkgs; fi

test-race: ## Run all tests with race detector
	@pkgs=$$(go list ./... 2>/dev/null); \
	if [ -z "$$pkgs" ]; then echo "No packages found, skipping tests."; \
	else go test -race -timeout 30m $$pkgs; fi

test-e2e: docker-build ## Build Docker images then run e2e tests against real containers
	tmp=$$(mktemp -d); \
	DB_AUTH_GATEWAY_IMAGE=$(DB_AUTH_GATEWAY_IMAGE) \
	LOKI_IMAGE=$(LOKI_IMAGE) \
	MIMIR_IMAGE=$(MIMIR_IMAGE) \
	TEMPO_IMAGE=$(TEMPO_IMAGE) \
	E2E_TEMP_DIR=$$tmp \
	go test -count=1 -v -tags=requires_docker -timeout 10m $(if $(RUN),-run $(RUN)) ./test/e2e

lint: ## Run golangci-lint (pinned version, downloaded via go run)
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run --build-tags=requires_docker ./...

fmt: ## Format Go source files
	gofmt -w ./cmd ./pkg ./test

check-fmt: ## Check Go source files are formatted (non-zero exit if not)
	@out=$$(gofmt -l ./cmd ./pkg ./test); \
	if [ -n "$$out" ]; then \
		echo "The following files need formatting:"; \
		echo "$$out"; \
		exit 1; \
	fi

check: fmt lint test ## Format, lint, and test

mod-check: ## Verify go.mod and go.sum are tidy
	go mod verify
	go mod tidy
	git diff --exit-code -- go.mod $(wildcard go.sum)

tidy: ## Run go mod tidy
	go mod tidy

install-hooks: ## Install git hooks via pre-commit framework
	pre-commit install

docker-build: ## Build Docker image (use GIT_REVISION or IMAGE_TAG= to tag)
	docker build \
		--build-arg VERSION=$(GIT_REVISION) \
		-t $(IMAGE_PREFIX):$(GIT_REVISION) \
		-f cmd/db-auth-gateway/Dockerfile .

clean: ## Remove build artifacts
	rm -rf bin/

check-license: ## Fail if any Go source file is missing the SPDX license header
	@missing=$$(find cmd pkg test -name '*.go' -print0 \
		| xargs -0 grep -L '^// SPDX-License-Identifier: AGPL-3.0-only'); \
	if [ -n "$$missing" ]; then \
		echo "The following files are missing the SPDX license header:"; \
		echo "$$missing"; \
		echo "Add '// SPDX-License-Identifier: AGPL-3.0-only' as the first line."; \
		exit 1; \
	fi

reference-help: build ## Regenerate the CLI flag reference file
	@(./bin/db-auth-gateway -help || true) > $(HELP_REF_DIR)/generated-help.txt

check-reference-help: reference-help ## Fail if the CLI flag reference file is out of date
	@git diff --exit-code -- $(HELP_REF_DIR)/generated-help.txt \
		|| (echo "CLI flag reference is out of date. Run 'make reference-help' and commit the result." && false)

help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"; printf "Usage:\n  make \033[36m<target>\033[0m\n\nTargets:\n"} \
	/^[a-zA-Z_-]+:.*##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)
