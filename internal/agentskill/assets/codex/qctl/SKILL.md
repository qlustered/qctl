---
name: qctl
description: Use for every task involving the qctl CLI or Qluster resources, including tables, rules, schemas, jobs, alerts, reviews, uploads, and organization-scoped operations; questions about what qctl supports; and authoring or editing Qluster Python rule files using qluster_sdk, RuleMetadata, or column_concepts. Always use this skill before running any qctl command.
---

# Operating qctl

The installed `qctl` binary is authoritative.

When operating Qluster or authoring rules, do not read qctl or atlas source.
Use the installed binary's help, `api-resources`, `explain` and `guide`.
For Python rule metadata, use the installed SDK:
`python -c "import qluster_sdk.rule as r; help(r.RuleMetadata)"`.
If these leave a gap, report it; do not assume checkout features are installed.

## Run the request first

Discover only after failure or when syntax is unknown. Avoid broad help, docs
or whole guides for routine work.

| Request | First step |
|---|---|
| Export / full data scan | `qctl download data <table>` (zipped CSV; defaults to clean rows) |
| Blocked or stuck table | `qctl describe table <table>`; read BLOCKING |
| What changed | `qctl get history --table-id <id> --since <duration>` |
| Test a rule | `qctl create dry-run-job -f <spec>`; spec: `qctl guide dry-run-jobs` |
| Fix a cell | `qctl rows set-cell --help` |
| Rule files to a table | `ops` only, else 403: `qctl submit rules -f <file> --check`, `--yes`; attach below |
| Approve a rule review | Hand off to a human; the `agent` role cannot approve (403) |
| `propose` in NEXT ACTIONS | Propose; a human approves |

## Stay in the current organization

Org precedence: `--org`, then `QCTL_ORG`, then context default.

- Once per session, run `qctl config current-context` and tell the user.
- An empty list can reflect your own filters or the account's table visibility.
- A 403 "not authorized to access table N" means this account lacks a table grant.
  "View-only" needs an editor/owner grant. Role permissions apply.
  "Not associated with this organization" means wrong org.
- A 404 or exit 3 can describe an existing table's state:
  `TABLE_SETUP_IN_PROGRESS`, empty data or a blocked table. Read the diagnostic.
- Never probe another org to work around missing resources or access.
  Change org or context only when the user names one; pass `--org` per command.
  Do not probe with `auth switch-org`, `config set-context` or `auth login`.
- Credentials are per server and org; `--org X` can fail "not logged in".
  Report it. Never start `auth login`: its device flow needs a human.

## Identifiers

- Table means dataset. Use `get table`; check installed help before using aliases.
- Positional `get table 42` means ID 42. `--table` always takes a name;
  `--table-id` and TableRule `spec.dataset_id` take integer IDs.
- Table names are exact and case-sensitive. On a name miss, run
  `qctl get tables --search <name>` once in the same organization before
  reporting. Do not substitute a similar name.
- `r-N` table-rule aliases are local, server-scoped cache entries; give another
  machine the full UUID.

## Trust the diagnostic

- Report and stop for authentication or access failures, or a missing resource ID
  the user supplied. Do not substitute another ID.
- For exit 2 (usage, non-TTY confirmation, or server validation), or a not-found
  caused by a command line you constructed, correct it within the user's request
  and re-run once. If the correction is unclear or the retry fails, report it
  and stop.
- Pass `--yes` only for the change the user asked for.
- Exit 2 "context X is protected" is the exception: add `--context X` only if
  the user named X for this change or confirms it. Never unprotect or delete X.

Once access is granted, re-run the failed command first.

## Escalate narrowly

1. `qctl <command> --help` — one command.
2. `qctl guide` — workflow index.
3. `qctl explain <kind>.<field>`, `qctl explain <kind> --grep <word>` or
   `qctl guide <topic> --section <word>`.
4. A whole `qctl explain <resource>` or guide page only if a gap remains.

Use `qctl api-resources` for an unknown resource.

## Read narrowly, write once

- Use `--search`, filters, `--columns` or `-o json | jq` for targeted reads.
- Resolve the exact target before a mutation; verify once afterward.
- Do not widen the request to repair, resubmit, migrate, disable, delete or edit
  adjacent resources. Report validation and lifecycle failures.

- Paged `get` lists return one page. Most print `{kind: List, items, page, limit,
  total, next_page}` in `-o json|yaml`; a table says "More available" on stderr.
  `get data` is one page too; use `download data` for full scans, and `qctl
  explain data --grep export` for export limits.
- Default `get data` and `describe row` output is for agents. `get data -o json`
  has positional row arrays aligned with fields. `describe row -o json` is rule evidence.
- `get alerts` hides row-level alerts unless you pass `--is-row-level`.
- `issue_pattern` can group many rows under one row's issue message. Quote the
  affected cell values, not values inferred from that message.

## Initializing submitted rules on a table

1. Resolve the table; inspect its schema, table rules and
   `qctl get rules --exclude-builtin`.
2. A report's rules live in its `kind: TableRuleTemplate` file (published
   releases, no table id): `qctl diff -f <file> --table <table>` (exit 0
   nothing to do, 1 changes, 2 a decision), then
   `qctl apply -f <file> --table <table> --yes --wait`.
3. Read back once:
   `qctl get table-rules <table> --columns instance_name,state,severity`.

Exit 2 `unmapped`: rerun diff with `--write-mappings` and fill each empty
value with a column (ask if unsure). Report skipped entries' next steps.
Guide: `qctl guide instantiate-rule`.

A one-off rule is a multi-document `kind: TableRule` manifest
(`--section "several rules"`). Omit `spec.severity` unless the user or an
existing manifest explicitly sets it, and `spec.instance_name` unless given.
Re-applying a create (no `metadata.id`) creates again; taken names and
nameless exact repeats exit 2. Use the instance ID for updates.

Preserve an explicit severity; never rewrite severities in bulk. Retired
`spec.treat_as_alert`: `true` becomes `as_declared`, and `false` becomes
`warning`. Manifest correction does not authorize editing/resubmitting rule source.

Attachment does not revalidate existing rows (`qctl rows
reprocess-quarantined|reprocess-clean <table> --all`; ask first unless
authorized). Do not change the schema while rule-validation dry runs run.

## Async work

Report `pending_validation` with the read-back command; it is not enabled or
failed. `apply -f --wait` waits for the verdict (exit 1: it did not pass). Do
not poll in a loop. A 202 "staged" or a resolve's "keyword change staged" is
accepted, not applied; follow its diagnostic or `guide manage-suggestions`.

Avoid unbounded listing `--watch`. Use `--timeout <duration>` with
`--watch --exit-on-change`, `get dry-run-job <id> --watch` or
`inspect dry-run-job <id> --follow` (preview if finished). Exit 124: only
the wait timed out; 130/143: interrupted. Dry runs you create change no
table data; they create job records.

## Authoring rules

`RuleMetadata.column_concepts` binds fields to existing standard field slugs: `qctl get standard-fields`.
Declare `column_field_types` for every read-side field, bound ones too.
In issue messages, format every row value with `self.value_for_msg(logical_column, value)` for PII markup; never interpolate raw values.
