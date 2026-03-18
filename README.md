# qctl - Qluster CLI

CLI tool for managing Qluster platform resources.

## Overview

`qctl` is a command-line interface for managing Qluster deployments, datasets, ingestion pipelines, and related resources. It supports multiple deployment contexts, allowing you to manage different Qluster environments from a single CLI.

## Installation

### Build from Source

```bash
# Build the binary
make build

# Install to GOPATH/bin
make install
```

The binary will be created as `qctl` in the current directory (or installed to `$GOPATH/bin` with `make install`).

## Quick Start

### env vars

If you are running our atlas backend locally, then:

`cp .envrc.example .envrc`

You can tell your Go CLI to trust your mkcert root specifically for development without changing the code. Go respects the standard SSL_CERT_FILE environment variable

### Configure a Context

```bash
# Create a context for your local development environment
qctl config set-context dev \
  --server http://localhost:8000 \
  --user dev@example.com \
  --output table

# Create a context for production
qctl config set-context prod \
  --server https://api.qluster.example \
  --user admin@company.com

# List all contexts
qctl config list-contexts

# Switch to a different context
qctl config use-context prod

# View current context
qctl config current-context
```

### Global Flags

- `--server <url>` - Override the current context's server URL
- `--user <email>` - Override the current context's user email
- `--output <format>` - Output format: `table`, `json`, `yaml`, or `name`
- `--allow-insecure-http` - Allow non-localhost HTTP endpoints (use with caution)
- `--config <path>` - Custom config file location (default: `~/.qctl/config`)

## Development

### Prerequisites

- Go 1.25.4 or later
- Make

### Running Tests

Run the complete test suite with race detection and coverage:

```bash
make test
```

This will:
- Run all unit tests with the `-race` flag to detect race conditions
- Generate a coverage report in `coverage.out`
- Display verbose test output

#### View Test Coverage

After running tests, you can view detailed coverage:

```bash
# View coverage in terminal
go tool cover -func=coverage.out

# Generate HTML coverage report
go tool cover -html=coverage.out
```

#### Run Tests for Specific Packages

```bash
# Test a specific package
go test -v ./internal/config

# Test with coverage for a specific package
go test -v -coverprofile=coverage.out ./internal/config

# Run a specific test
go test -v -run TestValidateServer ./internal/config
```

#### Run Tests Without Race Detection (faster)

```bash
go test -v ./...
```

#### Re-run Only the Last Failing Tests

After a failing run, you can re-run just the failures (similar to `pytest --lf`):

```bash
make test TEST_ARGS=--lf
```

Because `make` treats leading dashes as its own flags, pass test flags via `TEST_ARGS`. Use `--last-failed` interchangeably with `--lf`.

### API Client Generation

The project uses [oapi-codegen](https://github.com/oapi-codegen/oapi-codegen) to generate a Go client from the backend's OpenAPI specification.

```bash
# Generate client (downloads spec from localhost:8000 by default)
make generate-client

# Generate from a custom API URL
make generate-client API_URL=https://api.example.com/api/docs/openapi.json

# Force re-download of the OpenAPI spec
make clean-openapi && make generate-client
```

The generation process:
1. Downloads the OpenAPI spec from the API server
2. Converts from OpenAPI 3.1 to 3.0.3 format (for compatibility)
3. Generates Go types and client code using oapi-codegen

> **WARNING**: The generated file `internal/api/client.gen.go` must **NEVER** be manually modified. All changes must go through the code generator by updating the OpenAPI spec or the generator configuration (`internal/api/oapi-codegen.yaml`). If you need custom behavior, create wrapper types or helper functions in separate files.

### Other Development Commands

```bash
# Format code
make fmt

# Tidy go modules
make tidy

# Run linter (requires golangci-lint)
make lint

# Clean build artifacts
make clean

# Show all available targets
make help
```

## Configuration

Configuration is stored in `~/.qctl/config` (YAML format).

### Example Config

```yaml
apiVersion: qctl/v1
currentContext: dev
contexts:
  dev:
    server: http://localhost:8000
    user: dev@example.com
    output: table
  prod:
    server: https://api.qluster.example
    user: admin@company.com
    output: json
    organization: "550e8400-e29b-41d4-a716-446655440000"
    organizationName: "Acme Corp"
```

### Environment Variables

- `QCTL_SERVER` - Override server URL
- `QCTL_USER` - Override user email
- `QCTL_ORG` - Override organization (ID or name)
- `QCTL_INSECURE_HTTP` - Allow insecure HTTP endpoints (set to `1` or `true`)

## Shell Completion

Enable tab-completion for commands, flags, and arguments.

### Automatic Installation (Recommended)

```bash
qctl completion install
```

This auto-detects your shell and installs the completion script to the appropriate location.

### Manual Installation

If automatic installation doesn't work, see shell-specific instructions:

```bash
qctl completion bash --help
qctl completion zsh --help
qctl completion fish --help
qctl completion powershell --help
```

## Command Reference

qctl uses a kubectl-style command structure where verbs are top-level commands:

```
qctl <verb> <resource> [flags]
```

### Resource Commands

```bash
# List resources
qctl get tables
qctl get ingestion-jobs
qctl get files
qctl get destinations

# Show resource details
qctl describe table <id>
qctl describe ingestion-job <id>
qctl describe file <id>
```

### Action Commands

```bash
# Run ingestion jobs
qctl run ingestion-job <id>
qctl run ingestion-job --filter table-id=5

# Kill running jobs
qctl kill ingestion-job <id>
qctl kill ingestion-job --filter state=running

# File operations
qctl upload file data.csv --table-id 123
qctl download file <id> --output /path/to/file
qctl delete file <id>
qctl undelete file <id>

# Apply configurations from files
qctl apply destination -f config.yaml
qctl apply rules -f rules.py
```

## Project Structure

```
qctl/
  cmd/qctl/              # Main entry point
  internal/
    config/              # Configuration management
    commands/            # Command implementations
      root/              # Root command
      get/               # List resources
      describe/          # Show resource details
      run/               # Run operations
      kill/              # Kill operations
      upload/            # Upload operations
      download/          # Download operations
      delete/            # Delete operations
      undelete/          # Restore operations
      apply/             # Apply configurations
      config/            # Config subcommands
      auth/              # Authentication
      completion/        # Shell completions
      version/           # Version command
    version/             # Version info
  Makefile               # Build targets
  go.mod                 # Go dependencies
```

## Tech Stack

- **Language**: Go 1.25.4
- **CLI Framework**: [cobra](https://github.com/spf13/cobra)
- **Configuration**: YAML (via [yaml.v3](https://github.com/go-yaml/yaml))
- **Build**: Make

## License

See CONTRIBUTING.md for contribution guidelines.
