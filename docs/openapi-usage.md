# OpenAPI Spec Usage in qctl

## Overview

This document describes how the OpenAPI specification is used in the qctl codebase for client generation.

## Spec Source

- **Source URL**: `http://localhost:8000/api/docs/openapi.json` (default, for local development)
- **Production**: Override via `make generate-client API_URL=<production-url>`
- **Local copy**: `openapi.json` in repo root (reference only, not used for generation)
- **Codegen input**: `build/openapi.codegen.json` (downloaded during `make generate-client`)

## Spec Version

The API spec is **OpenAPI 3.1.0**.

### OpenAPI 3.1 Compatibility Notes

`oapi-codegen` has limited OpenAPI 3.1 support. Key incompatibilities that may require adaptation:

1. **`anyOf` with `null`**: OpenAPI 3.1 uses `anyOf: [{type: X}, {type: null}]` for nullable types instead of `nullable: true` (OpenAPI 3.0 pattern). This appears extensively in the spec for optional parameters.

2. **`const` keyword**: OpenAPI 3.1 supports `const` which may not be fully handled.

3. **JSON Schema alignment**: OpenAPI 3.1 is fully aligned with JSON Schema draft 2020-12.

If `oapi-codegen` fails due to these incompatibilities, a spec adaptation step will be added to `make generate-client` that transforms 3.1-specific constructs to 3.0-compatible equivalents.

## Endpoints in Spec

The OpenAPI spec contains **138 operation IDs** (endpoints) covering:

- `/api/alerts` - Alert management
- `/api/datasets` - Dataset (table) management
- `/api/data-sources` - Cloud source management
- `/api/ingestion-jobs` - Ingestion job management
- `/api/stored-items` - File (stored item) management
- `/api/warnings` - Warning management
- `/api/rule-version` - Rule version management
- `/api/users` - User management and authentication
- `/api/token` - Authentication token endpoint
- And many more...

## Generated Client Location

The generated client lives at:
- **Package**: `internal/api`
- **File**: `internal/api/client.gen.go`

This is an internal package not exposed as public API of the module.
