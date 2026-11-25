---
summary: which notification types you are emailed about, per organization
aliases: [email-preferences]
kind: NotificationPreferences
schema: manifest:notification-preference
guide:
signature: a1c7b98
---

## USAGE

    qctl apply -f <file.yaml>   # kind: NotificationPreferences
    qctl get notification-preferences
    qctl notification set-preference <type>...

## EXAMPLES

    # one row per type: EMAIL is what applies now, DEFAULT your role's default, OVERRIDDEN where they differ
    qctl get notification-preferences

    # an apply-able manifest: spec.email holds only the types that differ from the default
    qctl get notification-preferences -o yaml

    # replace every override from a file; a type the file leaves out follows the role default
    qctl apply -f prefs.yaml

    # stop the period summary email (read-modify-write: the other types keep their settings)
    qctl notification set-preference period_summary --email=false

    # turn the email on for two types at once
    qctl notification set-preference comment_mentioned comment_reply --email=true

## GOTCHAS

- the read answers with your EFFECTIVE setting per type and your role's default, never with the overrides stored for you, so an override equal to its default is invisible: OVERRIDDEN means only that the two differ, and `-o yaml` leaves such a type out of `spec.email`. [backend]
- the types are the ones the server lists, not every notification type: `issues_report_sent` and `issues_report_updated` send their own email and have no preference, and a write naming either is silently dropped by the server, so qctl refuses any type the read did not list with exit 2 before writing anything. [backend]
- the defaults depend on your role in that organization: the external role is emailed about comment_mentioned, external_issue_comment, comment_reply, corrections_not_carried and duplicate_thread_shared; every other role about comment_mentioned, comment_reply, severe_rule_bug, validation_failed, email_undeliverable, file_needs_decision, series_escalation, deliverable_at_risk, period_summary, daily_summary, pii_access_requested, series_rule_approval_stale and series_rule_approval_requested. `period_summary` and `daily_summary` have no effect for the external role: the summaries go only to non-external series followers, whatever the preference. [backend]
- preferences belong to your membership of one organization, and `-O` reads and writes another organization's. Without an active membership of that organization the server refuses before the route runs, with 403 "User is not a member of this organization" (or 401 "User membership has expired" when a time-limited membership lapses while the server still caches it), and an API token issued for another organization gets 403 "Bearer token is not authorized for this organization."; every one of these exits 4. [backend]
- the write REPLACES every stored override: `apply -f` sends only the `spec.email` entries that differ from their role default, so a type the manifest leaves out or sets to its default follows that default from then on, and `email: {}` restores every default. [backend]
- `apply -f` always writes, because the stored overrides cannot be read back to compare with, a manifest with no `spec.email` exits 2 rather than being read as `{}`, and an empty or null value (`period_summary:`) exits 2 naming the key rather than being read as false; it prints `notification-preference/<metadata.name> configured` when your effective settings changed and `unchanged` when they did not.
- `notification set-preference` is read-modify-write: it reads, sets each named type, and writes back every type that then differs from its default, so a change made elsewhere in between is overwritten, a type set to its default stops being an override, and nothing is written when no named type changes.
- `--email` takes exactly `true` or `false` and is required (exit 2 otherwise); it is a string flag, so `--email false` works as well as `--email=false`.
- `get notification-preferences` and `notification set-preference` have no `-o name` view (exit 2); `apply -f -o name` prints `notification-preference/<metadata.name>`.

## SEE ALSO

- `qctl explain notification`
