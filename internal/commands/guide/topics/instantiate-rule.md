# Attach rules to a table

A rule *revision* on a table is a **table rule**. There are two ways to put
them there:

- **A report's rules, on each of its tables:** a `kind: TableRuleTemplate`
  file names the published releases and their settings once, with no table in
  it. `qctl diff -f` previews it on a table, and `qctl apply -f` applies it.
  Running it again changes nothing; bumping a release in it upgrades the
  table. This is the default.
- **One rule on one table, or a change to one table rule:** a
  `kind: TableRule` manifest, below.

There is no `apply table-rule` subcommand; both go through `qctl apply -f`.

## A report's rules: a TableRuleTemplate

1. Look at the table, and find each rule and the release to pin.

   ```bash
   qctl describe table orders
   qctl get rules --exclude-builtin            # your org's rules
   qctl get rule order_total_check             # its releases and their states
   ```

   A template takes published releases: a release still `draft` or
   `in_review` is skipped, with the command that moves it on.

2. Write the template next to the report's rules (below), and preview it.

   ```bash
   qctl diff -f monthly-orders.yaml --table orders
   ```

   Exit 0: nothing to do. Exit 1: rules would be created or replaced, the plan
   is on stdout. Exit 2: an entry needs a decision, and the output says which
   (below). Each run stores a preview on the server (a rule merge you can read
   back with `qctl describe rule-merge`), but changes nothing on the table.

3. Apply it, and wait for the new rules' validation.

   ```bash
   qctl apply -f monthly-orders.yaml --table orders --yes --wait
   ```

   It previews again, applies, and ends with a SUMMARY: each entry's table
   rule and state, any draft migration the new rules wait on with its review
   alert, and the follow-ups it never runs itself. Exit 0 also covers a merge
   whose only wait left is a draft migration's review.

4. Run the same command on the report's next table. Running it again on this
   one prints "Nothing to do" and exits 0.

### The template file

```yaml
# monthly-orders.yaml
apiVersion: qluster.ai/v1
kind: TableRuleTemplate
metadata:
  name: monthly-orders          # lineage on each table is keyed on it: do not rename
spec:
  rules:
    - rule: order_total_check   # the rule's name in `qctl get rules`
      release: 2.0.0            # required: a published release
      instance_name: Order Total Check
      column_mapping:
        "Purchase Time": purchased_at
    - rule: customer_name_split
      release: 1.3.0
```

- `rule` and `release` are required. Everything else is managed only when you
  write it: a key present in an entry is set on the table rule and compared on
  every later run (`row_filter: null` included, which removes the filter); a
  key left out is never compared or changed.
- `column_mapping` can be partial. Each field you leave out binds to the one
  column that carries its standard field, has the field's name as its
  original header, or has the name the table's normalizer makes of it; on an
  upgrade it keeps the column the old rule used. The plan shows what bound
  each field.
- An entry's key is its `instance_name`, else its `rule`. A rule listed twice
  needs an `instance_name` on every entry. The key ties the entry to the rule
  it made on each table, so changing it, or `metadata.name`, makes the next
  run plan a new rule.

Every key, and how entries pair with the rules already on a table:
`qctl explain table-rule-template`.

### When a field cannot be mapped

A required field that binds to no column makes the entry a conflict
(`unmapped`), and diff exits 2 with the lines to add to the template:

```
COLUMN MAPPINGS TO ADD
Add these keys to the entries of monthly-orders.yaml and set each to a column of the table:
('qctl diff -f monthly-orders.yaml --table orders --write-mappings' inserts them with empty values.)

- rule: order_total_check
  instance_name: Order Total Check
  column_mapping:
    "Purchase Time": # choose one: purchased_at (type ok; shares 'purchase'), created_at (type ok)
```

1. Insert them into the file. The values stay empty, the candidates stay
   comments, and the rest of the file keeps its comments and layout:

   ```bash
   qctl diff -f monthly-orders.yaml --table orders --write-mappings
   ```

2. Set each value to a column. Until you do, diff and apply refuse the file
   (exit 2) naming the entry and field; qctl never writes a guess.
3. Commit the file, and run diff again: the entry plans normally.

The mapping now lives in the template, so every later table of the report
with the same columns needs nothing.

### Skipped entries and other decisions

- **Skipped** entries never block the rest. NEXT STEPS prints, for each, what
  it waits on and the command that moves it on, for example
  `qctl review request order_total_check --release 2.0.0` for a release still
  in draft. Approving a review needs a role that may approve.
- **A conflict other than unmapped** (the table's rule of that name differs
  from the entry, it changed on both sides, or two rules could match) is
  printed as a fill-in `RuleMergeResolution` document under UNRESOLVED
  CONFLICTS. Pass it to apply with `--resolutions`, keyed by entry key:

  ```bash
  qctl apply -f monthly-orders.yaml --table orders --resolutions orders-resolutions.yaml --yes
  ```

  Use `--resolutions` only for a table whose columns really differ from the
  report's. A choice every table needs belongs in the template.
- **Apply blockers** (another rule merge applying on the table, a draft
  migration while an entry would add columns, a locked period) make apply exit
  2 with nothing applied, and name what to wait for.

### Upgrading a report to a new release

Change `release:` in the template, commit it, and run diff then apply on each
of the report's tables. The entry plans `replaced`: a new table rule on the
new release, with the same instance name and the same mapping, which replaces
the old one once it is enabled. The plan's Effects count the quarantined rows
the server reprocesses then; there is nothing to run for that.

### Other ways rules reach a table

- `qctl merge table-rules orders --from orders_eu` merges one table's rules
  into another: `qctl explain rule-merge`.
- `qctl copy table orders --series "Monthly orders" --period-key 2026-11 --include rules`
  copies a period's table, with its rules as they are, into the series' next
  period: `qctl explain table-copy`.

## One rule on one table: a TableRule manifest

A `kind: TableRule` manifest attaches one rule revision to one table by its
`spec.dataset_id`, or, with `metadata.id`, patches one table rule.

1. Look at the table: its columns and the rules already on them.

   ```bash
   qctl describe table orders
   ```

2. Find each rule's revision, input columns and params, and preview how its
   fields bind to the table's columns.

   ```bash
   qctl get rules --exclude-builtin            # your org's rules; drop the flag for builtins
   qctl get rules --search requir              # find a slug
   qctl describe rule required_field           # revision UUID, input columns, params
   qctl describe rule required_field --table orders   # how each field binds on orders
   ```

3. Write one file with a document per rule (below), apply it, and wait for the
   validation verdict.

   ```bash
   qctl apply -f trips-table-rules.yaml --yes --wait
   ```

4. Read back once.

   ```bash
   qctl get table-rules 8 --columns instance_name,state,severity
   ```

Field-by-field reference, patching, replacing and staging rules:
`qctl explain table-rule`.

### Attaching several rules at once

One file holds every rule for a table. Separate the documents with `---`.
Apply runs them in file order, reports each one, and ends with a count.

```yaml
# trips-table-rules.yaml
apiVersion: qluster.ai/v1
kind: TableRule
metadata: {}
spec:
  dataset_id: 8
  rule_revision_id: 3d7770ae-4771-4510-9550-42a24df93a4c
---
apiVersion: qluster.ai/v1
kind: TableRule
metadata: {}
spec:
  dataset_id: 8
  rule_revision_id: def8757c-c210-4200-b6ca-96305818e2a5
```

```bash
qctl apply -f trips-table-rules.yaml --yes
```

```
Table rule created: Pickup Before Destination (283e9b44-1835-4b69-8417-2be0386493cc)
  bindings    : actual_end -> actual_end (original_name), actual_start -> actual_start (original_name)
  state       : pending_validation (validation: queued) -- its validation dry run decides: enabled if it passes, validation_failed if it fails
  existing    : rows already in the table are not checked by a new rule; once it is enabled, qctl rows reprocess-clean 8 --all re-runs the clean rows (moving them to quarantine first); qctl rows reprocess-quarantined 8 --all re-runs the quarantined rows; both run every enabled rule

  qctl get table-rules 8 --columns instance_name,state,severity   # where it stands

Table rule created: Vehicle Not Ancient (9c1f0e22-77a4-4c8e-9f31-6b2d4e7a1f05)
  bindings    : value -> vehicle_year (concept)
  state       : pending_validation (validation: queued) -- its validation dry run decides: enabled if it passes, validation_failed if it fails
  existing    : rows already in the table are not checked by a new rule; once it is enabled, qctl rows reprocess-clean 8 --all re-runs the clean rows (moving them to quarantine first); qctl rows reprocess-quarantined 8 --all re-runs the quarantined rows; both run every enabled rule

  qctl get table-rules 8 --columns instance_name,state,severity   # where it stands

2 documents: 2 succeeded
```

A failing document does not stop the run: the rest are still tried, the
summary counts the failures, and the command exits non-zero. `--fail-fast`
stops at the first failure. A `kind: Table` document can come first in the
same file.

### The fields that matter

- `spec.dataset_id` — the table's integer ID.
- `spec.rule_revision_id` — a full UUID, the `SHORT-ID` from `qctl get rules`,
  or a slug. Or name the rule with `spec.rule` (plus `spec.release` to pin one
  of several releases) instead.
- `spec.column_mapping` — leave it out when you can. Name your rule fields
  after the source headers and the server binds each field you leave out: on
  an upgrade to the column the replaced rule used, otherwise to the one
  column of a type the field accepts that carries its standard field, has the
  field's name as its original header, or has the name the table's
  normalizer makes of it. Write `{rule_field: table_column}` only for the
  fields it cannot resolve; an entry you write always wins. If a required
  field matches no column, or a field matches several, the apply exits 2,
  creates nothing, and prints each field's ranked candidate columns and a
  `column_mapping:` block to paste. The output's `bindings` line names what
  bound each field.
- `spec.column` — shorthand for `column_mapping` on a rule with exactly one
  required field.
- `spec.severity` — **omit it** unless the user asked for one: omitted, the
  revision's own default applies. `as_declared` keeps each finding's own
  severity, `warning` and `blocker` force every finding to one class.
- `spec.params` — checked against the revision's `param_schema` before the
  request.
- `spec.instance_name` — omit it and the server names the rule. Names are unique
  per table, and a deleted or `validation_failed` rule keeps its name.
- `spec.produced_fields` — the columns an enrichment or correcting rule writes:
  `qctl explain table-rule.spec.produced_fields`.

### What the apply leads to

- **`pending_validation`** — the rule's validation dry run decides it. `--wait`
  stays until the verdict: exit 0 passed, 1 did not pass, 124 the wait ran out
  (the rule is untouched). Without `--wait`, report the state and the read-back
  command; do not poll in a loop.
- **Passed, waiting for a draft migration** — the rule adds columns. `--wait`
  names the alert that applies them:

  ```
  Validation passed: Split Name (<dataset-rule-id>) stays pending_validation until draft migration 42 is applied
    alert       : 401 (needs_review_migration for draft migration 42)
    note        : apply_migration applies the table's WHOLE draft migration, not only this rule's columns
    key columns : a mirror or append table's first migration also takes --key-columns <a,b> or --no-key-columns

    qctl alert resolve 401 --action apply_migration --yes   # apply the draft; that enables the rule
    qctl describe table-schema --table-id 123               # the draft carrying the columns
  ```

  Applying it lands every change waiting in the draft, so run it only when the
  user asked for the rule's columns.
- **Staged (202)** — the table has a draft migration under review and the
  rule touches the schema, so nothing is created yet:

  ```
  tablerule "Split Name" staged for review
    staged : the whole instantiation (suggestion <id>, state: pending)
  ```

  Accept, edit or dismiss it: `qctl guide manage-suggestions`.
- **Rows already in the table** are not checked by a new rule. Once it is
  enabled, and only if the user asked for the existing rows to be re-checked:

  ```bash
  qctl rows reprocess-quarantined <dataset_id> --all --yes
  qctl rows reprocess-clean <dataset_id> --all --yes
  ```

### Change, replace, or re-apply

- **Applying a create manifest again creates again.** qctl refuses (exit 2) a
  taken `spec.instance_name` and a nameless exact repeat, before any prompt.
- **To change a rule**, add `metadata.id: <its id>`: the same manifest becomes
  a merge patch of only the fields it carries. Preview it with
  `qctl diff -f table-rule.yaml` (one document per file; exit 1 means a change).
  A patch to `params` or `state` while a draft migration is under review is
  staged too:

  ```
  tablerule "Booking number required" partially applied; params/state staged for review
    applied now : instance_name
    staged      : params (suggestion <id>, state: pending)
  ```

  Resolve it with `qctl get rule-suggestions --table-id <id>`, then
  `qctl suggestion accept <id> --table-id <id> --yes` or
  `qctl suggestion dismiss <id> --table-id <id> --yes`.
- **To replace a rule** with another revision, set `spec.upgradedFrom: <its id>`
  on a create; the fields you leave out of `column_mapping` keep the old
  rule's columns while the table still has them, and the old rule stays
  active until the new one is enabled. Preview the bindings with
  `qctl describe rule <rule> --table <t> --upgraded-from <its id>`.

The details of each are under `qctl explain table-rule`.

## See also

- `qctl explain table-rule-template` — every template key, and how a plan pairs entries with table rules
- `qctl explain table-rule` — the TableRule manifest field by field
- `qctl guide manage-suggestions` — staged changes
- `qctl guide rule-lifecycle` — from rule file to enabled table rule
- `qctl guide dry-run-jobs` — test a rule before attaching it
