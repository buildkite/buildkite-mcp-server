# Variables
BINARY_NAME := buildkite-mcp-server
CMD_PATH := ./cmd/$(BINARY_NAME)
COVERAGE_FILE := coverage.out

# Default target
.DEFAULT_GOAL := build

# Help target
.PHONY: help
help: ## Show this help message
	@echo "Available targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-15s %s\n", $$1, $$2}'

.PHONY: build
build: ## Build the binary
	go build -o $(BINARY_NAME) $(CMD_PATH)/main.go

.PHONY: install
install: ## Install the binary
	go install ./cmd/...

.PHONY: snapshot
snapshot: ## Build snapshot with goreleaser
	goreleaser build --snapshot --clean --single-target

.PHONY: update-docs
update-docs: ## Update documentation
	go run cmd/update-docs/main.go

.PHONY: run
run: ## Run the application with stdio
	go run $(CMD_PATH)/main.go stdio

.PHONY: test
test: ## Run tests with coverage
	go test -coverprofile $(COVERAGE_FILE) -covermode atomic -v ./...

.PHONY: test-coverage
test-coverage: test ## Run tests and show coverage report
	go tool cover -html=$(COVERAGE_FILE)

.PHONY: lint
lint: ## Run linter
	golangci-lint run ./...

.PHONY: lint-fix
lint-fix: ## Run linter with auto-fix
	golangci-lint run --fix ./...

.PHONY: clean
clean: ## Clean build artifacts
	rm -f $(BINARY_NAME) $(COVERAGE_FILE)
	go clean

.PHONY: deps
deps: ## Download and tidy dependencies
	go mod download
	go mod tidy

.PHONY: check
check: lint test ## Run all checks (lint + test)

.PHONY: all
all: clean deps check build ## Run full build pipeline
