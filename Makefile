SHELL := /bin/bash

IMAGE          ?= ghcr.io/octabits-io/cert-manager-webhook-porkbun
VERSION        ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
CHART_DIR      := charts/cert-manager-webhook-porkbun
PLATFORMS      ?= linux/amd64,linux/arm64
GROUP_NAME     ?= acme.example.com

# Conformance tests need real Porkbun credentials and a real domain; see
# testdata/README.md. They are skipped unless TEST_ZONE_NAME is set.
TEST_ZONE_NAME ?=

.DEFAULT_GOAL := check

.PHONY: check
check: tidy-check vet test lint-chart template ## Run everything CI runs

.PHONY: test
test: ## Unit tests with the race detector
	CGO_ENABLED=1 go test -race -count=1 ./...

.PHONY: test-conformance
test-conformance: ## cert-manager DNS01 conformance suite (needs real credentials)
	@if [ -z "$(TEST_ZONE_NAME)" ]; then \
		echo "TEST_ZONE_NAME is not set; see testdata/README.md"; exit 1; \
	fi
	TEST_ZONE_NAME=$(TEST_ZONE_NAME) go test -count=1 -tags=conformance -timeout=30m .

.PHONY: vet
vet: ## go vet
	go vet ./...

.PHONY: lint
lint: ## golangci-lint (containerised, no local install needed)
	docker run --rm -v "$(PWD)":/app -w /app golangci/golangci-lint:latest golangci-lint run --timeout=5m

.PHONY: tidy
tidy: ## Update go.mod/go.sum
	go mod tidy

.PHONY: tidy-check
tidy-check: ## Fail if go.mod/go.sum are not tidy
	@cp go.mod go.mod.bak && cp go.sum go.sum.bak
	@go mod tidy
	@if ! diff -q go.mod go.mod.bak >/dev/null || ! diff -q go.sum go.sum.bak >/dev/null; then \
		mv go.mod.bak go.mod; mv go.sum.bak go.sum; \
		echo "go.mod/go.sum are not tidy; run 'make tidy'"; exit 1; \
	fi
	@rm -f go.mod.bak go.sum.bak

.PHONY: build
build: ## Build the image for the host platform
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) .

.PHONY: build-multiarch
build-multiarch: ## Build (but do not push) the multi-arch manifest
	docker buildx build --platform=$(PLATFORMS) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) .

.PHONY: scan
scan: build ## Fail on HIGH/CRITICAL vulnerabilities in the image
	docker run --rm -v /var/run/docker.sock:/var/run/docker.sock \
		aquasec/trivy:latest image --severity HIGH,CRITICAL --exit-code 1 \
		--ignore-unfixed $(IMAGE):$(VERSION)

.PHONY: lint-chart
lint-chart: ## helm lint
	helm lint $(CHART_DIR) --set groupName=$(GROUP_NAME)

.PHONY: template
template: ## Render the chart and validate the output against Kubernetes schemas
	@helm template release $(CHART_DIR) -n cert-manager --set groupName=$(GROUP_NAME) > /tmp/cmwp-rendered.yaml
	@docker run --rm -v /tmp/cmwp-rendered.yaml:/r.yaml ghcr.io/yannh/kubeconform:latest \
		-strict -summary -ignore-missing-schemas /r.yaml

.PHONY: package
package: ## Package the chart
	helm package $(CHART_DIR) -d dist/

.PHONY: help
help: ## List targets
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'
