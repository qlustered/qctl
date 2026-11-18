.PHONY: build clean install test lint fmt help generate generate-client gotchas-sweep release

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
# Generated into $(BUILD_DIR) first, moved into place only once the generator
# succeeds: redirecting straight at the tracked file would truncate it before
# extract-spec-metadata.go got the chance to fail on a moved x- extension.
# The .tmp suffix keeps a half-written file out of `go build ./...`.
METADATA_TMP_FILE := $(BUILD_DIR)/metadata.gen.go.tmp
METADATA_FILE := internal/api/metadata.gen.go
# The manifest form of the settings model, derived from the freshly generated
# api.SettingsSchema rather than from the spec -- so unlike the two above it
# needs no download, and TestSettingsModelMirrorsTheWireSchema can check it is
# current on every `make test`. Same tmp-then-mv rule, for the reason the .tmp
# suffix is there above: a direct `> internal/datasets/settings_model.gen.go`
# truncates the committed file the moment the shell opens it, so a generator that
# then refuses a shape it will not guess at leaves the package empty and
# unbuildable, with nothing to diff against. (It does NOT need its own output to
# compile -- it imports internal/api only -- so the redirect would otherwise
# work.)
SETTINGS_MODEL_TMP_FILE := $(BUILD_DIR)/settings_model.gen.go.tmp
SETTINGS_MODEL_FILE := internal/datasets/settings_model.gen.go
# The CloudSource manifest's spec fields, derived the same way from
# api.DataSourceModelPatchRequestSchema; TestCloudSourceSpecCoversEveryPatchKey
# checks its key set and TestCloudSourceSpecMirrorsThePatchSchema each field's
# shape.
CLOUD_SOURCE_SPEC_TMP_FILE := $(BUILD_DIR)/cloud_source_spec.gen.go.tmp
CLOUD_SOURCE_SPEC_FILE := internal/cloud_sources/spec.gen.go
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
	@echo "Extracting spec metadata extensions..."
	@go run scripts/extract-spec-metadata.go < $(OPENAPI_RAW_FILE) > $(METADATA_TMP_FILE)
	@gofmt -w $(METADATA_TMP_FILE)
	@mv $(METADATA_TMP_FILE) $(METADATA_FILE)
	@echo "Spec metadata generated at $(METADATA_FILE)"
	@echo "Generating the settings manifest..."
	@go run scripts/generate-settings-model.go > $(SETTINGS_MODEL_TMP_FILE)
	@gofmt -w $(SETTINGS_MODEL_TMP_FILE)
	@mv $(SETTINGS_MODEL_TMP_FILE) $(SETTINGS_MODEL_FILE)
	@echo "Settings manifest generated at $(SETTINGS_MODEL_FILE)"
	@echo "Generating the cloud source manifest spec..."
	@go run scripts/generate-cloud-source-spec.go > $(CLOUD_SOURCE_SPEC_TMP_FILE)
	@gofmt -w $(CLOUD_SOURCE_SPEC_TMP_FILE)
	@mv $(CLOUD_SOURCE_SPEC_TMP_FILE) $(CLOUD_SOURCE_SPEC_FILE)
	@echo "Cloud source manifest spec generated at $(CLOUD_SOURCE_SPEC_FILE)"
	@echo "Client generated at internal/api/client.gen.go"
	@echo "NEXT: backend semantics may have moved — run 'make gotchas-sweep', re-verify every [backend] page gotcha, and consult docs/gotchas-sweep.md"

## gotchas-sweep: Report generated drift and summarize manual backend reviews
gotchas-sweep:
	@if [ -d tools/gotchassweep ]; then go run ./tools/gotchassweep; \
	else echo "gotchas-sweep: skipped, tools/gotchassweep is not in this tree"; fi

## release: Tag [TAG=X.Y.Z, default the next patch on atlas master's minor] [REF=<commit>] and commit its public snapshot to the mirror; PUSH=1 pushes without asking
release:
	@if [ -x mirror/release.sh ]; then TAG='$(TAG)' REF='$(REF)' PUSH='$(PUSH)' mirror/release.sh; \
	else echo "release: mirror/release.sh is not in this tree; releases are cut from the development repository" >&2; exit 1; fi
