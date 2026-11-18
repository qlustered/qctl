---
summary: a re-run of one historical rule run over the rows it left evidence for, to see whether the rules still agree
aliases:
kind:
schema:
guide:
signature: 5a0e85e
---

## USAGE

    qctl describe rule-run-replay <id>
    qctl run rule-run-replay <rule-run-id>

## EXAMPLES

    # find a rule run id: every evidence block on a row is headed "## RUN <id>"
    qctl describe row 1042 --table orders

    # replay run 4711 over up to 1000 of the rows it wrote evidence for
    qctl run rule-run-replay 4711

    # replay a small slice of it instead -- a different cap is a different sample
    qctl run rule-run-replay 4711 --max-rows 50

    # the replay's own view: how far it got, and the comparison once it finished
    qctl describe rule-run-replay 7

    # the same as a payload a script can poll
    qctl describe rule-run-replay 7 -o json

    # the flattened view, one row per field
    qctl describe rule-run-replay 7 -o table

    # the carrier job the replay rode, read like any other dry run
    qctl describe dry-run-job 900

## GOTCHAS

- rule-run ids do not have a listing of their own: `qctl describe row <row-id> --table <table>` prints one block per run that recorded a verdict for that row, headed `## RUN <id>`, and that id is what `qctl run rule-run-replay` takes.
- `--max-rows` decides WHICH rows are replayed, not only how many: the run's first that many evidence rows in row-id order are pinned, so the same cap on the same run pins the same rows and a different cap is a different sample. [backend]
- a non-positive `--max-rows` is refused client-side with exit 2 before anything is sent — the OpenAPI generator dropped the spec's `exclusiveMinimum: 0`, so the field arrives in `internal/api` as a plain `*int` and nothing downstream would catch it until the backend's own 422.
- launching is NOT idempotent: a second `qctl run rule-run-replay 4711` starts a second replay rather than returning the first, and the backend indexes `rule_run_id` without a uniqueness constraint, so there is nothing to make it return the earlier one; what bounds concurrent launches is a per-user cap answering 429. [backend]
- under `-o json` or `-o yaml`, `run rule-run-replay` prints the server's `{replay_id, dry_run_job_id, rows_in_evidence, rows_pinned}`, and `-o name` prints `rule-run-replay/<replay-id>`, which `describe rule-run-replay` takes back; the prose moves to stderr.
- that 429 carries the wait in a `Retry-After` header and repeats it in `detail.context.retry_after_seconds`, alongside `current_count`, `max_allowed` and `limit_kind`; qctl does not wait for you here (unlike `qctl create dry-run-job`, which retries), because re-sending a launch that always launches would start a second replay. [backend]
- `status.report` is null both before a replay finishes and after one whose carrier stopped, so `describe rule-run-replay` reads `status.progress` off the carrier's state instead — never infer "still running" from a missing report; `status.report_note` says which of the two it is. [backend]
- `status.progress` describes the current carrier attempt using the dry-run watch stop states: `finished` means success; `error`, `killed`, `stopped`, `version_id_mismatch`, `file_missing`, `blocked` and `partitioned` mean `failed`; `created`, `pending`, `running`, `requeued` and `staged` mean `running`; a missing or unrecognized state means `unknown`. `status.state` keeps the raw value. A failed attempt may later be retried, so it does not mean a report can never appear.
- four separate refusals arrive as a plain 409 with no `error_code`, so the server's sentence is the whole diagnosis: a run whose manifest listed no rules, a run whose rule revisions no longer exist, a run that wrote no per-row verdict evidence, and a table with no active data source to replay against. [backend]
- the replay reuses the ORIGINAL run's execution context — its `now_utc`, `seed` and `uuid_namespace` — rather than fresh values, so a rule that reads the clock or a random draw is compared against the same inputs it saw the first time. [backend]
- the work rides an ordinary dry-run job, so a replay writes nothing to the table and `qctl describe dry-run-job <id>` reads the carrier — but a replay skips the metrics, top rows and summary an ordinary dry run computes, so the comparison exists only on `describe rule-run-replay`. [backend]
- `status.rows_replayed` can be smaller than `spec.rows_pinned`: a pinned row that is no longer live is counted as missing rather than as a disagreement. [backend]
- launching needs the `rule:instantiate` permission, because a replay executes rules the way a dry-run launch does; reading one needs only `dataset:data:read`. Both are additionally checked against the caller's access to the table the run belongs to. [backend]

- A replay launch requires the role's `rule:instantiate` permission. Callers other than ops/admin also need an editor or owner grant on its table; a `read_only` grant is refused with 403 (exit 4). Reading a replay does not require an edit grant. [backend]

## SEE ALSO

- `qctl explain dry-run-job`
- `qctl explain data`
- `qctl explain table-rule`
