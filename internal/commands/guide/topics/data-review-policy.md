# Gate rows in quarantine for review

> **Which review guide?** Quarantined rows: `qctl guide review-data`. A rule
> revision, as its author: `qctl guide request-review`; as a reviewer:
> `qctl guide review-rules`. Whether rows need review at all: `qctl guide
> data-review-policy`; whether rules do: `qctl explain rule-review-policy`.

    qctl get data-review-policy --table orders -o yaml
    qctl apply -f data-review-policy.yaml --yes
    qctl get data orders --is-bad-data --held-for-approval

A **data review policy** decides what happens to rows that land in
**quarantine**: whether they must be approved before they are released, by
how many reviewers, and whether approval releases them. Read the effective
policy, edit the manifest, apply it, then check which rows it holds. The
org-level policy omits `spec.dataset_id` and is every table's default; a
per-table policy sets it and overrides the org policy for that table. Change
it only when the user asked for that policy.

## Create or update a policy

Write a manifest and apply it (the `kind` field routes it — no subcommand
needed):

```yaml
apiVersion: qluster.ai/v1
kind: DataReviewPolicy
metadata:
  name: org-data-review-policy
spec:
  requires_review: true          # undecided quarantined rows wait for approval
  min_reviewers: 2               # at least two distinct eligible reviewers must approve
  auto_release_on_approval: true # approved rows leave quarantine automatically
  # designated_reviewer_ids:      # optional: only these reviewers' approvals count
  #   - 00000000-0000-0000-0000-000000000000
  # dataset_id: 42                # optional: scope to one table instead of the org
```

```bash
qctl apply -f data-review-policy.yaml --yes
```

`min_reviewers` counts eligible approvals of the row's current version. Anyone
who submitted the row in any submission or ever edited it, including undo and
redo actors, is excluded.

`requires_review` is the only required field. `auto_release_on_approval: false`
keeps a row whose data review is approved in quarantine until someone
releases it. `qctl rows release` ignores the setting: it records the caller's
approval and releases a row once it has enough, and it skips the rows the
caller edited or submitted for review.

Apply is a PUT that replaces the whole policy at its scope: a field the
manifest omits is reset to its default (`min_reviewers: 1`,
`auto_release_on_approval: true`, no designated reviewers), not kept. Start
from `qctl get data-review-policy -o yaml` to change one field.

A per-table policy must be at least as strict as the org policy, or apply
fails with a 422 that says why (exit 2): it cannot turn off `requires_review`,
lower `min_reviewers`, turn on `auto_release_on_approval` when the org has it
off, or list a designated reviewer the org's list lacks, and its own list
must hold at least as many reviewers as the approvals required. An omitted
`auto_release_on_approval` counts as `true` here.

## What `requires_review` does to the rows

Every quarantined row nobody has decided is **held for approval**, rows that
still fail validation included, or **waiting for approval** while a
submitted data review of it is not closed. Row State says which:

```bash
qctl get data orders --is-bad-data --held-for-approval      # held-for-approval=review_policy
qctl get data orders --is-bad-data --waiting-for-approval   # waiting-for-approval
```

It only holds rows that are already in quarantine; a new row that passes at
ingest still goes to the clean table. `qctl rows release` then needs
`DATA_REVIEW_APPROVE` and records your approval instead of releasing
outright: it approves each row it names or matches that you neither
submitted nor edited, skips the rest, and releases a row only once it has
enough eligible approvals and no reject, cell veto or unfinished undo holds
it; the others stay undecided. It prints the rows it released, the ones
still short of approvals (`1 of 2 approvals`) and how many it skipped, and
exits 0 even when it releases nothing. The table's own
`quarantine_rows_until_approved` holds rows too, new ones included; when both
are on, every row that is not a possible duplicate reports
`review_policy`.

Turning `requires_review` off, by applying a manifest or with
`qctl delete data-review-policy`, sends the undecided rows back through
validation, but only on a table whose `quarantine_rows_until_approved` is off
too, and on a best-effort basis: check with `--held-for-approval` afterwards.
`qctl explain data` has the details.

## Inspect the current policy

`get` prints an apply-ready manifest you can edit and re-apply:

```bash
qctl get data-review-policy -o yaml               # org-level effective policy
qctl get data-review-policy --table orders -o yaml  # by table name
qctl get data-review-policy --table-id 42 -o yaml   # by table ID
```

`--table` is always a name here, never an ID; `--table-id` is the ID.

## See every field

```bash
qctl explain data-review-policy            # field-by-field, with quarantine semantics
```

## Related

- `qctl api-resources` — every resource and the verbs that act on it.
- `qctl guide review-data` — requesting, deciding and discussing the reviews
  this policy requires.
- `qctl explain rule-review-policy` — the *rule* review policy (governs rule
  revisions, not data quarantine).
