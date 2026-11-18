---
summary: standalone tables and table series with totals for internal triage
aliases:
kind:
schema: openapi:TablesOverviewListSchema
guide: triage-alerts
signature: 856a02a
---

## USAGE

    qctl get tables-overview

## EXAMPLES

    # one row per standalone table or series, ordered by name
    qctl get tables-overview

    # find the tables and series needing attention, most urgent first
    qctl get tables-overview --view needs-attention

    # inspect additional approval, hold and validation-issue counts
    qctl get tables-overview -v

    # list deleted tables individually
    qctl get tables-overview --view trash

    # scope the periods contributing to series totals
    qctl get tables-overview --state active --destination-id 1 --table-kind car-policy-bordereau

    # find a series by its name or a period's table fields
    qctl get tables-overview --search premium

    # order by bad-row totals, lowest first
    qctl get tables-overview --order-by bad_rows_count --reverse

    # select loading processes; repeated flags also work
    qctl get tables-overview --data-loading-process sync,append_skip_identical_rows

    # inspect the latest period separately from the top alert's period
    qctl get tables-overview --columns reference,name,period,latest_table,alert_table,alert_period,top_alert

    # obtain reusable table and table-series references
    qctl get tables-overview -o name

    # export a page of raw table and series records, including null counts
    qctl get tables-overview --page 2 --limit 20 -o json

    # drill down into a selected series's periods
    qctl get tables --series 11111111-2222-3333-4444-555555555555 --needs-attention

## GOTCHAS

- `get tables-overview` is an aggregate read; `get tables` continues to list individual tables. `--view` accepts `all`, `needs-attention` and `trash`; following is not exposed.
- Default columns are `reference,name,period,period_count,periods_needing_attention,clean_rows_count,bad_rows_count,attention_score,top_alert`. A table row's reference is `table/<id>`; a series row's is `table-series/<uuid>`. `-o name` emits those same reusable references in server order.
- `PERIOD` is a table's own period or a series's latest selected period, with `[locked]` when locked, or `-` for standalone tables. A series's `TOP-ALERT` includes its affected table reference and period key. `latest_table`, `alert_table` and `alert_period` are selectable columns; the latest table and top-alert table can differ.
- `-v` adds `approved_rows_count`, `on_hold_rows_count`, `rows_with_issues_count` and `needs_approval`. An explicit `--columns` overrides the default set. Unknown counts are blank; zero counts show `0`. Table-only state and destination columns show `-` on series rows; period counts are blank on table rows.
- JSON/YAML print a paginated `kind: List` with raw union rows, distinguished by `kind: table` or `kind: series`, retaining nulls and search-match metadata. `-v` and `--columns` do not project machine-readable items. `page`, `limit`, `total` and `next_page` describe overview rows, not the number of individual period tables.
- `--limit` accepts 1 through 100 and defaults to 100; `--page` starts at 1. The command reads one page and derives the next-page hint from the total. Invalid views, sorts, states, loading processes, blank non-search filter values and non-positive destination IDs exit 2 before authentication. `--search` requires at least three characters unless empty.
- `--view trash` refuses `--state` and `--states` with exit 2 because trash selects deleted tables. `--state` and `--states` also conflict with each other.
- The backend's `NO_TABLE_MESSAGE` response becomes an empty List with a scope hint on stderr; it does not establish that the organization has no tables. Other 404 responses preserve exit 3.
- The overview requires `dataset:list`. Outside trash it includes standalone tables and series with at least one accessible table selected by the state, destination, loading-process and table-kind filters; the default states are active and disabled. Empty series have no overview row. [backend]
- A series's period count, totals, latest period and approval requirement cover its accessible periods selected by those filters. Search selects rows by series name or a period's table name, state or fields without restricting totals to matching periods; `--name` selects the exact overview row name. [backend]
- A series's attention score is its highest unlocked-period score, and `periods_needing_attention` counts unlocked periods with positive scores. Locked periods still contribute row counts. Its top alert comes from the unlocked period with the highest score, latest on a tie, and is null when that score is zero or no top alert is available. [backend]
- The needs-attention view keeps rows with a positive attention score and series whose broker is `late` for the period being collected, as counted by `qctl get submission-statuses`; that period need not have a table, but the series row still needs a table selected by the filters. A late series can score zero, so it sorts after scored rows by default. [backend]
- Trash returns deleted tables individually, including members of series, rather than series aggregates. The default order is name ascending, or attention score descending for needs-attention; `bad_rows_count` also defaults descending. `--reverse` reverses the selected field's default direction. [backend]

## SEE ALSO

- `qctl explain table`
- `qctl explain table-series`
