---
summary: ops report of soft-deleted tables across every organization, with who deleted each and its destination size
aliases:
kind:
schema: openapi:SoftDeletedTablesReportSchema
guide:
signature: f85b828
---

## USAGE

    qctl get soft-deleted-tables

## EXAMPLES

    # tables deleted at least the server's default number of days ago, with sizes and a total on stderr
    qctl get soft-deleted-tables

    # every soft-deleted table, however recent the deletion
    qctl get soft-deleted-tables --min-age-days 0

    # tables deleted at least 90 days ago, with exact timestamps and byte counts
    qctl get soft-deleted-tables --min-age-days 90 --columns reference,organization,relation,deleted_at,size_bytes

    # the raw report, with each group's size and size_error
    qctl get soft-deleted-tables -o json

    # one table/<id> per line, for a script
    qctl get soft-deleted-tables -o name

## GOTCHAS

- The table view flattens the report to one row per table; the group totals, the cutoff and any unreadable destination are on stderr. `-o json|yaml` emit the wire report verbatim, groups included, and `-o name` prints `table/<id>` with no summary.
- A `table/<id>` reference belongs to the table's own organization, which is usually not the current context's; pass `--org` to act on it. `qctl undelete table` restores one.
- `SIZE` is `unknown` for every table of a group whose destination could not be read, and the stderr total says how many groups it leaves out. `--min-age-days` must be 0 or more (exit 2 before any request).
- Only the ops role can read the report: any other role gets 404, not 403, so qctl exits 3 and names the ops gate. The report is platform-wide, listing every organization's soft-deleted tables whichever organization the context points at. [backend]
- Without `--min-age-days` the server uses its `SOFT_DELETED_TABLES_REPORT_MIN_AGE_DAYS` setting (30 unless the deployment overrides it). A table is listed when its state is deleted and it was deleted on or before `deleted_before`, which is the request time minus that many days. [backend]
- Tables are grouped by organization, destination and database name, groups sorted by organization name, destination name and database name, tables oldest deletion first. Tables with no organization group under `No organization`. [backend]
- A table's size sums whichever of its destination tables (data, raw keys, snapshots, bad records, rule verdicts, linked-table bookkeeping) still exist, so `0 bytes` means none exist, not that the size is unknown. When a destination cannot be read, its group's `size_bytes` and every table's are null and `size_error` names the error type; the report's `size_bytes` excludes such groups. [backend]

## SEE ALSO

- `qctl explain table`
- `qctl explain tables-overview`
