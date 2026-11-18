# Preview a rule against real rows before enabling it

    qctl create dry-run-job -f spec.yaml --table orders
    qctl inspect dry-run-job <id> --follow --timeout 15m

A dry run runs one or two rules against a table's real rows and writes
nothing back: no row, no table rule. `--follow` waits for the job, then prints
the preview if it finished (exit 0); any other end exits 1 with a `Next:`
line, and running out of `--timeout` exits 124 with the job still running.
Read the verdict before you request a review or enable the rule.

## Write the spec

A dry-run job spec names the rule and the table to run it against:

    # spec.yaml
    apiVersion: qluster.ai/v1
    kind: DryRunJob
    metadata:
      cloud_source: my_source
    spec:
      rule_run_specs:
        - dataset_rule_id: "550e8400-e29b-41d4-a716-446655440099"
      max_rows: 1000

`rule_run_specs` takes 1-2 entries, each identifying a rule by
`dataset_rule_id` (a rule already attached to the table), `rule_revision_id`
(a specific revision, full UUID or 8-char short ID), or `rule` plus an
optional `release`. Two entries run both against the same rows in one job —
useful for comparing two rules (or two revisions) side by side. `qctl create
dry-run-job --help`
documents every spec attribute, including `max_rows` (default 1000) and
`nondeterminism_check` (default `sample`, which re-runs on a sample of rows
to catch nondeterministic rules).

## Create the job

    qctl create dry-run-job -f spec.yaml --table orders

`--table` (or `--table-id`) is only needed when the cloud source has to be
resolved by name or auto-detected — pass `--cloud-source-id` directly and the
table becomes optional.

Launching kills your own queued or running dry runs on the same cloud source
(rule validations and rule-run replays excepted), so one launch per source
supersedes the last. To compare two rules, put both in one job's
`rule_run_specs` rather than launching twice.

## When the launch is refused

The backend caps you at **3 dry runs executing** at once, per user per
organization, and answers the next launch with `429`. Only `pending` and
`running` count toward it — **queued runs are free**, so work that has not
started yet is not what is blocking you. Rule validations are excluded from
this budget entirely, and no capacity limit refuses one: the backend queues it
until the scheduler has room, which is why an authoring session no longer
collides with its own previews.

`qctl create dry-run-job` absorbs the refusal. It makes up to 5 attempts, waits
the number of seconds the server asks for in `Retry-After` (15 today), and
prints each wait to stderr so `-o json` stdout stays a single parseable
document:

      server is throttling dry runs; retrying in 15s (attempt 2 of 5)

If the cap is still full when the attempts run out, the command fails with the
backend's own message and exit code 1. Nothing is half-created — a refused
launch is rolled back whole — so re-running the same command is always safe.

## Watch it

    qctl get dry-run-job <id> --watch --timeout 30m
    qctl get dry-run-jobs --table orders

`get dry-run-job --watch` streams one job until it ends, and its exit code is
the verdict: 0 when it finished; 1 with a `Next:` line for any other terminal
state (`error`, `killed`, `stopped`, `blocked`, …); 3 when there is no such
job; 124 at `--timeout`, leaving the job running. `get dry-run-jobs --watch`
is a list watch with no end of its own: give it `--timeout` too.

## Read the verdict

    qctl describe dry-run-job <id>

Default output is state, rules, and timestamps, plus `status.error_message`
and `status.rule_load_errors` when the run has them: why a failed run failed.
Add `-v` for metrics, sampling detail and each load error's `stack_trace`,
`-vvv` for the raw API response.

`-v` also prints `status.structured_summary`, the same object the compact
view of `inspect dry-run-job` carries: `clean`/`warning`/`blocker` for one
run, `blockers`/`warnings` side by side for two. qctl prints it exactly as
the server sent it, and the server writes `rate_valid` only when a rate is
NOT valid, so a stat with no `rate_valid` key is valid. When it is `false`,
the `rate` is not a measurement; in a single-run summary read `count`, which
is then the observed row count. A comparison with `delta_valid: false` has a
meaningless `delta_rate` and an `is_significant` forced to `false`, while
`observed_delta` still counts real rows (it is `null` for an independent
comparison). `qctl explain dry-run-job --grep delta_rate` has the details.

## Look at the artifacts

    qctl inspect dry-run-job <id>
    qctl inspect dry-run-job <id> --view full

The default `compact` view is summarized stats and exemplar rows; `--view
full` is the row-level diff. `--follow` (shown at the top) waits first; its
`--timeout` defaults to 10 minutes, and `0` means no limit.

## Attach it to a review

    qctl review link-dry-run <review-id> <dry-run-job-id>

Without `--comment` the job becomes the review's headline dry run — the run
that the `require_dry_run` / `require_dry_run_success` checks look at before
enable — and it replaces whatever was linked before.

## See also

- `qctl explain dry-run-job`
- `qctl guide instantiate-rule`
- `qctl guide request-review`
