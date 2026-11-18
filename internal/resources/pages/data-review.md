---
summary: the data-row review workflow for quarantined rows
aliases:
kind:
schema:
guide: review-data
signature: 890a9eb
---

## USAGE

    qctl data-review accept-cell <review-id> (--field <name> | --history-id <id>)
    qctl data-review approve [<review-id>...]
    qctl data-review assign <review-id> --reviewer <reviewer>
    qctl data-review clear-cell <review-id> (--field <name> | --history-id <id>)
    qctl data-review close <review-id>
    qctl data-review comment <review-id>
    qctl data-review progress
    qctl data-review re-request <review-id>
    qctl data-review request --row-id <row-id> --title <title>
    qctl data-review unassign <review-id> --reviewer <reviewer>
    qctl data-review withdraw [<review-id>...]
    qctl data-review withdraw-cell <review-id> (--field <name> | --history-id <id>)
    qctl describe data-review <review-id>
    qctl get data-review-changes
    qctl get data-reviews

## EXAMPLES

    # the reviewer's queue: rows awaiting a decision, each with its review id
    qctl get data-reviews --table orders

    # rows a reviewer blocked, within one submission
    qctl get data-reviews --table-id 42 --state rejected --group 11111111-2222-4333-8444-555555555555

    # the active review ids on the second page, for scripting
    qctl get data-reviews --table orders --page 2 -o name

    # which requests each row's review is in, to act on one with data-review-group
    qctl get data-reviews --table orders --columns id,row,requester,requests

    # everything about one row's review: cell verdicts, decisions, thread, next steps
    qctl describe data-review 0190bbbb-1111-4222-8333-444444444444

    # the raw review with its per-field changes, for scripting
    qctl describe data-review 0190bbbb-1111-4222-8333-444444444444 -o json

    # the highest-leverage unresolved cell changes for a table
    qctl get data-review-changes --table orders

    # the second page of a submission's unresolved changes
    qctl get data-review-changes --table-id 42 --group 11111111-2222-4333-8444-555555555555 --offset 20

    # inspect changes that already received an approval verdict
    qctl get data-review-changes --table-id 42 --state accepted -o yaml

    # each side of a ranked change beside its unit tag: `decimal` marks a number
    qctl get data-review-changes --table orders --columns history_id,field,before,before_type,after,after_type

    # changes whose affected rows still carry an open user flag
    qctl get data-review-changes --table orders --columns history_id,field,rows,flagged

    # approve two rows' reviews by id; an explicit id list never prompts
    qctl data-review approve 0190bbbb-1111-4222-8333-444444444444 0190cccc-1111-4222-8333-555555555555

    # approve every open review in one submission, with a note, skipping the prompt
    qctl data-review approve --group 11111111-2222-4333-8444-555555555555 --comment "spot-checked" --yes

    # the processed/skipped split for a script; any skip still exits non-zero
    qctl data-review approve --group 11111111-2222-4333-8444-555555555555 --yes -o json

    # take back your own whole-row decision
    qctl data-review withdraw 0190bbbb-1111-4222-8333-444444444444

    # accept one cell, named by field; the field is looked up in the review's changes
    qctl data-review accept-cell 0190bbbb-1111-4222-8333-444444444444 --field amount

    # accept a cell by the [h: <id>] from describe data-review, skipping the lookup
    qctl data-review accept-cell 0190bbbb-1111-4222-8333-444444444444 --history-id 811 --comment "matches the invoice"

    # clear a cell's verdict, leaving it undecided
    qctl data-review clear-cell 0190bbbb-1111-4222-8333-444444444444 --field amount --comment "fixed upstream"

    # take back your own cell decision, without changing a whole-row decision
    qctl data-review withdraw-cell 0190bbbb-1111-4222-8333-444444444444 --field amount

    # withdraw by history id, skipping the lookup, and keep the affected statuses as JSON
    qctl data-review withdraw-cell 0190bbbb-1111-4222-8333-444444444444 --history-id 811 -o json

    # team and personal progress across the whole table
    qctl data-review progress --table orders

    # one request's row counts, approver (you) and sender states, plus table-wide sign-off
    qctl data-review progress --table-id 42 --group 11111111-2222-4333-8444-555555555555

    # the same counts as JSON, including request_state_counts.approver and .sender
    qctl data-review progress --table-id 42 --group 11111111-2222-4333-8444-555555555555 -o json

    # inspect this request's rows still needing your approval
    qctl get data 42 --is-bad-data --review-group-id 11111111-2222-4333-8444-555555555555 --request-state to_review

    # preserve both progress responses under their wire keys
    qctl data-review progress --table orders -o json

    # submit two quarantined rows as one submission and ask a reviewer
    qctl data-review request --table orders --row-id 1001,1002 --title "Fix Q3 amounts" --reviewer ada@example.com

    # ask one more reviewer; anyone already assigned is skipped rather than notified again
    qctl data-review assign 0190bbbb-1111-4222-8333-444444444444 --reviewer grace@example.com

    # remove a reviewer; the decisions they already made still count
    qctl data-review unassign 0190bbbb-1111-4222-8333-444444444444 --reviewer grace@example.com

    # notify every assignee who has not decided yet, again
    qctl data-review re-request 0190bbbb-1111-4222-8333-444444444444

    # close (cancel) a review from a script; the row stays in quarantine
    qctl data-review close 0190bbbb-1111-4222-8333-444444444444 --yes

    # post to a review's discussion; the result names the comment, its thread and the table for comment-thread
    qctl data-review comment 0190bbbb-1111-4222-8333-444444444444 --body "amount looks off by 10x"

    # a longer note from stdin, notifying a reviewer on a trailing cc line
    qctl data-review comment 0190bbbb-1111-4222-8333-444444444444 --body-file - --mention ada@example.com

    # reply under a comment, by the [c: <id>] tag describe data-review prints
    qctl data-review comment 0190bbbb-1111-4222-8333-444444444444 --reply-to 0190cccc-1111-4222-8333-444444444444 --body "fixed upstream"

## GOTCHAS

- `close` refuses another reviewer's standing whole-row reject with `REVIEW_HAS_STANDING_REJECT` (409); `context.data_review_ids` names the blocking reviews. The rejecting reviewer must approve or withdraw their reject before retrying. [backend]

- `close` maps `REVIEW_HAS_STANDING_REJECT` (409) to exit 2, lists the affected review IDs with `qctl describe data-review <id>` hints, and asks each rejecting reviewer to run `qctl data-review approve <review-id>` or `qctl data-review withdraw <review-id>` before retrying.

- `request` uses the batch submission endpoint and needs `data_review:request`, which every role but `read_only` holds, and access to the table or assigned sources. External callers can submit their own source rows with `--table-id` and full reviewer UUIDs, and a reviewer who is not an active organization member refuses their request with 403; for other callers the server drops such a reviewer without an error. This does not grant access to review detail, group listings, progress or approval commands. [backend]

- Name and email resolution for `--reviewer` and `--mention` reads all pages of the user directory, including users beyond the first 200.

- on a money or percent column a change's original and edited values are both stored text, never the display format: setting a converted percent cell shown as `12.50%` to `13` reads `0.125 → 0.13`, because the edit is recorded in the column's stored units — the `$`, grouping commas and `%` dropped and a percent divided by 100 — and the original is the text the cell held. A cell whose source text never converted still holds that text, so its original can read `12.5%` or `150` beside a stored `0.13`. `-o json` and `-o yaml` on `describe data-review` and `get data-review-changes` tag each side of a change, `old_value_type` and `value_type`: `decimal` marks a stored number and those unconverted originals are `str` — but `str` does not prove source text, because an edit recorded before the server began tagging stored numbers can be `str` too. [backend]
- the `get` commands, `data-review progress` and `data-review request` require exactly one table selector: `--table` is a name and `--table-id` is an ID; passing neither or both is a usage error (exit 2). `describe data-review` takes none, because the review names its table.
- `get data-reviews` shows `-` in the id column for nearly every row of `--state accepted`: accepted means the release gate passes, which normally means the review is already `approved`, and the by-rows overlay returns only `open` and `changes_requested` reviews. [backend]
- a row holds at most one non-closed review, so `get data-reviews` shows at most one id per row, and that id is the `<review-id>` every data-review verb that takes one expects. [backend]
- `get data-reviews` is scoped to one table (there is no organization-wide review inbox). Its data page and by-rows overlay both need `source:data:read` and access to the table or assigned sources; external callers see only rows from those sources. A refusal names the endpoint and exits 4. [backend]
- the `undecided` column of `get data-reviews` counts `comment_only` changes too: only `approve`, `reject` and `request_changes` decide a change, and the release gate holds a row while any other remains. [backend]
- `get data-reviews` adds a `cannot_approve` table column: `self` marks the rows whose `sent_or_changed_by_me` entry was true on the data page, including rows with no active review overlay; `-` makes no eligibility claim. Approval permission and reviewer designation are separate checks. The flag is aligned before sorting by row id and costs no extra request. JSON/YAML keep the review-overlay List contract; use `get data --is-bad-data -o json` for the raw flag. `describe data-review` does not fetch this data-page sidecar.
- the `approvals` column of `get data-reviews` is the release gate's own count, `<current>/<required>`: distinct approvals of the row's current version by people who neither edited the row nor submitted it for review, the same meter `describe data-review` reads for an active review, and a row that has reached its requirement is still held while `held` names anything but `releasable`. [backend]
- `get data-reviews` lists rows in ascending row id across pages (qctl sends `order_by=id`), so page 1 holds the lowest ids; `--page` and `--limit` default to 1 and 20, as on `get data`.
- the `requester` column of `get data-reviews` is whoever first asked for the row's review, even after a later request re-submitted it; the opt-in `requests` column lists, oldest first, every request the review is in, and that id is the `<group-id>` `qctl data-review-group assign|re-request|cancel` takes to act on a whole request rather than one row. [backend]
- `-o json` and `-o yaml` on `get data-reviews` emit a List whose items are the by-rows overlays, from which rows without an active review are absent, and whose `total` counts the bucket's rows rather than reviews; `-o name` likewise lists only rows that have one.
- `get data-review-changes` defaults to `--state unresolved`, which is the changes carrying no decision plus the `comment_only` ones; `request_changes` is a blocking verdict and sits in `--state decided`, and `--state undecided` is the no-decision subset alone. [backend]
- the changes footer says "up to" because the count is taken before live-row refinement removes superseded changes; a positive count can therefore accompany an empty list. [backend]
- `--limit` on `get data-review-changes` is 1-20; qctl refuses a larger value instead of accepting the backend's silent clamp.
- `get data-review-changes --group` restricts changes and their affected-row counts to the submission's snapshot; re-submitting a row does not inflate its reach or add later edits to the earlier request. [backend]
- changes rank by live affected-row count descending, then history id descending, before pagination; superseded changes are removed before `--offset` is applied. [backend]
- `get data-review-changes --offset` skips a non-negative number of ranked changes, defaults to 0, and works with or without `--group`; invalid group UUIDs and negative offsets exit 2 before any request. A full page whose upper-bound count suggests more prints the next offset on stderr with "may be available"; short and empty pages print no continuation hint.
- `-o json` and `-o yaml` on `get data-review-changes` emit a List of the changes with a null `total` and no footer, so neither carries the count; `-o name` is rejected because a cell change has no resource name.
- `get data-review-changes` prints `yes` under FLAGGED when a live row the change affects carries an active (not retracted, not detached) user-submitted alert or warning on any of its cells, not only the changed one; with `--group` only that submission's rows count. Alerts raised by rules never set it, and `-o json` and `-o yaml` carry it as `has_open_flag`. [backend]
- `data-review progress --group` scopes the `rows:` line and adds the submission's `approver (you):` and `sender:` request-state counts; sign-off remains table-wide because the signoff-progress endpoint has no submission-group parameter. [backend]
- progress reads cross separate permission boundaries: row counts and request-state counts need dataset-data read permission and sign-off needs data-review list permission, and all need access to the table; a refusal names the endpoint and exits 4. [backend]
- request-state counts cover live quarantine rows with a non-closed review in the group; an unknown group returns zero counts. Approver states are `to_review`, `flagged_by_me`, `approved`, `on_hold`; sender states are `waiting`, `flagged`, `approved`, `on_hold`. Holds outrank active flags, which outrank approvals, which outrank pending rows; `flagged_by_me` is your active flag and sender `flagged` is an active flag created after this submission. [backend]
- request-state `approved` differs by view: the sender requires the review's `required_approvals`, snapshotted from policy `min_reviewers` on submission; the approver also counts a row they have given an eligible, current approval, even below that threshold. With two required, somebody else's first approval leaves an eligible caller in `to_review`. Submitters and row editors cannot supply a counted approval and are excluded from `to_review`, so the two views need not total the same number of rows. These states describe request progress, not the row's release verdict. [backend]
- `data-review progress -o json|yaml` prints `count_by_state` and `signoff_progress`, plus `request_state_counts` only with `--group`; its `approver` and `sender` objects keep the wire state names. Without `--group` the command reads only row counts and sign-off.
- `get data-review-changes` table output resolves user UUIDs through the user directory; if that lookup fails, qctl warns once on stderr and prints the UUIDs instead.
- the unit tags are columns of their own on `get data-review-changes`, off by default and named for that table's own vocabulary: `--columns history_id,field,before,before_type,after,after_type`. `describe data-review` has no columns, so it marks a side tagged `decimal` inline instead — `before: "150" → after: "0.13" [decimal]`, with a one-line legend above the changes — and marks nothing else, because `str` is what a cell change tags the other side whether it holds text or a number. Neither view reformats a value: both print the text the server recorded.
- `describe data-review` shows an empty `CHANGES (0)` section, not a refusal, when you lack access to the table's data: the detail endpoint answers 200 with `changes: []` for such a caller and leaves out the release reason and the edited-since data too, so an empty section can mean no live edits or no access. [backend]
- the COMMENTS section of `describe data-review` is read through the table's comment-thread route, which needs comment read permission and access to the table; when that read fails the section shows the review's comment count and thread id instead and the command still exits 0. A review has no thread until its first comment. [backend]
- the `Approvals` line of `describe data-review` reads the backend's current eligible approval count from the by-rows overlay for the same review and row; it keeps every recorded decision visible, including approvals the backend excludes. The overlay serves only `open` and `changes_requested` reviews, so an `approved` or `closed` review shows `unavailable`; so does an absent or incomplete overlay, and a failed overlay read also warns on stderr without failing the detail command. [backend]
- the time on a `describe data-review` decision is when that reviewer FIRST decided: a reviewer holds one decision per review, changed in place without moving its timestamp, and withdrawing deletes it. [backend]
- `describe data-review` marks a cell "edited since your approval" only while your standing decision is approve: the backend measures `changes_since=me` from the latest approval still on your decision record and, with none (you never approved, or you withdrew), returns every edit the row ever had. [backend]
- `describe data-review` fetches the user directory only when the review names someone it does not embed, such as a decider who is not an assigned reviewer; the editor role can read reviews but not the user directory, so an editor sees those people as UUIDs after one warning on stderr. [backend]
- `describe data-review` asks who you are only when the review has at least one decision; if that lookup fails, qctl warns once on stderr and leaves out the `(you)` marks, the edited-since notes and the withdraw suggestion.
- `-o json` and `-o yaml` on `describe data-review` emit only the review with its changes, from a single request: the decisions, the thread and the lines about you are not in it, and `-o name` is rejected (exit 2).
- `-o json` and `-o yaml` on `describe data-review` and `get data-review-changes` re-encode the response from qctl's generated API types, under the wire names, rather than pass the server's bytes through — so a field newer than qctl's client is dropped, and a null field the types mark `omitempty` is left out.
- `approve` and `withdraw` take either review ids (at most 500 distinct, no prompt) or `--group <uuid>`, never both; `--group` asks for confirmation without a row count, because a submission id does not say which table to count in, and needs `--yes` when stdin is not a terminal.
- `--group` selects only the submission's `open` and `changes_requested` reviews, while an id list does not filter by status, so a closed id (and, for `approve`, an approved one) is skipped with `REVIEW_NOT_OPEN`. [backend]
- a skipped review is left untouched while the rest of the call still commits, and a review on a table you cannot access is skipped as `NOT_FOUND`, exactly like an id that does not exist. [backend]
- a bulk decision that skips any review exits non-zero although the server answered 200: when nothing was processed and every skip has the same code, the exit is 3 for `NOT_FOUND`, 4 for `VIEW_ONLY` (a table you can read but not edit) and `APPROVER_NOT_DESIGNATED`, and 2 for `REVIEW_NOT_OPEN`; any other mix exits 1.
- approving a row you submitted, in any of its submissions, or ever edited (superseded and undone edits included) is processed but never counts: it fills none of the row's cells and is left out of the result line's `approvals:` count. [backend]
- a whole-row `approve` by anyone who neither submitted nor edited the row also marks every cell of the row without a blocking verdict as approved, `comment_only` cells included, and a cell decision is shared by every row carrying the same change. [backend]
- an `approve` never overrides a veto: a standing whole-row reject or a blocking cell verdict keeps the review `changes_requested`, and qctl prints a stderr warning for each such processed review. [backend]
- when a review's policy designates its approvers, `approve` by anyone else is skipped with `APPROVER_NOT_DESIGNATED`; `withdraw` is not gated that way. [backend]
- `withdraw` deletes only your own whole-row decision, never the cell approvals your `approve` recorded, and never puts a released row back in quarantine; if what remains has no veto, no undecided cell and enough approvals, the review becomes `approved`, and the row is released when the table's policy has `auto_release_on_approval` on. [backend]
- a review that reaches `approved` releases its row only when the table's policy has `auto_release_on_approval` on and the row passes the release gate — except through `qctl rows release` on a table that requires approval — its data-review policy requires review or its `quarantine_rows_until_approved` is on — which releases every row it approves that passes the gate, whatever that setting says — and then it writes `release` over the row's hold decision — a person's `hold` included — so the row leaves quarantine as soon as it has no issues. An edit to that row while it is still in quarantine (`qctl rows set-cell`, `qctl rows search-replace`) takes the release back: the row is undecided again — waiting for approval while it keeps an unclosed submitted review, otherwise held for approval wherever a hold reason applies. [backend]
- `withdraw` on a review where you have no whole-row decision is skipped with `REVIEW_NOT_OPEN`, the backend's catch-all code, so read the reason rather than the code. [backend]
- `approve` and `withdraw` both need the data-review approve permission, which the editor and agent roles do not hold; the refusal exits 4. [backend]
- `-o json` and `-o yaml` on `approve` and `withdraw` emit the raw `{processed, skipped}` result without the stderr warnings, and `-o name` is rejected (exit 2).
- `accept-cell`, `clear-cell` and `withdraw-cell` take exactly one of `--field` (an exact, case-sensitive field name, looked up with one extra request) or `--history-id` (the `[h: <id>]` that `describe data-review` prints); an unknown field exits 2 and lists the fields the review changed.
- `--field` fails with exit 2 and suggests `--history-id` when the review shows no changes at all, which is ambiguous: the backend returns an empty change list both when the row has no live edits and when you lack access to the table's data. [backend]
- a cell can still carry a `reject` or `request_changes` verdict recorded before the backend stopped accepting either on a cell, and `describe data-review` prints it as the cell's verdict: on a row held in quarantine it keeps the review `changes_requested` until the cell is accepted or cleared, its decider withdraws it with `withdraw-cell`, or a later edit to that field replaces the change. [backend]
- `clear-cell` writes `comment_only` over the cell's verdict, which lifts a stored `reject` or `request_changes` but is not an approval: the cell counts as undecided, so the row stays held, with status `open` once no other veto stands, until the cell is accepted or a reviewer who neither submitted nor edited the row approves the whole row. [backend]
- `accept-cell` needs the data-review approve permission, and so do `clear-cell` and `withdraw-cell` on a cell carrying a stored `reject` or `request_changes`, so the agent role can neither accept a cell nor lift a veto; all three verbs also need data-review edit permission, which the editor role lacks, and access to the table's data. `clear-cell` can replace another reviewer's verdict, while `withdraw-cell` can delete only your own. [backend]
- the cell verbs' permission 403 carries no error code, so qctl preserves the server's cause and explains it by verb — the approve permission for `accept-cell`; for `clear-cell` and `withdraw-cell`, the approve permission a stored veto needs and the edit permission every call needs; for `withdraw-cell`, ownership too — and exits 4. A change review 403 carries a code and gets no such explanation.
- under `-o json` and `-o yaml` all three cell verbs print the server's `{ledger_id, decision, affected_data_review_ids, affected_review_statuses, affected_row_ids, released_row_ids}` and move the result line to stderr; `-o name` exits 2.
- `withdraw-cell` deletes your current decision on a shared change, including its comment and suggestion; it leaves whole-row decisions alone. Another reviewer's decision is refused with 403 (exit 4), and no standing decision with 422 (exit 2). It takes no `--comment`: the backend ignores `comment` and `suggested_value` on withdrawal. The result's `ledger_id` identifies the deleted decision; the audit keeps that id and its prior verdict. A released row is never put back in quarantine. [backend]
- a cell verdict is stored once per change, not once per review: every row still carrying that edit shares it, so one verdict can move and release other reviews' rows, and the `affected:` counts on the result line report every review and row it recomputed. [backend]
- a `suggests:` value under a change in `describe data-review` is a stored suggestion and only advice: nothing applies it, and the requester makes the edit with `qctl rows set-cell`. [backend]
- `accept-cell` and `clear-cell` overwrite the cell's previous comment and erase its stored suggestion: qctl always sends both, the comment empty when `--comment` is omitted and the suggestion always empty, because the backend keeps an old note whenever the new one is missing while recording the verdict under your name. [backend]
- the result line leaves out `status:` and says why on stderr when your review was not recomputed: the backend records or withdraws the cell decision on a closed review, or on one whose row is not currently held in quarantine (released, deleted, or submitted from clean data), without recomputing that review. [backend]
- `request` reuses a row's review whenever it is not closed — `open`, `changes_requested` or even `approved` — instead of opening a second one: the same id joins the new submission with its status recomputed, and qctl marks it `(re-submitted)`; only a row whose review was closed gets a new review. [backend]
- every `request` re-reads the table's effective policy, so a re-submitted review's required approvals and designated approvers are replaced with the policy's current values. [backend]
- `request` does not check that a row id exists or is in quarantine: the backend opens a review for any id it is given. [backend]
- `request` refuses a repeated `--row-id` (exit 2) instead of dropping it; 1 to 500 distinct positive ids, a non-blank `--title` and every `--reviewer` are all checked before anything is submitted.
- `assign` and `request --reviewer` ask people for a review but grant no approval authority, and neither is limited to the designated approvers: when the review's policy designates approvers, only they can `approve` the whole row, assigned or not, while the cell verbs are not gated by designation. [backend]
- `assign` asks the server to add active organization members to the reviewer list; the server drops anyone else without an error and re-sends notifications to each accepted reviewer except the requester, already assigned or not, so qctl reads the review first and leaves out anyone already assigned (named on stderr); when nobody is left it sends nothing and exits 0. [backend]
- `assign` and `request --reviewer` compare the reviewers sent with each returned review and name anyone missing on stderr as `not assigned (not an active member of this organization): <names>`; the command still exits 0. Names use the directory already read to resolve `--reviewer`, falling back to UUIDs without an extra lookup. Warnings stay on stderr under every output format, and a batch warning names the affected review.
- `unassign` removes no decisions: the reviewer's cell verdicts and whole-row approve or reject keep counting toward the row's status, and nothing stops you removing the last reviewer. [backend]
- `describe data-review` lists the cell verdicts, superseded ones included, on the changes the row was submitted with, by anyone who is neither assigned nor the requester, not only by people removed with `unassign`; the `retained:` line that `unassign` prints on stderr counts only the verdicts of the reviewers that call named. [backend]
- `re-request` changes nothing about the review: it only notifies again, by default every assignee without a whole-row decision, and with `--reviewer` the named assignees whether or not they have decided. [backend]
- `re-request` silently skips a named reviewer who is not assigned and never notifies the requester, so it can notify nobody; that prints a "nobody" line and still exits 0. [backend]
- `assign`, `unassign`, `re-request` and `close` work only on `open` and `changes_requested` reviews and refuse an `approved` one like a closed one (exit 2); `close`'s refusal then says "already closed" even for an approved review. [backend]
- `close` is final and releases nothing: the row stays in quarantine, and a later `request` for it opens a new review without the old one's comments or whole-row decisions, while cell verdicts on changes the row still carries apply again. [backend]
- anyone with data-review edit permission and access to the review's table can close any review on it, not only their own submission; the editor role lacks that permission, so it can `request` but cannot `assign`, `unassign`, `re-request` or `close`. [backend]
- `close` asks for confirmation; `--yes` skips it, and without `--yes` a non-interactive run exits 2 before any request. A protected context also needs `--context` naming it, whatever `--yes` says.
- `assign`, `unassign`, `re-request` and `close` print a result line per review then `Next:` commands on the table view; `-o json` and `-o yaml` print the review instead — for `assign` with nobody left to assign, the one it read — or, for `re-request`, the server's `{data_review_id, rerequested_reviewer_ids, status}`, with the lines on stderr, and `-o name` prints `data-review/<uuid>`. `comment` prints the created comment under `-o json` and `-o yaml`, whose `thread_id` is what `comment-thread reply` takes, and exits 2 on `-o name`. `request` prints the same lines on the table view; `-o json` and `-o yaml` print `{group, reviews}` instead — the new submission group, null when the response names none, and every row's review — with the lines on stderr, and `-o name` prints `data-review/<uuid>` for each row.
- `comment` is the only comment verb in this group: listing, editing, deleting and resolving a review's comments go through `qctl describe data-review` and `qctl comment-thread` on the review's thread with `--table-id <its table>`, which is why `comment` prints the thread id and the `comment-thread` reply and edit/delete recipes. [backend]
- a review has no discussion thread until its first comment, so before that there is no thread id to hand to `qctl comment-thread`; the first `comment` creates the thread, and so does the first `approve` given a `--comment`. [backend]
- an `approve` `--comment` is posted into the review's thread and counted in its comment count like any other comment, while a cell verb's `--comment` is not: it is stored on the cell's verdict and shown with that cell. [backend]
- replies nest to depth 3: a deeper `--reply-to` is refused with `COMMENT_DEPTH_EXCEEDED`, and one naming a deleted comment, a comment of another thread or no comment at all is refused with no error code; both exit 2. [backend]
- a bare `@alice` in a comment notifies nobody: only a structured `[@label](mention:<uuid>)` link does, which is the form `--mention` appends on a trailing `cc` line, and, for an author who is not `external`, the backend silently drops a mentioned UUID that is not an active member of the organization; an `external` author's comment is refused with 422 (exit 2) when it mentions anyone the table's mentionable people do not include. The backend never notifies you of your own mention. [backend]
- `--reviewer` and `--mention` take an email, a "First Last" name or a full UUID, and only the first two read the org user directory: that read needs `org:users:list`, which the `editor` and `external` roles lack although both may request a review and comment. When it is refused, `comment --mention` alone resolves against the review's table's mentionable users — the active non-`external` members who flagged rows of the table or commented in its threads — and only when the review itself was readable, which it is not to the `external` role; a name outside that list exits 2. A call naming everyone by full UUID reads no directory at all; a name or an email with no readable list exits 4 and says so. [backend]
- `comment` uses the review-comment endpoint and needs comment write permission and access to the review's table or, for external callers, its row's assigned source. The `read_only` role may comment too. No data-review permission is required, and the backend accepts comments on reviews of any status, including `approved` and `closed`. [backend]
- `comment` reads the review first only to print its table id: when that read is refused, the comment is still posted, with a warning on stderr and `<table-id>` in the `Next:` lines.
- `comment` adds `--reply-to` advice only when the server identifies that parent comment as missing; mention refusals and other validation failures retain their own diagnostic at exit 2.
- `comment` reads the directory again, best-effort, to label the `cc` line: a UUID `--mention` resolves without it, so when that read is refused the comment is still sent and the label falls back to the id it notifies, after one warning on stderr.
- `comment` has no `--dry-run-job`: the route accepts a linked dry-run job and silently discards it, because dry-run jobs belong to rule reviews. [backend]

- For callers other than ops/admin, a `read_only` table grant refuses `assign`, `unassign`, `re-request` and `close`, and the `data-review-group` edit verbs, even when the organization role holds data-review edit permission. Ask a table owner for an editor or owner grant; the role's permissions still apply. [backend]
- a caller who sees the table's PII masked — no `pii:view` and no active PII access grant on the table — gets a PII column's before, after and suggested values masked: `describe data-review` and `get data-review-changes` print `•••••`, or `•••••` and the value's last 4 characters when it has 9 or more, as sent, and `-o json|yaml` name the column in `masked_fields`, on the review and on each ranked change. `describe data-review` names the masked columns once under CHANGES and marks none of their sides `[decimal]`; two different values can print the same mask. [backend]
- the review table's change review policy governs `data-review accept-cell`, `clear-cell` and `withdraw-cell` (operation `data_review_decide`, keyed by the decision), `data-review approve` and `withdraw` (`data_review_approve` / `data_review_withdraw`, checked for each table of the batch before any review is decided, so one refusing table refuses the whole batch) and `data-review close` (`data_review_close`). A refusal is a 403 `CHANGE_REVIEW_POLICY_DENIED` (exit 4) whose `Hint:` names the operation and the refused keys and points at `qctl get change-review-policy --table <table>`; no key of these operations can be proposed, so `CHANGE_PROPOSAL_REQUIRED` never answers them. `request`, `comment`, `assign`, `unassign` and `re-request` are not subject to the policy. `data_review_approve` also governs the `duplicate-group` decisions, `approve`, `disagree`, `dismiss`, `withdraw`, `stop-series`, `allow-all`, `reapply` and `share`. [backend]

## SEE ALSO

- `qctl explain data-review-group`
- `qctl explain data`
- `qctl explain data-review-policy`
- `qctl explain comment-thread`
- `qctl explain rule-review`
- `qctl guide review-data`
