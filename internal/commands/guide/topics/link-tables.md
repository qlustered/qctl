# Compose one table out of several

A **linked table** joins one or more existing tables onto a *spine* table to
produce a new, derived table — for example, joining `orders` and `customers`
into an `orders_enriched` output. This guide takes one from a manifest to a
live table, and rebuilds it after a failed build.

The backend calls a linked table a **composite dataset**, hence the
`composite-dry-run-job` and `composite-run-job` resources. A manifest may say
`kind: CompositeDataset` instead of `kind: LinkedTable`; qctl reads both.

## 1. Write the manifest

    # linked-table.yaml
    apiVersion: qluster.ai/v1
    kind: LinkedTable
    metadata:
      name: orders_enriched           # required: the <name> used below
    spec:
      destination: warehouse          # required
      table:
        database: analytics           # required
        name: orders_enriched         # required: the table in the destination
      spine:
        source: orders                # required: a key under spec.sources
        natural_key_columns: [order_id]  # required, at least one column
      sources:                        # required, at least one entry
        orders:
          dataset: raw_orders          # required: the upstream table's name
                                       # the spine source takes no arity
        customers:
          dataset: raw_customers
          arity: zero_or_one           # required on every other source:
                                       # exactly_one|zero_or_one|at_least_one
          join_fields:
            customer_id: customer_id   # shorthand: spine column -> source column
      output:                          # required, at least one mapping
        order_id: { source: orders, field: order_id }
        cust_name: { source: customers, field: name }
      desired_state: enabled           # optional: enabled (default) or draft
      dry_run: { max_spine_rows: 1000, seed: 7 }   # optional: apply's sample

`spec.spine.source` has to name one of the keys under `spec.sources` —
here, `orders`. Every `dataset` value (`raw_orders`, `raw_customers`) is the
name of a table that must already exist; see `qctl guide onboard-table` if
one doesn't yet. The linked table's name is `metadata.name`, the `<name>` in
the commands below; `spec.table.name` names its table in the destination.

`arity` says how many rows of a source each spine row may match. The spine
source must omit it, and every other source must set it; the server refuses
the manifest otherwise (`non-spine source … is missing required arity`).

### What a linked table can see

- It is built from the spine table's **clean** rows (rows with only warnings
  included). Rows held or quarantined upstream never arrive; release or fix
  them there first.
- Joined sources are matched against their clean rows too. A spine row whose
  only match is quarantined has zero matches: the dry run reports it under
  `blocked_zero_match`, and a real build with `exactly_one` or `at_least_one`
  on that source fails as a whole.
- `output` copies a field from one source, `{source, field}`, and nothing
  else. Join clauses can use expressions; output cannot. To derive a value,
  add an enrichment rule to an upstream table that writes the column, then
  map that column (`qctl guide instantiate-rule`).

To change a linked table that already exists, start from its current spec
instead of writing one:

    qctl get linked-table orders_enriched -o yaml > linked-table.yaml

The file is this manifest, written from the table's latest revision, plus a
`status:` block that apply ignores. It carries no `release`, so an edited
spec gets the next `vN` when you apply it.

## 2. Check the natural key

`spec.spine.natural_key_columns` should identify each spine row. Nothing
enforces it (two spine rows with one key still get distinct identities), but
the web wizard's natural-key step checks it, and so can you:

    qctl rows check-unique raw_orders --column order_id

Repeat `--column` for a key of several columns; they are checked together,
over clean rows only. Exit 0 means unique. Exit 1 with nothing on stderr means
not unique: a NULL, or repeated values (up to five duplicate groups printed).
Exit 1 with a message means the check did not finish (a timeout or a server
error); exit 2, bad usage or a column the table does not have.

What does block enabling is a sampled spine row whose key is empty — NULL, or
`""` after `natural_key_normalization`: the dry run counts it as an activation
blocker (bucket `blocked_empty_natural_key`). check-unique counts only NULL,
so an empty string passes it, and it compares stored values, two of which a
`trim_*` normalization can make equal.

Settle the key before the first apply: once the table exists, an apply that
changes its spine table, natural key or another frozen field is refused
(`immutable_field_changed`); `qctl explain linked-table` names them.

## 3. Validate and preview

    qctl apply linked-table -f linked-table.yaml --validate-only

`--validate-only` has the server resolve and check the manifest as a real
apply would, then roll it back: nothing is persisted and no dry run launched.
A manifest that fails exits 2 listing each error as `<loc>: <msg> (<code>)`.
`qctl apply -f` takes it too, and refuses a file that also holds another kind
before applying anything. `spec.dry_run` is not a validate switch: it only sets
`max_spine_rows` and `seed` for the dry run a real apply launches.

    qctl diff -f linked-table.yaml

`qctl diff -f` makes the same check, then prints what the apply would change
against the table's latest spec revision and exits 0 when there is nothing to
change, 1 when there is. For a table that does not exist yet it lists the
spec fields the manifest sets as additions, leaving out `spec.dry_run` and any
output column the check reports as `deleted_field_pruned`. A manifest that
matches an older revision previews as differences from the latest one.

## 4. Apply it

    qctl apply linked-table -f linked-table.yaml --yes

It prints `linked-table/<name> created`, `configured` (a new spec revision) or
`unchanged` (the spec matches a revision the table already has), the
revision's state, and a `Next:` line. Re-applying converges the same linked
table. What happens to the revision:

- Already enabled: it stays enabled and gets no dry run, whatever
  `desired_state` says and even if its latest dry run no longer passes.
- Not enabled, with no current successful dry run: the apply launches one.
- Not enabled, with one: `desired_state: enabled` enables it during the apply
  (an enable gate that refuses fails the whole apply as `enable_failed`, exit
  2, and nothing is saved); `draft` leaves it as it is.

## 5. Follow the dry run

The dry run previews the join over a sample of spine rows. The apply prints
its id on the `dry run  :` line and a `Next:` line pointing at it. There is no
progress stream. Apply with `--wait` to poll it until it finishes and, with
`desired_state: enabled`, until the backend has enabled the revision or
decided not to. It exits 1 with the reason when the dry run ends without
finishing or the revision is not enabled, and 124 when `--wait-timeout` runs
out first:

    qctl apply linked-table -f linked-table.yaml --wait --yes

Or poll it yourself — not by re-applying, which can launch a new dry run that
stops this one:

    qctl describe composite-dry-run-job <id>       # state, then the summary once finished

    qctl inspect composite-dry-run-job <id> --bucket blocked_zero_match   # the rows behind a problem

    qctl get composite-dry-run-jobs --table <name>  # every dry run of this linked table, and its revision

To pass, the revision's latest dry run must have finished against its current
spec, sampled at least one spine row and found no activation blockers —
`status.structured_summary` shows `sample.sampled_spine_rows` and
`activation_blocker_count`.

To re-run it with another sample, launch one yourself, naming the revision
(the dry run's `spec.composite_spec_revision_id`; without `--revision` it is
the table's latest, which may not be the one the apply used):

    qctl create composite-dry-run-job --table <name> --revision <uuid> --seed 11 --max-spine-rows 200

`--pin-spine-row <row-id>` (repeatable) adds a clean, undeleted spine row to
the sample; any other id is dropped without an error, pins count against
`--max-spine-rows`, and `describe` shows the survivors under
`status.sampling`. The launch stops your own queued or running dry runs of
that revision, the apply's included, and a stopped apply dry run withholds its
auto-enable, so enable the revision yourself once the new one passes. Within
10 seconds of your previous launch of that revision, while it is still queued
or running, you get that one back instead: the new sample flags are ignored,
and `created` still prints.

## 6. Enable it

With `spec.desired_state: enabled`, a dry run that finishes and passes the
enable gates enables the revision. `status.auto_enable_state` says whether it
did; `withheld` and `failed` carry the reason in `status.auto_enable_detail`.

With `draft`, or after a re-run stopped the apply's dry run, enable it
yourself by the dry run's `spec.composite_spec_revision_id`; re-applying a
draft manifest once the revision has passed prints this on its `Next:` line:

    qctl enable linked-table --table <name> --revision <uuid>

Without `--revision` it enables the table's latest revision, which is not the
one an apply reused when the manifest matched an older revision. A gate that
refuses the enable exits 2 with the backend's reason. Enabling disables the
revision enabled before it and queues a rebuild (step 7).

Naming an older revision rolls the table back to it. `qctl diff -f` still
compares with the latest revision, so it then exits 1 for the old manifest and
the newer one alike, and re-applying the newer manifest with `desired_state:
enabled` re-enables the newer revision — during the apply if its latest dry
run still passes, otherwise through a new dry run that passes.

## 7. Confirm

    qctl diff -f linked-table.yaml     # exit 0: the latest revision matches

    qctl get composite-run-jobs --group-id <group-id> --reason revision_enabled   # the rebuild enabling queued

`qctl diff -f` exits 0 once the table's latest revision matches the manifest
and, unless `desired_state` is `draft`, is enabled. `<group-id>` is the dry
run's `spec.composite_spec_group_id`, also on the enable's `Next:` line; the
run appears once the scheduler picks the rebuild up.

`qctl get tables`, `qctl describe table` and `qctl get data` work on a linked
table as on any table, but `describe table`'s blockers do not cover its
build. When it has no rows, `qctl get data <name>` fails with the reason:

- not activated: no revision is enabled yet — steps 4 to 6;
- building: wait for the rebuild above;
- build blocked, stuck or failed, or built and now empty: step 8;
- every row quarantined: `--is-bad-data --include-dataset-rule-ids` reads
  them and the rule each one tripped;
- no rows matched: no spine row joined the other sources; a dry run's
  `--bucket blocked_zero_match` shows the spine rows that matched nothing.

When the server offers a rebuild for the reason, the error also names
`qctl run composite-run-job <table>`, which is step 8.

## 8. Rebuild it after a failure

Enabling a revision queues a rebuild of the linked table, and later changes to
the tables it reads from queue more. Each rebuild is a composite run job. List
this table's failed ones by its group (`--table` would instead filter on the
upstream table whose change triggered a run):

    qctl get composite-run-jobs --group-id <group-id> --state error --state killed

    qctl describe composite-run-job <id>     # the targets and the error

A build that keeps failing trips the table's build circuit breaker, and the
table is then not rebuilt at all until the breaker is reset.

If the fix is a change to the manifest, re-apply it with `desired_state:
enabled`: enabling the new revision resets the breaker and queues its own
rebuild. If the fix was elsewhere, reset the breaker and rebuild from the
revision already enabled:

    qctl run composite-run-job <name>

Its `Next:` line is the listing to follow; the run appears there once the
scheduler picks the rebuild up. `qctl enable linked-table` cannot do this: it
sends nothing for a revision that is already enabled.

## See also

- `qctl explain linked-table`
- `qctl explain composite-dry-run-job`
- `qctl explain composite-run-job`
- `qctl guide onboard-table`
- `qctl guide inspect-data`
