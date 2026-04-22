.PHONY: help test test-cover vet lint build build-cli examples tidy clean all

help: ## Show this help message
	@awk 'BEGIN {FS = ":.*##"; printf "Usage:\n  make \033[36m<target>\033[0m\n\nTargets:\n"} /^[a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

test: ## Run tests with race detector
	go test -race -count=1 -timeout 5m ./...

test-cover: ## Run tests with coverage report
	go test -race -coverprofile=coverage.out ./... && go tool cover -func=coverage.out

vet: ## Run go vet
	go vet ./...

lint: ## Run golangci-lint
	golangci-lint run --timeout=5m

build: ## Build all packages
	go build ./...

build-cli: ## Build tinyagentsctl binary into bin/
	go build -o bin/tinyagentsctl ./cmd/tinyagentsctl

examples: ## Build example programs
	go build ./examples/...

tidy: ## Tidy and verify module dependencies
	go mod tidy && go mod verify

clean: ## Remove build artifacts
	rm -rf bin/ coverage.out

all: vet test build ## Run vet, test, and build
