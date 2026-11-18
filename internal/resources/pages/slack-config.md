---
summary: an organization's Slack settings, from which use cases post to per alert type delays
aliases: [slack-settings]
kind:
schema: openapi:OrgSlackConfig
guide:
signature: 7eae9e3
---

## USAGE

    qctl get slack-config
    qctl slack set-alert-type <alert-type>...

## EXAMPLES

    # the use cases, alert_unattended's delay per tier and reminder interval, and every alert type entry
    qctl get slack-config

    # the config as the server answers it
    qctl get slack-config -o yaml

    # stop posting an alert type that posts by default
    qctl slack set-alert-type file_already_ingested --enabled=false

    # post two alert types an hour after they are raised instead of after their tier's delay
    qctl slack set-alert-type preload_check_held inbound_email_held --delay-hours=1

    # drop the entry: the type follows its tier again
    qctl slack set-alert-type file_already_ingested --reset

## GOTCHAS

- both routes need the org:slack:manage permission; without it the server answers 403 and qctl exits 4. [backend]
- the write is refused with 403 only when it changes which use cases post to Qluster's ops channel and the caller lacks ops:slack:manage; `slack set-alert-type` sends those settings back as it read them, so it never trips that refusal. [backend]
- an alert type posts by default only when its alert class has a Slack tier, and no route says which ones do: file_decision covers inbound_email_held, preload_check_held and series_file_routing, blocking covers destination_table_already_exists, file_already_ingested, incompatible_schema, initial_fields, migration_frozen, needs_review_migration and unexpected_field_name. Every other type is off until `--enabled=true`, and then waits the blocking tier's delay unless it has a delay of its own. [backend]
- the server refuses (422, exit 2) an alert type it does not know and `--enabled=true` for a row-level alert type; it accepts an entry for an alert type that pages ops, but such a type never posts as an unattended alert whatever its entry says. [backend]
- a delay counts from when the alert was raised, and only an open, file-level, blocking alert the organization's users can settle is posted. [backend]
- the read leaves out a stored entry for an alert type that can no longer be posted, and the next write, which sends what was read, removes it. [backend]
- `get slack-config` lists only the alert types that have an entry; a type without one follows its tier, so the listing does not say which types post.
- `slack set-alert-type` is read-modify-write: it reads the whole config, changes each named entry and writes all of it back, so a change made elsewhere in between is overwritten; nothing is written when no named entry changes. A flag left out keeps that half of the entry, and an entry left with neither half is removed.
- `--enabled` takes `true`, `false` or `default`, `--delay-hours` a whole number of hours (0 or more) or `default`, and `--reset` combines with neither; any other use exits 2 before a request. Alert type names are not checked locally: shell completion offers them, the server judges them.
- neither command has an `-o name` view (exit 2); `-o json|yaml` on `slack set-alert-type` prints the resulting config and moves the per type lines to stderr.

## SEE ALSO

- `qctl explain alert`
