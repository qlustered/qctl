# Propose an alert or warning resolution or a rule-param change, and decide one

A **change proposal** is an alert or warning resolution, or a change to a
table rule's params, written by one person and applied only when an approver
approves it. A table's change review policy
decides which roles may resolve a change directly, which may only propose it,
and which may approve it: `alert resolve` answers 403
`CHANGE_PROPOSAL_REQUIRED` when you may propose that change but not apply it.
Approving runs the resolution, or the params patch, as the approver.

    qctl alert propose 9 --action not_anomaly
    qctl get change-proposals --reviewable
    qctl describe change-proposal <proposal-id>
    qctl change-proposal approve <proposal-id> --yes

Proposal ids are full UUIDs: copy them from command output.

## 1. Propose (author)

```bash
qctl alert propose 9 --action not_anomaly          # or: qctl warning propose 55 --action ...
qctl alert propose 9 --action map_value --dry-run  # an action that needs input: write the manifest first
qctl alert propose -f fix.yaml --also-alert 10     # the filled-in manifest, also resolving alert 10
```

`propose` sends exactly the body `resolve` would send and checks it the same
way first. The proposal goes straight to review (`pending_review`) and holds
its alert or warning, and every one it also resolves, until it is decided;
`--draft` keeps it a draft that holds nothing until you submit it. A
row-scoped payload (`ignore_rule_for_row` or `overwrite_value`, and on a
warning `overwrite_rule_outputs`, naming `ids`) holds only those rows of its
target, so another row of the same issue can take a proposal of its own.
Another proposal that names a held issue or row is refused with 409
`CHANGE_PROPOSAL_ACTIVE`, and qctl names the holder.

Two proposals of one table can touch the same rows, file or column. To make
one wait for the other, name it: `--depends-on <proposal-id>` on `propose` or
`change-proposal edit` (`--remove-depends-on` drops it). Approving the
dependent proposal is then refused until the other is executed or obsolete,
and it turns `stale` if the other is rejected, withdrawn or fails.

### A rule's params

```bash
qctl table-rule propose amount_range --table orders --set-param max=500 --unset-param legacy_max
qctl change-proposal edit 0190bbbb-1111-2222-3333-444444444444 --set-param max=450   # change the params it stores
qctl change-proposal rerun-dry-run 0190bbbb-1111-2222-3333-444444444444             # its dry run ended killed, error or stopped
```

`table-rule propose` merges the edits into the rule's live params and
proposes the complete result: approving replaces the rule's params as a whole.
Params are the only part of a rule a proposal can change. Submitting it
records the rule's params at that moment as its base params and stages a
validation dry run of the live rule against the proposed params; the proposal
cannot be approved until that run has finished without rule errors. If the
rule's params change under it, REVIEW reads `stale` (`rule_params_changed`)
until an edit re-records them. `--also-alert` and `--also-warning` may name
only issues the rule itself raised.

PAYLOAD and `--set-param` use the units `qctl get table-rule -o yaml` shows;
REVIEW's params change and base params use the units the rule stores. They
differ only for a `range_rule`'s `min` and `max` on a percent column.

## 2. Find what waits for you

```bash
qctl get change-proposals --reviewable   # pending_review proposals you may decide
qctl get change-proposals --mine         # the ones you wrote or edited
```

## 3. Read one

```bash
qctl describe change-proposal 0190bbbb-1111-2222-3333-444444444444
```

The text payload shows the target issue as it is now, the payload as the
`AlertResolution` or `WarningResolution` manifest it carries, and a REVIEW
section: the server's review computed for you now.

- `Validity: current` applies as submitted; `stale` needs an edit (the
  reasons say why) and cannot be approved; `obsolete` can no longer apply,
  because its issue is resolved, gone or of another issue type, and its next
  step turns it obsolete.
- `Blockers` are the refusals the resolution would raise if you approved it
  now (a locked period, for one); a blocked proposal cannot be approved.
- `Changes` and `Changed since submit` say what approving it changes and
  whether that moved since it went to review; `Resource keys` name the files,
  sources, rows and columns it acts on.
- `Depends on` and OVERLAPS: the proposal this one waits for, and the other
  live proposals of the table that act on the same resources.
- For a rule-param proposal REVIEW shows the params change, the base params,
  the series rule approval (and whether your approval re-approves the series'
  rule set) and the validation dry run; its blockers name a dry run that has
  not finished or that recorded rule errors.

NEXT ACTIONS lists only the steps the server allows you now.

## 4. Decide (approver)

```bash
qctl change-proposal approve 0190bbbb-1111-2222-3333-444444444444 --yes
qctl change-proposal request-changes 0190bbbb-1111-2222-3333-444444444444 --reason "map 14-20 to 1420"
qctl change-proposal reject 0190bbbb-1111-2222-3333-444444444444 --reason "the anomaly is real"
```

`approve` prints the payload and the review, asks for confirmation at a
terminal unless `--yes` is given (without a terminal it needs `--yes`, so read
the proposal with `describe` first), and sends the version and impact digest
it read.
If the proposal, its review or its impact moved in between, the server
refuses with a 409 and nothing runs: read it again and decide again. qctl
never retries an approval. A proposal whose payload deletes, discards or
ignores something also needs `--context` on a protected context.

| Outcome | Exit | What it means |
|---|---|---|
| `executed` | 0 | the resolution ran; qctl prints its response and the issue's state (for a rule-param proposal: `applied`, or `staged` as a rule suggestion, the series rule approval and the rule's params) |
| `execution_failed` | 1 | it failed after committing part of its change, which stays; `qctl get history --table-id <id>` shows what moved |
| the resolution's own error | its code | it failed before changing anything; the proposal is still `pending_review` |
| `CHANGE_PROPOSAL_*` 409 | 2 | nothing ran; qctl prints the fresh state, reasons or blockers |

`request-changes` sends a `pending_review` proposal back to its authors, and
it keeps holding its issues; `reject` ends a `pending_review` or `changes_requested`
proposal and releases them. Both need `--reason`, which the authors see in
DECISION.

Only roles the table's policy lets approve may decide, and a policy that
requires a distinct approver refuses anyone who wrote or edited the proposal:
both are 403 `CHANGE_REVIEW_POLICY_DENIED` (exit 4).
`qctl get change-review-policy --table-id <id>` shows the policy in force on
a table: who may resolve, propose and approve each key.

## 5. Revise (author, after a request for changes)

```bash
qctl describe change-proposal 0190bbbb-1111-2222-3333-444444444444               # DECISION carries the reason
qctl change-proposal edit 0190bbbb-1111-2222-3333-444444444444 -f fix.yaml --submit
qctl change-proposal withdraw 0190bbbb-1111-2222-3333-444444444444               # or give up on it
```

Save the PAYLOAD manifest from `describe`, change it, and edit with `-f`.
`edit` sends the version it read, so an edit that crossed another write is
refused with 409 `CHANGE_PROPOSAL_VERSION_MISMATCH`: read it again and redo
the edit.

See also: `qctl explain change-proposal`, `qctl explain change-review-policy`,
`qctl guide triage-alerts`.
