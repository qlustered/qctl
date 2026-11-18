# Onboard a table from a manifest

    qctl explain table
    qctl apply -f table-orders.yaml --yes
    qctl describe table orders

A **table** (dataset) is created and updated from a `kind: Table` manifest
with `qctl apply`. Apply is **idempotent**: a new name creates the table, a
matching name updates it, and re-running the same file changes nothing. That
holds for `kind: Table`, not for a `kind: TableRule` create, which never
updates the rule it made (see `qctl guide instantiate-rule`). Write the
manifest (step 1), apply it (step 3), then feed the table data (next steps).

> **Table = dataset.** Qluster tables are also called *datasets*. `qctl apply dataset`
> is an alias for `qctl apply table`, and `qctl explain dataset` for
> `qctl explain table`. No other verb takes `dataset`: use `table`/`tables`
> (`qctl get tables`, `qctl describe table`).

## 1. Write a Table manifest

```yaml
# table-orders.yaml
apiVersion: qluster.ai/v1
kind: Table
metadata:
  name: orders
spec:
  destination_id: 3            # required: destination to write into
  database_name: analytics     # required
  table_name: orders           # required
  backup_settings_id: 1        # required
```

Required `spec` fields: `destination_id`, `database_name`, `table_name`,
`backup_settings_id`. This short form uses quick create; the server supplies
`schema_name`, `migration_policy`, and `data_loading_process` defaults. You can
set `data_loading_process` to `append`, `append_skip_identical_rows`, or `mirror` while
still using quick create. If you set other fields on a new table, include all
three of `schema_name`, `migration_policy`, and `data_loading_process` for a
full create. On an existing table, leaving them out keeps the current values.
More optional fields exist — discover them with the schema commands below.

## 2. Discover the full schema

For every field, its type, and allowed
values, ask qctl directly:

```bash
qctl explain table                     # full schema
qctl explain table.migration_policy    # allowed values for one field
qctl explain table.data_loading_process
```

## 3. Apply

Validation happens before qctl writes the table, so a malformed or incomplete
spec is rejected. Apply with the `table`
subcommand:

```bash
qctl apply table -f table-orders.yaml --yes
# or let the generic dispatcher route on the manifest's kind:
qctl apply -f table-orders.yaml --yes
```

Applying ends with one line naming the table and what happened to it. The
first apply creates the table:

```
table/orders created
```

Re-running the same file changes nothing and says so:

```
table/orders unchanged
```

When the file changes an existing table's settings, apply first prints the
organization, the table and the changes, and asks before writing; it then
prints `table/orders updated`. `--yes` (`-y`) skips the preview and the
question; without a terminal to ask on, or with `-o json|yaml`, apply refuses the
update unless you pass it. `destination_id`, `database_name`, `schema_name`,
`table_name` and `backup_settings_id` cannot change on an existing table: apply
refuses the file with exit 2.

## Next steps

A new table has no data yet. Feed it one of two ways:

- `qctl guide manage-files` — upload a file into the table.
- `qctl guide connect-sources` — connect a bucket, SFTP server or URL the table pulls files from.

Then:

- `qctl guide run-jobs` — re-queue the table's ingestion jobs and run profiling.
- `qctl guide instantiate-rule` — attach validation rules to the table.

### If `qctl get tables` comes back empty

An empty list is not proof the organization has none. A non-admin account sees
only the tables it is associated with, and the API reports "none associated
with you" the same way it reports "none exist". Check your role first:

```bash
qctl auth me
```

Role `agent` sees only associated tables; `admin` sees every table in the org.
