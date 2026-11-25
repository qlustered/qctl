---
summary: rows of a mirror or append table held or flagged together until a reviewed decision
aliases:
kind:
schema: openapi:DuplicateGroupDetailSchema
guide: review-data
signature: 2d6aca6
---

## USAGE

    qctl describe duplicate-group <group-id>
    qctl duplicate-group allow <group-id>
    qctl duplicate-group allow-all --file-id <file-id>
    qctl duplicate-group approve <group-id>
    qctl duplicate-group comment <group-id>
    qctl duplicate-group disagree <group-id>
    qctl duplicate-group dismiss <group-id>
    qctl duplicate-group keep-one <group-id>
    qctl duplicate-group mark
    qctl duplicate-group not-duplicates <group-id>
    qctl duplicate-group reapply
    qctl duplicate-group respond <group-id>
    qctl duplicate-group retract <group-id>
    qctl duplicate-group share <group-id>
    qctl duplicate-group stop-series <group-id>
    qctl duplicate-group withdraw <group-id>
    qctl duplicate-group withdraw-response <group-id> <response-id>
    qctl get duplicate-groups

## EXAMPLES

    # list a table's groups that need attention
    qctl get duplicate-groups --table orders --state needs_attention

    # reviewer groups from one cloud source
    qctl get duplicate-groups --table orders --cloud-source-id 7

    # the groups of one upload, ended ones included
    qctl get duplicate-groups --table-id 42 --file-id 311 --include-ended

    # find the group a row belongs to, then read it
    qctl get duplicate-groups --table orders --row-id 1042
    qctl describe duplicate-group 0190aaaa-1111-4222-8333-444444444444 --table orders

    # as a sender: the groups with a copy on your cloud sources, then one as you see it
    qctl get duplicate-groups --table-id 42 --sender
    qctl get duplicate-groups --table-id 42 --sender --cloud-source-id 7 --include-ended
    qctl describe duplicate-group 0190aaaa-1111-4222-8333-444444444444 --table-id 42 --sender

    # as a sender: ask that the copies load as separate rows (a request; the table's reviewers decide)
    qctl duplicate-group respond 0190aaaa-1111-4222-8333-444444444444 --table-id 42 --kind separate-rows --comment "two shipments, same day"

    # as a sender with copies on several cloud sources: say a corrected file follows for one of them
    qctl duplicate-group respond 0190aaaa-1111-4222-8333-444444444444 --table-id 42 --kind corrected-file-follows --comment "resending tonight" --cloud-source-id 7

    # withdraw a live response (its id is in describe duplicate-group --sender)
    qctl duplicate-group withdraw-response 0190aaaa-1111-4222-8333-444444444444 0190eeee-1111-4222-8333-444444444444 --table-id 42

    # allow every current copy of a repeated key
    qctl duplicate-group allow 0190aaaa-1111-4222-8333-444444444444 --table orders --yes

    # allow at most two copies, with a comment the decision keeps
    qctl duplicate-group allow 0190aaaa-1111-4222-8333-444444444444 --table-id 42 --count 2 --comment "two shipments" --yes

    # keep one member (a member id, not a row id) and reject the rest
    qctl duplicate-group keep-one 0190aaaa-1111-4222-8333-444444444444 --table orders --member 812 --yes

    # say a person-marked group's rows are not duplicates
    qctl duplicate-group not-duplicates 0190aaaa-1111-4222-8333-444444444444 --table orders --reason "different customers" --yes

    # approve the group's pending decision, pinning the one you read
    qctl duplicate-group approve duplicate-group/0190aaaa-1111-4222-8333-444444444444 --table orders --decision 0190bbbb-1111-4222-8333-444444444444 --yes

    # disagree with the pending decision: it cannot take effect while your disagreement stands
    qctl duplicate-group disagree 0190aaaa-1111-4222-8333-444444444444 --table orders --reason "the rows are two orders" --yes

    # as an admin or ops user: dismiss another reviewer's pending decision
    qctl duplicate-group dismiss 0190aaaa-1111-4222-8333-444444444444 --table orders --reason "proposed on the wrong key" --yes

    # withdraw your own pending decision
    qctl duplicate-group withdraw 0190aaaa-1111-4222-8333-444444444444 --table orders --comment "wrong key" --yes

    # propose to stop the series allow governing the group, in every period of its series
    qctl duplicate-group stop-series 0190aaaa-1111-4222-8333-444444444444 --table orders --comment "the key repeats only this month" --yes

    # allow every repeated key of one upload
    qctl duplicate-group allow-all --table orders --file-id 311 --yes

    # re-apply the decisions whose rows disagree with them
    qctl duplicate-group reapply --table orders --yes

    # mark rows as duplicates of one another and hold them until a reviewer decides
    qctl duplicate-group mark --table orders --row-id 1001,1002 --mode hold --comment "same order keyed twice" --yes

    # only flag them instead; they stay where they are
    qctl duplicate-group mark --table-id 42 --row-id 1001 --row-id 1007 --mode flag --comment "check with the sender" --yes

    # take a mark back: the group ends and its quarantined rows are reprocessed
    qctl duplicate-group retract 0190aaaa-1111-4222-8333-444444444444 --table orders --comment "they are two orders" --yes

    # discuss the group internally, then share the discussion with its data source
    qctl duplicate-group comment 0190aaaa-1111-4222-8333-444444444444 --table orders --body "same order twice?"
    qctl duplicate-group share 0190aaaa-1111-4222-8333-444444444444 --table orders --comment "please confirm" --yes

## GOTCHAS

- `--cloud-source-id` filters both reviewer and sender listings. `describe duplicate-group` lists each unapplied period as `table/<id>`, its `skipped_locked` or `failed` outcome and the server's reason; the unapplied summary and retry hint also cover row mismatches.

- The reviewer view of `describe duplicate-group` prints `MEMBER VALUES` by member and row ID, with fields sorted by name. A null or absent `values` map prints `(unavailable)`, an empty map prints `(no values)`, and a null cell prints `null`.
- Listing and reading groups needs read access to the table's data, which every role but external has. The list puts groups needing attention (needs_decision, exceeds_allowed, kept_content_not_found, flagged) first, then newest first, leaves out ended groups unless asked, and cannot filter by row. [backend]
- In the list a live group shows only its current members, while describe shows every member, including deleted ones and ones that left the group; an ended group lists all of them both ways. [backend]
- A group's key values, differing cells and member values are masked for a caller whose role the table masks those columns for, and `masked_fields` names the masked columns among them. [backend]
- `get duplicate-groups --row-id` reads every page of the list, 100 groups at a time, and keeps the groups with a listed member on that row, so it does not find a live group by a row that has left it; it takes no `--page` or `--limit`. `--state ended` implies `--include-ended`.
- A repeated_key, identical_rows or person_marked_duplicate alert offers no actions, and `qctl alert resolve` refuses it (exit 2) with the command that shows its group: a reprocess keeps its rows held and raises a new alert on them, and only the group's decision lets them load. The alert names its group in `body.group_id`, and `describe alert`'s NEXT ACTIONS prints the `describe duplicate-group` command for it. [backend]
- `--sender` reads the groups as a data source's sender sees them and needs SOURCE_DATA_READ, which every role has: an external user sees the groups with a copy on its granted cloud sources, any other role those on every source of the table. `get duplicate-groups --sender` shows only each group's undeleted copies on those sources, and only groups with one; `describe duplicate-group --sender` also lists the deleted copies there, with no outcome (`-o yaml` carries their `deleted_at` and `deleted_by`), and describes a group whose only copies there are deleted. A person_marked group in flag mode stays hidden there until its discussion is shared or an allow or keep-one on it has taken effect. An external user's `--cloud-source-id` or `--file-id` outside its granted sources is refused with 403 (exit 4). [backend]
- In the sender view a group's state is held (held_reason undecided, more_than_allowed or kept_line_changed), decided, loaded or ended; an undecided person_marked group in flag mode reads held (undecided) though its rows loaded, so read each copy's outcome. The state covers every copy of the group, other sources' copies you cannot see included, so a group can read held (undecided) while every one of your copies loaded, because another source's copy is held. The decision shown is the one in effect, the group's own or the allow it carries, never a pending one; a keep-one that kept another source's copy names no member and shows as kept elsewhere. [backend]
- The sender view's responses are those of your cloud sources only: the live ones, plus in describe the ended ones (replaced, withdrawn, group_ended, file_received); awaiting_corrected_file is set while a live one says corrected_file_follows. You may respond (`caller.can_respond`) unless you lack DATASET_INGEST_UPLOAD (every role but read_only has it, and a read_only grant on the table withholds it), every copy of yours in the group is deleted (reason `not_permitted`, the same as for the missing permission), the group has ended, or the table's period is locked. [backend]
- A sender's response is a request, not a decision: separate_rows asks that the copies load as separate rows, and only a decision by the table's reviewers changes them. A response replaces its cloud source's live response on the group and changes no row, member or decision; it rewrites the group's hold alert messages and notifies the group's internal participants (its marker, its decisions' proposers, the reviewers who approved or disagreed with them, and its discussion's commenters). [backend]
- A response is recorded for one cloud source: when your copies in the group are on several, `respond` without `--cloud-source-id` is refused with 422 (exit 2), and a cloud source with no copy shown to you answers 404 (exit 3). An ended group refuses responding and withdrawing with 409 DUPLICATE_GROUP_ENDED, withdrawing a response that has ended (replaced, withdrawn, or ended by a later file from the cloud source) is 409 DUPLICATE_RESPONSE_NOT_LIVE, and a locked period refuses both with 409 PERIOD_LOCKED (all exit 2). [backend]
- `respond` takes `--kind separate-rows|corrected-file-follows` (underscores accepted) and a required `--comment`, sent trimmed; a missing or unknown kind, or a missing or blank comment, exits 2 before any request. `respond` and `withdraw-response` do not prompt and take no `--yes`.
- `--sender` takes `--cloud-source-id`, `--file-id`, `--include-ended`, `--page` and `--limit`; `--origin`, `--state`, `--sender-response` or `--row-id` with it exits 2 before any request.
- Only mirror and append tables have duplicate groups; any other table answers 422 (exit 2). While the table's period is locked, every decision, approval, disagreement, dismissal, withdrawal, stop-series, reapply and share, and an allow-all with a group to allow, is refused with 409 PERIOD_LOCKED (exit 2). [backend]
- Deciding, approving, disagreeing, dismissing, withdrawing, stop-series, allow-all, reapply and share need DATA_REVIEW_APPROVE (admin, member and ops; not agent, editor or read_only) on the table, and the table's change review policy governs them under data_review_approve; they have no proposal path. Approving a pending decision, with `approve` or by sending its own choice, and disagreeing with it need you among its designated reviewers, and any other decision or a stop-series among the policy's; anyone else is refused with 403 APPROVER_NOT_DESIGNATED (exit 4), and an excluded reviewer may do none of them. Only an admin or ops user who did not propose it may dismiss a pending decision, and only its proposer may withdraw it; neither checks designation or exclusion. Commenting needs COMMENT_WRITE and read access to the table's data. [backend]
- A repeated_key allow takes `--count` of at least 2, or none for every current copy; an identical_rows allow requires `--count` of at least 1; a person_marked allow takes none. keep-one names an active member on repeated_key and person_marked groups, while on identical_rows it takes no member and the server records it as an allow of 1. not-duplicates decides only person_marked groups and needs a non-blank reason. [backend]
- Sending the same kind, count and kept member as the group's pending decision approves it; anything else supersedes it and restarts its approvals when you proposed it, and is refused with 409 DUPLICATE_DECISION_PENDING (exit 2) when another reviewer did. A decision takes effect when its counted approvals reach the policy's minimum while no counted reviewer disagrees with it; approvals by someone who edited or requested review of the group's rows, or who marked the group, do not count, and such a person is refused with 403 DUPLICATE_DECISION_REVIEWER_EXCLUDED (exit 4). [backend]
- A decision sent while another decision governs the group, its own or one it carries, is a change: it waits for approvals like any proposal, and the governing decision stays in force until the change takes effect. Withdrawing takes back only the group's pending decision, and only its proposer may: another reviewer's is refused with 403 DUPLICATE_DECISION_NOT_YOURS (exit 4), and a decision that is not the group's pending one with 409 DUPLICATE_DECISION_NOT_LIVE (exit 2). [backend]
- On a table in a series, a repeated_key allow proposed on a group no decision governs is scoped to the series: it also governs the key in the series' later uploads and periods. A decision proposed on a group an allow already governs is table-scoped and leaves that allow in force in the other periods. Withdrawing takes back only a pending decision, so an effective series allow keeps governing every period until a stop-for-series proposal on a group it governs takes effect. One decision can therefore change rows in other tables, which the result lists per table. [backend]
- Disagreeing needs a non-blank reason (422, exit 2) and replaces your earlier vote on the pending decision, an approval included. A counted disagreement holds the decision pending however many others approve it, until you approve it yourself, which replaces the disagreement. The decision's proposer is refused with 403 DUPLICATE_DECISION_PROPOSER (exit 4) and may withdraw it instead. [backend]
- Dismissing ends another reviewer's pending decision and needs a non-blank reason (422, exit 2) and an admin or ops role: any other role is refused with a 403 that carries no error code, and the decision's proposer with 403 DUPLICATE_DECISION_PROPOSER (both exit 4). The group keeps the decision that governed it, or is undecided again. [backend]
- `stop-series` proposes to stop the effective series allow `caller.stop_for_series_decision_id` names: the series allow governing the group, or its own series allow when a table-scoped decision of its own governs it. Any other decision is refused with 409 DUPLICATE_DECISION_NOT_SERIES_ALLOW, and a group with a pending decision with 409 DUPLICATE_DECISION_PENDING (both exit 2). The proposal is a pending stop_series decision carrying its proposer's approval, approved, disagreed with, withdrawn or dismissed like any other; once its approvals are in it is enacted and supersedes the allow, which then governs no group in any period of the series. [backend]
- `approve`, `disagree`, `dismiss` and `withdraw` read the group and act on its pending decision, which `--decision <uuid>` may pin; they exit 2 without a request when nothing is pending or the pending decision is not the one pinned. `approve` with no `--comment` on a decision you already approved sends nothing and exits 0. `stop-series` proposes to stop the series allow the group names in `caller.stop_for_series_decision_id`, and exits 2 without a request when it names none or a decision is pending. `disagree` and `dismiss` require a non-blank `--reason`.
- allow-all allows the current copies of each repeated_key group of the upload that still holds a copy, and skips a group whose own pending decision differs (pending_differs), whose own decision is effective (decided_here), or that you may not decide (not_designated, excluded). `--file-id` must name the upload, not one of its sections (422, exit 2). [backend]
- reapply re-applies decisions to rows that disagree with them, and retries a group whose latest apply in a period of its key reach was skipped because the period was locked or failed; it records no decision. [backend]
- A group's discussion is internal until shared. Sharing cannot be undone, makes the thread readable by the senders of the one data source its rows are on, and on a person_marked group also shows them the comment the group was marked with. A group whose rows span sources is refused with 409 DUPLICATE_THREAD_SPANS_SOURCES (exit 2). [backend]
- Sharing a discussion already shared posts nothing and notifies no one, so `share` refuses `--comment` there (exit 2); post with `comment` instead. `share` honors protected contexts.
- Marking and retracting need SUBMIT_ALERT, an internal role (not external) and access to the table, and a hold mark also needs SOURCE_DATA_WRITE. The table's change review policy governs a mark under user_alert_submit (and rows_quarantine_move for a hold) and a retract under user_alert_retract; neither has a proposal path. Both are refused with 409 PERIOD_LOCKED (exit 2) while the table's period is locked. [backend]
- A mark takes 2 to 500 distinct rows of the table and a non-blank comment of at most 10000 characters, which opens the group's internal discussion. A row that is not a live row of the table is refused with 422 naming it in `context.row_ids`; a row in another live marked group with 409 DUPLICATE_ROW_ALREADY_MARKED, and a row held by a loaded group awaiting a decision with 409 DUPLICATE_ROW_IN_UNDECIDED_GROUP, both naming the rows and groups in `context`. [backend]
- A hold mark holds its rows in quarantine, moving clean ones there, and raises a person_marked_duplicate alert on them; a flag mark only flags them and raises no alert. The marker never counts as the group's reviewer, so someone else must decide it. [backend]
- Retracting ends a person_marked group and queues its rows still in quarantine for a reprocess, which releases them unless something else holds them. A pending decision does not block it; an effective one is refused with 409 DUPLICATE_DECISION_EXISTS, an ended group with 409 DUPLICATE_GROUP_ENDED and a load-formed one with 422 DUPLICATE_GROUP_NOT_PERSON_MARKED. Only the marker may retract the group; anyone else is refused with 403 DUPLICATE_GROUP_NOT_MARKER (exit 4), and describe shows yours as "can retract the mark". [backend]
- `mark` takes no argument and checks `--row-id` (2-500 distinct positive ids, repeatable or comma-separated), `--mode hold|flag` (required) and `--comment` (required) before any request, at exit 2. `retract` reads the group first and exits 2 without a retract on a load-formed or ended group or one with an effective decision of its own.
- Every reviewer verb but `comment` prompts unless `--yes`. `-o json|yaml` print the server's response and move the result lines to stderr; `-o name` prints `duplicate-group/<uuid>`.

## SEE ALSO

- `qctl explain data-review`
- `qctl explain comment-thread`
- `qctl guide review-data`
