---
summary: the review workflow for a submitted rule revision
aliases:
kind:
schema:
guide: request-review
signature: 5510dbe
---

## USAGE

    qctl describe rule-review <review-id>
    qctl get rule-reviews
    qctl review approve <review-id>
    qctl review assign <review-id> --reviewer <reviewer>
    qctl review audit <review-id>
    qctl review close <review-id>
    qctl review comment <review-id>
    qctl review delete-comment <review-id> <comment-id>
    qctl review edit-comment <review-id> <comment-id>
    qctl review link-dry-run <review-id> <dry-run-job-id>
    qctl review re-request <review-id>
    qctl review reject <review-id>
    qctl review reopen <review-id>
    qctl review request <rule>
    qctl review request-changes <review-id>
    qctl review unassign <review-id> --reviewer <reviewer>
    qctl review withdraw <review-id>

## EXAMPLES

    # the reviews you are involved in
    qctl get rule-reviews

    # only the open reviews for one rule
    qctl get rule-reviews --rule dedupe-emails --status open

    # one rule's reviews, scoped to a table (--table is a name, --table-id an ID)
    qctl get rule-reviews --rule dedupe-emails --table-id 42

    # full detail for one review
    qctl describe rule-review 018f3c2e-4d5a-7b89-a1c2-3d4e5f607182

    # 1. ask for a review of a rule, scoped to a table by name
    qctl review request dedupe-emails --table orders --reviewer alice@corp.com

    # the same request scoped by table ID; omit both for an org-wide review
    qctl review request dedupe-emails --table-id 42

    # 2. discuss it on the review thread
    qctl review comment 018f3c2e-4d5a-7b89-a1c2-3d4e5f607182 --body "LGTM"

    # 3. approve it
    qctl review approve 018f3c2e-4d5a-7b89-a1c2-3d4e5f607182 --comment "ship it"

## GOTCHAS

- Name and email resolution for `--mention` reads all pages of the user directory, including users beyond the first 200.

- fifteen `qctl review *` verbs act on a review — `qctl review --help` lists them all.
- review and comment ids must be full UUIDs; a malformed id exits 2.
- The dry-run success gate line in `describe rule-review` names rule load errors, rule-bug and sandbox-failure findings; follow its `qctl inspect dry-run-job <id>` command to verify them.
- this is the review of a rule revision; the review of a quarantined row is the `data-review` resource, driven by `qctl data-review` and never by `qctl review` (`qctl guide review-data`).
- a decision is upserted per reviewer only **while the review is still open or in changes-requested**: resubmit `approve`/`reject`/`request-changes` then and it replaces your previous verdict rather than adding a second one, but once a decision completes the review the server refuses any further decision (422) — the likeliest case, changing your mind after your approval closed it, is `qctl review withdraw <review-id>`, not deciding again. [backend]
- approval does not necessarily enable the rule — qctl cannot tell from the decision response whether auto-enable fired, so it prints the manual path (`qctl enable rule <name> --release <release>`) after every completed approval regardless; check `qctl get rule <name>` to see whether it actually enabled. [backend]
- `close` is final: `reopen` takes only a `rejected` review, so a closed one cannot be reopened; request a new review instead. [backend]
- `close` does not ask for confirmation, but on a protected context it needs `--context` naming the context, and a non-interactive run without it exits 2 before any request.
- `get rule-reviews` is your inbox by default; `--rule <name>` narrows it to one rule's reviews instead.
- on `get rule-reviews` and `review request` alike, `--table` is a name and `--table-id` is an ID; neither accepts the other. A table whose name is a number (`42`) is reachable with `--table 42`, which older builds resolved as ID 42. Passing both is an error, not a precedence rule.
- `review request` prints the new review under `-o json` and `-o yaml`, as linked when `--dry-run-job` was given, and `rule-review/<uuid>` under `-o name`; when the link fails the review is still printed before the error. `approve`, `reject` and `request-changes` print the server's `{decision, review_completed, review_status}`, `withdraw`, `close`, `reopen`, `assign` and `unassign` the review, and `re-request` the server's `{review_id, rerequested_reviewer_ids, status}`, each with `rule-review/<uuid>` under `-o name`; `comment` and `edit-comment` print the comment and exit 2 on `-o name`. Each moves its result lines to stderr under those formats. `review audit` takes `-o json` and `-o yaml` but not `-o name`, and `delete-comment` and `link-dry-run` print prose only and exit 2 on `-o json`, `-o yaml` or `-o name`.
- the inbox means reviews where you're author, an assigned reviewer, or mentioned, and needs `REVIEW_EDIT`; the `--rule` listing needs only `REVIEW_LIST`. [backend]
- `review comment --mention` resolves an email or a "First Last" name through the org user directory, which needs `org:users:list`; commenting itself needs `REVIEW_LIST` and `COMMENT_WRITE`, which the `editor` role holds without directory access, so a mention given as a full UUID is the form that works there. [backend]

## SEE ALSO

- `qctl explain comment-thread`
- `qctl explain data-review`
- `qctl explain rule`
- `qctl guide request-review`
- `qctl guide review-rules`
