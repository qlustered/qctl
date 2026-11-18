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
  aliasstore/                       # bbolt-backed persistent UUID<->alias bijection (server-scoped)
  dataview/                         # get_data normalizer (page model) + LLM Markdown renderer
  cmdutil/
    bootstrap.go                    # Bootstrap() — resolves config/server/creds/org into CommandContext
    flags.go                        # ValidateConflictingFlags, RequireOneOfFlags, PositiveIDFlag, PositiveSecondsFlag, PositiveCountFlag, NonNegativeCountFlag, ParseIntArgs
    prompt.go                       # ConfirmYesNo, ConfirmYesNoDefault, PromptString
    wait_flags.go                   # AddWaitFlags, WaitFlags (--wait / --wait-timeout)
    watch.go                        # AddWatchTimeoutFlag, WatchBudget, StartWait/Waiter, ResumeCommand, RenderFlag (--timeout, signals, resume line)
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
    enable/                         # enable.go (parent), rule.go, cloud_source.go, linked_table.go
    disable/                        # disable.go (parent), rule.go, cloud_source.go
    set_default/                    # set_default.go (parent), rule.go — set is_default on rule revision
    run/                            # run.go (parent), ingestion_job.go, profiling_job.go
    kill/                           # kill.go (parent), ingestion_job.go, profiling_job.go
    create/                         # dry_run_job.go
    inspect/                        # dry_run_job.go
    upload/download/undelete/       # file operations
    explain/                        # Schema documentation from OpenAPI spec
    auth/                           # login.go, logout.go, switch_org.go, me.go
    config/                         # config.go, set_context.go, use_context.go, etc.
    cache/                          # cache.go (parent), aliases list/resolve/clear over aliasstore
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
  notifications/                    # Client + notification_type vocabulary (the inbox)
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

## Command Tree

<!-- BEGIN GENERATED: command-tree -->
```
qctl
├── alert               propose, resolve, retract, submit
├── api-resources
├── apply               -f <file>, backup-settings, cloud-source, destination, linked-table,
│                       table
├── auth                login, logout, me, sessions (revoke), switch-org
├── cache               aliases (clear, list, resolve), releases (gc, list, pin, unpin)
├── change-proposal     approve, edit, reject, request-changes, rerun-dry-run, submit, withdraw
├── comment-thread      create, delete-comment, edit-comment, reply, resolve, unresolve
├── completion          bash, fish, install, powershell, zsh
├── config              current-context, delete-context, list-contexts, set-context, use-context
├── copy                cloud-source, table
├── create              composite-dry-run-job, deliverable-run, dry-run-job
├── data-review         accept-cell, approve, assign, clear-cell, close, comment, progress,
│                       re-request, request, unassign, withdraw, withdraw-cell
├── data-review-group   assign, cancel, re-request
├── delete              change-review-policy, cloud-source, data-review-policy, deliverable,
│                       deliverable-revision, error-incident, file, rule, rule-review-policy,
│                       rules, standard-field, table-kind, table-series
├── deliverable         accept-column-suggestions, completeness, confirm-cells, confirm-columns,
│                       test-filter
├── deliverable-run     cancel, drift, mark-delivered, override-source, readiness, render,
│                       sign-off, unwaive, waive
├── describe            alert, backup-settings, change-proposal, cloud-source, comment-thread,
│                       composite-dry-run-job, composite-run-job, data-review, deliverable,
│                       deliverable-revision, deliverable-run, destination, dry-run-job,
│                       duplicate-group, error-incident, file, ingestion-job, privilege-request,
│                       profiling-job, queue-message, row, rule, rule-merge, rule-review,
│                       rule-run-replay, rule-suggestion, standard-field, table, table-kind,
│                       table-rule, table-schema, table-series, warning
├── diff                -f <file>
├── disable             cloud-source, email-intake, rule
├── docs
├── download            alert-report, data, deliverable-run, file, issues-report
├── duplicate-group     allow, allow-all, approve, comment, disagree, dismiss, keep-one, mark,
│                       not-duplicates, reapply, respond, retract, share, stop-series, withdraw,
│                       withdraw-response
├── email-intake        rotate
├── email-sender        add, remove, resend-welcome
├── enable              cloud-source, deliverable, email-intake, linked-table, rule
├── explain             RESOURCE[.FIELD]
├── file                keep-both, replace-older, send-issues-report, set-period
├── get                 alerts, backup-settings, change-proposals, change-review-policy,
│                       cloud-source, cloud-sources, comment-threads, composite-dry-run-jobs,
│                       composite-run-jobs, corrected-lines, data, data-review-changes,
│                       data-review-groups, data-review-policy, data-reviews, deleted-rows,
│                       deliverable, deliverable-revisions, deliverable-run, deliverable-runs,
│                       deliverables, destinations, dropped-lines, dry-run-job, dry-run-jobs,
│                       duplicate-groups, email-senders, error-incidents, files, history,
│                       ingestion-jobs, latest-alert, linked-table, notification-preferences,
│                       notifications, orgs, pii-access-requests, pii-grants, privilege-audit,
│                       privilege-requests, profiling-jobs, queue-messages, rule, rule-ignores,
│                       rule-review-policy, rule-reviews, rule-revisions, rule-suggestions,
│                       rule-upgrades, rules, slack-config, soft-deleted-tables, standard-field,
│                       standard-field-suggestions, standard-fields, submission-statuses, table,
│                       table-config, table-kind, table-kind-suggestions, table-kinds,
│                       table-members, table-rule, table-rules, table-schema, table-series,
│                       table-series-suggestion, tables, tables-overview, value-recommendations,
│                       warnings
├── guide               [topic]
├── info
├── inspect             composite-dry-run-job, deliverable, dry-run-job
├── kill                ingestion-job, profiling-job
├── merge               table-rules
├── notification        dismiss, read, set-preference
├── pii-access-request  approve, deny, request, withdraw
├── pii-grant           create, revoke
├── privilege           approve, reject, request
├── queue-message       resurrect, resurrect-dead, retire, retry
├── redo                cloud-source, destination, table
├── rekey               linked-table
├── reveal              alert, cell, warning
├── review              approve, assign, audit, close, comment, delete-comment, edit-comment,
│                       link-dry-run, re-request, reject, reopen, request, request-changes,
│                       unassign, withdraw
├── rows                check-unique, delete-quarantined, hold, ignore-rule, release,
│                       reprocess-clean, reprocess-quarantined, reset-hold, search-replace,
│                       send-to-quarantine, set-cell, unignore-rule
├── rule-upgrade        notify
├── run                 composite-run-job, ingestion-job, profiling-job, rule-run-replay,
│                       sensor-job
├── set-default         rule
├── share               table
├── skill               install, status, uninstall
├── slack               set-alert-type
├── submission-status   unwaive, waive
├── submit              rules, table-kinds
├── suggestion          accept, dismiss, edit
├── table-member        leave, remove, set-permission
├── table-rule          move, propose, recheck-clean-rows, test-filter
├── table-series        approve-rules, delete-period, lock, recreate-period, unlock
├── undelete            cloud-source, deliverable, file, table
├── undo                cloud-source, destination, table
├── upload              deliverable-template, file
├── version
└── warning             propose, resolve, retract, submit
```
<!-- END GENERATED: command-tree -->

Generated from the cobra tree by `TestPatternsGeneratedBlocks`. Do not hand-edit
either block — an edit is reverted by the next re-stamp and fails CI until then.
Re-stamp after adding or renaming a command:

    go test ./internal/catalog -run TestPatternsGeneratedBlocks -update

## Guide Topics

`qctl guide <topic>` — offline, no auth. Registry: `internal/commands/guide/topics.go`.

<!-- BEGIN GENERATED: guide-topics -->
```
discover-capabilities      Start here: find the command, resource or guide for a task
check-inbox                Read and clear your notification inbox
onboard-table              Create or update a table (dataset) from a manifest
standard-field-vocabulary  Define standard fields, tag tables and columns, find matching rules
manage-files               Upload a file, follow its first ingest, download or ignore it
connect-sources            Connect a cloud storage bucket as a data source and ingest from it
link-tables                Build a linked table that joins several tables on a spine
deliverables               Author a spreadsheet setup, produce a run, sign off and record delivery
inspect-data               Read a table's rows, clean or quarantined, and export them all
edit-data                  Fix bad values in quarantined rows: search-replace, set-cell, undo
rule-lifecycle             A rule end to end: submit, dry run, review, enable, attach
submit-rules               Submit Python rule files to the org catalog; what a file may contain
instantiate-rule           Attach a rule to a table or column (create a table rule)
manage-suggestions         Inspect, accept, edit or dismiss proposed new rules and rule changes
run-jobs                   Run, kill and wait for ingestion and profiling jobs; job states
dry-run-jobs               Dry-run a rule against real rows; which job is which
triage-alerts              Resolve alerts and warnings: what fired, on which rows, which action
propose-changes            Propose an alert or warning resolution for an approver, and decide one
unblock-table              Find what blocks a table (draft schema, alert, suggestion) and clear it
handle-incidents           Diagnose a failed ingestion or processing job, then retry it
request-review             Rule review, author side: request one and drive it to enable
review-rules               Rule review, reviewer side: threads, evidence, approve or reject
inspect-schema             Read a table's schema (columns, types) and its pending draft
data-review-policy         Data review policy: which quarantined rows need approval
review-data                Data review: submit quarantined rows for approval, and decide them
authenticate               Log in, check who you are, and switch organizations
switch-context             Point qctl at another server or context; protected contexts
use-aliases                Use qctl's short aliases instead of raw UUIDs
output-and-scripting       Exit codes, output formats, --yes and timeouts for scripts and agents
```
<!-- END GENERATED: guide-topics -->

A code block that quotes what qctl prints is pinned to the code that prints
it: `guidetest.AssertQuoted(t, "<topic>", rendered)`
(`internal/commands/guide/guidetest`), called from a `guide_quote*_test.go` file
in the renderer's own package so an unexported renderer is reachable. It
requires the rendered lines as consecutive whole lines of one code block, with
the block's edge, a blank line, or a `...` line on each side; a `...` line at
either end of the rendered text is its own boundary. A new quoted block needs a
new pin; `grep -rn guidetest.AssertQuoted internal` lists the existing
ones.

## Global Flags

```
--config string              Config file (default $HOME/.qctl/config)
-o, --output string          Output format: table|json|yaml|name (default "table"); validated per command in root's PersistentPreRunE
-O, --org string             Organization ID or name
-v, --verbose count          Verbosity: 7=structured HTTP with redacted bodies, 8=curl redacted, 9=curl full
--server string              API server URL override
--user string                User email override
--no-headers                 Omit table headers
--columns string             Comma-separated column list (table format)
--max-column-width int       Max column width, 0=unlimited (default 80 on a terminal, unlimited otherwise)
--allow-plaintext-secrets    Show secrets in json/yaml output
--allow-insecure-http        Allow non-localhost http://
--request-timeout duration   Deadline for ONE HTTP attempt, 0=30s default; overrides a package's own
--retries int                Retries for a failed GET/HEAD/OPTIONS, 0=never; POST/PATCH/PUT/DELETE never
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

            // 5. Print one page: rows + stderr hint under table/name,
            //    a List envelope of the raw API items under json/yaml
            lp := listPage{noun: "<resources>", page: &params.Page, limit: &params.Limit,
                total: resp.TotalRows, more: resp.Next != nil}
            return printListPage(cmd, resp.Results, lp, func(data any) error {
                return printXxxResults(cmd, data)
            })
        },
    }
    addXxxFlags(cmd)
    return cmd
}

// printXxxResults projects the raw slice for table and name; a List passes
// through untouched, so json/yaml carry the API's values.
func printXxxResults(cmd *cobra.Command, data any) error {
    if results, ok := data.([]xxx.XxxTiny); ok {
        data = xxx.ToDisplayList(results)
    }
    return tableui.PrintFromCmd(cmd, data, "col1,col2,col3")
}
```

Key details:
- Use `setDefaultColumns(cmd, "...")` for table format defaults (defined in `ingestion_jobs.go`)
- Declare the columns the table can render with `cmdutil.DeclareColumns(cmd, []XxxDisplay(nil))`, one sample per display type, and build a local `--columns` flag's `Available:` list from its return value. Root's `PersistentPreRunE` checks `--columns` against the declaration under every `-o`, before `RunE`; a command that declares nothing is checked only by its printer. `TestEveryLocalColumnsFlagIsDeclared` covers commands with a local `--columns` flag only.
- `printListPage` and `listPage` live in `internal/commands/get/list_page.go`.
  Under `-o json|yaml` the command prints an `output.List`
  (`{kind: List, items, page, limit, total, next_page}`) and no stderr hint;
  under table and name it prints the rows, then the "More available" hint.
  `next_page` comes from page/limit/total, never from the server's `next`:
  atlas sends `next` only on its cursor path (page 1, PK order, integer PK).
  A list with no `--page` passes a nil page; an empty result still runs
  `printEmptyResult` first and falls through with the empty slice.
- JSON/YAML items are the raw API results. A display projection (relative
  times, joined tags, short ids) is for table and name only. `get rule-ignores`
  is the exception: its items are its `ruleIgnoreRow` projection, which joins
  the table rule's name and carries RFC3339 times.
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
- The template is the manifest-style describe: with `-o` unset it prints YAML, often the API response converted to manifest format for round-trip `apply` compatibility. It branches on `Changed("output")`, so a context's `output:` default switches it too.
- Not every describe follows it — the Printer column of `docs/commands.md` gives each one's default. `describe rule` and the agent-text-payload describes print **plain text** unless `-o json|yaml` is passed (see "Rule-Specific Command Details" for `describe rule`). `describe table` and `describe cloud-source` print a **text summary** with no `Changed` check — unset and `-o table` are the same view, `-o json|yaml` is that summary as an object — and their manifests come from `get table|cloud-source <id> -o yaml`.
- Verbosity levels (-v, -vv) control detail tiers in some describe commands (alerts, rules)

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

- File: `internal/commands/apply/generic.go` — `genericApply()` routes through `genericKinds`; the map is the current list of accepted kinds and handlers.
- `.py` files are rejected with a hint to use `qctl submit rules`
- Multi-document YAML (`---` separators) for every kind, via `applyStream` in `internal/commands/apply/stream.go`. `applyFunc` is `func(cmd *cobra.Command, doc manifestDoc) (*applyResult, error)` — a handler receives one document's bytes and returns its document and resource reference alongside any error, including a partial result when writes preceded a failure. The runner prints references under `-o name` and the original documents under `-o json|yaml`. Adding a kind means writing a `parse<Kind>Manifest(data []byte)` and an `apply<Kind>(cmd, doc)`, then one entry in `genericKinds`; it is multi-document for free.
- `internal/commands/apply/documents.go` splits the stream with the yaml.v3 parser, not a line split, so a `---` inside a block scalar stays inside its document. A file yielding exactly one document passes the **original bytes** through unchanged, which is what keeps single-document parse errors (line numbers included) byte-identical. Documents are parsed independently, so a YAML anchor defined in one document can no longer be aliased from another — each document must repeat the block it needs.
- Flag: `--fail-fast` (persistent on `apply`) — stop at the first failure and name the untried remainder.
- Throttle handling: `internal/cmdutil/throttle.go`'s `cmdutil.RetryOnThrottle` retries a 429 with backoff (2s doubling to 16s, 5 attempts), detecting it via `*apierror.ThrottleError` — the HTTP status, never the message. Two callers: `applyTableRuleInstantiate` (`internal/commands/apply/table_rules.go`) and `runCreateDryRunJob` (`internal/commands/create/dry_run_job.go`), because the backend enforces its per-user dry-run cap at both the instantiate and the launch endpoint. The caller chooses the writer for the progress line: prose-only handlers pass `cmd.OutOrStdout()`, handlers whose stdout is JSON pass `cmd.ErrOrStderr()`. Tests drive the clock with `cmdutil.SetThrottleSleep`/`ResetThrottleSleep`. Documents are applied sequentially on purpose.
- File: `internal/commands/apply/env.go` — `expandEnvVars(s)` substitutes an exact `${VAR}` field value and preserves other dollar signs

### The Catalog (`internal/catalog`) — no hand-written registry

There is no hand-written resource table. Two things join to form the catalog,
and each owns a different half of the truth:

- **Existence**, owned by the cobra tree. Every command node carries a
  `resource` annotation via one of the helpers in `internal/catalog/tree.go`:
  `catalog.For("<name>")` (acts on exactly one resource), `catalog.Parent()`
  (a pure dispatch parent like `get`/`describe`, whose children carry the
  truth), or `catalog.Dispatcher(kinds)` (a generic `-f` dispatcher —
  `apply`/`diff` — annotated with the manifest kinds it accepts).
- **Semantics**, owned by one markdown page per resource under
  `internal/resources/pages/<name>.md`. Frontmatter carries `summary`,
  `aliases`, `kind` (apply manifest kind, if any), `schema`, `writeSchema`
  (the `openapi:` component writes take, when it differs from the read
  `schema`; `explain` merges its top-level fields in and marks each one only
  one side has `<write-only>` or `<read-only>`), `createOnly` (top-level
  fields of that document apply takes only when it creates the resource:
  `[state]` marks `<create-only>`, while `{state: [draft]}` marks
  `<create-only: draft>` and records the accepted create values separately
  from the read enum), `guide`, and a `signature`;
  the body carries USAGE/EXAMPLES/GOTCHAS. Loaded via `resources.Pages()`
  (`internal/resources/pages.go`), which imports nothing else under
  `internal/` — commands import it, never the reverse, or the import graph
  would cycle.

`internal/catalog` (`build.go`) joins the two: `catalog.Build(root)` walks the
tree, unions each resource's tree verbs with its dispatch verbs, classifies it
(`ClassApply`/`ClassCreate`/`ClassReadOnly`), and returns one `Resource` (page
+ verbs + class) per name — a resource present in the tree but missing a page
is still returned, with usage synthesized from the tree, so a missing page
never blocks a user. `catalog.Lookup(root, name)` resolves a canonical name or
alias to one `Resource`. Consumers: `qctl api-resources`, `qctl explain`, and
the `qctl create` redirect all read from `catalog.Build`/`catalog.Lookup` —
none of them has its own resource list. `qctl apply --help`'s valid-kinds line
is the exception: it does not read from the catalog at all. It (`applyKindsLine()`
in `internal/commands/apply/apply.go`) and the `apply` command's
`catalog.Dispatcher` annotation are both generated independently from
`apply.genericKinds` (`internal/commands/apply/generic.go`), so help text and
the catalog annotation cannot disagree — but neither one is sourced *from*
`internal/catalog`.

The "no drift" tests live in `internal/catalog/*_test.go`, not in
`internal/resources`: `TestEveryCommandIsAnnotated` (no command node without a
`resource` annotation), `TestEveryResourceHasPage` (no tree resource without a
page — run with `-create` to scaffold one), and `TestDispatcherKindsResolve`
(no kind a `Dispatcher` accepts without exactly one page declaring it via
`kind:`; the backend's `CompositeDataset` wire alias for `LinkedTable` is
resolved by `pkgmanifest.CanonicalKind` (`internal/pkg/manifest/manifest.go`),
which `apply -f` and `diff -f` both call, so it is deliberately not a catalog
kind in its own right).

### Smart Apply for Rules (kind: Rule)

File: `internal/commands/apply/rules.go` — `applyRuleYAML()`

Not a simple create/update — implements a **smart diff** against live state:
1. Fetches live state via `GetRuleRevisionDetails`
2. Compares field by field using JSON normalization (`fieldEquals` via `json.Marshal`)
3. Outcomes per document: `"patched"`, `"unchanged"`, or `"failed"`
4. Output: `rule/<name> (<short-id>) patched|unchanged|failed`

Immutable fields (changes rejected → user told to use `qctl submit rules`):
`release`, `code`, `description`, `default_severity`, `input_columns`, `optional_columns`, `validates_columns`, `corrects_columns`, `enriches_columns`, `affected_columns`, `affected_column_types`, `param_schema`, `is_builtin`, `is_caf`

Server-computed fields ignored by apply: `params_renderability`, `params_renderable_in_basic_ui`, `renderability_ruleset_version`. They may differ from a saved manifest after a renderability ruleset update.

Patchable fields: `state` (from `spec` or `status`), `is_default` (from `spec` or `status`)

### Apply Table Rules (kind: TableRule)

File: `internal/commands/apply/table_rules.go`

Two modes based on manifest content:
- **Patch** (`metadata.id` present): sends a merge-patch built by `dataset_rules.BuildPatchMap()` from the spec keys listed below
- **Instantiate** (`spec.rule_revision_id` present, no `metadata.id`): creates new dataset rule via `client.InstantiateRule()`
- Instantiate validates `params` with `jsonschemautil.ValidateParams()`; patch apply and diff share `dataset_rules.PreparePatchRevisionFields`, which validates params and resolves produced fields against the governing revision
- `spec.rule`+`spec.release` and `spec.column` are shorthands: they resolve into `spec.rule_revision_id` and `spec.column_mapping` (`ResolveManifestRule`, `resolveShorthandColumn`) before the patch body is built, which is why they are not patch keys of their own

**kind: TableRule fields.** Generated from `TableRuleSpec` and from
`BuildPatchMap` itself — see `tableRuleManifestFields` in
`internal/catalog/docs_test.go`. Do not hand-edit.

<!-- BEGIN GENERATED: table-rule-manifest-fields -->
```
get/describe table-rule -o yaml writes these spec keys:
  row_filter
  dataset_id
  state
  severity
  rule_revision
  params
  column_mapping
  produced_fields

apply -f / diff -f turn these spec keys into a PATCH body:
  spec.row_filter
  spec.rule_revision_id
  spec.instance_name
  spec.state
  spec.severity
  spec.params
  spec.column_mapping
  spec.produced_fields
```
<!-- END GENERATED: table-rule-manifest-fields -->

Manifest structure:
```yaml
apiVersion: qluster.ai/v1
kind: Table|Destination|CloudSource|DryRunJob|Rule|TableRule|RuleReviewPolicy|DataReviewPolicy
metadata:
  name: <string>
spec:
  ...
```

Loading: `pkgmanifest.LoadBytes(data)` → check `APIVersion` + `Kind` → `pkgmanifest.StrictUnmarshal(data, &typedManifest)`

- `metadata` may be empty for instantiate (`metadata: {}`) — `kind: TableRule` is
  exempt from the generic `metadata.name` requirement (`kindsWithSpecIdentity`
  in `internal/pkg/manifest/manifest.go`).
- Instantiate always fetches the rule revision once, then:
  `column_mapping` omitted/empty → identity mapping built from `input_columns`
  (rarely useful for a builtin, whose input names are placeholders, not real
  table columns); explicit mapping → must cover every *required* input column
  (`input_columns` minus the revision's `optional_columns`) — optional inputs
  and validates/corrects/enriches columns are allowed keys but not mandatory
  ones (client-side errors name missing/unknown keys). `spec.column` is sugar
  for the common one-required-column case: it expands to a full
  `column_mapping` before the confirmation prompt and is rejected outright for
  multi-column rules or alongside `column_mapping`.
- `spec.upgradedFrom` is an instantiate-only, apply-only dataset-rule UUID or
  `r-*` alias. `resolveUpgradeSource` resolves the alias in the active server
  scope, fetches the predecessor, validates the same dataset and rule family,
  and puts the UUID in `upgraded_from_dataset_rule_id`. It intentionally does
  not live on the round-trip `TableRuleSpec` and is rejected in patch mode.
  The result renderer branches on created state versus staging so it never
  claims the predecessor was retired while the replacement is still pending.
- `qctl apply --yes`/`-y` skips both confirmation prompts; without it, a
  non-TTY stdin fails fast with a message pointing at `--yes`
  (`cmdutil.CanPrompt`).

### Apply Linked Tables (kind: LinkedTable / CompositeDataset)

File: `internal/commands/apply/linked_table.go` — `applyLinkedTable()`
Domain package: `internal/linked_tables/` (`manifest.go`, `client.go`, `export.go`).

- "Linked table" is the CLI vocabulary for the backend "composite dataset".
  The CLI accepts `kind: LinkedTable` or `kind: CompositeDataset`; `ToAPIRequest`
  always sends `kind: CompositeDataset` on the wire.
- The backend endpoint `POST /orgs/{org}/composite-datasets/apply` is idempotent
  server-side (create-or-converge), so the client does a single POST — no
  client-side get/create/update split.
- `spec.sources.<key>.join_fields.<col>` is a union: a shorthand string (source
  field name) or an expanded `CompositeJoinClause`. The CLI models this with the
  `JoinField` type + custom `UnmarshalYAML`; conversion uses the generated
  `FromApplySourceSpecJoinFields0` (string) / `FromCompositeJoinClause` (clause).
  `JoinField` also has `MarshalYAML`/`MarshalJSON` for the manifest
  `get linked-table` writes (`linked_tables.ExportManifest`, `export.go`).
- `LoadManifest` strict-decodes into `LinkedTableManifestWithStatus`, so the
  `status:` block `get linked-table` writes is accepted and dropped; any other
  unknown key is still exit 2.
- `--validate-only` sets the `validate_only=true` query param (validate, persist
  nothing) and goes through `Client.Validate` instead of `Client.Apply`. This is
  **not** the same as manifest `spec.dry_run` (`max_spine_rows`, `seed`), which
  only tunes the composite dry run a real apply launches. To persist a
  non-active linked table, use `spec.desired_state: draft`.
- The generated `JSON200` is untyped (`*interface{}`), so both methods read
  `resp.Body`. `Apply` decodes it into `linked_tables.ApplyResponse`, a local
  mirror of atlas `CompositeDatasetApplyResponse` (the generated client has no
  type for it until a regen picks up atlas #1091); a body with no `result` is
  still a successful apply and comes back as a nil answer, not an error.
  `Validate` decodes `{valid, errors, warnings}` and refuses an
  answer without `valid` (exit 5); `ValidateResult.Err` turns `valid: false`
  into an exit-2 error listing each issue. Issues are
  `api.CompositeValidationIssue`, rendered by `linked_tables.FormatIssue` as
  `<loc>: <msg> (<code>)` with `loc` verbatim.
- A `400`/`409` whose `detail` is an issue list comes back as
  `*linked_tables.IssuesError`, which unwraps to the HTTP error so the exit code
  survives; any other refusal is the plain HTTP error.
- Default output (`writeLinkedTableApplied`) is `linked-table/<name>
  created|configured|unchanged` (`revision_added` prints as `configured`),
  then `revision :` and `dry run  :` lines and a `Next:` line chosen by the
  converge outcome; a nil answer prints `linked-table/<name> applied`.
  `--validate-only` prints `validated`, valid answers only. Warnings go to
  stderr in both, and `-o json` dumps the raw backend response body.

### Authoring a `qctl guide` topic

Guides are offline, task-oriented tutorials in `internal/commands/guide`.
To add one:
1. Create `internal/commands/guide/topics/<name>.md` (plain markdown; it is
   embedded via `//go:embed topics/*.md`).
2. Add a `{Name, Summary, File}` entry to the `topics` slice in `topics.go`
   (registry order = index order).
That's it — `topics_test.go` fails if a file is orphaned or a body is empty.
Output is pretty ANSI on a TTY and raw markdown when piped (agent-friendly).

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

Settle the mode choice and parse positional IDs or the `--filter` payload
before `cmdutil.Bootstrap`; every local refusal is bad usage (exit 2). Carry
the parsed IDs or request params into the authenticated execution path rather
than parsing the command line a second time after credentials are loaded.

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

`resp.JSON200` holds every number under an `interface{}` field as a `float64`,
so an integer past 2^53 is already rounded. Where such a field carries user
data, or anything qctl sends back to the server, decode `resp.Body` with
`jsonnum.Unmarshal` into the same type and return that instead — as
`GetDataset`, `GetDatasetRuleDetail`, `GetRuleRevisionDetails`,
`CheckColumnUniqueness`, `GetDryRunJob` and the dry-run previews do. It cannot reach below a generated `UnmarshalJSON` (the
`AdditionalProperties` structs), which runs its own `json.Unmarshal`;
`decodeReview` in `internal/data_reviews/client.go` patches those values from
a second decode of the same body.

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

### notifications (internal/notifications/)

Standard-pattern client (`NewClient` returns `*Client`, no error) over the inbox's seven generated operations, plus a `types.go` carrying the `notification_type` vocabulary every notification command shares.

- `types.go` — `Type` (alias for `api.NotificationType`), `Types`, `TypeNames()`, `IsNeedsYou(Type)`, `ParseTypes([]string) ([]Type, error)`. `ParseTypes` returns `qerrors.NewBadUsagef` (exit 2) for a value the backend enum does not carry, so a bad `--type` never reaches the wire. An AST drift test in `types_test.go` pins the vocabulary against `client.gen.go`, so a spec change cannot leave a stale value behind.
- `client.go` — `List`, `UnreadCount`, `MarkRead`, `MarkAllRead`, `Dismiss`, `DismissAll`, `DismissByType`.

**Read and dismissed are two independent axes, end to end.** The backend keeps `read_at` and `dismissed_at` as separate columns with separate endpoints, and neither is ever set from the other: marking read never dismisses, dismissing never marks read. `ListOptions.UnreadOnly` narrows the read axis, `ListOptions.IncludeDismissed` widens the dismissed axis, and they compose. Both are sent only when true — the backend defaults each to false, so an explicit `false` would put a filter on every request the user never typed. `Page`/`Limit` are likewise sent only when positive.

`MarkAllRead` takes a repeatable type filter and is one request; `DismissByType` takes ONE type in the path, so dismissing several types is one request each. The asymmetry is the endpoints', not qctl's.

`GET /notifications/rule-upgrades` is deliberately unwrapped: it is strictly dominated by `List` with a `rule_upgrade` type filter, which also honours `unread_only` (the alias route does not), so a client method for it would be untested dead code.

Command files (four, across two parents — `get` reads the inbox, the `notification` group writes to it):

- `internal/commands/get/notifications.go` — `qctl get notifications` (the listing) AND `--count`, which branches to `UnreadCount`. `rejectCountFilters` runs AHEAD of `cmdutil.Bootstrap`, so a rejected combination costs no config read and no request. The two modes share one file and one `catalog.For("notification")` annotation but nothing else: `--count` refuses `--type`/`--unread`/`--include-dismissed`/`--page`/`--limit` and `-o name` in `rejectCountFilters`, all at exit 2, and it prints a fixed two-column table rather than a row projection.
- `internal/commands/notification/notification.go` — the `notification` parent (`catalog.Parent()`), registering `read` and `dismiss`.
- `internal/commands/notification/read.go` — `qctl notification read <id>|--all`, with `--type` (repeatable) legal only under `--all`.
- `internal/commands/notification/dismiss.go` — `qctl notification dismiss <id>|--all`, same shape plus a `--yes` confirmation gate on the bulk form only.

`scopeFlags` (in `notification.go`, beside the parent) is the shared arg/flag validator for both verbs: exactly one of `<id>` or `--all`, `--type` only with `--all`, and a single id goes through `cmdutil.ValidateUUIDArgs(args, "notification id")`. Every refusal is `qerrors.NewBadUsage[f]` (exit 2). It is why the two verbs cannot drift apart on argument handling.

The `--type` fan-out asymmetry above surfaces here: `read --all --type a --type b` is one request, `dismiss --all --type a --type b` is two. The dismiss path sums the counts, reports them per type, and on a part-way failure attempts every remaining type, prints what landed, and returns the FIRST failure wrapped with `%w` — so the exit code is the failure's, not a flat 1.

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
- `GetAllRuleRevisions(accessToken, params)` — consume every matching ordered page; uses `total_rows` because Atlas does not set `next` on ordered pagination
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
- Resolution order: full UUID (fast path, no request) → exact name or slug → UUID prefix / SHORT-ID → fuzzy substring. Non-hex names use every matching `search_query` page. A prefix-shaped input such as `decade` first consumes those pages only to find an exact name/slug; after an exact-name miss it expands from the `aliasstore` short-ID cache, falling back to an unfiltered catalog listing of the first 1000 revisions, which names that bound only when nothing in those rows matches (the backend cannot search by `id`)
- `RememberRevisionShortIDs(serverURL, orgID, ids)` — seeds the short-ID cache; called by `get rules`, `get rule-revisions`, and by the resolver itself after any listing

Describe (`describe.go`):
- `FormatDescribe(detail, showCode)` — produces human-readable plain text (NOT a YAML manifest)
- Shows: Identity, Flags (State/Default/Built-in/CAF), Description, Columns, Param Schema, Provenance, Code (15-line preview by default)

Display (`display.go`):
- `RuleRevisionDisplay` struct with `Tags` and `ShortID` computed fields
- `ToDisplayList(rules)` — builds tags: Default, Built-in, CAF, Update available, Newer than default. `Update available` / `Newer than default` both derive from the server's `upgrade_available`, which capi sets by semver against the family default — qctl never recomputes it.

Types (`types.go`):
- `RuleGetManifest` / `RuleGetSpec` / `RuleGetStatus` — for `get rule -o yaml` (apply-compatible, kind: Rule). This is the only manifest `get rule` emits; there is no multi-release/`kind: RuleFamily` document, and `-o yaml`/`-o json`/`-o code` all require `--release` when the name has several.
- `RuleApplyManifest` / `PatchManifest` — for parsing `apply -f rule.yaml` input
- `FullResponseToGetManifest(resp)` — converts `RuleRevisionFull` to `RuleGetManifest` (apply-compatible)

### dataset_rules (internal/dataset_rules/)

Full domain client for table rules (dataset rules). Used by `get table-rules`, `describe table-rule`, `apply` (kind: TableRule).

Files: `client.go`, `resolver.go`, `display.go`, `types.go`

Client methods:
- `GetDatasetRules(accessToken, datasetID, params)` — list rules for a table; params include `InstanceName` for server-side filtering
- `GetDatasetRuleDetail(accessToken, datasetRuleID)` — single rule detail (**flat endpoint**, no datasetID needed)
- `ResolveDatasetRuleID(accessToken, datasetID, input)` — resolves name/short-ID/UUID to full UUID; full UUID fast-paths without API call, every other input first uses the exact server-side `InstanceName` filter with a one-row uniqueness bound, and a UUID-like exact-name miss consumes every ordered table-rule page for prefix matching
- `PatchDatasetRule(accessToken, datasetID, datasetRuleID, body)` — patch
- `InstantiateRule(accessToken, ruleRevisionID, body)` — instantiate a rule revision onto a dataset (POST); returns `(*InstantiateOutcome, error)`: `Created` (`*DatasetRuleDetail`) on 201, `Staged` (`*Suggestion`) on 202 when a draft migration was open and the rule touched the schema

Resolver: `ResolveDatasetRule(rules, input)` — in-memory resolution by instance_name or UUID/short-ID prefix (used internally by `ResolveDatasetRuleID`)

Display (`display.go`):
- `DatasetRuleDisplay` struct: ID, ShortID, InstanceName, Release, Position, State (title-cased), Filter (opt-in `--columns filter`: trimmed row-filter description, blank for no filter, `Checks only some rows` when undescribed), Severity (`spec.severity` verbatim: `warning`/`blocker`/`as_declared`), Author, CreatedAt, UpdatedAt, DatasetColumns
- `ToDisplayList(rules)` — converts list of `DatasetRuleTiny` to display structs
- `DetailToDisplay(detail)` — converts single `DatasetRuleDetail` to display struct
- Both display conversions carry opt-in `validation_state`, `pending_migration_model_id` and `schema_change_blocks_enable` columns directly from the response.

Types (`types.go`):
- `TableRuleManifest` / `TableRuleRawManifest` — for describe output; the round-trip manifest carries read-only `status.validation_state`, `status.validation_dry_run_job_id` and `status.pending_migration_id` when available, plus explicit `status.schema_change_blocks_enable` and `status.validation_blocks_enable` booleans
- `TableRuleApplyManifest` — for apply input; has `IsPatch()` and `IsInstantiate()` methods
- `APIResponseToManifest(resp)` — converts API detail to `TableRuleManifest`, the round-trip shape (spec keys generated above). Takes no verbosity: the `-v` tier it once had added `status.dataset_field_names`, which the API no longer returns
- `APIResponseToRawManifest(resp)` — converts API detail to `TableRuleRawManifest` for -vv raw dump

## Rule-Specific Command Details

### get rules (`internal/commands/get/rule_families.go`)

Lists rule **families** (one row per rule name, not individual revisions).
- Uses `rule_families.NewClient` and `tableui.PrintFromCmd`
- Default columns: `slug,release,state,tags,author,short_id`
- Flags: `--limit` (default 1000), `--page`, `--order-by` (default `impact_score`), `--reverse`, `--search`, `--exclude-builtin`

### get rule-revisions (`internal/commands/get/rule_revisions.go`)

Lists all individual revisions (flat list).
- Default columns: `slug,release,state,tags,short_id`; with `-v`: `slug,release,state,tags,id,description,affected_columns`.
- Flags: `--limit`, `--page`, `--order-by`, `--reverse`, `--state`, `--search`, `--only-default`, `--has-upgrade-available`, repeatable `--table-kind`, `--general`, `--for-table`, `--match`. Table output includes the backend's table-kind and General facet counts. `--for-table` uses the table match endpoint and adds match columns and filtered full/partial/generic counts; among these flags it accepts `--match`, `--page`, `--limit`, `--search`, and `--table-kind`.
- A non-enabled `--state` sends `only_default=false` unless `--only-default` is explicit; `enabled`, `none`, and an omitted state leave the server's default in place.

### get tables --for-rule (`internal/commands/get/datasets_matches.go`)

Lists tables matching a rule revision. A rule name or slug selects its default revision; `--release` selects another release, and a revision ID selects that revision directly. The match route accepts only `--page` and `--limit` (default 20); table output shows `name,match,concepts,missing` with full/partial counts, while JSON/YAML print the raw match items.

### get rule \<name-or-id\> (`internal/commands/get/rule.go`)

Fetches a single rule (or all releases of a rule family).
- Flag: `--release` — pins to a single revision; omitted → shows all releases
- Output formats (declared as `table,json,yaml,code,name`):
  - `table` (default): `tableui.PrintFromCmd` with display structs
  - `name`: same call — `tableui.PrintFromCmd` delegates it to the printer, which prints one bare revision ID per line
  - `code`: `qctl get rule foo -o code` — prints raw Python source only (via `writeCode()`)
  - `yaml`/`json`: single release → `kind: Rule` manifest via `FullResponseToGetManifest()` (apply-compatible); multi-release without `--release` → error
- Helper: `encodeStructured(cmd, format, data)` — shared JSON/YAML encoder (also used by `get table-rule`)

### get table-rule \<name-or-id\> (`internal/commands/get/table_rule.go`)

Fetches a single table rule (dataset rule) by instance name, short ID, or full UUID.
- **Required flag:** a table selector — `--table <name>` or `--table-id <id>`; `--table` is a NAME, never an ID (`cmdutil.AddTableFlags`, resolved via `cmdutil.ResolveTableIDFromFlags`)
- Uses `dataset_rules.NewClient()` (error-returning constructor)
- Resolves via `client.ResolveDatasetRuleID()` → `client.GetDatasetRuleDetail()`
- Output:
  - `table` (default): `tableui.PrintFromCmd` with `dataset_rules.DetailToDisplay()`; default columns: `instance_name,release,state,severity,short_id`; `-v` swaps `short_id` for `id,created_at,updated_at`
  - `json`/`yaml`: `dataset_rules.APIResponseToManifest(detail)` — produces kind: TableRule manifest

### describe table-rule \<name-or-id\> (`internal/commands/describe/table_rule.go`)

Shows details of a table rule with YAML manifest output (default).
- **Required flag:** a table selector — `--table <name>` or `--table-id <id>`; `--table` is a NAME, never an ID (`cmdutil.AddTableFlags`, resolved via `cmdutil.ResolveTableIDFromFlags`)
- Default output: YAML (standard describe pattern)
- **Verbosity tiers:**
  - (default): the round-trip manifest via `APIResponseToManifest()` — spec keys generated above, apply-compatible
  - `-vv`: Raw API response dump via `APIResponseToRawManifest()`
  - `-v` adds nothing here; it once added `status.dataset_field_names`, which the API dropped
- Supports `-o json` / `-o yaml`

### describe rule (`internal/commands/describe/rule.go`)

**Exception to standard describe pattern**: default output is **plain text** (not YAML manifest).
- Uses `rule_versions.FormatDescribe()` for human-readable non-round-trippable output
- Flags: `--release`, `--show-code` (show full code; default is 15-line preview)
- With `-o yaml`/`-o json`: outputs raw API response
- Users should use `get rule -o yaml` for apply-compatible output

### get / describe table-schema (`internal/commands/get/table_schema.go`, `internal/commands/describe/table_schema.go`)

Read-only view of a table's schema (backend "migration") state. The positional
arg is a table id-or-name resolved with `datasets.Client.ResolveID`.

- Domain client methods (in `internal/datasets/client.go`):
  `GetSchemaDetails(token, datasetID)` → `*datasets.TableSchemaDetail`
  (= `api.MigrationDetailResponse`) and `GetSchemaFields(token, datasetID)` →
  `[]string` (current applied column names; server-side cached).
- **Shared grid renderer**: `internal/datasets/schema_display.go` —
  `RenderSchemaGrid(cmd, detail, verbosity)` is used by BOTH commands so the
  field grid renders identically. Default columns `field,type,nullable`;
  `action` is appended only when a draft exists (`migration_state !=
  current_only`); `-v` adds `indexed,original_name,datetime_formats,display_decimals,flags`
  (flags via `tags.Build`).
- Structured output: `SchemaStructuredPayload(detail, includeNotableValues)`
  roundtrips through JSON→`interface{}` (YAML-safe for the generated union
  types) and strips per-field `notable_values` unless verbosity >= 1.
- The apply-compatible manifest carries every writable field property,
  including `display_decimals`; pointer fields preserve omission separately
  from meaningful zero/false values, and apply/diff share the same overlay.
  `display_decimals` is the one property with a third state: a stated
  `null` asks for the backend's default and is sent as a null.
  `ParseTableSchemaManifest` marks it from a second, non-strict read of the
  document (`markResetDisplayDecimals`), because yaml.v3 resolves a null
  scalar to the destination's zero value before any custom unmarshaler runs.
- `get table-schema -o name` uses ONLY the fields endpoint (never falls back
  to details) and always reflects the current applied schema.
- `describe table-schema` default output is **plain text**: header
  (`FormatSchemaHeader`) + pending suggestions (`FormatSuggestionsSection`)
  + grid. Suggestions are paginated, with update/delete detail reads while a
  draft exists; failed suggestion reads degrade to unavailable. Structured
  views skip these reads. `-v` appends `FormatSchemaChanges` (per-field
  diffs + technical IDs); `-vv` dumps the raw API response. Non-json/yaml `-o`
  values fall back to the text view.
- Recreated periods add previous keys and reuse availability in the header,
  previous-key counts through `FormatKeyColumnCandidates`, and the pending
  carried-rule fit table through `FormatRecreatedPeriodRules`. Fits describe
  column availability, not types; dismissals can change them. These fields
  come from the schema response without extra requests.
- Naming: the user-facing term is "table schema"; "migration" never appears in
  help text or grid headers (raw API field names in payloads are fine).

### submit rules (`internal/commands/submit/rules.go`)

- `-f` flag is `StringArrayVar` — accepts multiple files
- Validates all files are `.py` before bootstrapping auth
- Client file limit: 500,000 Unicode code points; server preflight also caps UTF-8 bytes at 500,000
- Flags: `--force` (skip, rather than reject, a locked revision whose code changed), `--yes`, `--check`
- `--check`: `checkRuleFile` sends `submitRequest`'s body — the one `submitRuleFile` sends — to `CheckRuleVersion` (`POST rule-revisions/check`) and renders `api.RuleRevisionCheckResponseSchema` with `displayRulesCheckResult`. Errors go through `renderSubmitError`; `handleCheckError` (`internal/rule_versions/client.go`) turns 404/405 into exit 5 and prefixes a 422 with `submit would reject this file`.
- Shows "Unchanged rule(s):" section for rules already at latest version
- Phase 3 (revise & resubmit): request wrapper `rule_versions.RuleVersionSubmitRequest{FileText, Force, Verbose}`; `Verbose` is set when Bootstrap verbosity ≥ 1 and asks the backend for the full diff on a locked-revision conflict. The client returns `*rule_versions.VersionConflictError{Diff, Err}` only for a 422 `ERR_RULE_VERSION_CONFLICT` with a non-empty `detail.diff` (`errors.As` in the command; `Unwrap` preserves exit-code mapping).
- Draft breadcrumbs: after a successful submit, `printReviewBreadcrumbs` looks up each added slug via `GetAllRuleRevisions(SearchQuery=<slug>, OnlyDefault=false)` and prints `Next: qctl review request <name> --release <release>` for revisions in `draft`. Every matching page is read; lookup failures remain best-effort and print nothing.
- Resubmission semantics (backend-verified): re-submitting the same release while the revision is `draft`/`in_review` updates it in place and appears in **neither** `added` nor `not_changed`; stale approvals are dismissed per policy. Force-skipped locked revisions are also absent from the response — hence the standing `--force` note in the output.

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

For `-o name`, both reflection printers print `<kind>/<id>` per object (`output.NameRefs`): the kind is `output.NameKind(cmd)` — the command's `output.WithNameKind` annotation, else its catalog resource — and the id is the JSON field `id`, or the one `Printer.NameAs` names. An object with no id emits no line. Every argument that takes the kind strips it again: positionals through `cmdutil.StripKindRefs` (slot kinds from `cmdutil.WithArgKinds`, default the command's resource, `-` for a slot that is not a reference) and flags registered with `cmdutil.RefInt`/`RefString`; `cmdutil.ParseKindRef` is the one parser.

Human renderers make server-supplied C0/C1 terminal controls visible as ASCII
`\xNN` escapes and Unicode bidirectional ordering controls visible as ASCII
`\uXXXX` escapes before adding any qctl-owned ANSI styling. This applies to
Markdown detail text, table cells, and `-o name`; JSON/YAML encoders retain the
original string value and escape it according to their format. Join controls
such as ZWJ and ZWNJ remain unchanged. The shared policy is
`terminal.Sanitize` in `internal/terminal/sanitize.go`.

`--max-column-width` is measured in terminal cells, not bytes. Ordinary table
cells are truncated only between whole grapheme clusters and retain the
historical `...` suffix; message cells additionally shorten marked values
before clipping.

Read the cap through `output.MaxColumnWidth(cmd)`
(`internal/output/column_width.go`), never from the flag. A passed value wins,
`0` included; otherwise it is 80 when stdout is a terminal and 0 (no limit) when
it is not. Root registers the flag with default 0. A test that asserts
truncation must pass `--max-column-width`, because a `bytes.Buffer` is not a
terminal.

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
    rootCmd.PersistentFlags().Int("max-column-width", 0, "")
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
| `cmdutil.PositiveIDFlag(cmd, name)` | same | Read an optional int ID flag → `(value, set, error)`; rejects `0`/negatives instead of silently dropping the filter |
| `cmdutil.PositiveSecondsFlag(cmd, name)` | same | Read a poll-interval flag → `(value, error)`; rejects `< 1`, and seconds that overflow `time.Duration`, instead of panicking `time.NewTicker` |
| `cmdutil.PositiveCountFlag(cmd, name)` | same | Read `--page`/`--limit`/`--chunk-size` → `(value, error)`; rejects `<= 0` instead of letting the client drop the parameter |
| `cmdutil.NonNegativeCountFlag(cmd, name)` | same | Read the `0 = unlimited` `--limit` of `get cloud-sources`/`get files` → `(value, error)`; rejects negatives only |
| `cmdutil.ParseIntArgs(args, name)` | same | Parse positional int args |
| `cmdutil.AddWaitFlags(cmd, what)` | `internal/cmdutil/wait_flags.go` | Register `--wait` and `--wait-timeout` (seconds, default 900) |
| `cmdutil.WaitFlags(cmd)` | same | Read them → `(timeout, wait, error)`; `--wait-timeout` without `--wait`, or below 1, exits 2 |
| `cmdutil.AddWatchTimeoutFlag(cmd, def)` | `internal/cmdutil/watch.go` | Register a watch's `--timeout`, a duration defaulting to `def` (`0` = no limit) |
| `cmdutil.WatchBudget(cmd)` | same | The watch's budget, `0` = no limit: `--timeout` when set, else `QCTL_WATCH_TIMEOUT`, else `--timeout`'s default. A negative `--timeout` or an invalid `QCTL_WATCH_TIMEOUT` exits 2 |
| `cmdutil.StartWait(cmd, cmdutil.WaitSpec{...})` | same | Write the wait's one-line banner to stderr and return a context cancelled at `Budget` (unless `SelfTimed`) and on the first SIGINT/SIGTERM not already ignored, plus the `*Waiter`. `Kind` is `WaitUntilDone` (running out of time fails) or `WatchStream` (running out of time ends cleanly); `Next` is the resume command. The first signal also arms a 2 s timer that exits the process unless `Stop` runs first |
| `waiter.Ended(err)` | same | Classify the loop's result `err`: after a signal, a `*qerrors.Interrupted`; at the budget, when the loop gave up (a context error, an error wrapping one such as `qerrors.Caused(msg, context.Cause(ctx))`, or nil for a `WatchStream`), a `WatchStream` writes `timed out after <Budget>` and the resume clause to stderr and returns nil, and a `WaitUntilDone` returns a `qerrors.NewTimedOut` error ending in the resume line, if any; otherwise `err` itself |
| `waiter.Stop()` | same | `defer` it after `StartWait`: releases the signal registration, stops the timer a signal armed and cancels the wait's context; safe to call twice |
| `cmdutil.SetInterruptGrace(d)` / `ResetInterruptGrace()` | same | Test hook for the signal grace period: set `0` before starting a wait to disable its process-exit timer, then restore the two-second default with `t.Cleanup(cmdutil.ResetInterruptGrace)` after the wait has stopped. Use only in sequential tests |
| `cmdutil.ResumeCommand(cmd, drop...)` | same | The command line that re-runs `cmd`: `qctl`, its path, its positionals, then the set flags not named in `drop` (sorted, rendered by `RenderFlag`), all shell-quoted; when a positional starts with `-`, the flags go first and `--` precedes the positionals |
| `cmdutil.RenderFlag(f)` | same | One set flag as the words that set it again: `--name` or `--name=false` for a bool, one `--name value` per slice element, `--name=value` for a flag with an optional value |
| `pollwait.Run(ctx, poll, opts)` | `internal/pkg/pollwait/pollwait.go` | The `--wait` poll loop: polls until `Settled`, ends on a permanent poll error, retries any other, and reports `*Expired` at `Timeout`; each poll receives a context bounded by `Timeout` and `ctx`. Parent cancellation ends it, even mid-request or mid-sleep, with `pollwait.ErrStopped`. `Now`/`Sleep` are its clock |
| `qerrors.IsPermanent(err)` | `internal/errors/errors.go` | Whether a failed poll ends a wait: bad usage, not found, unauthorized or API incompatible |
| `qerrors.NewTimedOut(cause)` | same | Exit 124: a wait that gave up. Its message is `cause`'s, and `errors.Is`/`errors.As` reach `cause` through it |
| `qerrors.Caused(message, cause)` | same | An error whose message is `message` and whose `Unwrap` is `cause` — how a wait loop says what it still waited for over `context.Cause(ctx)` |
| `qerrors.Interrupted{Signal, Message}` | same | Exit 128 + the signal (130 SIGINT, 143 SIGTERM); `GetExitCode` checks it before anything else in the chain. `qerrors.Exit` prints `Message` and re-raises `Signal`, exiting 128+N itself if the process survives |
| `config.WatchTimeoutFromEnv()` | `internal/config/config.go` | `QCTL_WATCH_TIMEOUT` as a Go duration → `(d, set, error)`; unset or empty is `set == false`, a negative or unparsable value an error |
| `ws.ReconnectNotice(w)` | `internal/ws/client.go` | A `ws.Config.OnReconnect` that writes `connection lost (<cause>); reconnecting in <wait> (attempt <n>)` to `w` |
| `client.Follow(ctx, token, job, dry_runs.FollowOptions{...})` | `internal/dry_runs/follow.go` | Hand a dry-run job's progress frames to `OnFrame` until a terminal state, the stream's end or `ctx`'s end; returns nil for finished, the job's `Verdict` for another terminal state, and once `ctx` is done an error whose `Unwrap` is `context.Cause(ctx)`. A stream the server ends early is settled by reading the job again |
| `dry_runs.Verdict(job, blocker)` / `client.Verdict(token, job)` | `internal/dry_runs/verdict.go` | nil for a finished job; otherwise an error naming the state, then its failure detail, then a `Next:` command. The method finds a blocked job's alert through its ingestion job when the job names none |
| `dry_runs.IsTerminal(state)` | same | Whether a state ends a dry-run progress watch: finished, error, killed, stopped, version_id_mismatch, file_missing, blocked or partitioned. A watch stop state may later be retried |
| `dry_runs.WriteFailureDetail(b, job)` | same | Append a dry run's rule load errors and error message to `b` |
| `cmdutil.ConfirmYesNo(cmd.ErrOrStderr(), prompt)` | `internal/cmdutil/prompt.go` | y/N confirmation on stderr |
| `cmdutil.ResolveTable(ctx, tableID, tableName)` | `internal/cmdutil/resolve.go` | Resolve table by ID or name |
| `cmdutil.AddTableFlags(cmd, purpose)` | `internal/cmdutil/table_selector.go` | Register the standard `--table <name>` / `--table-id <id>` pair |
| `cmdutil.ValidateTableSelector(cmd, positional)` | same | Before `Bootstrap`: validate a required selector when the command also takes a table positional arg |
| `cmdutil.ValidateTableSelectorFromFlags(cmd)` | same | Before `Bootstrap`: validate a required flags-only selector |
| `cmdutil.ValidateOptionalTableSelector(cmd, positional)` | same | Before `Bootstrap`: validate an optional table filter / org-wide default |
| `cmdutil.ResolveTableID(ctx, cmd, positional)` | same | After `Bootstrap`: resolve a required selector; defensively revalidates |
| `cmdutil.ResolveTableIDFromFlags(ctx, cmd)` | same | After `Bootstrap`: resolve a required flags-only selector; defensively revalidates |
| `cmdutil.OptionalTableID(ctx, cmd, positional)` | same | After `Bootstrap`: resolve an optional selector — `(0, false, nil)` when omitted; defensively revalidates |
| `cmdutil.ResolveCloudSource(ctx, ...)` | `internal/cmdutil/resolve.go` | Resolve cloud source by ID, name, or auto-detect |
| `cmdutil.RetryOnThrottle(out, what, call)` | `internal/cmdutil/throttle.go` | Retry a 429 with backoff; caller picks the progress writer and the subject |
| `printer.NewPrinterFromCmd(cmd)` | `internal/pkg/printer/helpers.go` | Create printer from cmd flags |
| `printer.NewPrinterFromCmdWithMarkdown(cmd, fields)` | same | Printer whose table renders `fields` as backend messages (one line; marked values shortened first, then the message clipped at `--max-column-width`) |
| `tableui.PrintFromCmd(cmd, data, defaultColumns)` | `internal/pkg/tableui/table.go` | Lipgloss-styled table printer (rule commands) |
| `tableui.PrintMessagesFromCmd(cmd, data, defaultColumns, messageColumns)` | same | `PrintFromCmd` with backend message columns rendered by `markdown.Renderer.RenderCell` |
| `markdown.PlainText(msg)` / `markdown.Inline(msg, opts)` | `internal/markdown/message.go` | Render a backend Markdown message (errors, history, job/alert msgs) as plain text / one line; unwraps `~{PII}~` and `~[field name]~`, decodes `~*value*~`, and shortens only marked values to fit |
| `terminal.Sanitize(text)` | `internal/terminal/sanitize.go` | Make C0/C1 and Unicode bidi controls visible before untrusted text enters a human terminal renderer; never apply to JSON/YAML values |
| `output.NewPrinterFromCmd(cmd)` | `internal/output/helpers.go` | Older printer (same API) |
| `output.WriteRaw(w, format, data)` | `internal/output/raw_body.go` | `-o json` / `-o yaml` of a backend response emitted verbatim: JSON field names in both, integers kept exact in YAML; no secret masking, no columns |
| `jsonnum.Unmarshal(data, v)` | `internal/pkg/jsonnum/jsonnum.go` | `json.Unmarshal` for a target that can hold an `interface{}`: a number there becomes an `int64` when it fits, else a `float64` for a fraction or exponent or for an integer literal `strconv.FormatFloat(f, 'f', -1, 64)` writes for that `float64`, else stays a `json.Number` — so YAML prints `1234567`, not `1.234567e+06`, and JSON keeps integers past 2^53 |
| `jsonnum.NewYAMLEncoder(w)` / `jsonnum.MarshalYAML(v)` | `internal/pkg/jsonnum/yaml.go` | `yaml.NewEncoder` / `yaml.Marshal` for anything a command prints: a whole `float64` below 2^53 comes out as an integer (`1234567`, not `1.234567e+06`), a `json.Number` as a number rather than a quoted string, a literal `<<` mapping key quoted, and a newline-only string quoted so it reads back unchanged; the rest is yaml.v3's own output. `TestNoBareYAMLEncoder` fails on a yaml.v3 `NewEncoder` or `Marshal` under `internal/` outside `internal/pkg/jsonnum` and its allowlist. yaml.v3 reads `yaml` tags only, so what these encode must carry them: a generated api type goes through `output.WriteRaw` instead, and `TestYAMLEncodesWriteYAMLTaggedFields` fails on an encode whose argument can reach a field with no `yaml` tag or a type whose only marshaler is `MarshalJSON` |
| `setDefaultColumns(cmd, cols)` | `internal/commands/get/ingestion_jobs.go` | Set default columns for table format |
| `tags.Build(pairs...)` | `internal/pkg/tags/tags.go` | Build comma-separated tag string from label/bool pairs |
| `timeutil.FormatRelative(t)` | `internal/pkg/timeutil/relative.go` | "5 minutes ago" formatting |
| `timeutil.FormatRelativePtr(t)` | same | Nil-safe version |
| `manifest.LoadFile(path)` | `internal/pkg/manifest/manifest.go` | Load + validate generic manifest |
| `manifest.StrictUnmarshal(data, v)` | same | YAML parse with unknown-field rejection and date text preserved in free-form values; valid JSON objects use a JSON decoder so control characters and numbers retain their values, with duplicate and case-mismatched keys rejected before decoding |
| `manifest.APIVersionV1` | same | Constant: `"qluster.ai/v1"` |
| `apierror.HandleHTTPError(resp, msg)` | `internal/apierror/http.go` | Convert HTTP errors to user-friendly errors |
| `apierror.HandleHTTPErrorFromBytes(code, body, msg)` | same | Same, from pre-read body |
| `apierror.WithPermissionNote(err, code, operationID)` | `internal/apierror/permissions.go` | Append a 403's endpoint permission, granting roles and `qctl auth me` advice using generated spec metadata; `operationID` is the OpenAPI snake-case ID. Deliverables use it on both response and captured-error paths. |
| `apierror.SetOrgScope(display, source)` | `internal/apierror/scope.go` | Record this invocation's organization scope. ONE caller: `cmdutil.resolveOrganization`. |
| `apierror.WithScopeNote(msg)` | same | Append the organization-scope note to a message qctl built itself. Required on every organization-scoped `qerrors.NewNotFound` outside `internal/errors` — `TestEveryClientSideNotFoundCarriesTheScopeNote` fails otherwise. Local contexts and server-scoped alias-cache misses carry no organization note. HTTP 403/404 get it automatically in `buildHTTPError`, except for a response whose `error_code` is in `stateCodes` (`scope.go`); a 404 matching `isUnknownColumnMessage` also skips it. |
| `jsonschemautil.ValidateParams(params, schema)` | `internal/pkg/jsonschemautil/validate.go` | Validate JSON params against JSON Schema (table rule apply and diff) |
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
| `testutil.AssertYAMLReadsAsJSON(t, yamlOut, jsonOut)` | `internal/testutil/yaml_twin.go` | Fail unless a command's `-o yaml` reads back as the tree its `-o json` prints |
| `testutil.LockedBuffer` | `internal/testutil/output.go` | A `bytes.Buffer` several goroutines can write to (`Write`, `String`) |

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
- `GET /api/orgs/{orgID}/rule-revisions` — list rule revisions
- `GET /api/orgs/{orgID}/rule-revisions/{id}` — get rule revision detail
- `PATCH /api/orgs/{orgID}/rule-revisions/{id}` — patch rule revision
- `DELETE /api/orgs/{orgID}/rule-revisions/{id}` — delete rule revision
- `POST /api/orgs/{orgID}/rule-revisions/submit` — submit rule
- `POST /api/orgs/{orgID}/rule-revisions/unsubmit` — unsubmit rule
- `GET /api/orgs/{orgID}/datasets/{id}/dataset-rules` — list table rules (nested under dataset)
- `GET /api/orgs/{orgID}/dataset-rules/{ruleID}` — get table rule detail (**flat path**, no dataset ID)
- `PATCH /api/orgs/{orgID}/datasets/{id}/dataset-rules/{ruleID}` — patch table rule (nested)
- `POST /api/orgs/{orgID}/rule-revisions/{id}/instantiate` — instantiate table rule

## Output Format Behavior

- an unrecognised `-o` value exits 2 (`internal/cmdutil/output_format.go`, called from root's `PersistentPreRunE`); the valid set is the four advertised values unless the command declares its own with `cmdutil.WithFormats(...)`
- commands that declare a narrower or wider set: every `describe` (`table,json,yaml`), `get data`, `get table-kind`, `explain`, `api-resources` (`table,json,yaml`), `get rule` (`+code`), `inspect dry-run-job` (`json,yaml`). The complete list is pinned in `declaredOutputFormats` (`internal/commands/root/output_format_test.go`)
- **a `-f` dispatcher parent with its own `RunE` needs its own `WithFormats`** — `qctl apply` renders results itself, so it declares its vocabulary as its subcommands do. `TestDeclaredOutputFormatsMatchTheInventory` compares declarations against the inventory in both directions, but a command carrying *no* annotation is invisible to both halves: an omitted declaration is the one class of mistake here that no test catches
- `get` commands: default output is `table` format
- `get rule -o code`: outputs raw Python source code (special format for rules only)
- `describe` commands: default output is `yaml` format (override with `-o`), except the text-default ones the Printer column of `docs/commands.md` marks
- `describe rule`: default output is plain text (not YAML); use `get rule -o yaml` for apply-compatible output
- `describe table` / `describe cloud-source`: a text summary for unset `-o` and `-o table` alike, the summary as an object for `-o json|yaml`; use `get table|cloud-source <id> -o yaml` for apply-compatible output
- `apply` commands: the table view prints `<resource>/<name> <action>` on success (e.g., `table/orders created`); `-o name` prints each handler's resource reference, including unchanged and partial results, in file order. Prose goes to stderr under `-o json|yaml|name`.
- `apply` rules: print `rule/<name> (<short-id>) patched|unchanged|failed`
- `apply` review policies: print `<kind-hyphenated>/<name> created|updated (<scope>)` where scope is `organization` or `table <id>` (e.g., `rule-review-policy/strict updated (table 42)`)
- `get *-review-policy -o yaml`: apply-compatible manifest
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
| problem_domain (formerly dataset_kind) | table-kind | `dataset_kinds` |
| migration (`migration-details`) | table-schema (aliases `migration`, `table-migration`) | `datasets` |

## Pattern: LLM Data Inspection (`get data`)

A dedicated pipeline, separate from the global printer framework, because the `DataSchema`
parallel-array response needs specialized alignment, annotations, and aliasing.

- **Client:** `datasets.Client.GetData(token, datasetID, *api.GetDataParams)` wraps the generated
  `GetDataWithResponse` (`GET /api/orgs/{org}/datasets/{id}/data`). `DataPage = api.DataSchema`.
- **Auto-derived flags** (`internal/commands/get/data_flags.go`): `RegisterDataFlags` reflects over
  `api.GetDataParams` and registers one flag per field; `BuildDataParams` reads changed flags back
  into the struct. Flag name = wire (`form` tag) name with `_`→`-` and trailing `-` trimmed. Denylist:
  `full_values`, `oauth_session`, `alert_item_id`. `page`/`limit` default to 1/20; all others stay nil
  unless set. Adding a backend parameter yields a flag automatically — no hand-maintained mapping.
  The denylist governs DERIVATION, not the flag set: `alert_item_id` is on it because oapi-codegen
  emits its `int | str` union as an inline anonymous struct that cannot be built outside
  `package api` and panics the generated request builder, yet `--alert-item-id` is a flag on both
  `get data` and `download data` — hand-registered by `internal/paramflags/alertitem.go` and put on
  the wire by `datasets.WithAlertItemID`, a `RequestEditorFn` that appends the query parameter
  (`internal/datasets/alert_item_filter.go`). `paramflags.AlertItemIDFilter` reads it back and
  refuses it, exit 2, when it is non-positive or when `--is-bad-data` is absent.
  `paramflags.ValidateDataFilterFlags` performs the matching pre-request check for every active
  generated quarantine-only option the current command exposes: `quarantine_hold`,
  `passes_validation_`, `awaiting_processing_`, `dataset_rule_ids_`, `include_dataset_rule_ids`,
  `review_status`, `review_group_id`, and `review_change_history_id`. The two commands pass their
  own derived flag inventory, so get-only review filters are not invented on `download data`.
- **Normalizer** (`internal/dataview/normalize.go`): `Normalize(schema, opts) (*Page, error)` validates
  alignment, builds a `columnLayout` that maps every display column back to its `schema.Fields`
  index, consumes `metadata_`, the two issue-token columns, quarantine state, and row signatures
  out of band, formats converted numeric cells through `field_display_formats`, attaches
  `[H]`/`[T]` annotations plus `[U]` for a quarantine cell indexed by `not_converted`, records
  per-row rule UUIDs, and rejects malformed row widths. `history.go` folds `row_logs_` under
  `--show-row-logs` (unless explicitly requested with `--fields`) and aligns `cell_flag_events`
  with results, adding `[F]` on displayed cells. No cobra/HTTP dependency.
- **Renderer** (`internal/dataview/render.go`): `Render(w, page, RuleAliaser)` writes the summary,
  conditional legend, aligned table (including the Issues column), optional ISSUES, ROW SIGNATURES,
  and SOURCE FILES dictionaries, CELL HISTORY under `--show-row-logs`, and the pagination hint.
  Rule cells print only `[R: r-N]` aliases (never raw UUIDs).
- **Row anchor:** `--anchor-row-id <id>` overrides `--page` within the current filters and ordering. The normalizer prefers the response's resolved `page` over `RequestedPage`; the renderer tells callers to remove the anchor before fetching the next page with `--page`. `dataRequestError` in `data.go` adds the requested selection to anchored 404s without breaking the exit-code chain.
- **Output rule:** `-o json|yaml` emit the raw response; `-o table` (and the unset default) renders
  Markdown; `-o name` is rejected with exit 2 — this command declares `table,json,yaml` only and has
  no name view.

## Pattern: Identifier Aliases (`internal/aliasstore`)

- UUIDs are not reversibly compressible, so qctl persists permanent aliases in an embedded bbolt
  store at `~/.qctl/cache/aliases.db` (0600 file, 0700 dir). Integer IDs are never aliased.
- Scope = normalized server URL (`config.NormalizeEndpointKey`). Bucket layout per server:
  `forward` (uuid→alias), `reverse` (alias→uuid), `counters` (resourceType→monotonic uint64).
- `Allocate` is idempotent within one read-write transaction (look up, else increment + write both
  directions). Resolve/List open read-only (concurrent readers); Allocate/Clear open read-write with
  a bounded `Timeout`. Aliases are permanent and never reused unless cleared.
- Resource prefixes live in one table (`resourcePrefixes`): `r` = dataset rule. Add a row to alias a
  new resource type (e.g. `u` for users).
- Transparent resolution: `cmdutil.ResolveTableRuleAlias` resolves `r-*` to a dataset-rule UUID at the
  table-rule resolver boundary, bypassing `--table`. Malformed/unknown/wrong-resource aliases fail
  before any API request. A cache failure fails the command (no unstable aliases are printed).
