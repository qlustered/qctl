.PHONY: build clean install test lint fmt help generate generate-client

# Build variables
BINARY_NAME=qctl
VERSION?=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT?=$(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DIR=./build
LDFLAGS=-ldflags "-X github.com/qlustered/qctl/internal/version.Version=$(VERSION) -X github.com/qlustered/qctl/internal/version.Commit=$(COMMIT)"

## help: Display this help message
help:
	@echo "Available targets:"
	@grep -E '^## ' Makefile | sed 's/^## /  /'

## build: Build the qctl binary
build:
	@echo "Building $(BINARY_NAME) $(VERSION) ($(COMMIT))..."
	go build $(LDFLAGS) -o $(BINARY_NAME) ./cmd/qctl

## install: Install qctl to GOPATH/bin
install:
	@echo "Installing $(BINARY_NAME)..."
	go install $(LDFLAGS) ./cmd/qctl

## clean: Remove build artifacts
clean:
	@echo "Cleaning..."
	rm -f $(BINARY_NAME)
	rm -rf $(BUILD_DIR)/dist

## test: Run tests
test:
	go run ./tools/testsummary $(TEST_ARGS)

## lint: Run golangci-lint
lint:
	golangci-lint run

## fmt: Format code
fmt:
	go fmt ./...
	gofumpt -l -w .

## tidy: Tidy go modules
tidy:
	go mod tidy

## generate: Generate code from OpenAPI spec
generate:
	@echo "Running code generation..."
	go generate ./...

# OpenAPI client generation variables
API_URL ?= http://localhost:8000/api/docs/openapi.json
OPENAPI_RAW_FILE := $(BUILD_DIR)/openapi.raw.json
OPENAPI_CODEGEN_FILE := $(BUILD_DIR)/openapi.codegen.json
OAPI_CODEGEN_VERSION := v2.4.1

## generate-client: Download OpenAPI spec and generate Go client
generate-client:
	@mkdir -p $(BUILD_DIR)
	@echo "Downloading OpenAPI spec from $(API_URL)..."
	@curl -fsSL "$(API_URL)" -o "$(OPENAPI_RAW_FILE)" || { \
		echo "ERROR: Failed to download OpenAPI spec from $(API_URL)"; \
		echo "Make sure the API server is running or provide a valid API_URL."; \
		echo "Usage: make generate-client API_URL=https://your-server/api/docs/openapi.json"; \
		rm -f "$(OPENAPI_RAW_FILE)"; \
		exit 1; \
	}
	@echo "Converting OpenAPI 3.1 to 3.0.3 compatible format..."
	@go run scripts/convert-openapi-31-to-30.go < $(OPENAPI_RAW_FILE) > $(OPENAPI_CODEGEN_FILE)
	@echo "Generating API client..."
	@cd internal/api && go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$(OAPI_CODEGEN_VERSION) \
		-config oapi-codegen.yaml \
		../../$(OPENAPI_CODEGEN_FILE)
	@echo "Client generated at internal/api/client.gen.go"

## release: Build release binaries (requires goreleaser)
release:
	goreleaser release --snapshot --clean
