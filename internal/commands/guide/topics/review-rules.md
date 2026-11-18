# Participate in rule reviews: threads, evidence, decisions

> **Which review guide?** Quarantined rows: `qctl guide review-data`. A rule
> revision, as its author: `qctl guide request-review`; as a reviewer:
> `qctl guide review-rules`. Whether rows need review at all: `qctl guide
> data-review-policy`; whether rules do: `qctl explain rule-review-policy`.

    qctl get rule-reviews --status open --reviewer-type reviewer
    qctl describe rule-review <review-id>
    qctl get rule dedupe-emails --release 2.1.0 -o code
    qctl review comment <review-id> --body "Recommending approval." --mention alice@corp.com
    qctl review re-request <review-id>

A reviewer finds the review, reads it and its evidence, and discusses it. Only
a human decides it: the `agent` role cannot approve or reject (403), so an
agent's review ends in a summary comment that mentions a human (section 5).
The author's side is `qctl guide request-review`.

## 1. Find work

```bash
qctl get rule-reviews                                # inbox: reviews where you are author, reviewer, or mentioned
qctl get rule-reviews --status open --reviewer-type reviewer
qctl get rule-reviews --all                          # everyone's reviews (admin/ops only)
```

The inbox requires the `REVIEW_EDIT` permission. Read-only roles list one
rule's reviews instead — that needs only `REVIEW_LIST`:

```bash
qctl get rule-reviews --rule dedupe-emails --release 2.1.0
```

The inbox is paginated (`--page`, `--limit`, max 100); a hint on stderr
tells you when more pages exist.

## 2. Understand a review

```bash
qctl describe rule-review 018f3c2e-4d5a-7b89-a1c2-3d4e5f607182
```

The default output is a self-describing text payload:

```
# RULE REVIEW 018f3c2e-4d5a-7b89-a1c2-3d4e5f607182
Title: Review dedupe-emails@2.1.0
Rule: dedupe-emails @ 2.1.0 (revision 550e8400-e29b-41d4-a716-446655440000)
Scope: table orders (42)
Status: open | Approvals: 1/2 | Requestor: Alice Doe
Reviewers: Bob Smith
Dry run: job 512 (finished) — gate: require_dry_run_success; job state satisfies it, success also needs no rule load errors, rule-bug or sandbox-failure findings — verify: qctl inspect dry-run-job 512

## POLICY (effective, resolved now — may differ from the creation-time snapshot)
requires_review: true | min_reviewers: 2 | require_dry_run: true
require_dry_run_success: true | auto_enable_on_approval: true | source: dataset

## DECISIONS (1)
- APPROVE by Bob Smith (2 hours ago): "LGTM"

## COMMENTS (1 top-level, 2 total)
[c: 7a1b2c3d-4e5f-6071-8293-a4b5c6d7e8f9] Alice Doe (admin) (3 hours ago):
  Please add a null check to apply().
  [c: 9c2d3e4f-5a6b-7c8d-9e0f-1a2b3c4d5e6f] ↳ Bob Smith (editor) (2 hours ago):
    Done in the resubmission.

## NEXT ACTIONS
- comment:            qctl review comment 018f3c2e-4d5a-7b89-a1c2-3d4e5f607182 --body "..."
- approve (human):    qctl review approve 018f3c2e-4d5a-7b89-a1c2-3d4e5f607182
...
```

How to read it:

- The `[c: <uuid>]` tags are comment ids — echo them back to `--reply-to`,
  `edit-comment`, and `delete-comment`.
- The `Dry run:` line reports the enable gate but never asserts success —
  verify with the `qctl inspect dry-run-job` command it prints.
- The POLICY block is resolved now; the review's approvals requirement was
  snapshotted at creation and may differ.
- `-o json|yaml` emits the raw API response instead.

## 3. Inspect the change

```bash
qctl get rule dedupe-emails --release 2.1.0 -o code        # the raw Python
qctl describe rule dedupe-emails --release 2.1.0 --show-code
qctl inspect dry-run-job 512 --view compact                # the linked evidence
```

## 4. Discuss

```bash
qctl review comment <review-id> --body "Consider trimming whitespace before comparing."
qctl review comment <review-id> --reply-to <comment-uuid> --body "Agreed, see the new run."
qctl review comment <review-id> --body-file notes.md       # or --body-file - for stdin
```

- Bodies are markdown, 1-10000 chars, stored verbatim. Commenting is open to
  every role regardless of review status.
- Threads nest to depth 3; a deeper reply is rejected
  (`COMMENT_DEPTH_EXCEEDED`) — reply to the parent one level up.
- **Mentions are structured links, not prose.** A bare `@name` in the body
  notifies nobody. Use `--mention alice@corp.com` (repeatable; email,
  `"First Last"`, or UUID) — each resolved user is appended on a trailing
  `cc` line and notified.
- Attach evidence: `--dry-run-job 512` embeds a run in the comment
  (display-only), or link one to an existing comment:
  `qctl review link-dry-run <review-id> 512 --comment <comment-uuid>`.
  Without `--comment`, `link-dry-run` replaces the review's **headline** run
  — the one the enable gate actually checks.
- Maintain your own comments:
  `qctl review edit-comment <review-id> <comment-id> --body "..."` (the
  thread flags it `(edited)`) and
  `qctl review delete-comment <review-id> <comment-id>` (leaf comments are
  removed; comments with replies become `[comment deleted]` tombstones;
  prompts unless `--yes`).

## 5. Decide — humans only

**The `agent` role cannot approve or reject.** Decisions require the
`REVIEW_APPROVE` permission, which excludes agents by backend design — this
is not a CLI limitation; an agent gets a 403. An AI agent's terminal action
is a summary comment plus a hand-off to a human:

```bash
qctl review comment <review-id> \
  --body "Reviewed: logic sound, dry run 512 clean. Recommending approval." \
  --mention alice@corp.com
qctl review re-request <review-id>     # nudge the assigned reviewers
```

The human renders the verdict (documented here for the human reader):

```bash
qctl review approve <review-id> --comment "LGTM"
qctl review request-changes <review-id> --comment "Handle null emails first"
qctl review reject <review-id> --comment "Wrong approach for this table"
```

```
rule-review/018f3c2e-4d5a-7b89-a1c2-3d4e5f607182 approved (status: approved, approvals: 2/2, review completed)
If auto-enable is off or the dry-run gate failed, enable manually: qctl enable rule dedupe-emails --release 2.1.0
Next: qctl describe rule-review 018f3c2e-4d5a-7b89-a1c2-3d4e5f607182
```

- `--comment` (max 4000 chars) is materialized as a real thread comment.
- Deciding again **replaces** your standing verdict while the review is
  `open` or `changes_requested` — that is also how you change your mind. Once
  a decision completes it, as above, another is refused (422); withdraw
  instead.
- One `reject` terminates the review. Approvals ≥ required (with no standing
  request-changes) complete it; the requestor's own approve never counts.
- Undo your standing decision with `qctl review withdraw <review-id>` — the
  approval meter is recomputed and the printed status comes from the backend
  (the review may reopen, stay approved, or close as superseded). Withdrawal
  is refused (`REVIEW_REOPEN_BLOCKED`) when it would un-approve a review
  whose rule is already enabled — disable the rule first.

## 6. Housekeeping

```bash
qctl review close <review-id>                        # a moot review; closed is terminal
qctl review reopen <review-id>                       # recover from a mistaken reject
qctl review assign <review-id> --reviewer bob@corp.com
qctl review unassign <review-id> --reviewer bob@corp.com
qctl review audit <review-id>                        # who did what, when (paginated)
```

- `close` works on active reviews only and cannot be undone — request a new
  review to restart the process.
- `reopen` deletes the reject decisions and recomputes the status. If
  another active review took the same (revision, table) slot in the
  meantime, the reopen lands back in `closed` ("superseded") — trust the
  printed status.
- Unassigning an approver retracts their approval and can reopen an
  approved review — unlike withdraw, even when its rule is already enabled,
  and the rule stays enabled. Assign works only on active reviews, unassign
  on approved ones too (`REVIEW_NOT_OPEN` otherwise), and
  designated-reviewer policies gate who may be assigned.

## Next steps

- The author's loop — submit, request, respond, land: `qctl guide request-review`
- Rows, not rules — the data-row review workflow: `qctl guide review-data`
