---
summary: a preview of what a linked table's spec would produce, sampled from its spine table, writing nothing
aliases:
kind:
schema:
guide: link-tables
signature: dff095a
---

## USAGE

    qctl create composite-dry-run-job [table]
    qctl describe composite-dry-run-job <id>
    qctl get composite-dry-run-jobs
    qctl inspect composite-dry-run-job <id>

## EXAMPLES

    # dry-run a linked table's latest spec revision
    qctl create composite-dry-run-job orders_enriched

    # a reproducible sample of 200 spine rows
    qctl create composite-dry-run-job --table orders_enriched --seed 7 --max-spine-rows 200

    # dry-run one named revision of the table, by table ID
    qctl create composite-dry-run-job --table-id 12 --revision 550e8400-e29b-41d4-a716-446655440000

    # always include two spine rows in the sample
    qctl create composite-dry-run-job orders_enriched --pin-spine-row 1024 --pin-spine-row 2048

    # only composite-dry-run-job/<id>, for a script
    qctl create composite-dry-run-job orders_enriched -o name

    # launch one and wait for it, exiting 1 if it ends without finishing
    qctl create composite-dry-run-job orders_enriched --wait

    # wait at most five minutes
    qctl create composite-dry-run-job orders_enriched --wait --wait-timeout 300

    # every composite dry run in the organization, newest first
    qctl get composite-dry-run-jobs

    # only the dry runs of one linked table (the downstream table, by name)
    qctl get composite-dry-run-jobs --table orders_enriched

    # the same filter by ID
    qctl get composite-dry-run-jobs --table-id 12

    # only the dry runs that ended without finishing
    qctl get composite-dry-run-jobs --state error --state stopped

    # every dry run of one spec revision
    qctl get composite-dry-run-jobs --revision-id 550e8400-e29b-41d4-a716-446655440000

    # one dry run: what was launched, its state, and its structured summary
    qctl describe composite-dry-run-job 5512

    # the same as JSON, for a script
    qctl describe composite-dry-run-job 5512 -o json

    # the untouched API response, metrics included
    qctl describe composite-dry-run-job 5512 -vv

    # the per-row evidence behind the summary
    qctl inspect composite-dry-run-job 5512

    # only the spine rows that matched nothing in a source table
    qctl inspect composite-dry-run-job 5512 --bucket blocked_zero_match

    # only the rows attributed to one source alias, second page
    qctl inspect composite-dry-run-job 5512 --source claims --page 2

    # the evidence as YAML
    qctl inspect composite-dry-run-job 5512 -o yaml

## GOTCHAS

- `--wait-timeout` and SIGINT/SIGTERM cancel HTTP polls in flight, including requests waiting to retry; a successful response arriving after cancellation is discarded.

- This is not the rule dry-run family: `qctl get dry-run-jobs` and `qctl describe dry-run-job` preview a rule against one table, while a composite dry run previews a linked table's whole spec by sampling its spine table and joining the sources. `qctl create composite-dry-run-job` launches one by hand; `qctl apply` of a `LinkedTable` launches one too when the revision it converges is not enabled and has no current successful dry run, and its `dry run  :` line names it. Both print `Next: qctl describe composite-dry-run-job <id>`.
- `create composite-dry-run-job` stops your own dry runs of the same revision that are still `created` or `running` (`error_type: superseded`), including the one `qctl apply` just launched — and a superseded apply dry run withholds its auto-enable, so `spec.desired_state: enabled` will not enable the revision. Within 10 seconds of an identical launch of the same spec it instead returns that dry run's ID, ignoring `--max-spine-rows`, `--seed` and `--pin-spine-row`; the command still prints `created`, because the response cannot tell the two apart. [backend]
- `--pin-spine-row` takes ids of the spine table's CLEAN, undeleted rows; any other id is dropped without an error. Pins are de-duplicated and count against `--max-spine-rows`, which trims them to fit. `describe` shows what survived as `status.sampling.pinned_spine_row_ids` next to `requested_pinned_spine_row_ids`. [backend]
- Without `--seed` the sample is the lowest spine row ids (`ORDER BY id ASC`), not a random one; with a seed it is `ORDER BY md5(id || seed)`, and the same seed samples the same rows. `--seed 0` is a seed. [backend]
- A launch spends the same per-user budget as a rule dry run: with 3 of your dry runs executing in the organization, not counting your own dry runs of this revision, which the launch supersedes before it counts, the server answers 429 with `Retry-After: 15`, and the command retries up to 5 attempts, printing each wait to stderr. The 429 rolls the whole launch back, including the supersede, so a retry is the identical request. [backend]
- With no `--revision` the dry run previews the table's latest spec revision — the newest one, whatever its state, which is the one the web Preview & activate panel dry-runs. A table that is not a linked table has no such revision and fails with 404. [backend]
- `--revision` must be a revision of the linked table's current spec group; one of another table, or one from before the table was re-keyed, exits 2 before anything is launched.
- A composite dry run only reaches five states: `created` (queued), `running`, `finished`, `stopped` and `error`. `stopped` carries `error_type: superseded` when the same user launched a new dry run of the same revision while this one was still `created` or `running` (a relaunch of the same spec within 10 seconds returns this one instead), or `error_type: spec_drift` when the revision's spec changed after launch; `error` carries the exception's type and text, a plain-text `user_message`, and a `wizard_step` when the failure points at one step of the spec. `--state` accepts every value of the shared job-state enum because that is the type the route declares, so the other eight are accepted and match nothing. [backend]
- `summary`, `status.structured_summary`, the list's `activation_blocker_count` and the rows `inspect` prints are all written when the dry run FINISHES, in the same transaction as the `finished` state, so a dry run that is `created`, `running`, `stopped` or `error` has none of them: describe leaves `structured_summary` out rather than printing a summary of zeros, the count is null, and the examples page is empty. [backend]
- There is no progress stream for a composite dry run — no `--follow` as on `qctl inspect dry-run-job`, because the backend has no websocket route for it. `--wait` on `create composite-dry-run-job`, `apply linked-table` and `apply -f` polls for you; otherwise poll `qctl describe composite-dry-run-job <id>` until `status.state` is `finished`, `stopped` or `error`. [backend]
- `create composite-dry-run-job --wait` polls the dry run — every 2 seconds, then every 5 once it has waited 30 — until its state is neither `created` nor `running` and its `auto_enable_state` is not `pending`. It exits 0 for a `finished` dry run whose `auto_enable_state` is null or `enabled`, and 1 for any other ending, with the dry run's error and auto-enable reason; running out of `--wait-timeout` (seconds, default 900) exits 124, and the dry run carries on. Each change of state prints to stderr; under `-o json`, `yaml` or `name` stdout is the launch answer, as without `--wait`, and how the dry run ended goes to stderr. `--wait-timeout` without `--wait` exits 2.
- Before its first poll `--wait` prints one line to stderr, `waiting for composite dry run <id> to settle, for up to 15m0s; check with: qctl describe composite-dry-run-job <id>` at the default `--wait-timeout`, and `apply linked-table --wait` and `apply -f --wait` print the same line for the dry run an apply launched. Ctrl-C (SIGINT) ends the wait with exit 130, and SIGTERM with 143, printing `interrupted; check with: qctl describe composite-dry-run-job <id>` on stderr; as on a timeout, only the wait stops and the dry run carries on.
- A dry run can read `finished`, `error` or `stopped` while its `auto_enable_state` is still `pending`: the backend commits the dry run's state first and records the auto-enable outcome in a second transaction, which is why `--wait` waits for both. If the worker dies between the two, nothing retries the enable — the intent stays `pending` until a later apply of the table, or a manual enable of another of its revisions, withholds it — so `--wait` runs out its timeout. Applying the manifest again withholds it, then enables the revision during the apply if that dry run finished without activation blockers, or launches a new dry run if it did not. [backend]
- `status.auto_enable_state` is present only on a dry run that `qctl apply` launched with `spec.desired_state: enabled`, or that such an apply returned instead of launching because you launched the same spec within the previous 10 seconds; every other launch leaves it null. `pending` means the backend has not recorded an outcome yet, `enabled` means the revision was enabled, `withheld` means it was not for the reason in `auto_enable_detail` (an enable gate failed, the dry run failed, stopped or was superseded, or a newer apply or a manual enable of another revision of the table voided the intent), and `failed` means the enable attempt raised. [backend]
- `--table` / `--table-id` filter on the linked table the dry run previews — sent as `downstream_dataset_id` — not on its spine or any source table.
- `--reverse` reverses the SERVER's default, and that default is descending: the list route declares `reverse: bool = Query(True)`, so the unflagged listing is newest-first and `--reverse` asks for oldest-first. [backend]
- `inspect --bucket` and `--source` are exact-match strings the server does not check — the bucket is stored as text so the set can grow — so a misspelled bucket is an empty page at exit 0. The eleven buckets today: emitted_clean, emitted_warning_only, emitted_blocked, emitted_rule_error, emitted_governed_duplicate, blocked_zero_match, blocked_multi_match, blocked_spec_error, blocked_empty_natural_key, blocked_required_join_null, skipped_spine_quarantined. [backend]
- In the examples, a numeric value whose field sets display decimals comes back as its display string (`"$1,234.56"`, `"12.50%"`) — `join_values` in the spine table's formats, `synthesized_values` in the linked table's — and every other value as stored, except that a caller without unmasked personal-data access to that table (no `pii:view` in its own organization and no active grant) gets each of its personal-data fields as `•••••` plus the value's last 4 characters (`•••••` alone under 9), named in `join_masked_fields` or `synthesized_masked_fields`, with the row's `message` masked to match. `inspect` prints the page as received rather than re-encoding it, so an integer join key past 2^53 keeps every digit in JSON and YAML alike. [backend]
- `describe composite-dry-run-job` renders a manifest by default: `spec` is what was launched (revision, group, linked table, `max_spine_rows`, `seed`), `status` is what the dry run did. `metrics` is only in the `-vv` dump, which is the untouched API response; `-v` adds nothing.

## SEE ALSO

- `qctl explain linked-table`
- `qctl explain composite-run-job`
- `qctl explain dry-run-job`
- `qctl guide link-tables`
