# Read your notification inbox and clear it

    qctl get notifications --count -o json
    qctl get notifications --unread -o json
    qctl notification read <id>
    qctl notification dismiss <id> --yes

Review requests, review decisions, `@mentions`, rule upgrades and severe rule
bugs all arrive in one per-user, per-org inbox, and several have no other read
path. Check it first: an agent that never does cannot tell it has been asked a
question. Then act on what it points at, and clear what you handled. Every
state and paging rule: `qctl explain notification`.

## Is anything waiting?

    qctl get notifications --count

Two numbers, in one request with no filters (`-o json` gives
`{"needs_you": 3, "total_unread": 11}`). `total_unread` counts everything you
have neither read nor dismissed. `needs_you` is the subset asking you for
something: mentions, rule- and data-review requests and assignments, a file
ready to download (an older server counts that one in `total_unread` only),
and `email_undeliverable`, `series_escalation`, `support_request`,
`change_proposal_submitted`, `pii_access_requested`,
`series_rule_approval_stale`, `series_rule_approval_requested`,
`deliverable_at_risk`, `issues_report_counts_changed` and
`file_needs_decision` on servers that send them; a `file_needs_decision` counts only while its alert is unresolved.
`corrections_not_carried`, `quarantined_rows`, `comment_reply`,
`problem_on_our_side`, `period_summary`, `file_not_loaded`,
`change_proposal_decided`, `change_proposal_execution_failed`,
`issues_report_sent`, `pii_access_request_decided`, `issues_report_updated`
and `external_issue_comment` are informational, like a rule upgrade or review
decision: they move `total_unread` alone. So `needs_you: 0`
with a non-zero total means things changed under you rather than somebody
waiting on you.

    qctl get notifications
    qctl get notifications --unread
    qctl get notifications --unread --type review_requested --type comment_mentioned

The inbox for the current context's org, newest first: type, message, who
caused it, and which table. Read and dismissed are two independent states:
dismissed notifications are hidden, read ones still shown. `--unread` narrows
one axis, `--include-dismissed` widens the other, and `--type` is repeatable;
add `review_assigned`, `data_review_requested`, `data_review_assigned`,
`data_review_group_assigned`, `file_ready_to_download`,
`email_undeliverable`, `series_escalation`, `support_request`,
`change_proposal_submitted`, `pii_access_requested`,
`series_rule_approval_stale`, `series_rule_approval_requested`,
`deliverable_at_risk`, `issues_report_counts_changed` and
`file_needs_decision` to the two above to cover the `needs_you` types.

## Act on it

A notification is a pointer. The ids you act on (`review_id`,
`data_review_id`, `thread_id`, `dataset_rule_id`, `change_proposal_id`) are in
the raw object that `-o json` and `-o yaml` emit. `thread_id` and
`change_proposal_id` are also available through `--columns` in the table view.

    qctl describe rule-review <review_id>
    qctl describe comment-thread <thread_id> --table-id <dataset_id>
    qctl describe data-review <data_review_id>
    qctl get data-reviews --table-id <dataset_id> --group <review_group_id>
    qctl get data <dataset_id> --history-ids <history_id>
    qctl describe change-proposal <change_proposal_id>

- `review_id`: a rule review request or decision.
- `thread_id`: a mention on a comment thread.
- `data_review_id`: `data_review_assigned` and `data_review_decision`, about
  one row's review.
- `review_group_id`: `data_review_group_assigned`, a whole submission.
- `history_id`: `data_review_clean_affected`, which names no review: a change
  a reviewer just blocked is also on rows you submitted that are now in clean
  data.
- `change_proposal_id`: `change_proposal_submitted`, `change_proposal_decided`
  and `change_proposal_execution_failed`. Describe the proposal to inspect its
  state, review and available next actions.

The table listing ends with next-step commands on stderr, one for each
distinct target on the page: the change proposal a row names, and the
commands the sections below give for `series_escalation`,
`problem_on_our_side`, `comment_reply`, `support_request` and
`file_needs_decision`. `-o json`,
`-o yaml` and `-o name` print none. Select a proposal's context explicitly
with:

    qctl get notifications --type change_proposal_submitted --columns type,table,change_proposal_id,alert_item_id,warning_item_id,msg

`alert_item_id` or `warning_item_id` identifies the issue the proposal
resolves; either column is blank when absent.

A mention that carries `review_id` is on a rule review's discussion, which
`describe comment-thread` cannot read; `describe rule-review` prints it. One
that carries `data_review_id` is on that row review's thread, which both
`describe comment-thread <thread_id> --table-id <dataset_id>` and
`describe data-review` read. A `data_review_decision` sent because of a decision on
one cell also carries that change's `history_id`, the `[h: <id>]` in that
output. The data-review workflow: `qctl guide review-data`.

The three informational types also carry context you can select in a table:

    qctl get notifications --type corrections_not_carried --columns type,table,job_id,job_type,msg
    qctl get notifications --type quarantined_rows --columns type,table,quarantined_rows_count
    qctl get notifications --type external_issue_comment --columns type,table,row_id,field_name,msg

For `corrections_not_carried`, when `job_type` is `IngestionJob`, inspect the
upload with `qctl describe ingestion-job <job_id>`. For `quarantined_rows`,
`quarantined_rows_count` is the recipient's quarantined row count; `count`
counts notification raises. Inspect the quarantine with
`qctl get data <dataset_id> --is-bad-data`. For `external_issue_comment`,
`row_id` and `field_name` locate the cell; use the raw object's `thread_id`
with `qctl describe comment-thread <thread_id> --table-id <dataset_id>`.
Absent context fields are blank; a zero quarantine count prints `0`.

A `file_needs_decision` notification means an open alert stops a file until
someone decides: a pre-load check hold, a question about which period an
emailed file belongs to, or a held email. It goes to the followers of the
file's series or table, or to admin and ops members when nobody follows it.
`alert_item_id` is the blocking alert and `stored_item_id` the file; the
alert's actions are the decisions:

    qctl get notifications --type file_needs_decision --columns type,table,alert_item_id,stored_item_id,msg
    qctl describe alert <alert_item_id>
    qctl describe file <stored_item_id>

A `comment_reply` means someone else commented on a thread you opened or
wrote in. `thread_id` is the thread and `comment_id` the new comment:

    qctl get notifications --type comment_reply --columns type,table,actor,thread_id,comment_id
    qctl describe comment-thread <thread_id> --table-id <dataset_id>

A `problem_on_our_side` is Qluster telling an admin or ops member that a file
stopped on Qluster's side; there is nothing for you to do. The same row comes
back unread when the problem is fixed. `job_id` is the stopped ingestion job:

    qctl get notifications --type problem_on_our_side --columns type,table,job_id,count,msg
    qctl describe ingestion-job <job_id>

A `series_escalation` says a broker intake's period of a series you follow
needs you, once per intake, period and `trigger` (`still_failing`,
`failing_share`, `ready_for_review`, `not_loaded`, `exception_requested`,
`rule_disputed`). `series_id` and `period_key` open the series board for that
period; with no `series_id`, `cloud_source_id` opens the intake's board.
The period's table, when it has one, is the notification's table:

    qctl get notifications --type series_escalation --columns type,table,trigger,period_key,cloud_source_id,series_id,msg
    qctl get submission-statuses --series <series_id> --period <period_key>
    qctl get submission-statuses --cloud-source-id <cloud_source_id>
    qctl describe table <dataset_id>

A `support_request` is a "Need help?" message someone sent an org admin from
a task on an alert. The actor sent it, `support_step` names the task step,
and `stored_item_id` is the file they named, when they named one:

    qctl get notifications --type support_request --columns type,table,actor,alert_item_id,stored_item_id,support_step,msg
    qctl describe alert <alert_item_id>
    qctl describe file <stored_item_id>

A `file_download_failed` says a file you asked for was not prepared; only a
file's issues report writes one. `broker_report_refusal` says why the report
was refused, and is blank when the server failed to build or store it. The
row names the report's table, not the file:

    qctl get notifications --type file_download_failed --columns type,table,broker_report_refusal,msg
    qctl download issues-report <file-id>

## Clear what you handled

    qctl notification read 0190bbbb-1111-2222-3333-444444444444
    qctl notification read --all
    qctl notification read --all --type rule_upgrade

Marking read clears the badge and leaves the notification in the list. It is
idempotent, and there is no mark-unread.

    qctl notification dismiss 0190bbbb-1111-2222-3333-444444444444 --yes
    qctl notification dismiss --all --yes
    qctl notification dismiss --all --type rule_upgrade --yes

Dismissing takes it out of the inbox, read or not. There is no un-dismiss
verb. Re-blasts and repeat review, validation, file-ready or
corrections-not-carried events can bring a row back and bump its count.
Quarantine count updates preserve a dismissal until a zero/null-to-positive
transition outside the backend cooldown. A later provider comment after
reading or dismissal creates a new notification; a repeat mention on the
same comment stays gone. `--type` on `dismiss --all` costs one request per
type, so the output reports a count for each.

## See also

- `qctl explain notification`
- `qctl guide review-rules`
- `qctl guide review-data`
- `qctl guide triage-alerts`
