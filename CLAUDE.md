# qctl - Qluster CLI

CLI tool for managing Qluster platform resources.

## Tech Stack

- **Language**: Go 1.25.4
- **CLI Framework**: cobra + viper
- **Build**: Make, goreleaser
- **CI**: GitHub Actions (lint, test, build)

## Implementation Conventions (Go CLI)
- A comprehensive patterns reference is maintained in `docs/patterns.md`. It documents:
  - Directory layout and command tree
  - Canonical code patterns for `get`, `describe`, `apply`, `run/kill`, and domain client implementations
  - Test setup boilerplate (mock server, test env, fixtures)
  - All key utility functions with file paths
  - API URL patterns and output format behavior
- Consult `patterns.md` before exploring the codebase — it should have what you need to implement new commands or modify existing ones.
- When implementing CLI commands, follow established patterns in the codebase for:
  - cobra command structure (flags, args, subcommands),
  - resolver/client patterns,
  - display/output formatting,
  - error handling and exit behavior,
  - test structure and fixtures.
- Prefer consistent UX:
  - stable output formats,
  - clear error messages,
  - avoid breaking changes to output unless explicitly required.

## Configuration

Config file: `$HOME/.qctl.yaml`

```yaml
api_endpoint: https://api.qluster.example.com
api_token: your-token-here
output: table  # table|json|yaml
```

Global flags:
- `--config <path>` - Custom config file
- `--output <format>` - Output format (table|json|yaml)

## Completion

```bash
qctl completion bash|zsh|fish|powershell
```

## Testing

- Run all tests: `make test`
- Re-run only the last failing tests (pytest-style): `make test TEST_ARGS=--lf` (`--last-failed` also works). Pass flags via `TEST_ARGS` so they are not consumed by `make`.

## API Client Generation

The project uses [oapi-codegen](https://github.com/oapi-codegen/oapi-codegen) to generate a Go client from the backend's OpenAPI spec.

```bash
# Generate client (downloads spec from localhost:8000 by default)
make generate-client

# Generate from a custom API URL
make generate-client API_URL=https://api.example.com/api/docs/openapi.json
```

**IMPORTANT**: The generated file `internal/api/client.gen.go` must NEVER be manually modified. All changes must go through the code generator. If you need to customize behavior, use wrapper types or helper functions in separate files.

