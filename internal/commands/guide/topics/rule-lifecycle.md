# A rule from source file to enabled on a table

    qctl submit rules -f rules.py --yes                          # 1. into the catalog
    qctl get rule my_rule                                        # 2. its revisions
    qctl create dry-run-job -f spec.yaml --table orders          # 3. try it on real rows
    qctl review request my_rule --release 1.0.0 --dry-run-job 512  # 4. only if review is required
    qctl enable rule my_rule --release 1.0.0                     # 5. only if submit left it draft
    qctl apply -f table-rule.yaml --yes                          # 6. attach it to a table
    qctl set-default rule my_rule --release 1.0.0                # 7. optional
    qctl get rule-upgrades my_rule                               # 8. tables still on the old release

The map from a Python file to a rule doing work on real rows, and on to the
rollout when a later release replaces it. Each step names the one command and
the guide that covers it in depth.

## 1. Submit

    qctl submit rules -f rules.py --yes

Adds the rule to your organization's catalog. Authoring, file constraints,
and the SDK: `qctl guide submit-rules`.

## 2. Find the revision

    qctl get rules
    qctl get rule my_rule

`get rules` lists rule families, one row per name. `get rule my_rule` shows
every release of one family — the revision id you'll need for step 6 comes
from here.

## 3. Dry run

    qctl create dry-run-job -f spec.yaml --table orders

Runs the revision against rows from the table's cloud source without
attaching it. A policy with `require_dry_run` gates enable on a run linked to
the review; `require_dry_run_success` on a passing one. Writing the spec,
watching the job, and reading the verdict: `qctl guide dry-run-jobs`.

## 4. Review

    qctl review request my_rule --release 1.0.0 --dry-run-job 512

Only needed when the organization's review policy sets `requires_review` —
`submit rules` then reports the revision as `draft` and prints this command.
The request moves it to `in_review`; `--dry-run-job` links the step 3 run as
the review's evidence. To link a later run:
`qctl review link-dry-run <review-id> <job-id>`. Full workflow, including
responding to feedback: `qctl guide request-review`. The reviewer's side:
`qctl guide review-rules`.

## 5. Enable the revision

    qctl enable rule my_rule --release 1.0.0

When the organization's policy does not set `requires_review`, the revision
is already `enabled` at submit; skip this step. Otherwise a `draft` revision
with no review request fails with a 422,
`Invalid state transition: 'draft' -> 'enabled'`, and an `in_review` one fails
with the backend's `REVIEW_REQUIRED` error until its review is approved and
any dry run the policy requires is linked. With `auto_enable_on_approval`, the
final approval enables it for you once that dry-run requirement is met.

## 6. Attach it to a table

    qctl apply -f table-rule.yaml --yes

Enabling a rule only makes it *eligible* to run — it does nothing until
it's instantiated on a table. Manifest shape, patch vs. instantiate mode,
and what happens when a patch gets staged: `qctl guide instantiate-rule`.
Resolving a staged change: `qctl guide manage-suggestions`.

## 7. Make it the default

    qctl set-default rule my_rule --release 1.0.0

Optional. Marks this release as the family's default (`get rule`/`get
rules` show a **Default** tag; an older default revision shows **Update
available**).

## 8. Roll it out

    qctl get rule-upgrades my_rule

Making a release the default does not move the tables already running an
older one — it only marks them **Update available** and puts a `rule_upgrade`
notification in the inboxes of the instances' notified users and of the
editors and admins with access to those tables. `get rule-upgrades` lists
exactly which rule instances are still on the old code. To re-send the notice
to the users who dismissed it:

    qctl rule-upgrade notify my_rule --yes

Upgrading an instance is step 6 again, with the new revision and
`spec.upgradedFrom` set to the old instance's id
(`qctl get rule-upgrades my_rule --columns table,rule,id`); the old instance
is retired when the replacement is enabled. Full detail:
`qctl guide instantiate-rule` and `qctl explain rule-upgrade`.

## See also

- `qctl guide submit-rules`
- `qctl guide request-review`
- `qctl guide review-rules`
- `qctl guide dry-run-jobs`
- `qctl guide instantiate-rule`
- `qctl guide manage-suggestions`
- `qctl guide check-inbox`
