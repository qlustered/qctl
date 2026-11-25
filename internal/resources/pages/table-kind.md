---
summary: an organization-owned group of standard fields used to classify tables and match rules
aliases: [problem-domain]
kind: TableKind
schema: openapi:ProblemDomainWithConceptsSchema
guide: standard-field-vocabulary
signature: bb58a89
---

## USAGE

    qctl apply -f <file.yaml>   # kind: TableKind
    qctl delete table-kind <slug-or-name-or-id>
    qctl describe table-kind <slug-or-id>
    qctl diff -f <file.yaml>   # kind: TableKind
    qctl get table-kind <slug-or-id>
    qctl get table-kind-suggestions [table-id-or-name]
    qctl get table-kinds
    qctl submit table-kinds

## EXAMPLES

    # list every table kind in the org
    qctl get table-kinds

    # exclude built-in table kinds
    qctl get table-kinds --include-builtin=false

    # one table kind by slug (or by ID), plus its linked standard fields as a grid
    qctl get table-kind car-policy-bordereau

    # untagged table kinds sharing assigned column standard fields with a table
    qctl get table-kind-suggestions orders

    # the same linked standard fields with diagnostic details
    qctl describe table-kind car-policy-bordereau

    # export an applyable manifest with the full standard field set
    qctl get table-kind car-policy-bordereau -o yaml

    # preview, then apply a manifest that replaces the full standard field set
    qctl diff -f table-kind.yaml
    qctl apply -f table-kind.yaml

    # define a table kind: the file has [problem_domain] and optional [[concepts]] entries
    qctl submit table-kinds -f car-policy-bordereau.toml

    # skip the confirmation prompt, e.g. in CI
    qctl submit table-kinds -f car-policy-bordereau.toml --yes

    # find tables tagged with this kind before deletion
    qctl get tables --table-kind car-policy-bordereau

    # untag this kind from visible tables, then delete it
    qctl delete table-kind car-policy-bordereau --untag-tables

    # delete an unused, non-built-in table kind
    qctl delete table-kind car-policy-bordereau

    # skip the deletion prompt
    qctl delete table-kind car-policy-bordereau --yes

## GOTCHAS

- `apply -f <file> -o name` prints `table-kind/<uuid>`, including an unchanged result. Several applied documents print one reference per line in file order; prose goes to stderr.

- `submit table-kinds` caps each file at 500,000 Unicode code points in qctl, rejecting a larger file with exit 2 before sending it; the Size preview counts code points, including combining marks separately.
- Empty positional table-kind references exit 2 before authentication or requests.
- `get table-kind-suggestions` lists untagged kinds with at least the server's minimum number of assigned column standard fields found on the table (3 by default), ordered by overlap. It is read-only and not paginated; `-o json|yaml` preserves the full matched standard-field objects. [backend]
- the backend renamed this from *dataset kind* to *problem domain*; qctl kept the CLI-facing name `table kind` (and the `dataset_kinds` package name) unchanged.
- slug, name and `SHORT-ID` resolution reads the whole matching catalog before deciding whether the identifier is unique, never just the first 500 rows, and exits non-zero rather than guess when the server returns fewer distinct table kinds than it counted or the catalog keeps changing across three reads; a full UUID skips the lookup entirely.
- table-kind slugs are unique within an organization; a full UUID skips catalog lookup. [backend]
- a `kind: TableKind` manifest uses `metadata.slug` for identity and requires `spec.name` plus the complete `spec.standard_fields` slug list; use `[]` to unlink all. Omitted or null `spec.description` clears it. A new kind's slug must equal the slug generated from its name. `diff -f` previews the exact request body. `spec.concepts` is still accepted as an alias; setting both exits 2.
- Multi-document `apply -f` labels failures and `--fail-fast` skips as `table-kind/<slug>` when `metadata.name` and `metadata.id` are absent. A rejected envelope keeps this label when its kind and slug can be decoded.
- `get table-kind -o yaml|json` exports an applyable manifest with a read-only `status.id`.
- `submit table-kinds -f` remains the bulk import for TOML or YAML files that define standard fields inline; `apply -f` uses existing standard-field slugs and replaces the full link set.
- under `-o json` or `-o yaml`, `submit table-kinds` prints `{created, table_kind}` for each file, `table_kind` being the manifest `get table-kind -o yaml` prints, and several `-f` files print one List; `-o name` prints `table-kind/<uuid>`, and the prose moves to stderr.
- re-submitting an existing slug updates that organization's copy, including a built-in; a changed built-in becomes customized. The import matches standard fields by slug across the organization, overwrites names, replaces descriptions, aliases and expected field types only when non-empty, and only adds links listed in `[problem_domain].concepts`. It cannot clear those values or unlink standard fields; use `qctl get table-kind <slug> -o yaml` to inspect the resulting links. [backend]
- deletion removes a table kind and its standard-field links while keeping the standard fields; built-in kinds and kinds tagged on active or disabled tables are refused. Tags on deleted tables are removed on successful deletion. [backend]
- `qctl delete table-kind` checks for a built-in before prompting. Use `--yes` to skip the prompt in scripts.
- Use `qctl delete table-kind <slug> --untag-tables` to remove the kind from visible active and disabled tables before deletion. The command reads all pages using the first-page total, refuses duplicate rows or a mismatched count before any writes, confirms the count, and sends batches of at most 200. `--yes` skips confirmation; `-v` shows table names.
- Bulk untag keeps each table's other kinds and commits successful removals even when another table is inaccessible or missing. The command stops before deletion on a partial response. [backend]
- A bulk untag `view_only` result is a failed table, like `no_access` or `not_found`. qctl counts every committed removal in the batch before returning exit 1 and does not delete the kind.
- Tables hidden from your listing can still block deletion, and a table tagged during the cascade can also cause a 409. Earlier untags remain in either case; rerun with `--untag-tables` after resolving access or concurrent changes. Deleted tables do not need recovery or editing.
- the web UI and the server's error messages also say *table kind*; *problem domain* survives only in the API — the `/problem-domains` routes, `problem_domain_id`, the `problem_domain:read` permission and a `submit table-kinds` file's `[problem_domain]` section — and qctl accepts `problem-domain`/`problem-domains` wherever `table-kind`/`table-kinds` names a command.
- `delete table-kind --untag-tables` untags in batches of 200 tables, and each batch is checked against the change review policy of every table in it (operation `problem_domains_put`, the operation `apply -f Table` uses for `spec.table_kinds`): one refusing table refuses the whole batch with nothing in it untagged, a 403 `CHANGE_REVIEW_POLICY_DENIED` (exit 4) whose `Hint:` names the operation and points at `qctl get change-review-policy --table <table>`, while earlier batches stay untagged and the kind is not deleted. The key cannot be proposed. Deleting the kind itself, `apply -f TableKind` and `submit table-kinds` are not subject to the policy. [backend]

## SEE ALSO

- `qctl guide standard-field-vocabulary`
- `qctl explain rule`
- `qctl submit table-kinds`
- `qctl delete table-kind`
