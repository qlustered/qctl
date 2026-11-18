# Resolve proposed new rules and rule changes

    qctl get rule-suggestions orders --show-params
    qctl describe rule-suggestion <id> --table orders
    qctl suggestion accept <id> --table orders --yes
    qctl suggestion dismiss <id> --table orders --yes

A suggestion proposes a new rule or a change to an existing rule. Profiling
proposes new rules, and your own instantiations and edits can be staged for
schema review or validation. An `action: create` proposal has no live rule
yet. Accept or dismiss only what the user asked you to decide; revalidation
candidates are promoted by their own validation run.
Every flag and edge case: `qctl explain rule-suggestion`.

## What is staged

`get rule-suggestions` lists `--state pending` by default; add `--state
applied|dismissed` for history, `--source periodic|profiling|recreated_period|user` for origin,
and `--show-kind` to mark revalidation candidates. `--show-params` adds the
proposed parameters to the table, fetching full details for each displayed
row; `--columns id,params` does the same with only those columns. Both keep
the selected page. JSON/YAML/name keep the compact list without extra reads;
use `describe rule-suggestion -o json|yaml` for full structured parameters.
`describe rule-suggestion` needs the table and the full UUID; its diff against the live rule *is* the
preview. To preview a patch you have not applied yet, use `qctl diff -f
table-rule.yaml` (`qctl guide instantiate-rule`).

## Leaving one pending is a decision

When you resolve the table's migration alert with `qctl alert resolve <id>
--action apply_migration`, the backend decides pending suggestions for you:

- pending **`create`** suggestions staged against that migration are
  **auto-accepted**;
- pending **`update`** and **`delete`** ones **block** it with `409
  PENDING_SUGGESTIONS_BLOCK_MIGRATION`, and nothing changes; accept or dismiss
  each, then re-run (a revalidation candidate whose dry run passed does not
  block);
- the migration job then tries to apply every other pending `user` and
  `profiling` suggestion on the table.

`qctl alert resolve` lists all four groups before it asks, and stops without
sending when there are blockers.

## Take it

    suggestion 550e8400-e29b-41d4-a716-446655440000 accepted — tablerule "high_confidence_dedupe" updated

Accepting applies the change at once; the trailing verb is `created`,
`updated` or `deleted`. A **revalidation candidate** — raised when a patch
changed the rule's revision, column mapping or produced fields, or `params`
on a rule in `error` — cannot be accepted (409): its own dry run promotes it
when it passes. Dismiss it to cancel, or patch the rule again to replace
what it validates.

## Drop it

    suggestion 550e8400-e29b-41d4-a716-446655440000 dismissed — tablerule "high_confidence_dedupe" unchanged

`--family` also stops the backend re-suggesting any revision of this rule
family for this table.

## Several at once

    qctl suggestion accept <id> <id> <id> --table orders --yes
    qctl get rule-suggestions orders --source profiling
    qctl suggestion dismiss --source profiling --table orders --yes

Up to 100 ids, read back first: one unknown or resolved id refuses the whole
command (exit 2, nothing written). Once writing starts, a refused item is
named and the command exits 1. `--source` and `--action` select every
pending match instead of ids (never both); list the same selection with
`get rule-suggestions` first, as above.

## Tune one before accepting it

    qctl suggestion edit <id> --table orders --set-param threshold=5

`edit` changes a pending suggestion's `params`, column mapping,
`severity` or row filter without resolving it: `--row-filter-file <file>`
replaces the filter and `--remove-row-filter` clears it. A value that
parses as JSON is sent as JSON: `threshold=5` is a number, `code='"5"'` a
string. A revalidation
candidate is refused with exit 2 before a patch; edit its TableRule manifest
with `qctl apply -f <file>` instead.

## Confirm

    qctl describe table-rule <name-or-id> --table orders

## See also

- `qctl explain rule-suggestion`
- `qctl guide instantiate-rule`
- `qctl guide unblock-table`
