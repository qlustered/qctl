# Inspect a table's schema

To see a table's columns, run:

    qctl get table-schema orders

That is the whole answer. The rest is drafts, pending changes and how a
schema change gets applied. Every flag and edge case:
`qctl explain table-schema`.

A table (dataset) has a **current** schema (applied in the warehouse) and at
most one **draft** (a pending change, after new data arrived or the manifest
changed).

> **Table schema = migration.** The API calls a schema, and a pending change to
> one, a *migration*: `describe table-schema -v` prints `migration_id`, and the
> table's `migration_policy` decides when a draft is applied. `qctl get
> migration` is an alias of `qctl get table-schema`.

## See the fields of a table

    qctl get table-schema orders

     FIELD    TYPE            NULLABLE   ACTION

     amount   Decimal(10,2)   true       added
     id       Integer         false      unchanged
     name     String(255)     false      modified

The ACTION column appears only while a draft is pending: `added` (new, or a
deleted column restored), `modified`, `removed` (deleted, or gone from the
draft), `unchanged`. Rows show the draft's definition. `-v` adds INDEXED,
ORIGINAL-NAME, DATETIME-FORMATS, DISPLAY-DECIMALS and FLAGS (a deleted column
is flagged `deleted`, and stays restorable).

For a script, `qctl get table-schema orders -o name` prints one column name per
line — current and draft flattened, with nothing marking which are pending.

## Understand the schema state

    qctl describe table-schema orders

    Table:        orders (id: 42)
    Schema state: draft_with_current
    State:        draft (not confirmed)
    Fields:       3 total (+1 added, ~1 modified, -0 removed)
    Created:      2 days ago
    Updated:      3 hours ago
    Suggestions:  3 pending (1 block the migration)
    ...

`Schema state` is `current_only` (nothing pending), `draft_only` (never
applied; every field shows as added) or `draft_with_current` (a draft on top
of the applied schema). `-v` adds per-field diffs; `-vv` the raw response.

The text header counts pending rule proposals and how many block the draft.
Inspect their proposed parameters before deciding:

    qctl get rule-suggestions orders --show-params

`describe rule-suggestion <id> --table orders` shows one proposal in full,
diffed against its live rule when it has one. Suggestions you cannot read
show as unavailable in the schema header; the field grid still prints.

Until a mirror or append table's first migration is confirmed, the header
also shows `Key step:` and `Key columns:`, and a key candidates section
follows it: suggested key column sets, each with its duplicate, identical
and blank row counts. `qctl explain table-schema --grep key_step` explains them.

    qctl describe table-schema orders -v -o yaml

is the full structured state for an agent, including notable values — sample
and boundary data from the table, useful for judging a pending change.

## Change the schema

    qctl get table-schema orders -o yaml > schema.yaml
    qctl diff -f schema.yaml
    qctl apply -f schema.yaml --yes

Edit only the columns you mean to change; an omitted column or property is
left as it is. One table per file: `diff -f` refuses a multi-document file.

    spec:
      fields:
        amount:
          type: Decimal
          args: [12, 2]
        shipped_at:
          type: DateTime
          is_nullable: true
        legacy_ref: null

- A name the schema does not have **adds** a column (always nullable), so
  `diff -f` shows `shipped_at: add column` — check it for typos.
- `null` un-pins a column; editing one pins its whole definition.
- Removing a column from the file does nothing. `is_deleted: true` deletes
  it, alone in its block, and `is_deleted: false`, alone, restores it; the
  file carries the line only for a deleted column. Any other edit of a
  deleted column, `null` included, exits 2: restore it in one apply and
  change it in the next. You cannot rename a column.
- Writing needs `MIGRATION_EXECUTE` (ops, admin, agent).

The apply does not change the warehouse by itself. The line after it says what
happens next: `Waiting for review` with the `qctl alert resolve` command to
run, `Applied with no review step`, or `No review step and no migrate job`. A
`409 ERR_MIGRATION_CONFLICT` means the policy is `frozen`, a confirmed change
is still in flight, a column you edit was deleted while the apply ran, or
your file is stale; the backend's sentence above it says which. Re-reading
with `get table-schema -o yaml` fixes a stale file, and for a deleted column
the next apply exits 2 with the restore steps; for the others read
`qctl get table-config` and `describe table-schema`.

## Confirm or discard a pending draft

Under the default `ask_user` policy a draft waits for a
`needs_review_migration` alert to be resolved:

    qctl get alerts --table orders
    qctl describe table-schema orders
    qctl alert resolve 9 --action apply_migration --yes

On a mirror or append table's first migration `describe table-schema` shows
`Key step: required` and the key candidates: add `--key-columns <a,b>`, or
`--no-key-columns` for a table with no key, to the `apply_migration` line,
which is refused with neither (exit 2). Applying locks the key columns.

`discard_migration` throws the draft away instead. Applying is asynchronous;
re-run `describe table-schema` to see it land. Pending rule suggestions are
decided along with it, and pending `update`/`delete` ones block it — qctl lists
them first (`qctl guide manage-suggestions`).

## Related

- `qctl guide onboard-table` — change the table manifest that drives schema
  changes
- `qctl explain table-schema`
