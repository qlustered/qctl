# qctl - Qluster CLI

CLI tool for managing Qluster platform resources.

## Tech Stack

- **Language**: Go 1.25.4
- **CLI Framework**: cobra + viper
- **Build**: Make, goreleaser
- **CI**: GitHub Actions (lint, test, build)

## Implementation Conventions (Go CLI)

### Documentation Lookup Order

Before exploring the codebase, consult these docs in order:

1. **`docs/commands.md`** — **Command-to-file map.** Use this FIRST to find the exact file path for any CLI command, its domain client package, printer, and test file. This eliminates the need to search or explore the directory structure.

2. **`docs/patterns.md`** — **Canonical implementation patterns.** Use this when you need to understand HOW to implement or modify a command. It documents:
   - Canonical code patterns for `get`, `describe`, `apply`, `run/kill`, `enable/disable/set-default`, and domain client implementations
   - Test setup boilerplate (mock server, test env, fixtures)
   - All key utility functions with file paths
   - API URL patterns and output format behavior
   - Rule-specific command details (smart apply, resolvers, manifest types)
   - Printer packages and when to use each

### Workflow for modifying or adding commands

1. Look up the command in `docs/commands.md` to find the implementation file and domain client.
2. Read the implementation file directly — do not explore the directory.
3. If you need to understand the pattern (e.g., how describe commands work), consult the relevant section of `docs/patterns.md`.
4. For new commands, find an existing command of the same verb (get/describe/apply/etc.) in `docs/commands.md`, read it as a template, and follow the pattern from `docs/patterns.md`.
5. Register the new subcommand in the parent file listed in `docs/commands.md` → "Parent Command Files" section.

### Code conventions
- Follow established patterns in the codebase for cobra command structure, resolver/client patterns, display/output formatting, error handling, and test structure.
- Prefer consistent UX: stable output formats, clear error messages, avoid breaking changes to output unless explicitly required.

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

