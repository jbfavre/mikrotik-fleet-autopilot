.PHONY: help build test test-coverage test-verbose clean install lint fmt vet check-fmt run dev

# Binary name
BINARY_NAME=mikrotik-fleet-autopilot
BUILD_DIR=bin
GO=go

# Version info (can be overridden)
VERSION?=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT?=$(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME?=$(shell date -u '+%Y-%m-%d_%H:%M:%S')

# Linker flags to set version info
LDFLAGS=-ldflags "-X main.Version=$(VERSION) -X main.Commit=$(COMMIT) -X main.BuildTime=$(BUILD_TIME)"

# Help target - default when running just "make"
help: ## Show this help message
	@echo "Available targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'

build: ## Build the binary
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	$(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) .

install: ## Install the binary to $GOPATH/bin
	@echo "Installing $(BINARY_NAME)..."
	$(GO) install $(LDFLAGS) .

clean: ## Remove build artifacts
	@echo "Cleaning..."
	@rm -rf $(BUILD_DIR)
	@$(GO) clean

test: ## Run all tests
	@echo "Running tests..."
	$(GO) test -v ./...

test-benchmark: ## Run benchmarks
	@echo "Running benchmarks..."
	$(GO) test -bench=. ./...

test-coverage: ## Run tests with coverage report
	@echo "Running tests with coverage..."
	@mkdir -p $(BUILD_DIR)
	@$(GO) test -coverprofile=$(BUILD_DIR)/coverage.out ./...
	@echo "\n=== Coverage by Package ==="
	@$(GO) tool cover -func=$(BUILD_DIR)/coverage.out | grep -v "^mode:"
	@echo "\n=== Total Coverage ==="

lint: ## Run linter (requires golangci-lint)
	@if command -v golangci-lint >/dev/null 2>&1; then \
		echo "Running linter..."; \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not installed. Install with: brew install golangci-lint"; \
		exit 1; \
	fi

fmt: ## Format code with gofmt
	@echo "Formatting code..."
	@$(GO) fmt ./...

check-fmt: ## Check if code is formatted
	@echo "Checking code formatting..."
	@if [ -n "$$(gofmt -l .)" ]; then \
		echo "Code is not formatted. Run 'make fmt' to fix:"; \
		gofmt -l .; \
		exit 1; \
	fi

vet: ## Run go vet
	@echo "Running go vet..."
	@$(GO) vet ./...

check: check-fmt vet test ## Run all checks (format, vet, test)

run: build ## Build and run (usage: make run ARGS="updates --help")
	@$(BUILD_DIR)/$(BINARY_NAME) $(ARGS)

dev: ## Run directly without building binary (usage: make dev ARGS="updates --help")
	@$(GO) run . $(ARGS)

deps: ## Download dependencies
	@echo "Downloading dependencies..."
	@$(GO) mod download

tidy: ## Tidy go.mod
	@echo "Tidying go.mod..."
	@$(GO) mod tidy

verify: ## Verify dependencies
	@echo "Verifying dependencies..."
	@$(GO) mod verify

# Quick dev workflow
quick: fmt vet test ## Quick check before commit (fmt + vet + test)

# Full CI-like workflow
ci: clean check-fmt vet test-coverage ## Full CI workflow

# List all subcommands
list-cmds: ## List all available subcommands
	@echo "Available subcommands:"
	@ls -1 cmd/ | grep -v "_test.go"

.DEFAULT_GOAL := help
