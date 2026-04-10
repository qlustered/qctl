# qctl Implementation Patterns Reference

## Directory Layout

```
cmd/qctl/                           # Main entry point
internal/
  api/
    client.gen.go                   # Auto-generated OpenAPI client (NEVER modify)
    oapi-codegen.yaml               # Code generation config
  client/
    client.go                       # HTTP client wrapper (bearerTransport, loggingTransport, curl logging)
  cmdutil/
    bootstrap.go                    # Bootstrap() — resolves config/server/creds/org into CommandContext
    flags.go                        # ValidateConflictingFlags, RequireOneOfFlags, ParseIntArgs
    prompt.go                       # ConfirmYesNo, ConfirmYesNoDefault, PromptString
    resolve.go                      # ResolveTable, ResolveCloudSource
  commands/
    root/root_test.go
    apply/                          # apply.go (parent + generic dispatch), generic.go, env.go, dataset.go,
                                    #   destination.go, cloud_source.go, rules.go (smart apply), table_rules.go (patch/instantiate)
    submit/                         # submit.go (parent), rules.go (Python file submission)
    get/                            # datasets.go, dataset.go, ingestion_jobs.go, files.go, alerts.go,
                                    #   rule_families.go, rule_revisions.go, rule.go, table_rules.go, etc.
    describe/                       # destination.go, alert.go, file.go, ingestion_job.go, rule.go, etc.
    delete/                         # delete.go (parent), file.go, error_incident.go, rule.go, rules.go
    enable/                         # enable.go (parent), rule.go — set rule state to "enabled"
    disable/                        # disable.go (parent), rule.go — set rule state to "disabled"
    set_default/                    # set_default.go (parent), rule.go — set is_default on rule revision
    run/                            # run.go (parent), ingestion_job.go, profiling_job.go
    kill/                           # kill.go (parent), ingestion_job.go, profiling_job.go
    create/                         # dry_run_job.go
    inspect/                        # dry_run_job.go
    upload/download/undelete/       # file operations
    explain/                        # Schema documentation from OpenAPI spec
    auth/                           # login.go, logout.go, switch_org.go, me.go
    config/                         # config.go, set_context.go, use_context.go, etc.
    completion/                     # completion.go, install.go
    version/                        # version.go
    docs/                           # docs.go
  config/                           # Config loading, server/org resolution
  auth/                             # Credential storage (keyring + plaintext fallback)
  org/                              # Organization resolver (name/UUID/prefix/fuzzy matching)
  datasets/                         # Client + types + apply + manifest for tables
  destinations/                     # Client + types + manifest
  ingestion/                        # Client + manifest
  cloud_sources/                    # Client + apply + manifest
  stored_items/                     # Client + manifest
  warnings/                         # Client
  alerts/                           # Client
  errorincidents/                   # Client + types
  profiling/                        # Client + manifest
  dry_runs/                         # Client + manifest
  rule_families/                    # Client + types + display (list rule families for `get rules`)
  rule_versions/                    # Client + resolver + describe + display + types (full domain package)
  dataset_rules/                    # Client + resolver + display + types (full domain package)
  dataset_kinds/                    # Client + resolver + describe + types (table kind domain package)
  ws/                               # WebSocket client
  schema/                           # OpenAPI schema cache + metadata for `explain`
  output/                           # Older printer (output.NewPrinterFromCmd)
  pkg/
    printer/                        # Printer: printer.NewPrinterFromCmd, NewPrinterFromCmdWithMarkdown
    tableui/                        # Lipgloss-styled table printer: tableui.PrintFromCmd (used by rule commands)
    manifest/                       # Manifest loading: LoadFile, LoadBytes, StrictUnmarshal, APIVersionV1
    timeutil/                       # FormatRelative, FormatRelativePtr
    tags/                           # tags.Build(pairs...) — comma-separated tag strings for table columns
    logs/                           # Log helpers
    secrets/                        # Secret field injection for env vars
    jsonschemautil/                 # ValidateParams — validates JSON params against JSON Schema (table rule apply)
  markdown/                         # Markdown/ANSI rendering, ProcessFieldsPlain
  apierror/                         # HandleHTTPError, HandleHTTPErrorFromBytes
  errors/                           # Exit code mapping (FromHTTPStatus)
  testutil/
    config.go                       # NewTestEnv, SetupConfigWithOrg, SetupCredential
    http.go                         # NewMockAPIServer, RespondJSON, RespondError, RespondText,
                                    #   MockPaginatedHandler, MockUnauthorizedHandler, MockNotFoundHandler,
                                    #   MockInternalServerErrorHandler, RespondWithCookie
    fixtures.go                     # IntPtr, StringPtr, BoolPtr
  version/                          # Version info
pkg/
  filters/                          # Filter parsing
  table/                            # Table formatting
```

## Command Tree (from `--help`)

```
qctl
├── get           tables, table, files, ingestion-jobs, profiling-jobs, alerts, warnings,
│                 cloud-sources, destinations, error-incidents, rules, rule-revisions,
│                 rule, table-rules, table-rule, dry-run-job, dry-run-jobs, job-activity,
│                 table-kinds, table-kind
├── describe      table, file, ingestion-job, profiling-job, alert, warning,
│                 cloud-source, destination, error-incident, rule, table-rule, dry-run-job,
│                 table-kind
├── apply         -f <file> (generic dispatch by kind), table, destination, cloud-source
├── submit        rules, table-kinds
├── delete        file, error-incident, rule, rules
├── enable        rule
├── disable       rule
├── set-default   rule
├── run           ingestion-job, profiling-job
├── kill          ingestion-job, profiling-job
├── create        dry-run-job
├── inspect       dry-run-job
├── upload        file
├── download      file
├── undelete      file
├── explain       RESOURCE[.FIELD] (schema docs from OpenAPI)
├── auth          login, logout, switch-org, me
├── config        set-context, use-context, delete-context, list-contexts, current-context
├── completion    bash, zsh, fish, powershell (+install subcommand)
├── version
└── docs
```

## Global Flags

```
--config string              Config file (default $HOME/.qctl/config)
-o, --output string          Output format: table|json|yaml|name (default "table")
-O, --org string             Organization ID or name
-v, --verbose count          Verbosity: 7=structured HTTP, 8=curl redacted, 9=curl full
--server string              API server URL override
--user string                User email override
--no-headers                 Omit table headers
--columns string             Comma-separated column list (table format)
--max-column-width int       Max column width, 0=unlimited (default 80)
--allow-plaintext-secrets    Show secrets in json/yaml output
--allow-insecure-http        Allow non-localhost http://
--request-timeout duration   Request timeout
--retries int                Retry count
```

## Pattern: "get" List Command

File: `internal/commands/get/<resource>.go`

```go
package get

func NewXxxCommand() *cobra.Command {
    cmd := &cobra.Command{
        Use:   "<cli-name>",         // e.g., "tables", "ingestion-jobs"
        Short: "List <resources>",
        Long:  `List all <resources> in the current organization.`,
        RunE: func(cmd *cobra.Command, args []string) error {
            // 1. Bootstrap auth context
            ctx, err := cmdutil.Bootstrap(cmd)
            if err != nil { return err }

            // 2. Validate conflicting flags (if any)
            if err = cmdutil.ValidateConflictingFlags(cmd, []string{"state", "states"}); err != nil {
                return err
            }

            // 3. Parse params from flags
            params, err := parseXxxParams(cmd)
            if err != nil { return err }

            // 4. Create domain client and fetch
            client := xxx.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
            resp, err := client.GetXxx(ctx.Credential.AccessToken, params)
            if err != nil { return fmt.Errorf("failed to get <resources>: %w", err) }

            // 5. Print results using printer
            setDefaultColumns(cmd, "col1,col2,col3")
            printer, err := output.NewPrinterFromCmd(cmd)  // or printer.NewPrinterFromCmd
            if err != nil { return fmt.Errorf("failed to create output printer: %w", err) }
            return printer.Print(resp.Results)
        },
    }
    addXxxFlags(cmd)
    return cmd
}
```

Key details:
- Use `setDefaultColumns(cmd, "...")` for table format defaults (defined in `ingestion_jobs.go`)
- Pagination hint: if `resp.Next != nil`, print to `cmd.ErrOrStderr()`
- Three printer options exist (see "Printer Packages" section below). Use whichever the surrounding code uses.

## Pattern: "describe" Detail Command

File: `internal/commands/describe/<resource>.go`

```go
func NewXxxCommand() *cobra.Command {
    cmd := &cobra.Command{
        Use:   "<resource> <id>",
        Short: "Show details of a specific <resource>",
        Args:  cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            id, err := strconv.Atoi(args[0])
            // ... or resolve by name

            ctx, err := cmdutil.Bootstrap(cmd)
            client := xxx.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
            result, err := client.GetXxx(ctx.Credential.AccessToken, id)

            // Output format: describe defaults to YAML
            outputFormat, _ := cmd.Flags().GetString("output")
            if !cmd.Flags().Changed("output") {
                outputFormat = "yaml"
            }

            switch outputFormat {
            case "json":
                encoder := json.NewEncoder(cmd.OutOrStdout())
                encoder.SetIndent("", "  ")
                return encoder.Encode(result)
            case "table":
                p, err := printer.NewPrinterFromCmd(cmd)
                return p.Print(manifest)
            default: // yaml
                encoder := yaml.NewEncoder(cmd.OutOrStdout())
                encoder.SetIndent(2)
                defer encoder.Close()
                return encoder.Encode(manifest)
            }
        },
    }
    return cmd
}
```

Key details:
- Describe commands default to YAML output (not table)
- Often convert API response to manifest format for round-trip `apply` compatibility
- Verbosity levels (-v, -vv) control detail tiers in some describe commands (alerts, rules)
- **Exception**: `describe rule` outputs **plain text** by default (not YAML manifest) — see "Rule Commands" section

## Pattern: "apply" Manifest Command

File: `internal/commands/apply/<resource>.go`

```go
func NewXxxCommand() *cobra.Command {
    cmd := &cobra.Command{
        Use:   "<resource>",
        Short: "Apply a <resource> configuration from a file",
        RunE: func(cmd *cobra.Command, args []string) error {
            filePath, _ := cmd.Flags().GetString("filename")
            if filePath == "" { return fmt.Errorf("filename is required (-f or --filename)") }

            // Load and validate manifest
            manifest, err := loadXxxManifest(filePath)
            if errs := manifest.Validate(); len(errs) > 0 { ... }

            ctx, err := cmdutil.Bootstrap(cmd)
            client := xxx.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
            result, err := client.Apply(ctx.Credential.AccessToken, manifest)

            // Success output
            fmt.Fprintf(cmd.OutOrStdout(), "<resource>/%s %s\n", manifest.Metadata.Name, result.Action)
            return nil
        },
    }
    cmd.Flags().StringP("filename", "f", "", "Path to the YAML manifest file (required)")
    _ = cmd.MarkFlagRequired("filename")
    return cmd
}
```

### Generic Apply Dispatch

`apply -f <file>` (no subcommand) reads the `kind` field from the manifest and dispatches automatically:

- File: `internal/commands/apply/generic.go` — `genericApply()` routes to `applyTable`, `applyDestination`, `applyCloudSource`, `applyRuleYAML`, `applyTableRuleYAML`
- `.py` files are rejected with a hint to use `qctl submit rules`
- Supports multi-document YAML (`---` separators)
- Flag: `--fail-fast` — stop on first document failure
- File: `internal/commands/apply/env.go` — `expandEnvVars(s)` expands `${VAR}` patterns in manifests

### Smart Apply for Rules (kind: Rule)

File: `internal/commands/apply/rules.go` — `applyRuleYAML()`

Not a simple create/update — implements a **smart diff** against live state:
1. Fetches live state via `GetRuleRevisionDetails`
2. Compares field by field using JSON normalization (`fieldEquals` via `json.Marshal`)
3. Outcomes per document: `"patched"`, `"unchanged"`, or `"failed"`
4. Output: `rule/<name> (<short-id>) patched|unchanged|failed`

Immutable fields (changes rejected → user told to use `qctl submit rules`):
`release`, `code`, `input_columns`, `validates_columns`, `corrects_columns`, `enriches_columns`, `param_schema`, `is_builtin`, `is_caf`

Patchable fields: `description` (from `spec`), `state` (from `spec` or `status`), `is_default` (from `spec` or `status`)

### Apply Table Rules (kind: TableRule)

File: `internal/commands/apply/table_rules.go`

Two modes based on manifest content:
- **Patch** (`metadata.id` present): patches `instance_name`, `is_enabled`, `treat_as_alert`, `params`, `column_mapping` on existing rule
- **Instantiate** (`spec.rule_revision_id` present, no `metadata.id`): creates new dataset rule via `client.InstantiateRule()`
- Both modes validate `params` against the rule revision's `param_schema` using `jsonschemautil.ValidateParams()` when params are provided

Manifest structure:
```yaml
apiVersion: qluster.ai/v1
kind: Table|Destination|CloudSource|DryRunJob|Rule|TableRule
metadata:
  name: <string>
spec:
  ...
```

Loading: `pkgmanifest.LoadBytes(data)` → check `APIVersion` + `Kind` → `pkgmanifest.StrictUnmarshal(data, &typedManifest)`

## Pattern: "run/kill" Action Commands

```go
// Accepts IDs as positional args OR --filter for bulk operations
// Uses --dry-list to preview, --yes to skip confirmation
cmd := &cobra.Command{
    Use:  "<resource> [id...]",
    RunE: func(cmd *cobra.Command, args []string) error { ... },
}
cmd.Flags().String("filter", "", "Filter format: key1=val1,key2=val2")
cmd.Flags().Bool("dry-list", false, "Preview without acting")
cmd.Flags().Bool("yes", false, "Skip confirmation")
```

## Pattern: "enable/disable/set-default" State Commands

Files: `internal/commands/enable/rule.go`, `internal/commands/disable/rule.go`, `internal/commands/set_default/rule.go`

All three follow the same pattern:
1. Resolve rule by name/ID/short-ID using `rule_versions.ResolveRule()`
2. Patch the rule revision via `client.PatchRuleRevision()`
3. Print confirmation

```go
func NewRuleCommand() *cobra.Command {
    cmd := &cobra.Command{
        Use:   "rule <name-or-id>",
        Args:  cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            ctx, err := cmdutil.Bootstrap(cmd)
            client, err := rule_versions.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
            // Fetch all revisions for resolution
            revisions, err := client.GetRuleRevisions(ctx.Credential.AccessToken, ...)
            // Resolve to a single revision
            releaseFilter, _ := cmd.Flags().GetString("release")
            resolved, err := rule_versions.ResolveRule(revisions, args[0], releaseFilter)
            // Patch
            _, err = client.PatchRuleRevision(ctx.Credential.AccessToken, resolved.ID, body)
            fmt.Fprintf(cmd.OutOrStdout(), "rule/%s (%s) enabled\n", resolved.Name, shortID)
            return nil
        },
    }
    cmd.Flags().String("release", "", "Disambiguate when rule has multiple releases")
    return cmd
}
```

Flag: `--release` — required to disambiguate when a rule has multiple releases.

## Pattern: Domain Client

File: `internal/<domain>/client.go`

### Standard Pattern (most domain packages)

```go
type Client struct {
    baseURL        string
    organizationID openapi_types.UUID
    verbosity      int
    timeout        time.Duration
}

func NewClient(baseURL, organizationID string, verbosity int) *Client {
    orgUUID, _ := uuid.Parse(organizationID)
    return &Client{
        baseURL:        baseURL,
        organizationID: openapi_types.UUID(orgUUID),
        verbosity:      verbosity,
        timeout:        30 * time.Second,
    }
}

// Type aliases from generated client
type XxxFull = api.XxxSchemaFull
type XxxTiny = api.XxxTinySchema

func (c *Client) GetXxx(accessToken string, params ...) (*XxxPage, error) {
    apiClient, err := client.New(client.Config{
        BaseURL:     c.baseURL,
        AccessToken: accessToken,
        Timeout:     c.timeout,
        Verbosity:   c.verbosity,
    })
    // ... call apiClient.API.XxxWithResponse(...)
    // ... check resp.StatusCode(), use apierror.HandleHTTPErrorFromBytes on error
}
```

### Error-returning Constructor (rule_versions, dataset_rules)

`rule_versions.NewClient` and `dataset_rules.NewClient` return `(*Client, error)` — they validate the UUID at construction time:

```go
func NewClient(baseURL, organizationID string, verbosity int) (*Client, error) {
    orgUUID, err := uuid.Parse(organizationID)
    if err != nil { return nil, fmt.Errorf("invalid organization ID: %w", err) }
    return &Client{...}, nil
}
```

Key: Domain clients use `client.New()` to create the generated API client wrapper, then call the generated methods.

## Rule Domain Packages

### rule_families (internal/rule_families/)

Client for listing rule families (grouped by name). Used by `get rules`.

Files: `client.go`, `display.go`, `types.go`

- `NewClient(baseURL, organizationID, verbosity)` — returns `*Client` (standard pattern)
- `GetRuleFamilies(accessToken, GetRuleFamiliesParams)` — params: `SearchQuery`, `ExcludeBuiltin`, `OrderBy`, `Page`, `Limit`, `Reverse`
- `ToDisplayList(families)` — converts to `[]RuleFamilyDisplay`, emits 1 row per primary revision + optional 2nd row for secondary (newer-than-default) revision
- `RuleFamilyDisplay` struct: `Name`, `Release`, `State`, `Tags`, `Author`, `ShortID`, `CreatedAt`, `UpdatedAt`

### rule_versions (internal/rule_versions/)

Full domain client for individual rule revisions. Used by `get rule`, `get rule-revisions`, `describe rule`, `enable/disable/set-default rule`, `apply` (kind: Rule), `submit rules`, `delete rule(s)`.

Files: `client.go`, `resolver.go`, `describe.go`, `display.go`, `types.go`

Client methods:
- `GetRuleRevisions(accessToken, params)` — list revisions
- `GetRuleRevisionAllReleases(accessToken, ruleRevisionID)` — all releases for a family
- `GetRuleRevisionDetails(accessToken, ruleRevisionID)` — full detail including code
- `PatchRuleRevision(accessToken, ruleRevisionID, body)` — patch state, is_default, description
- `DeleteRuleRevision(accessToken, ruleRevisionID)` — delete
- `SubmitRuleVersion(accessToken, req)` — submit Python code
- `UnsubmitRuleVersion(accessToken, fileText)` — reverse of submit
- `ResolveRuleIDAny(accessToken, input)` — resolves name/short-ID/UUID to rule revision UUID; lenient (picks first on multi-release)
- `ResolveRuleFull(accessToken, input, release)` — resolves to full `ResolvedRule` struct; strict (errors on multi-release without `--release`)

Resolver (`resolver.go`):
- `ResolveRule(rules, input, releaseFilter)` — strict: errors on multi-release ambiguity
- `ResolveRuleAny(rules, input)` — lenient: silently picks first on multi-release
- `ShortID(uuid)` — strips dashes and returns first 8 hex chars
- Resolution order: full UUID (fast path) → exact name → UUID prefix/short-ID → fuzzy substring

Describe (`describe.go`):
- `FormatDescribe(detail, showCode)` — produces human-readable plain text (NOT a YAML manifest)
- Shows: Identity, Flags (State/Default/Built-in/CAF), Description, Columns, Param Schema, Provenance, Code (15-line preview by default)

Display (`display.go`):
- `RuleRevisionDisplay` struct with `Tags` and `ShortID` computed fields
- `ToDisplayList(rules)` — builds tags: Default, Built-in, CAF, Update available, Newer than default

Types (`types.go`):
- `RuleManifest` / `RuleRawManifest` — for describe output (includes `status`, NOT apply-compatible)
- `RuleGetManifest` / `RuleGetSpec` / `RuleGetStatus` — for `get rule -o yaml` (apply-compatible, kind: Rule)
- `RuleFamilyManifest` / `RuleFamilyRawManifest` — for multi-release output (kind: RuleFamily)
- `RuleApplyManifest` / `PatchManifest` — for parsing `apply -f rule.yaml` input
- `FullResponseToGetManifest(resp)` — converts `RuleRevisionFull` to `RuleGetManifest` (apply-compatible)

### dataset_rules (internal/dataset_rules/)

Full domain client for table rules (dataset rules). Used by `get table-rules`, `describe table-rule`, `apply` (kind: TableRule).

Files: `client.go`, `resolver.go`, `display.go`, `types.go`

Client methods:
- `GetDatasetRules(accessToken, datasetID, params)` — list rules for a table; params include `InstanceName` for server-side filtering
- `GetDatasetRuleDetail(accessToken, datasetRuleID)` — single rule detail (**flat endpoint**, no datasetID needed)
- `ResolveDatasetRuleID(accessToken, datasetID, input)` — resolves name/short-ID/UUID to full UUID; full UUID fast-paths without API call, names use server-side `InstanceName` filter, short IDs fall back to listing all
- `PatchDatasetRule(accessToken, datasetID, datasetRuleID, body)` — patch
- `InstantiateRule(accessToken, ruleRevisionID, body)` — create dataset rule from revision (POST, 201)

Resolver: `ResolveDatasetRule(rules, input)` — in-memory resolution by instance_name or UUID/short-ID prefix (used internally by `ResolveDatasetRuleID`)

Display (`display.go`):
- `DatasetRuleDisplay` struct: ID, ShortID, InstanceName, Release, Position, State (title-cased), Severity ("Blocker"/"Warning"), CreatedAt, UpdatedAt
- `ToDisplayList(rules)` — converts list of `DatasetRuleTiny` to display structs
- `DetailToDisplay(detail)` — converts single `DatasetRuleDetail` to display struct

Types (`types.go`):
- `TableRuleManifest` / `TableRuleRawManifest` — for describe output
- `TableRuleApplyManifest` — for apply input; has `IsPatch()` and `IsInstantiate()` methods
- `APIResponseToManifest(resp, verbosity)` — converts API detail to `TableRuleManifest`; verbosity 0=essential fields + params + column_mapping, 1=adds dataset_field_names
- `APIResponseToRawManifest(resp)` — converts API detail to `TableRuleRawManifest` for -vv raw dump

## Rule-Specific Command Details

### get rules (`internal/commands/get/rule_families.go`)

Lists rule **families** (one row per rule name, not individual revisions).
- Uses `rule_families.NewClient` and `tableui.PrintFromCmd`
- Default columns: `name,release,state,tags,author,short_id`
- Flags: `--limit` (default 1000), `--page`, `--order-by` (default `impact_score`), `--reverse`, `--search`, `--exclude-builtin`

### get rule-revisions (`internal/commands/get/rule_revisions.go`)

Lists all individual revisions (flat list).
- Default columns: `name,release,state,tags,short_id`; with `-v`: adds `id,description,input_columns`
- Flags: `--limit`, `--page`, `--order-by`, `--reverse`, `--state`, `--search`, `--only-default`, `--has-upgrade-available`

### get rule \<name-or-id\> (`internal/commands/get/rule.go`)

Fetches a single rule (or all releases of a rule family).
- Flag: `--release` — pins to a single revision; omitted → shows all releases
- Output formats:
  - `table` (default): `tableui.PrintFromCmd` with display structs
  - `code`: `qctl get rule foo -o code` — prints raw Python source only (via `writeCode()`)
  - `yaml`/`json`: single release → `kind: Rule` manifest via `FullResponseToGetManifest()` (apply-compatible); multi-release without `--release` → error
- Helper: `encodeStructured(cmd, format, data)` — shared JSON/YAML encoder (also used by `get table-rule`)

### get table-rule \<name-or-id\> (`internal/commands/get/table_rule.go`)

Fetches a single table rule (dataset rule) by instance name, short ID, or full UUID.
- **Required flag:** `--table` (table/dataset ID)
- Uses `dataset_rules.NewClient()` (error-returning constructor)
- Resolves via `client.ResolveDatasetRuleID()` → `client.GetDatasetRuleDetail()`
- Output:
  - `table` (default): `tableui.PrintFromCmd` with `dataset_rules.DetailToDisplay()`; default columns: `instance_name,release,state,severity,short_id`; `-v` adds `id,created_at,updated_at`
  - `json`/`yaml`: `dataset_rules.APIResponseToManifest(detail, verbosity)` — produces kind: TableRule manifest

### describe table-rule \<name-or-id\> (`internal/commands/describe/table_rule.go`)

Shows details of a table rule with YAML manifest output (default).
- **Required flag:** `--table` (table/dataset ID)
- Default output: YAML (standard describe pattern)
- **Verbosity tiers:**
  - (default): Essential fields including params and column_mapping (round-trip apply compatible)
  - `-v`: Adds `dataset_field_names`
  - `-vv`: Raw API response dump via `APIResponseToRawManifest()`
- Supports `-o json` / `-o yaml`

### describe rule (`internal/commands/describe/rule.go`)

**Exception to standard describe pattern**: default output is **plain text** (not YAML manifest).
- Uses `rule_versions.FormatDescribe()` for human-readable non-round-trippable output
- Flags: `--release`, `--show-code` (show full code; default is 15-line preview)
- With `-o yaml`/`-o json`: outputs raw API response
- Users should use `get rule -o yaml` for apply-compatible output

### submit rules (`internal/commands/submit/rules.go`)

- `-f` flag is `StringArrayVar` — accepts multiple files
- Validates all files are `.py` before bootstrapping auth
- File size limit: 500 KB
- Flags: `--force` (resubmit existing versions), `--yes`
- Shows "Unchanged rule(s):" section for rules already at latest version

### delete rules (`internal/commands/delete/rules.go`)

- Accepts `-f <file.py>` (Python files, not YAML)
- Calls `client.UnsubmitRuleVersion(token, fileText)` — server finds matching rule versions
- Result in three sections: Deleted, Not found, Skipped (with reason)

## Printer Packages

Three printer packages exist:

| Package | Function | Used by | Notes |
|---|---|---|---|
| `internal/output` | `output.NewPrinterFromCmd(cmd)` | Older commands (datasets, files, etc.) | Reflection-based struct-to-table |
| `internal/pkg/printer` | `printer.NewPrinterFromCmd(cmd)` | Newer commands | Same API as output |
| `internal/pkg/tableui` | `tableui.PrintFromCmd(cmd, data, defaultColumns)` | Rule commands (`get rules`, `get rule`, etc.) | Lipgloss-styled tables; delegates to `output` for json/yaml/name formats |

`tableui` uppercases headers and replaces underscores with dashes (e.g., `short_id` → `SHORT-ID`).

Use whichever the surrounding code uses. For new rule-related commands, prefer `tableui.PrintFromCmd`.

## Pattern: Test Setup

File: `internal/commands/<verb>/<resource>_test.go`

```go
const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

func setupTestCommand() *cobra.Command {
    rootCmd := &cobra.Command{Use: "qctl"}
    // Add global flags matching root command
    rootCmd.PersistentFlags().String("server", "", "API server URL")
    rootCmd.PersistentFlags().String("user", "", "User email")
    rootCmd.PersistentFlags().StringP("output", "o", "table", "output format")
    rootCmd.PersistentFlags().Bool("no-headers", false, "")
    rootCmd.PersistentFlags().String("columns", "", "")
    rootCmd.PersistentFlags().Int("max-column-width", 80, "")
    rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "")
    rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "")
    rootCmd.PersistentFlags().CountP("verbose", "v", "")  // NOTE: CountP, not BoolP!
    // Add the command under test
    parentCmd := &cobra.Command{Use: "<verb>"}
    parentCmd.AddCommand(NewXxxCommand())
    rootCmd.AddCommand(parentCmd)
    return rootCmd
}

func TestXxxCommand(t *testing.T) {
    tests := []struct {
        name               string
        args               []string
        wantErr            bool
        wantOutputContains []string
    }{ ... }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // 1. Setup test env (temp HOME, plaintext tokens)
            env := testutil.NewTestEnv(t)
            defer env.Cleanup()

            // 2. Create mock API server
            mock := testutil.NewMockAPIServer()
            defer mock.Close()

            // 3. Setup config and credentials
            endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
            env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
            env.SetupCredential(endpointKey, testOrgID, "test-token")

            // 4. Register mock handlers
            mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/...", func(w http.ResponseWriter, r *http.Request) {
                testutil.RespondJSON(w, http.StatusOK, responseData)
            })

            // 5. Create command, capture output, execute
            cmd := setupTestCommand()
            var buf bytes.Buffer
            cmd.SetOut(&buf)
            cmd.SetErr(&buf)
            cmd.SetArgs(append([]string{"<verb>", "<resource>"}, tt.args...))
            err := cmd.Execute()

            // 6. Assert
            if (err != nil) != tt.wantErr { t.Errorf(...) }
            for _, want := range tt.wantOutputContains {
                if !strings.Contains(buf.String(), want) { t.Errorf(...) }
            }
        })
    }
}
```

CRITICAL: The verbose flag MUST use `CountP("verbose", "v", "")` not `BoolP`. Bootstrap reads it with `GetCount("verbose")`.

## Key Utility Functions

| Function | Location | Purpose |
|---|---|---|
| `cmdutil.Bootstrap(cmd)` | `internal/cmdutil/bootstrap.go` | Resolve config → server → creds → org → verbosity into `CommandContext` |
| `cmdutil.BootstrapWithoutAuth(cmd)` | same | For commands that don't need creds (e.g., login) |
| `cmdutil.ValidateConflictingFlags(cmd, sets...)` | `internal/cmdutil/flags.go` | Reject mutually exclusive flags |
| `cmdutil.RequireOneOfFlags(cmd, flags)` | same | Require at least one from set |
| `cmdutil.ParseIntArgs(args, name)` | same | Parse positional int args |
| `cmdutil.ConfirmYesNo(prompt)` | `internal/cmdutil/prompt.go` | y/N confirmation |
| `cmdutil.ResolveTable(ctx, tableID, tableName)` | `internal/cmdutil/resolve.go` | Resolve table by ID or name |
| `cmdutil.ResolveCloudSource(ctx, ...)` | same | Resolve cloud source by ID, name, or auto-detect |
| `printer.NewPrinterFromCmd(cmd)` | `internal/pkg/printer/helpers.go` | Create printer from cmd flags |
| `printer.NewPrinterFromCmdWithMarkdown(cmd, fields)` | same | Printer with markdown rendering |
| `tableui.PrintFromCmd(cmd, data, defaultColumns)` | `internal/pkg/tableui/table.go` | Lipgloss-styled table printer (rule commands) |
| `output.NewPrinterFromCmd(cmd)` | `internal/output/helpers.go` | Older printer (same API) |
| `setDefaultColumns(cmd, cols)` | `internal/commands/get/ingestion_jobs.go` | Set default columns for table format |
| `tags.Build(pairs...)` | `internal/pkg/tags/tags.go` | Build comma-separated tag string from label/bool pairs |
| `timeutil.FormatRelative(t)` | `internal/pkg/timeutil/relative.go` | "5 minutes ago" formatting |
| `timeutil.FormatRelativePtr(t)` | same | Nil-safe version |
| `manifest.LoadFile(path)` | `internal/pkg/manifest/manifest.go` | Load + validate generic manifest |
| `manifest.StrictUnmarshal(data, v)` | same | YAML parse with unknown-field rejection |
| `manifest.APIVersionV1` | same | Constant: `"qluster.ai/v1"` |
| `apierror.HandleHTTPError(resp, msg)` | `internal/apierror/http.go` | Convert HTTP errors to user-friendly errors |
| `apierror.HandleHTTPErrorFromBytes(code, body, msg)` | same | Same, from pre-read body |
| `jsonschemautil.ValidateParams(params, schema)` | `internal/pkg/jsonschemautil/validate.go` | Validate JSON params against JSON Schema (table rule apply) |
| `encodeStructured(cmd, format, data)` | `internal/commands/get/rule.go` | Shared JSON/YAML encoder for structured output |
| `testutil.NewTestEnv(t)` | `internal/testutil/config.go` | Create temp HOME with isolated config |
| `testutil.NewMockAPIServer()` | `internal/testutil/http.go` | Mock HTTP transport (no real sockets) |
| `testutil.RespondJSON(w, status, data)` | same | Write JSON mock response |
| `testutil.RespondError(w, status, msg)` | same | Write error mock response |
| `testutil.RespondText(w, status, text)` | same | Write plain text mock response |
| `testutil.MockPaginatedHandler(cb)` | same | Paginated response helper |
| `testutil.MockUnauthorizedHandler()` | same | Returns 401 handler |
| `testutil.MockNotFoundHandler(msg)` | same | Returns 404 handler |
| `testutil.MockInternalServerErrorHandler()` | same | Returns 500 handler |
| `testutil.RespondWithCookie(w, status, data, name, val)` | same | JSON response with cookie |

## CommandContext

`cmdutil.Bootstrap(cmd)` returns `*CommandContext`:

```go
type CommandContext struct {
    Config           *config.Config
    Credential       *auth.Credential
    ServerURL        string
    Verbosity        int
    OrganizationID   string
    OrganizationName string   // human-readable org name (used in confirmations)
}
```

## API URL Patterns

All API endpoints follow: `/api/orgs/{orgID}/<resource>`

Examples:
- `GET /api/orgs/{orgID}/datasets` — list tables
- `GET /api/orgs/{orgID}/datasets/{id}` — get table
- `GET /api/orgs/{orgID}/stored_items` — list files
- `GET /api/orgs/{orgID}/data_sources` — list cloud sources
- `GET /api/orgs/{orgID}/destinations` — list destinations
- `GET /api/orgs/{orgID}/ingestion_jobs` — list ingestion jobs
- `GET /api/orgs/{orgID}/alert_items` — list alerts
- `GET /api/orgs/{orgID}/warnings` — list warnings
- `GET /api/orgs/{orgID}/error_incidents` — list error incidents
- `GET /api/orgs/{orgID}/rule_families` — list rule families
- `GET /api/orgs/{orgID}/rule_revisions` — list rule revisions
- `GET /api/orgs/{orgID}/rule_revisions/{id}` — get rule revision detail
- `PATCH /api/orgs/{orgID}/rule_revisions/{id}` — patch rule revision
- `DELETE /api/orgs/{orgID}/rule_revisions/{id}` — delete rule revision
- `POST /api/orgs/{orgID}/rule_revisions/submit` — submit rule
- `POST /api/orgs/{orgID}/rule_revisions/unsubmit` — unsubmit rule
- `GET /api/orgs/{orgID}/datasets/{id}/dataset-rules` — list table rules (nested under dataset)
- `GET /api/orgs/{orgID}/dataset-rules/{ruleID}` — get table rule detail (**flat path**, no dataset ID)
- `PATCH /api/orgs/{orgID}/datasets/{id}/dataset-rules/{ruleID}` — patch table rule (nested)
- `POST /api/orgs/{orgID}/rule_revisions/{id}/instantiate` — instantiate table rule

## Output Format Behavior

- `get` commands: default output is `table` format
- `get rule -o code`: outputs raw Python source code (special format for rules only)
- `describe` commands: default output is `yaml` format (override with `-o`)
- `describe rule`: **exception** — default output is plain text (not YAML); use `get rule -o yaml` for apply-compatible output
- `apply` commands: print `<resource>/<name> <action>` on success (e.g., `table/orders created`)
- `apply` rules: print `rule/<name> (<short-id>) patched|unchanged|failed`
- `delete/run/kill` commands: print confirmation message to stdout
- `enable/disable/set-default`: print `rule/<name> (<short-id>) enabled|disabled|set as default`

## Naming Convention (Backend → CLI)

| Backend | CLI | Go package |
|---|---|---|
| dataset | table | `datasets` |
| stored item | file | `stored_items` |
| data source | cloud source | `cloud_sources` |
| alert_item | alert | `alerts` |
| data_source_model | cloud source | (within cloud_sources) |
| rule_family | rule (in list context) | `rule_families` |
| rule_revision | rule (in detail context) | `rule_versions` |
| dataset_rule | table-rule | `dataset_rules` |
| dataset_kind | table-kind | `dataset_kinds` |
