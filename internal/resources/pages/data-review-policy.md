---
summary: which rows are held in quarantine for human review
aliases:
kind: DataReviewPolicy
schema: manifest:data-review-policy
guide: data-review-policy
signature: e2acbe5
---

## USAGE

    qctl apply -f <file.yaml>   # kind: DataReviewPolicy
    qctl delete data-review-policy
    qctl get data-review-policy

## EXAMPLES

    # apply a data review policy manifest through the generic dispatcher; spec.dataset_id present = table override, absent = org policy
    qctl apply -f data-review-policy.yaml

    # remove one table's override by ID; the table falls back to the org policy, or the system default if there is none
    qctl delete data-review-policy --table-id 42

    # same, addressed by table name instead of ID
    qctl delete data-review-policy --table orders

    # the effective org-level policy (no table override to check)
    qctl get data-review-policy

    # the effective policy for one table: its own override, or the org policy if it has none
    qctl get data-review-policy --table orders

    # apply-compatible manifest, for editing and re-applying
    qctl get data-review-policy --table-id 42 -o yaml

## GOTCHAS

- `apply -f <file> -o name` prints `data-review-policy/<policy-uuid>` for the saved policy; `get` and `delete` still select policies by scope with `--table` or `--table-id`, rather than by policy UUID. Several applied documents print one reference per line in file order; prose goes to stderr.

- Integer fields in a YAML manifest reject fractional numbers during parsing (exit 2): `2.7` is refused instead of becoming `2`. Whole-valued YAML numbers such as `2.0` remain accepted.
- `--table` is a name and `--table-id` is an ID; neither accepts the other. A table whose name is a number (`42`) is reachable with `--table 42`, which older builds resolved as ID 42.
- the same table-selector behavior as `rule-review-policy` (optional on `get`, where omitting it means the org policy; required on `delete`, which shares the same flag resolver) and the same `-o yaml` apply-compatible manifest.
- and the same stricter-only rule: a table-level override is rejected 422 unless it is at least as strict as the org policy on every field, so an override can tighten quarantine review for one table but never relax it below the org setting. [backend]
- `delete data-review-policy` removes only the **table-level override**, never the organization policy — the table then inherits the org policy or, absent one, the system default (`requires_review: false`), so deleting an override can leave that table's rows unreviewed rather than reverting to something equally strict. [backend]
- `min_reviewers` counts distinct eligible approvals of the row's current version. Anyone who submitted the row in any of its submissions or ever edited it is excluded, including authors of superseded or undone edits and actors who undid or redid a row edit. [backend]
- this gates **rows** held in quarantine, not rules — `rule-review-policy` is the one that gates rule revisions. [backend]
- an effective `requires_review: true` holds every undecided quarantined row for approval — `qctl get data <t> --is-bad-data` reads `held-for-approval=review_policy` in its Row State, or `possible_duplicate` for a suspected duplicate, even on a row that still fails validation, and `waiting-for-approval` instead while a submitted data review of the row is not closed — and `qctl rows release` then needs the data-review approve permission and records your approval instead of releasing outright: it approves each row it names or matches that you neither submitted nor edited, skips the rest, and releases a row only once it has enough eligible approvals and no reject, cell veto or unfinished undo holds it, leaving the others undecided; a designated-approver list that leaves you out for any row it would approve — that row's unclosed review's list, or the policy's for a row with none — refuses the whole call (403 `APPROVER_NOT_DESIGNATED`). It holds only rows already in quarantine: a new row that passes at ingest still goes to the clean table. `qctl explain data` has the rest of the held-for-approval state. [backend]
- turning `requires_review` off, by `qctl apply -f` or `qctl delete data-review-policy`, re-queues the live undecided quarantined rows of each table it affects for validation, but only for a table whose `quarantine_rows_until_approved` is off too, and best-effort: the policy change commits first and a failed re-queue is only logged. An org policy change reaches every table of the organization. [backend]

## SEE ALSO

- `qctl explain data-review`
- `qctl explain rule-review-policy`
- `qctl guide data-review-policy`
- `qctl guide review-data`
- `qctl guide inspect-data`
