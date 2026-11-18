# Request and drive a review for a submitted rule

> **Which review guide?** Quarantined rows: `qctl guide review-data`. A rule
> revision, as its author: `qctl guide request-review`; as a reviewer:
> `qctl guide review-rules`. Whether rows need review at all: `qctl guide
> data-review-policy`; whether rules do: `qctl explain rule-review-policy`.

    qctl get rule-review-policy --table orders
    qctl submit rules -f rule.py --yes
    qctl create dry-run-job -f dry-run.yaml
    qctl review request dedupe-emails --release 2.1.0 --table orders --dry-run-job 512
    qctl describe rule-review <review-id>
    qctl enable rule dedupe-emails --release 2.1.0

The author's loop: check the policy, submit, attach a dry run, request the
review, respond until it is `approved`, then enable the revision (or let
`auto_enable_on_approval` do it). The reviewer's side is `qctl guide
review-rules`; an agent cannot approve or reject. Two rules trip agents up:

1. **Reviews bind to a revision, never a family.** A new release is a new
   revision and needs a new review; approvals never carry over.
2. **One active review per (revision, table-scope) slot.** Requesting a
   second one is a 409 — continue in the existing review instead (the error
   prints its id).

Review ids are full UUIDs. Copy them verbatim from command output.

## 1. Check the policy

```bash
qctl get rule-review-policy                  # organization-level
qctl get rule-review-policy --table orders   # effective policy for one table
```

- `requires_review` — submissions land in `draft` and must pass review before
  enable.
- `min_reviewers` — approvals needed; snapshotted into each review at
  creation.
- `require_dry_run` — a dry run must be linked to the review before enable;
  `require_dry_run_success` — and it must have passed: `finished`, with no
  rule load errors and no rule-bug or sandbox failure.
- `auto_enable_on_approval` — final approval enables the revision
  automatically.
- `dismiss_stale_reviews` — resubmitting code deletes existing approvals.
- `designated_reviewers` — when set, only these users can be assigned and
  only their approvals count.
- `source` — which level resolved the policy: system default, organization,
  or table override.

## 2. Submit the rule

```bash
qctl submit rules -f rule.py --yes
```

When the policy requires review, the new revision lands in `draft` and the
output ends with a breadcrumb:

```
NAME           VERSION
dedupe-emails  2.1.0
rule/dedupe-emails@2.1.0 submitted (state: draft — review required before enable)
Next: qctl review request dedupe-emails --release 2.1.0
New to the review flow? Run: qctl guide request-review
```

The table shows the rule's slug and the `rule/` and `Next:` lines its name;
this class sets both to `dedupe-emails`. Left unset, the name is the class name
and the slug derives from it (`DedupeEmails` becomes `dedupe-emails-rule`).
`qctl review request` takes either. Authoring: `qctl guide submit-rules`.

## 3. Attach evidence: run a dry run

Most policies require one (`require_dry_run` / `require_dry_run_success`).

```bash
qctl create dry-run-job -f dry-run.yaml
```

```
{
  "dry_run_job_id": 512
}
```

Writing the spec and reading the verdict: `qctl guide dry-run-jobs`. Then
inspect:

```bash
qctl inspect dry-run-job 512 --view compact   # summarized stats + exemplar rows, built for agents
qctl inspect dry-run-job 512 --follow         # stream progress; the preview only if it finished
```

## 4. Request the review

```bash
qctl review request dedupe-emails --release 2.1.0 --table orders \
  --reviewer alice@corp.com --dry-run-job 512
```

```
rule-review/018f3c2e-4d5a-7b89-a1c2-3d4e5f607182 created (status: open, approvals: 0/2)
rule dedupe-emails@2.1.0 is now in_review
rule-review/018f3c2e-4d5a-7b89-a1c2-3d4e5f607182 dry run linked: job 512
Next: qctl describe rule-review 018f3c2e-4d5a-7b89-a1c2-3d4e5f607182
```

`--table` takes a name and `--table-id` an ID; omit both for an org-wide
review. `--reviewer` (repeatable) takes an email, `"First Last"` or UUID, and
`--dry-run-job` links the evidence in the same step.

Already open? The 409 error code is `rule_review_already_open` and the
message names the existing review — continue there instead of retrying:

```
a review is already open for this revision and scope:
  rule-review/018f3c2e-4d5a-7b89-a1c2-3d4e5f607182 (status: open) "Review dedupe-emails@2.1.0"
Next: qctl describe rule-review 018f3c2e-4d5a-7b89-a1c2-3d4e5f607182  (or close it: qctl review close 018f3c2e-4d5a-7b89-a1c2-3d4e5f607182)
```

## 5. Monitor and respond

```bash
qctl get rule-reviews                                        # your inbox
qctl get rule-reviews --rule dedupe-emails --release 2.1.0   # this rule's reviews
qctl describe rule-review <review-id>                        # full detail + NEXT ACTIONS
```

`describe rule-review` is the workhorse: status, the approvals meter, the
effective policy, the dry-run gate line, decisions, the comment thread, and
copyable next commands.

When the review lands in `changes_requested`:

1. Read the thread — comments carry `[c: <uuid>]` tags for replying.
2. Fix the code **keeping the same release** and resubmit:

   ```bash
   qctl submit rules -f rule.py --yes
   ```

   The revision is updated **in place** and the open review stays attached
   to the new code — same thread, same reviewers. Expect the submit output
   to say "No new rules were added (rules may already exist)": for an
   in-place update that is success, not a failure. If the policy sets
   `dismiss_stale_reviews`, all existing approvals are deleted and an
   `approved` review reopens — reviewers must re-approve the new code.

3. Attach fresh evidence and reply on the thread:

   ```bash
   qctl review comment <review-id> --body "Addressed in the resubmission." --dry-run-job 513
   qctl review link-dry-run <review-id> 513    # update the headline run the enable gate checks
   ```

4. Nudge the reviewers whose decision is still awaited:

   ```bash
   qctl review re-request <review-id>
   ```

   ```
   rule-review/018f3c2e-4d5a-7b89-a1c2-3d4e5f607182 re-requested (status: open)
   Nudged: Alice Doe
   ```

## 6. Land it

On `approved`:

- With `auto_enable_on_approval` and a satisfied dry-run gate, the revision
  is already enabled. Verify:

  ```bash
  qctl get rule dedupe-emails      # STATE column shows enabled
  ```

- Otherwise enable manually (this re-checks the review + dry-run gate):

  ```bash
  qctl enable rule dedupe-emails --release 2.1.0
  ```

  Enabling prematurely fails with the backend's `REVIEW_REQUIRED` error —
  the revision still needs an approved review (and a passing dry run when
  the policy requires one). `qctl describe rule-review <review-id>` shows
  what is missing.

On `rejected`: fix under a **new** release (bump `release` in
`RuleMetadata`), submit, and request a fresh review — approvals never carry
over. The rejected review remains as history; reopen it only if the reject
itself was a mistake (`qctl review reopen <review-id>`).

To withdraw a revision from review entirely: apply a `kind: Rule` manifest
with `state: draft` (`qctl apply -f rule.yaml`), then close the review
(`qctl review close <review-id>`).

## The review state machine

A **revision** is one release of a rule family: immutable code, a UUID id and
a `release` string, in `draft → in_review → enabled → disabled`. A **review**
binds to one revision, optionally scoped to one table (omit = org-wide), and
holds one **decision** per reviewer; deciding again replaces it.

1. Created as `open`; the revision moves `draft → in_review`.
2. A standing request-changes decision holds it at `changes_requested`;
   withdrawing the last one returns it to `open`.
3. Approvals ≥ required (and no standing request-changes) → `approved`.
4. A single reject → `rejected` (recoverable only via `qctl review reopen`).
5. `close` → `closed` — terminal; a closed review is never reopened.

## Error recovery

| Error code | Meaning | Next command |
|---|---|---|
| `rule_review_already_open` (409) | an active review already holds this (revision, table) slot | `qctl describe rule-review <id-from-error>` and continue there |
| `REVIEWER_NOT_DESIGNATED` (422) | the policy designates reviewers; yours is not one | `qctl get rule-review-policy [--table t]` and pick from `designated_reviewers` |
| `review_not_active` (409) | nudging a review that is not open/changes_requested | check the status via `qctl describe rule-review <id>`; request a new review if needed |
| `REVIEW_REOPEN_BLOCKED` (422) | withdraw would un-approve a review whose rule is enabled | `qctl disable rule <name> --release <r>` first |
| `COMMENT_DEPTH_EXCEEDED` (422) | replies nest to depth 3 at most | reply to the parent comment one level up |

## Next steps

- The reviewer's side — threads, evidence, decisions: `qctl guide review-rules`
- Attach the enabled rule to a table: `qctl guide instantiate-rule`
