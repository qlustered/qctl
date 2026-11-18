# Build and use a standard field vocabulary

    qctl get standard-fields --search order           # does the standard field exist?
    qctl get standard-field-suggestions orders        # which standard fields fit this table's columns
    qctl diff -f table-orders.yaml                    # preview a tag change
    qctl apply -f table-orders.yaml --yes
    qctl get rule-revisions --for-table orders        # rules whose fields match this table

A **standard field** names what a column means. A **table kind** groups standard fields for
one type of table. The organization tags tables and rule fields with the same
standard field slugs to find matches and suggest column mappings. Pick your step:
defining standard fields and kinds is for admin and ops (1); tagging a table and its
columns is for anyone who can edit it (2); binding a rule field is the rule
author's (3); finding matches is everyone's (4). Preview every manifest with
`qctl diff -f` before applying it.

## 1. Define the vocabulary (admin or ops)

Check for an existing standard field before creating one:

```bash
qctl get standard-fields --search order
```

Save this as `standard-field.yaml`, using a new slug in your organization:

```yaml
apiVersion: qluster.ai/v1
kind: StandardField
metadata:
  slug: order-id
spec:
  name: Order ID
  description: Identifier assigned to an order
  aliases: [order_number]
```

Preview and apply it, then group it in a table kind. A `TableKind` manifest
lists the **complete** set of standard field slugs for that kind:

```bash
qctl diff -f standard-field.yaml
qctl apply -f standard-field.yaml --yes
qctl describe standard-field order-id
```

Save this as `table-kind.yaml`:

```yaml
apiVersion: qluster.ai/v1
kind: TableKind
metadata:
  slug: order-records
spec:
  name: Order Records
  description: Tables containing orders
  standard_fields: [order-id]
```

```bash
qctl diff -f table-kind.yaml
qctl apply -f table-kind.yaml --yes
qctl get table-kind order-records
```

For a bulk vocabulary import that defines standard fields inline, use
`qctl submit table-kinds -f <file.toml>` instead. To remove a standard field from an
existing kind, export it with `qctl get table-kind order-records -o yaml`,
remove the slug from `spec.standard_fields`, and apply the edited manifest. Submit
only adds links; it does not remove old ones.

## 2. Tag a table and its columns (ops, admin, member, editor or agent)

Read the existing table and ask for suggestions. Suggestions are read-only;
choose the assignments that fit the data:

```bash
qctl get table orders -o yaml
qctl get standard-field-suggestions orders
qctl get table-kind-suggestions orders
```

Save the exported `kind: Table` manifest as `table-orders.yaml`. Add or edit
these fields under its existing `spec` (keep its required identity fields):

```yaml
spec:
  table_kinds: [order-records]
  column_standard_fields:
    order_number: order-id
```

The column must already exist in the table's live schema. An omitted
`table_kinds` or `column_standard_fields` key leaves that assignment type unchanged;
`table_kinds: []` clears the tags, and `column_standard_fields: {}` clears visible
column assignments. Preview the separate writes before applying:

```bash
qctl diff -f table-orders.yaml
qctl apply -f table-orders.yaml --yes
qctl describe table orders
```

The table edit can make more than one request. If a later assignment fails,
an earlier write may already have succeeded; read the table again before
retrying. Table assignments do not appear in `qctl get history` and cannot be
reversed with `qctl undo`. Edit and reapply the manifest to change them.

## 3. Bind a rule field to a standard field (rule author)

In a Python rule file, put `column_concepts` inside `RuleMetadata` with a key
for the rule field and a value for the existing standard field slug. Declare the
field's type in `column_field_types` too. For example, a rule that reads the
field `Order ID` can use:

```python
metadata = RuleMetadata(
    release="1.0.0",
    validates_columns=["Order ID"],
    column_field_types={"Order ID": "any"},
    column_concepts={"Order ID": "order-id"},
)
```

The complete class and submit workflow are in `qctl guide submit-rules`.
Submit the file, then inspect the binding. The example below assumes your
class is named `OrderIdCheckRule`; use the slug printed by submit if yours is
different:

```bash
qctl submit rules -f rules.py --yes
qctl describe rule order-id-check-rule
```

An unknown standard field slug fails submission. An unbound rule field stays generic;
it does not require a table column with a matching standard field.

## 4. Find matches and instantiate

Browse revisions by kind, find rules for a table, or find tables for a rule:

```bash
qctl get rule-revisions --table-kind order-records
qctl get rule-revisions --for-table orders
qctl get tables --for-rule order-id-check-rule
qctl get tables --table-kind order-records
```

`--for-table` includes matching default revisions and a generic bucket.
`--for-rule` uses a rule's default revision unless you pass `--release` or a
revision ID. These are discovery views; inspect the rule before applying it:

```bash
qctl describe rule order-id-check-rule
```

To attach it, save a `kind: TableRule` manifest as `table-rule.yaml`. Use the
table ID from `qctl get table orders`:

```yaml
apiVersion: qluster.ai/v1
kind: TableRule
metadata: {}
spec:
  dataset_id: 123
  rule: order-id-check-rule
```

```bash
qctl apply -f table-rule.yaml --yes
```

When the rule field's standard field points to exactly one live column, instantiation
prefills that field's column mapping. An explicit `spec.column_mapping` entry
wins; two columns with the same standard field need an explicit choice. See
`qctl guide instantiate-rule` for mapping, parameters, and staged results.

## Permissions and cleanup

- Ops and admin can create and edit standard fields and table kinds. Member, editor,
  agent and read_only can list and read them; external cannot use this
  vocabulary surface.
- Ops, admin, member, editor and agent can tag a table or assign its column
  standard fields. Except for ops and admin, they also need a grant on that table
  that is not read-only.
  Read_only can inspect assignments on a table it can read but cannot edit
  them; external has no access here.
- Built-in standard fields and table kinds cannot be deleted. A custom standard field
  cannot be deleted while a table kind, an active or disabled table column, or
  a rule revision references it. A custom table kind cannot be deleted while
  an active or disabled table is tagged with it. Deleting either cleans up
  references on deleted tables; deleting a kind keeps its standard fields.

Inspect the kind and its tables before deleting a custom entry:

```bash
qctl get standard-fields --table-kind order-records
qctl get tables --table-kind order-records
```

`--untag-tables` removes the kind from every active or disabled table you can
see, then deletes the kind. Untags already made stay if a later step fails:

```bash
qctl delete table-kind order-records --untag-tables --yes
```

Only delete a standard field after its table-kind links, column assignments and rule
revision bindings are gone. Deleting the kind removes its standard field links. To
clear a column assignment, remove `order_number` from `spec.column_standard_fields`
in the exported table manifest, then preview and apply it:

```bash
qctl get table orders -o yaml
qctl diff -f table-orders.yaml
qctl apply -f table-orders.yaml --yes
```

This example's rule revision still binds `order-id`, so deleting that standard field
would be refused. Check the deletion
error before deciding what else to change.
