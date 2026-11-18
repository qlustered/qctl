# Unblock a table

    qctl describe table orders

Run this first. Its BLOCKING section lists the holds that apply, each paired
with the command that clears it. Fix them in this order, which is not
BLOCKING's own:

    qctl describe table-schema orders                        # 1. a draft schema waiting?
    qctl get rule-suggestions orders                         # 2. decide these before step 5
    qctl get latest-alert --table orders                     # 3. the alert to act on first
    qctl alert resolve 4821 --action disable_rule --yes      # 4. a crashed rule
    qctl alert resolve 4822 --action apply_migration --yes   # 5. confirm the draft

BLOCKING can also lead with `table state is disabled`: the table itself is not
`active`, and none of these steps changes that. Resolve only what the user
asked you to clear: each `alert resolve` writes.

## 1. Is a draft schema waiting?

    qctl describe table-schema orders

The header says whether a draft exists and whether it is confirmed. Under the
default `migration_policy`, `ask_user`, a schema change parks in a draft and
raises `needs_review_migration` instead of applying. An additive-only patch to
a table with no draft applies itself and leaves nothing waiting here (`qctl
explain table-schema`). Details: `qctl guide inspect-schema`.

## 2. Is anything undecided?

    qctl get rule-suggestions orders
    qctl suggestion accept <id> --table orders --yes
    qctl suggestion dismiss <id> --table orders --yes

Do this before step 5. Confirming a draft migration decides the table's
pending suggestions as a side effect: pending `create` suggestions staged
against it are accepted, while pending `update` and `delete` ones **block the
confirmation outright** with `409 PENDING_SUGGESTIONS_BLOCK_MIGRATION`, and
nothing changes (a revalidation candidate whose dry run passed does not
block). Details: `qctl guide manage-suggestions`.

## 3. What needs attention?

    qctl get latest-alert --table orders
    qctl get alerts --table orders

`get latest-alert` prints the one alert to act on first, with the counts
behind it. When the newest alert depends on another, it reports the
dependency instead: the alert you have to clear first. `get alerts` is the
whole list. Details: `qctl guide triage-alerts`.

## 4. Did a rule crash?

    qctl describe alert 4821
    qctl alert resolve 4821 --action disable_rule --yes

`describe table`'s `Rules:` line counts rules in the `error` state (`, 1
error`), and BLOCKING names the open `severe_rule_bug` alert holding the
table. `error` is the backend's crash-loop circuit breaker, not `disabled`:
nobody decided anything, the rule broke. `disable_rule` clears the blocker at
once, and `upgrade_rule_revision` once its replacement is live. On the
ordinary close path, `409 SEVERE_RULE_BUG_RULE_STILL_ACTIVE` refuses while the
rule is neither disabled nor deleted; the other actions and their refusals are
in `qctl explain alert`.

Disabling the rule with a `kind: TableRule` manifest (`spec.state: disabled`)
clears it too — unless any draft migration exists for the table: the backend
then stages the patch as a rule suggestion (202), and nothing clears until you
accept it. That keys on the migration being in the `draft` state, not on
waiting for review: even a confirmed draft already queued for an additive-only
auto-apply stages the patch until it finishes. The `disable_rule` action is
never staged, so it is the exit that works first time.

BLOCKING can name an open `severe_rule_bug` alert "though no rule is in error
state" while `Rules:` shows no error count: the rule was already disabled or
deleted and the alert was never resolved. `qctl describe alert <id>` lists the
actions that still move it.

## 5. Clear it

    qctl describe alert 4822
    qctl alert resolve 4822 --action apply_migration --yes

`describe alert` lists the actions this alert offers you; that list is
authoritative, filtered by your role and by per-alert runtime checks. On
`apply_migration`, qctl re-runs the step 2 check first and refuses rather than
letting the backend reject the whole request.

A mirror or append table's first migration also chooses its key columns, and
`describe table-schema` says so with `Key step: required` and lists key
candidates. Add the choice to the same command; qctl refuses it without one
(exit 2):

    qctl alert resolve 4822 --action apply_migration --key-columns order_id --yes
    qctl alert resolve 4822 --action apply_migration --no-key-columns --yes   # the table has no key

Applying locks the key columns.

## 6. Wait for the next one

### A confirmed schema change has not landed

    qctl describe table orders
    qctl describe table-schema orders
    qctl get ingestion-jobs --table orders --state running --columns id,state,is_dry_run
    qctl get dry-run-jobs orders --columns id,state,ingestion_job_id

Confirmation queues the migration; it does not make the schema apply at once.
The scheduler waits for running ingestion jobs on the table, including the
ingestion carriers of rule-validation dry runs. These are separate from the
profiling job slot. `describe table`'s BLOCKING section names those carriers
with `qctl describe ingestion-job <id>`, even when its newest ingestion job
has already finished. Match the carrier's ID to the dry-run list's
`ingestion_job_id` to find the dry-run ID you can watch:

    qctl get dry-run-job <id> --watch --timeout 10m

Let the running jobs finish, then check the schema again. The previews before
`qctl apply -f` of a TableSchema and `qctl alert resolve --action
apply_migration` go to stderr even under `--yes`; that flag skips confirmation
but does not bypass this wait. A `pending validation` rule count alone does
not establish a running job; inspect the job states.

Another running profiling job can also delay the migration:

    qctl get profiling-jobs --table orders
    qctl get alerts --table orders

If no ingestion job is running, use these reads to check the profiling job
and any migration alert, then `qctl describe alert <id>` for its offered
actions.

### Watch for a new alert

    qctl get latest-alert --table orders --watch --exit-on-change --timeout 10m -o json

Exits 0 with the change on stdout, or 124 when the ten minutes pass with no
change. Without `--exit-on-change` it prints an entry each time the latest
alert changes, until `--timeout` runs out, and exits 0.

## See also

- `qctl guide inspect-schema` — how schema drafts work
- `qctl guide manage-suggestions` — accepting and dismissing suggestions
- `qctl guide triage-alerts` — alerts and warnings end to end
- `qctl explain alert --gotchas` — every resolve action and its refusals
