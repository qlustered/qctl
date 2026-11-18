---
summary: one version of a rule, locked once it's enabled or disabled
aliases:
kind:
schema:
guide:
signature: d79cc1e
---

## USAGE

    qctl get rule-revisions

## EXAMPLES

    # the default revision of every rule in the org catalog
    qctl get rule-revisions

    # only the revisions that have a newer release available
    qctl get rule-revisions --has-upgrade-available true

    # all draft revisions
    qctl get rule-revisions --state draft

    # find revisions by name
    qctl get rule-revisions --search email

    # rank weighted search matches by relevance
    qctl get rule-revisions --search email --order-by relevance

    # revisions whose required standard fields all belong to one table kind
    qctl get rule-revisions --table-kind vehicle-inventory

    # revisions belonging to either table kind
    qctl get rule-revisions --table-kind vehicle-inventory --table-kind person-records

    # revisions with no required standard fields
    qctl get rule-revisions --general

    # default revisions whose required standard fields overlap a table's columns
    qctl get rule-revisions --for-table orders

    # search matching revisions from either table kind
    qctl get rule-revisions --for-table orders --search email --table-kind vehicle-inventory --table-kind person-records

    # generic revisions whose input types fit the table
    qctl get rule-revisions --for-table orders --match generic

## GOTCHAS

- a rule revision is locked once it reaches `enabled` or `disabled` — resubmitting the same `release` with changed code is then rejected; a `draft`/`in_review` revision is updated in place instead, no `release` bump needed. [backend]
- `qctl submit rules --force` does **not** break that lock: it downgrades the rejection to a silent skip, and a force-skipped rule is listed as neither added nor unchanged, so the submit looks like it worked — bump `release` in `RuleMetadata` when you need the code to actually change. `qctl submit rules --check --force` lists such a rule as `skipped`, with the reason.
- `set-default` marks a release as the family's default — a `Default` tag in `get rule`/`get rules` — and it is the revision the backend treats as canonical for the family: setting `is_default` is the only thing that raises rule-upgrade notifications, and only an `enabled` revision may be default. The `Update available`/`Newer than default` tags are computed differently by each command: `get rules` reads the backend's own primary/secondary-revision split, which is release-aware — a `Newer than default` row there is genuinely a newer release. `get rule <name>` computes the same tags client-side and does not compare releases at all: once a default exists, every non-default revision is tagged `Newer than default` and the default row is tagged `Update available`, even if the only other revision is an older release. [backend]
- `get rule-revisions` shows neither a revision's `default_severity` nor its `affected_column_types`: the list endpoint answers with the tiny schema, which carries neither field. `qctl describe rule <name-or-id>` and `qctl get rule <name-or-id> -o yaml` are where both appear. [backend]
- `--order-by relevance` ranks weighted matches from `--search`; without `--search` the backend falls back to name order. qctl defaults to `--order-by name`, so request relevance explicitly when searching. [backend]
- `get rule-revisions --state` accepts only the values `qctl get rule-revisions --help` lists; any other value is bad usage (exit 2) before a request is sent. `none` is qctl's own value: it sends no state filter at all, the same as leaving `--state` off.
- `get rule-revisions` lists only default revisions by default: the server defaults `only_default` to true, and only an `enabled` revision may be default. With `--state draft`, `--state in_review` or `--state disabled`, qctl sends `only_default=false` unless `--only-default` is set explicitly. [backend]
- there is no `describe rule-revision` command — `qctl describe rule <name-or-id>` shows the diagnostic view of one revision (add `--release` if the name matches more than one).
- Repeat `--table-kind` to include revisions belonging to any listed kind; `--table-kind` and `--general` cannot be combined. Table output shows counts for each matching table kind and General even when a kind filter is set; these facet counts honor other filters and search, but ignore the kind filter. [backend]
- `--for-table` lists matching default revisions in either the standard-field or generic bucket. `--search` and repeated `--table-kind` values filter that list before paging and filter all three Match counts in the table footer. `--match generic` with `--table-kind` is rejected because generic revisions have no required standard fields. Its default limit is 20. `-o json` and `-o yaml` print a List of the match items whose `total` counts the requested bucket; the three Match counts are not in it. [backend]
- A `full` standard-field match says the table carries each required standard field. It does not check column types or whether a standard field appears on more than one column; inspect the rule before attaching it. [backend]

## SEE ALSO

- `qctl explain rule`
- `qctl guide rule-lifecycle`
