---
summary: your inbox — what has been asked of you, and what changed under you
aliases: [notifications, inbox]
kind:
schema: openapi:NotificationSchema
guide: check-inbox
signature: c0f2a29
---

## USAGE

    qctl get notifications
    qctl notification dismiss [<notification-id>]
    qctl notification read [<notification-id>]

## EXAMPLES

    # your inbox: everything still active, newest first
    qctl get notifications

    # just the ones you have not read yet
    qctl get notifications --unread

    # unread mentions and rule-review requests
    qctl get notifications --unread --type review_requested --type comment_mentioned

    # including the ones you already dismissed
    qctl get notifications --include-dismissed

    # the raw payload, with the review_id / thread_id / dataset_rule_id to act on
    qctl get notifications --unread -o json

    # the ingestion job whose upload did not carry corrections
    qctl get notifications --type corrections_not_carried --columns type,table,job_id,job_type,msg

    # quarantine counts (separate from how many times a notification was raised)
    qctl get notifications --type quarantined_rows --columns type,table,quarantined_rows_count

    # files an open alert holds until someone decides: the alert and the file
    qctl get notifications --type file_needs_decision --columns type,table,alert_item_id,stored_item_id,job_id,msg

    # provider comments: inspect the cell and follow thread_id in the raw payload
    qctl get notifications --type external_issue_comment --columns type,table,row_id,field_name,msg

    # why a broker's period needs you, and which board to open
    qctl get notifications --type series_escalation --columns type,table,trigger,period_key,cloud_source_id,series_id,msg

    # replies on threads you took part in: the thread and the new comment
    qctl get notifications --type comment_reply --columns type,table,actor,thread_id,comment_id

    # help requests from a task: the alert, the file and the step
    qctl get notifications --type support_request --columns type,table,actor,alert_item_id,stored_item_id,support_step,msg

    # downloads that were not prepared, and why an issues report was refused
    qctl get notifications --type file_download_failed --columns type,table,broker_report_refusal,msg

    # proposals waiting for review: inspect the proposal named in the hint
    qctl get notifications --type change_proposal_submitted --columns type,table,change_proposal_id,alert_item_id,warning_item_id,msg

    # the ids alone, to pipe
    qctl get notifications --unread -o name

    # the badge: needs_you and total_unread, for a script
    qctl get notifications --count -o json

    # mark one read; it stays in the list
    qctl notification read 0190bbbb-1111-2222-3333-444444444444

    # clear the unread badge without dismissing anything
    qctl notification read --all

    # ... or only the rule-upgrade noise
    qctl notification read --all --type rule_upgrade

    # take one out of the inbox for good
    qctl notification dismiss 0190bbbb-1111-2222-3333-444444444444

    # empty the inbox, unprompted
    qctl notification dismiss --all --yes

    # one request per type, counts reported per type
    qctl notification dismiss --all --type rule_upgrade --type validation_failed --yes

## GOTCHAS

- read and dismissed are two INDEPENDENT states and neither implies the other: `--unread` narrows the read axis, `--include-dismissed` widens the dismissed axis, and they compose. [backend]
- the default listing hides dismissed notifications and shows read ones, which is the backend's default, not a qctl choice — `include_dismissed` and `unread_only` both default to false. [backend]
- the inbox is per-USER and per-ORG: there is no `--all` that shows a colleague's, and switching org with `-O` switches inboxes. [backend]
- ACTOR is `-` for anything no human caused: it is an ALIAS the view adds beside `last_blast_by`, not a rename — both carry the same user resolved from `last_blast_by_id`, and all three are on the wire — and it is left NULL by rule-upgrade generation and by every validation-failure caller. [backend]
- the TABLE column is the notification's own `dataset_name`/`dataset_id` — the backend resolves the name server-side, so a listing never costs a second lookup to be readable. [backend]
- COUNT is how many times THIS notification has been raised, not how many notifications there are; a re-blast increments it and clears both `read_at` and `dismissed_at`. [backend]
- `--columns` can add `job_id`, `job_type`, `quarantined_rows_count`, `row_id`, `field_name`, `change_proposal_id`, `alert_item_id`, `warning_item_id`, `stored_item_id`, `thread_id`, `comment_id`, `support_step`, a `file_download_failed` row's `broker_report_refusal`, and a series escalation's `trigger`, `period_key`, `cloud_source_id` and `series_id`; absent context fields are blank, while a zero quarantine count prints `0`.
- the table listing ends with one next-step command on stderr for each distinct target on the page: `qctl describe change-proposal` for a row naming a proposal, and the commands each `comment_reply`, `problem_on_our_side`, `series_escalation`, `support_request` and `file_needs_decision` bullet below names; `-o json`, `-o yaml` and `-o name` omit these hints.
- `change_proposal_submitted` asks eligible reviewers for review, including after an edit that leaves the proposal pending review; `change_proposal_decided` informs active authors other than the decider of a request for changes, rejection or execution; `change_proposal_execution_failed` informs active authors and the approver of a failed execution. Repeated events of the same type for the same user and proposal bump one row, clear its read and dismissed timestamps, and refresh its table and alert or warning context. [backend]
- a notification is a POINTER, not the thing: `review_id`, `review_group_id` and `dataset_rule_id` are carried by `-o json` and `-o yaml` alone — both emit the raw wire object — and the table view has nowhere to put them; `thread_id` is also a `--columns` column.
- the list is newest-first by `last_notified_at` and there is no sort flag, because the endpoint exposes no `order_by` — it orders by (last_notified_at, created_at, id) descending, which is a total order, so pages never overlap or skip. [backend]
- paging is plain page numbers, not opaque cursors: `next`/`previous` carry a `page` integer, and `--limit` caps at 100 server-side, so `--limit 200` is a 422 (exit 2) rather than a bigger page. [backend]
- `--type` values are checked against the backend enum before the request and a wrong one exits 2; the vocabulary includes `file_ready_to_download`, while two values the API removed in 2026-08 (`review_mentioned`, `data_review_mentioned`) are now the single `comment_mentioned`. [backend]
- `--count` answers with TWO numbers, and `needs_you` is not "the ones you decide matter": it is a fixed backend set — comment_mentioned, review_requested, review_assigned, data_review_requested, data_review_assigned, data_review_group_assigned, file_ready_to_download, email_undeliverable, series_escalation, support_request, change_proposal_submitted, pii_access_requested, series_rule_approval_stale and file_needs_decision, which counts only while its alert is unresolved. Older servers count period_question_unanswered in place of file_needs_decision, lack series_rule_approval_stale, pii_access_requested, change_proposal_submitted, support_request, series_escalation or email_undeliverable, or count file_ready_to_download only in total_unread. [backend]
- the other TWENTY-FIVE types — rule_upgrade, external_issue_comment, comment_reply, review_decision, validation_failed, data_review_decision, data_review_clean_affected, severe_rule_bug, file_download_failed, corrections_not_carried, quarantined_rows, problem_on_our_side, period_summary, file_not_loaded, change_proposal_decided, change_proposal_execution_failed, issues_report_sent, pii_access_request_decided, issues_report_updated, duplicate_group_rows_changed, rows_moved_to_quarantine, duplicate_group_decision_changed, duplicate_group_response, duplicate_thread_shared and daily_summary, joined by file_ready_to_download on older servers — are counted in `total_unread` only, so a non-zero total with `needs_you: 0` means "things changed under you", not "something is waiting on you". [backend]
- a `pii_access_requested` row is listed, counted, marked read and dismissed only while its table has an undecided, unexpired PII access request; once every request is decided, withdrawn or expired it drops out of the listing and both `--count` numbers, and the next request on that table bumps the same row back. [backend]
- `period_summary` is an email preference, not an inbox type: the server writes no notification row for it, so `qctl get notifications --type period_summary` lists nothing and neither `--count` number includes it. After the latest broker due date of a table series' period has passed, the period's summary is emailed at most once, to the series' non-external followers who can see its table, keep the preference on (on by default for those roles) and have email on for their follow of the table or series, which falls back to their follow-email default (on unless changed); the preference has no effect for the external role, which never receives the summary, and both are checked again when the email is sent, so `qctl notification set-preference period_summary --email=false` also stops a summary already queued. [backend]
- `daily_summary` is an email preference too, not an inbox type: the server writes no notification row for it, so `qctl get notifications --type daily_summary` is accepted and lists nothing, and neither `--count` number includes it. The daily summary of followed table series is emailed on the organization's weekdays, at most once a day and only when something is new since the previous one, to active non-external members who follow a series, keep the preference on (on by default for those roles) and have email on for their follow of one of those series; the preference has no effect for the external role, and both are checked again when the email is sent. [backend]
- both `--count` numbers are over rows that are unread AND undismissed, so dismissing lowers the badge exactly as marking read does. [backend]
- `--count` takes no filters — the endpoint has no filter parameters — so combining it with `--type`, `--unread`, `--include-dismissed`, `--page` or `--limit` exits 2 instead of quietly answering the whole-inbox question, and `-o name` is refused for the same reason.
- `notification read` is idempotent — the backend coalesces `read_at`, so the first read keeps its timestamp and a second call is a 200, not an error. [backend]
- a `rule_upgrade` notification says a rule family moved, not what it affects: `qctl get rule-upgrades <rule>` lists the tables still on the old revision.
- a `file_ready_to_download` row is marked READ by whoever collects its file — `download data`, `download alert-report`, `download issues-report`, and `download file` on the encrypted branch — and is never dismissed, so it stays in the default listing until you dismiss it yourself; a failure to mark it read is a warning on stderr, not a failed download, because the file is already on disk.
- a `file_download_failed` row says a file you asked for was not prepared, and only a file's issues report writes one (`qctl download issues-report`): it carries the table, `file_operation_action`, the request's `download_handle` and `broker_report_refusal`, the reason the report was refused, null when the workbook failed to render or upload, but no `stored_item_id`, so it names the report and its table and not the file. A repeat failure of the same report bumps the same row. [backend]
- `download issues-report` ends its wait on its own `file_download_failed` row and leaves that row unread; no other download command lists `file_download_failed`.
- a `file_needs_decision` notification says an open alert stops a file until someone decides: a `preload_check_held` hold, a `series_file_routing` alert asking which period an emailed file belongs to, or an `inbound_email_held` hold. It goes to the internal followers of the file's table series who can see its table (for a source in no series, the internal followers of its table who receive its notifications), else to the org's active admin and ops members; it comes once per user, alert and file, carries `alert_item_id` (the blocking alert), `stored_item_id` (the file), the table and, when known, `job_id` (the blocked ingestion job), and counts toward `needs_you` only while that alert is unresolved. `qctl describe alert <alert_item_id>` lists the actions that decide it and `qctl describe file <stored_item_id>` shows the file. [backend]
- a `comment_reply` notification goes to each active member who opened a comment thread or wrote another undeleted comment on it when someone else adds a comment — never to that comment's author or anyone it mentions (they get `comment_mentioned`), never to an external member on an internal thread, and never for a rule-review thread; each new comment makes a new row and an edit makes none, and the row carries `thread_id`, `comment_id` and the thread's table, so `qctl describe comment-thread <thread_id> --table-id <dataset_id>` reads it, data-review threads included. [backend]
- a `problem_on_our_side` notification goes to each active admin and ops member of the org when Qluster opens an ops page for a file a file-level alert stopped from loading; it has no actor, its message says there is nothing for you to do, and the SAME row is bumped — unread, undismissed, COUNT up, message replaced — when the page is resolved, which only a page already posted to ops' Slack reports, and again if the page reopens. `job_id` is the stopped ingestion job: `qctl describe ingestion-job <job_id>`. [backend]
- a `series_escalation` notification tells the internal followers of a table series who can see the period's table (every internal follower when the period has no table) that a broker intake's period needs the MGA, and `trigger` says why: `still_failing` (failing rows after several files or days), `failing_share` (failing rows above a share), `ready_for_review` (the period is clean), `not_loaded` (a file arrived and nothing loaded), `exception_requested` (a broker asked for a data review) or `rule_disputed` (a broker flagged a row or commented). Each trigger fires ONCE per intake and period and never bumps or fires again, even for a follower who joins later; it has no actor, and the series switches and thresholds each trigger in `spec.deadline_schedule`. `qctl get submission-statuses --series <series_id> --period <period_key>` opens the board (`--cloud-source-id <cloud_source_id>` when `series_id` is null) and `qctl describe table <dataset_id>` the period's table, when it has one. [backend]
- a `support_request` notification is a "Need help?" message someone sent from a task on an alert: it goes to each active member with the admin role exactly, other than the sender, who is its ACTOR; its message is theirs, every request makes a new row, `support_step` names the task step, and `stored_item_id` is set only when they named a file of the alert. `qctl describe alert <alert_item_id>` and `qctl describe file <stored_item_id>` follow it; qctl has no command that sends one. [backend]
- there is no mark-unread or un-dismiss verb. Re-blasts, review re-requests, repeat validation failures, decisions, and a repeat file-ready, file-download-failed, corrections-not-carried, file-not-loaded, issues-report, change-proposal, PII-access, problem-on-our-side, stale series-rule-approval, undeliverable-email or duplicate-group-rows-changed event can clear both timestamps and increment COUNT. `quarantined_rows` resurfaces only when its count goes from zero/null to positive outside the backend cooldown; another `external_issue_comment`, `duplicate_group_response` or `duplicate_group_decision_changed` after reading or dismissal creates a new row. A repeat mention on the SAME comment stays gone. [backend]
- `read --all` skips DISMISSED notifications while `read <id>` will mark a dismissed one read — the bulk form carries `dismissed_at IS NULL` in its filter and the single form does not. [backend]
- `read --all --type a --type b` is ONE request: mark-all-read takes the repeatable filter as a query parameter, which is why only this verb can scope a bulk change atomically. [backend]
- `--type` needs `--all` on both verbs; on a single id it would be dropped, so it is refused with exit 2 rather than acted around.
- `notification dismiss <id>` is NOT idempotent: the backend's update filters `dismissed_at IS NULL` and the view turns a zero rowcount into a 404, so dismissing an already-dismissed notification exits 3 rather than doing nothing. [backend]
- `dismiss --all --type a --type b` is N requests, one per type, because dismiss-by-type takes ONE type in the URL path; the counts are summed and reported per type, and a failure part-way still prints what already landed before exiting non-zero. [backend]
- `dismiss --all` asks before it runs: pass `--yes` for scripts, where a missing confirmation is exit 2 and declining at the prompt is exit 1.
- `dismiss --all` with no `--type` uses a different endpoint from the by-type form and answers with one total; only the by-type form can report a per-type breakdown. [backend]
- `notification read` and `dismiss` print `{id, read: true}` or `{id, dismissed: true}` under `-o json` and `-o yaml` for one id, whose route answers only `{message, success}`, and `{read_count}` or `{dismissed_count}` for `--all`, the dismiss count summed over the types that landed; the result line moves to stderr, and `-o name` exits 2.

## SEE ALSO

- `qctl explain notification-preference`
- `qctl explain rule-review`
- `qctl explain rule-upgrade`
- `qctl explain comment-thread`
- `qctl explain alert`
- `qctl explain change-proposal`
- `qctl guide check-inbox`
- `qctl guide review-rules`
