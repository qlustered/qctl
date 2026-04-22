# Command Reference — File Map

Quick lookup: every CLI command → implementation file + domain client + test file.

Use this to jump straight to code without exploring the codebase.

## Naming Convention (Backend → CLI)

| Backend term | CLI term | Go package |
|---|---|---|
| dataset | table | `internal/datasets` |
| stored_item | file | `internal/stored_items` |
| data_source | cloud-source | `internal/cloud_sources` |
| alert_item | alert | `internal/alerts` |
| rule_family | rule (list) | `internal/rule_families` |
| rule_revision | rule (detail) | `internal/rule_versions` |
| dataset_rule | table-rule | `internal/dataset_rules` |
| dataset_kind | table-kind | `internal/dataset_kinds` |

## get commands

| Command | File | Domain Client | Printer | Test File |
|---|---|---|---|---|
| `get tables` | `internal/commands/get/datasets.go` | `datasets.NewClient` | `output.NewPrinterFromCmd` | `datasets_test.go` |
| `get table <id>` | `internal/commands/get/dataset.go` | `datasets.NewClient` | `output.NewPrinterFromCmd` | `dataset_test.go` |
| `get files` | `internal/commands/get/files.go` | `stored_items.NewClient` | `output.NewPrinterFromCmd` | `files_test.go` |
| `get ingestion-jobs` | `internal/commands/get/ingestion_jobs.go` | `ingestion.NewClient` | `output.NewPrinterFromCmd` | `ingestion_jobs_test.go` |
| `get profiling-jobs` | `internal/commands/get/profiling_jobs.go` | `profiling.NewClient` | `output.NewPrinterFromCmd` | `profiling_jobs_test.go` |
| `get alerts` | `internal/commands/get/alerts.go` | `alerts.NewClient` | `output.NewPrinterFromCmd` | `alerts_test.go` |
| `get warnings` | `internal/commands/get/warnings.go` | `warnings.NewClient` | `output.NewPrinterFromCmd` | `warnings_test.go` |
| `get cloud-sources` | `internal/commands/get/cloud_sources.go` | `cloud_sources.NewClient` | `output.NewPrinterFromCmd` | `cloud_sources_test.go` |
| `get destinations` | `internal/commands/get/destinations.go` | `destinations.NewClient` | `output.NewPrinterFromCmd` | `destinations_test.go` |
| `get error-incidents` | `internal/commands/get/error_incidents.go` | `errorincidents.NewClient` | `output.NewPrinterFromCmd` | `error_incidents_test.go` |
| `get rules` | `internal/commands/get/rule_families.go` | `rule_families.NewClient` | `tableui.PrintFromCmd` | `rule_families_test.go` |
| `get rule <name>` | `internal/commands/get/rule.go` | `rule_versions.NewClient` | `tableui.PrintFromCmd` | `rule_test.go` |
| `get rule-revisions` | `internal/commands/get/rule_revisions.go` | `rule_versions.NewClient` | `tableui.PrintFromCmd` | `rule_revisions_test.go` |
| `get table-rules` | `internal/commands/get/table_rules.go` | `dataset_rules.NewClient` | `tableui.PrintFromCmd` | `table_rules_test.go` |
| `get table-rule <name>` | `internal/commands/get/table_rule.go` | `dataset_rules.NewClient` | `tableui.PrintFromCmd` | `table_rule_test.go` |
| `get dry-run-jobs` | `internal/commands/get/dry_run_jobs.go` | `dry_runs.NewClient` | `output.NewPrinterFromCmd` | `dry_run_jobs_test.go` |
| `get dry-run-job <id>` | `internal/commands/get/dry_run_job.go` | `dry_runs.NewClient` | `output.NewPrinterFromCmd` | `dry_run_job_test.go` |
| `get job-activity` | `internal/commands/get/job_activity.go` | `ingestion.NewClient` | `output.NewPrinterFromCmd` | `job_activity_test.go` |
| `get table-kinds` | `internal/commands/get/table_kinds.go` | `dataset_kinds.NewClient` | `output.NewPrinterFromCmd` | `table_kinds_test.go` |
| `get table-kind <slug>` | `internal/commands/get/table_kind.go` | `dataset_kinds.NewClient` | `output.NewPrinterFromCmd` | `table_kind_test.go` |
| `get orgs` | `internal/commands/get/orgs.go` | `orgs.NewClient` | `tableui.PrintFromCmd` | `orgs_test.go` |

Notes:
- `get rules` lists rule **families** (grouped by name), not individual revisions.
- `get rule <name> -o code` outputs raw Python source.
- `get rule <name> -o yaml` outputs apply-compatible `kind: Rule` manifest.
- `get table-rules` and `get table-rule` require `--table <id>`.
- `setDefaultColumns(cmd, "col1,col2")` is defined in `internal/commands/get/ingestion_jobs.go` and used across get commands.
- `watch.go` contains the `--watch` flag implementation shared by get commands.

## describe commands

| Command | File | Domain Client | Default Output | Test File |
|---|---|---|---|---|
| `describe table <id>` | `internal/commands/describe/dataset.go` | `datasets.NewClient` | yaml | `dataset_test.go` |
| `describe file <id>` | `internal/commands/describe/file.go` | `stored_items.NewClient` | yaml | `file_test.go` |
| `describe ingestion-job <id>` | `internal/commands/describe/ingestion_job.go` | `ingestion.NewClient` | yaml | `ingestion_job_test.go` |
| `describe profiling-job <id>` | `internal/commands/describe/profiling_job.go` | `profiling.NewClient` | yaml | `profiling_job_test.go` |
| `describe alert <id>` | `internal/commands/describe/alert.go` | `alerts.NewClient` | yaml | `alert_test.go` |
| `describe warning <id>` | `internal/commands/describe/warning.go` | `warnings.NewClient` | yaml | `warning_test.go` |
| `describe cloud-source <id>` | `internal/commands/describe/cloud_source.go` | `cloud_sources.NewClient` | yaml | `cloud_source_test.go` |
| `describe destination <id>` | `internal/commands/describe/destination.go` | `destinations.NewClient` | yaml | `destination_test.go` |
| `describe error-incident <id>` | `internal/commands/describe/error_incident.go` | `errorincidents.NewClient` | yaml | `error_incident_test.go` |
| `describe rule <name>` | `internal/commands/describe/rule.go` | `rule_versions.NewClient` | **plain text** | `rule_test.go` |
| `describe table-rule <name>` | `internal/commands/describe/table_rule.go` | `dataset_rules.NewClient` | yaml | `table_rule_test.go` |
| `describe dry-run-job <id>` | `internal/commands/describe/dry_run_job.go` | `dry_runs.NewClient` | yaml | `dry_run_job_test.go` |
| `describe table-kind <slug>` | `internal/commands/describe/table_kind.go` | `dataset_kinds.NewClient` | **plain text** | `table_kind_test.go` |

Notes:
- All describe commands default to yaml except `describe rule` which outputs **plain text** (human-readable, not round-trippable).
- `describe table-rule` requires `--table <id>`.
- `describe rule` flags: `--release`, `--show-code`.
- `describe table-rule` verbosity: default=essential, `-v`=adds dataset_field_names, `-vv`=raw dump.

## apply commands

| Command | File | Domain Client | Test File |
|---|---|---|---|
| `apply -f <file>` (generic) | `internal/commands/apply/generic.go` | (dispatches by kind) | `generic_test.go` |
| `apply table` | `internal/commands/apply/dataset.go` | `datasets.NewClient` | `dataset_test.go` |
| `apply destination` | `internal/commands/apply/destination.go` | `destinations.NewClient` | `destination_test.go` |
| `apply cloud-source` | `internal/commands/apply/cloud_source.go` | `cloud_sources.NewClient` | `cloud_source_test.go` |
| (rule via generic) | `internal/commands/apply/rules.go` | `rule_versions.NewClient` | `rules_test.go` |
| (table-rule via generic) | `internal/commands/apply/table_rules.go` | `dataset_rules.NewClient` | `table_rules_test.go` |

Notes:
- `apply -f` reads `kind` from manifest and dispatches via `genericApply()`.
- Supported kinds: `Table`, `Destination`, `CloudSource`, `DryRunJob`, `Rule`, `TableRule`.
- `.py` files rejected with hint to use `submit rules`.
- Supports multi-document YAML (`---` separators) and `--fail-fast`.
- `env.go` handles `${VAR}` expansion in manifests.
- Rule apply uses **smart diff** (not simple create/update) — see `docs/patterns.md`.
- TableRule apply has two modes: **patch** (has `metadata.id`) and **instantiate** (has `spec.rule_revision_id`).

## submit commands

| Command | File | Domain Client | Test File |
|---|---|---|---|
| `submit rules -f <files>` | `internal/commands/submit/rules.go` | `rule_versions.NewClient` | `rules_test.go` |
| `submit table-kinds -f <files>` | `internal/commands/submit/table_kinds.go` | `dataset_kinds.NewClient` | `table_kinds_test.go` |

Notes:
- `-f` is `StringArrayVar` (multiple files).
- `submit rules`: Only `.py` files accepted. Max 500 KB per file. Flags: `--force`, `--yes`.
- `submit table-kinds`: Only `.toml`, `.yaml`, `.yml` files accepted. Max 500 KB per file. Flags: `--yes`.

## delete commands

| Command | File | Domain Client | Test File |
|---|---|---|---|
| `delete file <id>` | `internal/commands/delete/file.go` | `stored_items.NewClient` | `file_test.go` |
| `delete error-incident <id>` | `internal/commands/delete/error_incident.go` | `errorincidents.NewClient` | `error_incident_test.go` |
| `delete rule <name>` | `internal/commands/delete/rule.go` | `rule_versions.NewClient` | `rule_test.go` |
| `delete rules -f <files>` | `internal/commands/delete/rules.go` | `rule_versions.NewClient` | `rules_test.go` |

Notes:
- `delete rules -f` takes Python files (not YAML), calls `UnsubmitRuleVersion`.

## run / kill commands

| Command | File | Domain Client | Test File |
|---|---|---|---|
| `run ingestion-job` | `internal/commands/run/ingestion_job.go` | `ingestion.NewClient` | `ingestion_job_test.go` |
| `run profiling-job` | `internal/commands/run/profiling_job.go` | `profiling.NewClient` | `profiling_job_test.go` |
| `kill ingestion-job` | `internal/commands/kill/ingestion_job.go` | `ingestion.NewClient` | `ingestion_job_test.go` |
| `kill profiling-job` | `internal/commands/kill/profiling_job.go` | `profiling.NewClient` | `profiling_job_test.go` |

Notes:
- Accept IDs as positional args OR `--filter` for bulk operations.
- `--dry-list` previews, `--yes` skips confirmation.

## enable / disable / set-default commands

| Command | File | Domain Client | Test File |
|---|---|---|---|
| `enable rule <name>` | `internal/commands/enable/rule.go` | `rule_versions.NewClient` | `rule_test.go` |
| `disable rule <name>` | `internal/commands/disable/rule.go` | `rule_versions.NewClient` | `rule_test.go` |
| `set-default rule <name>` | `internal/commands/set_default/rule.go` | `rule_versions.NewClient` | `rule_test.go` |

Notes:
- All use `rule_versions.ResolveRule()` for name resolution.
- Flag: `--release` to disambiguate multi-release rules.

## create / inspect commands

| Command | File | Domain Client | Test File |
|---|---|---|---|
| `create dry-run-job` | `internal/commands/create/dry_run_job.go` | `dry_runs.NewClient` | `dry_run_job_test.go` |
| `inspect dry-run-job <id>` | `internal/commands/inspect/dry_run_job.go` | `dry_runs.NewClient` | `dry_run_job_test.go` |

## upload / download / undelete commands

| Command | File | Domain Client | Test File |
|---|---|---|---|
| `upload file` | `internal/commands/upload/file.go` | `stored_items.NewClient` | `file_test.go` |
| `download file <id>` | `internal/commands/download/file.go` | `stored_items.NewClient` | `file_test.go` |
| `undelete file <id>` | `internal/commands/undelete/file.go` | `stored_items.NewClient` | `file_test.go` |

## auth / config / other commands

| Command | File |
|---|---|
| `auth login` | `internal/commands/auth/login.go` |
| `auth logout` | `internal/commands/auth/logout.go` |
| `auth switch-org` | `internal/commands/auth/switch_org.go` |
| `auth me` | `internal/commands/auth/me.go` |
| `config set-context` | `internal/commands/config/set_context.go` |
| `config use-context` | `internal/commands/config/use_context.go` |
| `config delete-context` | `internal/commands/config/delete_context.go` |
| `config list-contexts` | `internal/commands/config/list_contexts.go` |
| `config current-context` | `internal/commands/config/current_context.go` |
| `explain` | `internal/commands/explain/explain.go` |
| `completion` | `internal/commands/completion/completion.go` |
| `completion install` | `internal/commands/completion/install.go` |
| `version` | `internal/commands/version/version.go` |
| `docs` | `internal/commands/docs/docs.go` |

## Domain Client Packages

| Package | Path | Constructor | Notes |
|---|---|---|---|
| `datasets` | `internal/datasets/client.go` | `NewClient(base, orgID, verb)` | Standard (no error) |
| `stored_items` | `internal/stored_items/client.go` | `NewClient(base, orgID, verb)` | Standard |
| `ingestion` | `internal/ingestion/client.go` | `NewClient(base, orgID, verb)` | Standard |
| `profiling` | `internal/profiling/client.go` | `NewClient(base, orgID, verb)` | Standard |
| `alerts` | `internal/alerts/client.go` | `NewClient(base, orgID, verb)` | Standard |
| `warnings` | `internal/warnings/client.go` | `NewClient(base, orgID, verb)` | Standard |
| `cloud_sources` | `internal/cloud_sources/client.go` | `NewClient(base, orgID, verb)` | Standard |
| `destinations` | `internal/destinations/client.go` | `NewClient(base, orgID, verb)` | Standard |
| `errorincidents` | `internal/errorincidents/client.go` | `NewClient(base, orgID, verb)` | Standard |
| `dry_runs` | `internal/dry_runs/client.go` | `NewClient(base, orgID, verb)` | Standard |
| `rule_families` | `internal/rule_families/client.go` | `NewClient(base, orgID, verb)` | Standard |
| `rule_versions` | `internal/rule_versions/client.go` | `NewClient(base, orgID, verb)` | **Returns `(*Client, error)`** |
| `dataset_rules` | `internal/dataset_rules/client.go` | `NewClient(base, orgID, verb)` | **Returns `(*Client, error)`** |
| `dataset_kinds` | `internal/dataset_kinds/client.go` | `NewClient(base, orgID, verb)` | Standard |
| `orgs` | `internal/orgs/client.go` | `NewClient(base, orgID, verb)` | Standard |

## Parent Command Files (register subcommands)

Each verb has a parent file that registers all subcommands:

| Parent | File | Registers |
|---|---|---|
| `root` | `internal/commands/root/root.go` | All top-level verbs |
| `get` | `internal/commands/get/get.go` | All get subcommands |
| `describe` | `internal/commands/describe/describe.go` | All describe subcommands |
| `apply` | `internal/commands/apply/apply.go` | All apply subcommands |
| `delete` | `internal/commands/delete/delete.go` | All delete subcommands |
| `run` | `internal/commands/run/run.go` | All run subcommands |
| `kill` | `internal/commands/kill/kill.go` | All kill subcommands |
| `submit` | `internal/commands/submit/submit.go` | All submit subcommands |
| `enable` | `internal/commands/enable/enable.go` | All enable subcommands |
| `disable` | `internal/commands/disable/disable.go` | All disable subcommands |
| `set-default` | `internal/commands/set_default/set_default.go` | All set-default subcommands |
| `create` | `internal/commands/create/create.go` | All create subcommands |
| `inspect` | `internal/commands/inspect/inspect.go` | All inspect subcommands |
| `upload` | `internal/commands/upload/upload.go` | All upload subcommands |
| `download` | `internal/commands/download/download.go` | All download subcommands |
| `undelete` | `internal/commands/undelete/undelete.go` | All undelete subcommands |
| `auth` | `internal/commands/auth/auth.go` | All auth subcommands |
| `config` | `internal/commands/config/config.go` | All config subcommands |
| `completion` | `internal/commands/completion/completion.go` | All completion subcommands |
