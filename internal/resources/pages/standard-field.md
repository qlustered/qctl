---
summary: one piece of business information a table holds, like Policy Number or VIN, whatever each file calls the column
aliases: [concept]
kind: StandardField
schema: openapi:ConceptSchemaFull
guide: standard-field-vocabulary
signature: 3821b05
---

## USAGE

    qctl apply -f <file.yaml>   # kind: StandardField
    qctl delete standard-field <ref>
    qctl describe standard-field <slug-or-name-or-id>
    qctl diff -f <file.yaml>   # kind: StandardField
    qctl get standard-field <slug-or-name-or-id>
    qctl get standard-field-suggestions [table-id-or-name]
    qctl get standard-fields

## EXAMPLES

    # list the current organization's standard fields
    qctl get standard-fields

    # search names and slugs, excluding built-in standard fields
    qctl get standard-fields --search policy --include-builtin=false

    # list standard fields linked to one table kind
    qctl get standard-fields --table-kind car-policy-bordereau

    # standard fields suggested for unassigned columns of a table
    qctl get standard-field-suggestions orders

    # export one standard field as an apply-compatible manifest
    qctl get standard-field policy-number -o yaml

    # preview and apply a standard field manifest
    qctl diff -f standard-field.yaml
    qctl apply -f standard-field.yaml

    # read one standard field as text
    qctl describe standard-field policy-number

    # delete an unreferenced custom standard field
    qctl delete standard-field policy-number --yes

## GOTCHAS

- `apply -f <file> -o name` prints `standard-field/<uuid>`, including an unchanged result. Several applied documents print one reference per line in file order; prose goes to stderr.

- Empty standard-field references and an explicitly empty `get standard-fields --table-kind` value exit 2 before authentication or requests.
- `get standard-field-suggestions` reads unassigned live columns. Its table view shows the source and match; `-o json|yaml` preserves the full response, including `similarity_unavailable`. A true value means similarity suggestions were unavailable while the other sources remain. A slow embedding-server startup can return 504; `--retries 1` retries that GET.
- `get standard-fields --search` requires at least three characters; shorter searches exit 2 instead of returning an unfiltered list.
- a standard-field reference resolves an exact slug or name first, then a UUID prefix, then a name or slug substring; an ambiguous reference exits 2, and a missing reference exits 3. A full UUID goes straight to the detail route.
- `get standard-fields --table-kind` resolves a name or slug before listing linked standard fields; an unknown name or slug exits with an error. A full UUID skips that lookup and is sent to the filter without local verification.
- `describe standard-field` shows the standard field's own record, including description and aliases. It does not enumerate table kinds that use it.
- a `kind: StandardField` manifest identifies the standard field by `metadata.slug`; `spec.name` is required on create. On update, omitted spec keys stay unchanged, and `description: null` or `expected_field_type: null` clears that field. `diff -f` previews the exact POST or PATCH body.
- Multi-document `apply -f` labels failures and `--fail-fast` skips as `standard-field/<slug>` when `metadata.name` and `metadata.id` are absent. A rejected envelope keeps this label when its kind and slug can be decoded.
- `get standard-field -o json|yaml` exports an apply-compatible manifest with a read-only `status.id`.
- `apply -f` can clear a standard field's description or expected field type. The table-kind import-config route updates only non-empty values for those fields, so it cannot clear them. [backend]
- `delete standard-field` refuses a built-in before prompting. Use `--yes` to skip the prompt in scripts.
- To remove a visible table column's reference before deleting a standard field, export that table's manifest, remove the column from `spec.column_standard_fields`, and apply the manifest. Use `column_standard_fields: {}` to clear every visible assignment. A dropped column or deleted table cannot be edited through this manifest path.
- a standard field cannot be deleted while a table kind, a column on an active or disabled table, or a rule revision in any state references it. On successful deletion the server removes remaining assignments on deleted tables; a dropped column on a live table can still block deletion. [backend]
- qctl, the web UI (the Standard Fields tab beside Table Kinds) and the server's error messages say standard field; the API, the JSON/YAML keys that mirror it, the `concept` and `concepts` command aliases and the `kind: Concept` manifest alias keep `concept`.

## SEE ALSO

- `qctl guide standard-field-vocabulary`
- `qctl explain table-kind`
- `qctl get table-kinds`
