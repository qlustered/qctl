---
summary: data-review requests (submission batches of rows), assigned, re-requested or cancelled as a whole
aliases:
kind:
schema:
guide: review-data
signature: 294f996
---

## USAGE

    qctl data-review-group assign <group-id> --reviewer <reviewer>
    qctl data-review-group cancel <group-id>
    qctl data-review-group re-request <group-id>
    qctl get data-review-groups

## EXAMPLES

    # open submission groups for a table
    qctl get data-review-groups --table orders

    # include finished submissions: groups with no open or changes_requested review
    qctl get data-review-groups --table-id 42 --include-closed

    # continue through a long listing
    qctl get data-review-groups --table orders --limit 50 --offset 50

    # focus on each request's note and requested reviewers
    qctl get data-review-groups --table-id 42 --columns id,title,description,reviewers

    # consume the raw page envelope, including its total
    qctl get data-review-groups --table-id 42 -o json

    # print one stable resource name per group
    qctl get data-review-groups --table orders -o name

    # create a submission group: the first line of output names it
    qctl data-review request --table-id 42 --row-id 1001 --row-id 1002 --title "Email typos"

    # ask one more reviewer to look at every row of a request
    qctl data-review-group assign 11111111-2222-4333-8444-555555555555 --reviewer ada@example.com

    # remind the request's reviewers who have not decided yet
    qctl data-review-group re-request 11111111-2222-4333-8444-555555555555

    # cancel a request on a protected context, reading what was removed and closed
    qctl data-review-group cancel 11111111-2222-4333-8444-555555555555 --context prod --yes -o json

## GOTCHAS

- `cancel` refuses with `REVIEW_HAS_STANDING_REJECT` (409), changing nothing, when another reviewer has a standing whole-row reject on an active review it would close; a review shared with another request does not block cancellation. `context.data_review_ids` names the blocking reviews. [backend]

- `cancel` maps `REVIEW_HAS_STANDING_REJECT` (409) to exit 2, lists the affected review IDs with `qctl describe data-review <id>` hints, and reports that cancellation changed nothing; ask each rejecting reviewer to run `qctl data-review approve <review-id>` or `qctl data-review withdraw <review-id>` before retrying.

- the command requires exactly one table selector: `--table` is a name and `--table-id` is an ID; passing neither or both is a usage error (exit 2).
- the default listing omits a group once none of its member reviews is `open` or `changes_requested`, so a finished submission whose reviews are all `approved` is hidden; `--include-closed` includes those groups, but never one cancelled with `qctl data-review-group cancel`, which has no member reviews left to list. [backend]
- `rows` counts every member review and `open` counts active members, while `undecided`/`approved`/`rejected` count live cell changes on active members only; the cell-count sum is not expected to equal `rows`. [backend]
- pagination uses `--limit` 1-200 and a zero-based `--offset`; when more groups remain, qctl prints the next offset on stderr.
- `-o json` and `-o yaml` emit the raw `{groups,total}` page and skip the presentation-only user-directory request; `-o name` prints `data-review-group/<uuid>`.
- table output resolves the requestor through the user directory; if that lookup fails, qctl warns once on stderr and prints the UUID instead.
- `description` and `reviewers` are shown by default and can be selected with `--columns`; reviewers use the embedded user's name, email, or UUID, so a failed directory lookup does not hide their names.
- the note and reviewers belong to the submission: reviewers are those requested when it was created plus anyone added by `qctl data-review-group assign`; `qctl data-review assign` and `unassign` on one row's review do not change that list. [backend]
- `data-review-group assign` adds only active organization members, silently dropping anyone else, and notifies each reviewer new to the request once, never the requester; it also adds them to each open or `changes_requested` review of the request, and its response lists everyone assigned to the request afterwards, not only those added. [backend]
- `data-review-group re-request` notifies the request's own reviewer list, the one the `reviewers` column shows, minus the requester, anyone no longer an active organization member, and anyone with a whole-row decision on every open review of the request; a reviewer added to one row's review with `qctl data-review assign` is not on it. Notifying nobody is a success. [backend]
- `assign` and `re-request` refuse a request with no `open` or `changes_requested` review left with `REVIEW_NOT_OPEN` (exit 2); `cancel` has no such check. [backend]
- `data-review-group cancel` removes every review from the request, `approved` and `closed` ones included, and closes each one that was `open` or `changes_requested` and is in no other request; it approves and releases no row, notifies nobody, and deletes the request's assignment notifications and change snapshots. Cancelling again answers two empty lists and exits 0. [backend]
- `cancel` prompts before sending; `--yes` skips the prompt and a non-interactive run needs it, and a protected context also needs `--context` naming it, whatever `--yes` says.
- all three take the request's full UUID or `data-review-group/<uuid>`; a malformed id exits 2 before any request, and a request the organization does not have exits 3.
- a group is created only by `qctl data-review request`, and the backend returns no group id of its own: qctl takes the newest entry in the first review's `review_groups`, which lists every submission that review has been part of. [backend]
- `data-review-group cancel` is subject to the change review policy of the request's table (operation `data_review_close`, as `data-review close` is) and is refused before anything changes, with a 403 `CHANGE_REVIEW_POLICY_DENIED` (exit 4) whose `Hint:` names the operation and points at `qctl get change-review-policy --table <table>`. The key cannot be proposed, so `CHANGE_PROPOSAL_REQUIRED` never answers it. `assign` and `re-request` are not subject to the policy. [backend]

## SEE ALSO

- `qctl explain data-review`
- `qctl explain data`
- `qctl explain data-review-policy`
- `qctl guide review-data`
