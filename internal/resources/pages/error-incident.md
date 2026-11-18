---
summary: an ingestion or processing failure the platform recorded
aliases:
kind:
schema: openapi:ErrorIncidentSchema
guide: handle-incidents
signature: c49e943
---

## USAGE

    qctl delete error-incident <id>
    qctl describe error-incident <id>
    qctl get error-incidents

## EXAMPLES

    # clear one incident out of the active list (soft delete -- see GOTCHAS)
    qctl delete error-incident 123

    # `error` is a shorter alias for the same command
    qctl delete error 123

    # full detail for one error incident
    qctl describe error-incident 123

    # every active incident in the org
    qctl get error-incidents

    # the same list as a payload for a script
    qctl get error-incidents -o json

    # narrow to the component that raised them
    qctl get error-incidents --module sensor

    # narrow to one job's failures
    qctl get error-incidents --job-name sensor-1

    # narrow to one table by exact name or ID
    qctl get error-incidents --table orders
    qctl get error-incidents --table-id 42

    # find incidents by message text, e.g. chasing one credential problem
    qctl get error-incidents --search "AccessDenied"

    # deleted incidents only, not the active ones too
    qctl get error-incidents --deleted

## GOTCHAS

- `get error-incidents --table <name>` and `--table-id <id>` filter by exact table ID. The CLI scans all incident pages in the selected organization under the other filters before applying `--page`/`--limit` to the matches, so a table filter can require multiple requests. The total and next-page hint count matching incidents; incidents with no table are excluded.
- `describe` and `delete error-incident <id>` reject a non-numeric ID locally as bad usage (exit 2), before any API request.
- `describe error-incident <id>` renders the backend Markdown in `spec.msg` for table and YAML output. Table output embeds that plain text in its JSON cell, then prints the complete value under `Message`; JSON remains the raw API response and preserves the wire text.
- an error incident is a **platform** failure recorded automatically by sensors, ingestion jobs, or other backend components — not a rule finding; no table rule's `spec.severity` policy decides where it lands, and it has no link back to a table rule the way an alert or warning does. [backend]
- this whole resource is restricted to operations staff: `get error-incidents`, `describe error-incident`, and `delete error-incident` all gate on the same ops-only permission — any other credential gets an error on all three, not a scoped or empty result. [backend]
- `delete error-incident` itself launches no job — the underlying failure needs its own fix. It is not purely cosmetic either: besides resolving the linked alert, on a **composite** cloud source it clears that source's tripped composite-build circuit breaker, and because a failed build leaves the table marked stale, the scheduler's stale-table healer then queues its rebuild as a `scheduler_stale_reconciliation` run. `qctl run composite-run-job <table>` resets the breaker, resolves the table's composite incidents and their alerts, and queues the rebuild itself. [backend]
- `delete error-incident` is a soft delete server-side: the row is marked `deleted` (and its linked alert resolved), not physically removed — `qctl get error-incidents --deleted` still shows it, and that flag is an *equality* filter: it returns the deleted ones **only**, never mixed with the active ones. [backend]

## SEE ALSO

- `qctl explain ingestion-job`
- `qctl explain composite-run-job`
- `qctl guide handle-incidents`
