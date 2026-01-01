# vCluster Generic Sync Plugin Makefile

# Variables
PLUGIN_IMAGE ?= ghcr.io/kupe/vcluster-generic-sync-plugin
VERSION ?= local
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
GOOS ?= linux
GOARCH ?= amd64
CLUSTER_NAME ?= vcluster-plugin-dev
E2E_CLUSTER_NAME ?= vcluster-generic-sync-e2e
E2E_KUBECONFIG_OUT ?= .e2e-kubeconfig

# Go settings
GO := go
GOFLAGS := -mod=vendor

# Linker flags for version injection
LDFLAGS := -X github.com/kupecloud/vcluster-generic-sync-plugin/syncers.Version=$(VERSION) \
           -X github.com/kupecloud/vcluster-generic-sync-plugin/syncers.GitCommit=$(GIT_COMMIT) \
           -X github.com/kupecloud/vcluster-generic-sync-plugin/syncers.BuildDate=$(BUILD_DATE)

.PHONY: all build test lint clean docker-build docker-push dev dev-build dev-deploy dev-purge vendor tidy kind-create kind-delete logs logs-tail logs-all version e2e e2e-debug e2e-clean

all: build

## Build

build: ## Build the plugin binary for linux/amd64
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o plugin main.go

build-local: ## Build for local OS
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o plugin main.go

version: ## Display version information
	@echo "Version:    $(VERSION)"
	@echo "Git Commit: $(GIT_COMMIT)"
	@echo "Build Date: $(BUILD_DATE)"

## Dependencies

tidy: ## Run go mod tidy
	$(GO) mod tidy

vendor: tidy ## Vendor dependencies
	$(GO) mod vendor

## Testing

test: ## Run unit tests
	$(GO) test $(GOFLAGS) -v ./...

test-coverage: ## Run tests with coverage
	$(GO) test $(GOFLAGS) -v -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html

e2e: ## Run E2E tests (default behavior)
	@KIND_CLUSTER_NAME=$(E2E_CLUSTER_NAME) E2E_CLUSTER_BASE_PATH=~/git/kupe/helm-charts/cluster-base $(GO) test $(GOFLAGS) -count=1 -tags=e2e -timeout=30m -v ./test/e2e; status=$$?; \
	$(GO) clean -testcache; \
	exit $$status

e2e-debug: ## Run E2E tests and keep the kind cluster for debugging
	@E2E_KEEP_CLUSTER=true E2E_KEEP_RESOURCES=true E2E_KUBECONFIG_OUT=$(E2E_KUBECONFIG_OUT) E2E_LOG_CMD_OUTPUT=true E2E_CLUSTER_BASE_PATH=~/git/kupe/helm-charts/cluster-base \
	KIND_CLUSTER_NAME=$(E2E_CLUSTER_NAME) $(GO) test $(GOFLAGS) -count=1 -tags=e2e -timeout=30m -v ./test/e2e; status=$$?; \
	$(GO) clean -testcache; \
	exit $$status

e2e-clean: ## Delete the E2E kind cluster
	@echo "Deleting E2E Kind cluster: $(E2E_CLUSTER_NAME)"
	@kind delete cluster --name $(E2E_CLUSTER_NAME) || true
	@rm -f $(E2E_KUBECONFIG_OUT)
	@$(GO) clean -testcache

## Linting

lint: ## Run linter
	golangci-lint run ./...

fmt: ## Format code
	$(GO) fmt ./...
	gofmt -s -w .

## Docker

docker-build: ## Build Docker image
	docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg GIT_COMMIT=$(GIT_COMMIT) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-t $(PLUGIN_IMAGE):$(VERSION) .

docker-push: docker-build ## Push Docker image
	docker push $(PLUGIN_IMAGE):$(VERSION)

## Development

dev: build ## Start DevSpace dev mode (builds binary, syncs to container, starts vCluster)
	devspace dev -n vcluster

dev-build: build ## Rebuild plugin binary (triggers DevSpace sync and container restart)
	@echo "Binary rebuilt - DevSpace will sync and restart the container"

dev-deploy: ## Deploy vCluster without dev mode
	devspace deploy -n vcluster

dev-purge: ## Cleanup DevSpace environment
	devspace purge -n vcluster

## Logging - filter plugin logs from vCluster logs
## vCluster adds "component":"/plugins/generic-sync" to all plugin logs

VCLUSTER_POD := $(shell kubectl get pods -n vcluster -l app=vcluster -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
PLUGIN_FILTER := grep -E '/plugins/generic-sync'

logs: ## Get plugin-only logs from vCluster pod
	@if [ -z "$(VCLUSTER_POD)" ]; then echo "No vCluster pod found in namespace 'vcluster'"; exit 1; fi
	@kubectl logs $(VCLUSTER_POD) -n vcluster -c syncer 2>&1 | $(PLUGIN_FILTER)

logs-tail: ## Tail plugin-only logs from vCluster pod (follow mode)
	@if [ -z "$(VCLUSTER_POD)" ]; then echo "No vCluster pod found in namespace 'vcluster'"; exit 1; fi
	@kubectl logs $(VCLUSTER_POD) -n vcluster -c syncer -f 2>&1 | $(PLUGIN_FILTER)

logs-all: ## Get ALL logs from vCluster pod (unfiltered)
	@if [ -z "$(VCLUSTER_POD)" ]; then echo "No vCluster pod found in namespace 'vcluster'"; exit 1; fi
	@kubectl logs $(VCLUSTER_POD) -n vcluster -c syncer

## Local testing with Kind

kind-create: ## Create local Kind cluster
	@kind create cluster --name $(CLUSTER_NAME)
	@echo ""
	@echo "Waiting for cluster to be ready..."
	@kubectl wait --for=condition=Ready nodes --all --timeout=300s

kind-delete: ## Delete Kind cluster
	@echo "Deleting Kind cluster: $(CLUSTER_NAME)"
	kind delete cluster --name $(CLUSTER_NAME)

## Cleanup

clean: ## Clean build artifacts
	rm -f plugin
	rm -rf bin/
	rm -f coverage.out coverage.html

## Help

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'
