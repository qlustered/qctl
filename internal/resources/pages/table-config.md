---
summary: a table's settings model and ingestion configuration
aliases: []
kind:
schema: openapi:SettingsSchema
guide: onboard-table
signature: 63d9327
---

## USAGE

    qctl get table-config [table]

## EXAMPLES

    # the settings model and everything around it
    qctl get table-config orders

    # address the table by the flag pair instead of positionally
    qctl get table-config --table orders

    # machine-readable, for diffing two tables' ingestion settings
    qctl get table-config orders -o yaml

    # include the values people marked as not anomalies, grouped by column
    qctl get table-config orders --show-not-anomalies

## GOTCHAS

- `table-config` is a qctl view, not a backend resource: it re-projects the same `GET /datasets/{id}` response that `qctl get table -o yaml` turns into a manifest, so their shared settings fields never disagree and neither costs an extra request. The optional not-anomaly ledger is deliberately absent from the manifest.
- it is read-only — there is no `apply table-config`. To change any of it, edit the `spec` of `qctl get table <table> -o yaml` and run `qctl apply table -f <file>`; the settings model lives under `spec.settings_model` there.
- `qctl describe table` deliberately does not show this. It showed the settings model and no column names until 2026-08-03, which is the reason this command exists.
- values marked as not anomalies are omitted by default because the ledger can be large. Pass `--show-not-anomalies` to include `not_anomalies_per_column` in text, JSON or YAML output. The field stays read-only and is never added to a Table manifest, where applying a stale copy could undo newer decisions.

## SEE ALSO

- `qctl explain table`
- `qctl get table-schema <table>`
- `qctl guide onboard-table`
