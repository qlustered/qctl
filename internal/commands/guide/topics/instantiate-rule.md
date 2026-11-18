# Attach a rule to a table

Attaching a rule *revision* to a table creates a **table rule**. It is one
`kind: TableRule` manifest applied with `qctl apply -f`; there is no
`apply table-rule` subcommand.

1. Look at the table: its columns and the rules already on them.

   ```bash
   qctl describe table orders
   ```

2. Find each rule's revision, input columns and params.

   ```bash
   qctl get rules --exclude-builtin            # your org's rules; drop the flag for builtins
   qctl get rules --search requir              # find a slug
   qctl describe rule required_field           # revision UUID, input columns, params
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
  column_mapping:
    actual_start: actual_start
    actual_end: actual_end
---
apiVersion: qluster.ai/v1
kind: TableRule
metadata: {}
spec:
  dataset_id: 8
  rule_revision_id: def8757c-c210-4200-b6ca-96305818e2a5
  column: vehicle_year
```

```bash
qctl apply -f trips-table-rules.yaml --yes
```

```
Table rule created: Pickup Before Destination (283e9b44-1835-4b69-8417-2be0386493cc)
  mapping     : actual_end -> actual_end, actual_start -> actual_start
  state       : pending_validation (validation: queued) -- its validation dry run decides: enabled if it passes, validation_failed if it fails
  existing    : rows already in the table are not checked by a new rule; once it is enabled, qctl rows reprocess-clean 8 --all re-runs the clean rows (moving them to quarantine first); qctl rows reprocess-quarantined 8 --all re-runs the quarantined rows; both run every enabled rule

  qctl get table-rules 8 --columns instance_name,state,severity   # where it stands

Table rule created: Vehicle Not Ancient (9c1f0e22-77a4-4c8e-9f31-6b2d4e7a1f05)
  mapping     : value -> vehicle_year
  state       : pending_validation (validation: queued) -- its validation dry run decides: enabled if it passes, validation_failed if it fails
  existing    : rows already in the table are not checked by a new rule; once it is enabled, qctl rows reprocess-clean 8 --all re-runs the clean rows (moving them to quarantine first); qctl rows reprocess-quarantined 8 --all re-runs the quarantined rows; both run every enabled rule

  qctl get table-rules 8 --columns instance_name,state,severity   # where it stands

2 documents: 2 succeeded
```

A failing document does not stop the run: the rest are still tried, the
summary counts the failures, and the command exits non-zero. `--fail-fast`
stops at the first failure. A `kind: Table` document can come first in the
same file.

## The fields that matter

- `spec.dataset_id` — the table's integer ID.
- `spec.rule_revision_id` — a full UUID, the `SHORT-ID` from `qctl get rules`,
  or a slug. Or name the rule with `spec.rule` (plus `spec.release` to pin one
  of several releases) instead.
- `spec.column` — the column, for a rule with exactly one required input.
  Otherwise `spec.column_mapping: {rule_field: table_column}`. A field whose
  standard field matches exactly one table column is filled for you.
- `spec.severity` — **omit it** unless the user asked for one: omitted, the
  revision's own default applies. `as_declared` keeps each finding's own
  severity, `warning` and `blocker` force every finding to one class.
- `spec.params` — checked against the revision's `param_schema` before the
  request.
- `spec.instance_name` — omit it and the server names the rule. Names are unique
  per table, and a deleted or `validation_failed` rule keeps its name.
- `spec.produced_fields` — the columns an enrichment or correcting rule writes:
  `qctl explain table-rule.spec.produced_fields`.

## What the apply leads to

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

## Change, replace, or re-apply

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
  on a create; the old rule stays active until the new one is enabled.

The details of each are under `qctl explain table-rule`.

## See also

- `qctl guide manage-suggestions` — staged changes
- `qctl guide rule-lifecycle` — from rule file to enabled table rule
- `qctl guide dry-run-jobs` — test a rule before attaching it
