---
summary: the alert to look at first on a table, and the counts behind it
aliases:
kind:
schema: openapi:MenuStatsSchema
guide: unblock-table
signature: 97941d3
---

## USAGE

    qctl get latest-alert [table]

## EXAMPLES

    # what needs attention on this table, and which alert first
    qctl get latest-alert --table orders

    # same, addressing the table positionally (ID first, then name)
    qctl get latest-alert orders

    # alert/<id>, to pipe into describe alert
    qctl get latest-alert --table orders -o name

    # the whole payload, for an agent
    qctl get latest-alert --table-id 42 -o json

    # tell me when something changes on this table
    qctl get latest-alert --table orders --watch

    # block until something changes, then exit -- the form to call from a script
    qctl get latest-alert --table orders --watch --exit-on-change --timeout 10m -o json

## GOTCHAS

- the alert reported is not always the newest one: when the newest alert depends on another, the backend substitutes the id of the one that must be resolved first, so this is the alert to act on rather than the alert that arrived last. [backend]
- a table selector is required and there is no org-wide form: without a table the endpoint leaves `latest_alert_item_id` null for every role — it only ever fills the field on the per-table branch — which would read as "nothing to do" to a user who has several. [backend]
- the counts here and the row count of `qctl get alerts --table <name>` apply different server-side filters and will not always agree; neither is wrong. [backend]
- this is a notification view, not a listing — it reports one alert. Use `qctl get alerts --table <name>` for the full set.
- `--watch` compares each response to the previous one from this same endpoint, so it fires on a moved count as well as on a new alert — and it cannot see a change that moves neither, such as an existing alert's `count` incrementing. [backend]
- `--timeout` bounds the whole watch, not a single HTTP request — that is `--request-timeout`, which bounds each poll inside the watch and is independent of this one. Left unset it is no limit unless `QCTL_WATCH_TIMEOUT` sets one, and without `--watch` it is exit 2.
- a plain `--watch` that runs out of time exits 0 after one stderr line, `timed out after <d>; resume with: <command>`. With `--exit-on-change` running out of time is a failed wait instead: stdout stays empty and it exits 124 with `timed out after <d>: no change to the latest alert on table <table>` followed by a `Resume with: <command>` line.
- SIGINT (Ctrl-C) ends a watch at exit 130 and SIGTERM at 143, after one stderr line, `interrupted; resume with: <command>`. Every resume command repeats the flags you set, `--timeout` included.
- in `--watch` mode `-o json` emits NDJSON and `-o yaml` emits `---`-separated documents — one record for the starting state, left out under `--exit-on-change`, then one per change — and the banner goes to stderr so a piped stream stays clean: `watching the latest alert on table <table> every <interval>`, or under `--exit-on-change` `waiting for the latest alert on table <table> to change, checking every <interval>`, then `, for up to <d>` when there is a limit and `; resume with: <command>`.

## SEE ALSO

- `qctl describe alert <id>`
- `qctl get alerts --table <name>`
- `qctl alert resolve <id> --action <action>`
- `qctl guide unblock-table`
