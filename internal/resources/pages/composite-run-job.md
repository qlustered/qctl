---
summary: one real rebuild of a composite (linked) table after something upstream changed, or on request
aliases:
kind:
schema:
guide: link-tables
signature: cec4971
---

## USAGE

    qctl describe composite-run-job <id>
    qctl get composite-run-jobs
    qctl run composite-run-job [table]

## EXAMPLES

    # the whole composite recompute history, newest first
    qctl get composite-run-jobs

    # only the runs that ended badly
    qctl get composite-run-jobs --state error --state killed

    # only the runs a change to one table triggered
    qctl get composite-run-jobs --table orders

    # the same filter by ID (--table is a name, --table-id an ID)
    qctl get composite-run-jobs --table-id 5

    # only the runs published for one kind of invalidation
    qctl get composite-run-jobs --reason revision_enabled

    # every run that named one composite spec group, or rebuilt it as a target
    qctl get composite-run-jobs --group-id 550e8400-e29b-41d4-a716-446655440000

    # oldest first (the server's own default is newest first)
    qctl get composite-run-jobs --reverse

    # full detail for one run: targets, timings, and the rule runs it recorded
    qctl describe composite-run-job 123

    # the same as JSON, for a script
    qctl describe composite-run-job 123 -o json

    # the untouched API response, for debugging
    qctl describe composite-run-job 123 -vv

    # rebuild a linked table now, resetting its build circuit breaker first
    qctl run composite-run-job orders_enriched

    # the same, naming the linked table by ID
    qctl run composite-run-job --table-id 12

    # the server's answer for a script; its run_job_id is an outbox row id, not a composite run job id
    qctl run composite-run-job orders_enriched -o json

## GOTCHAS

- This is not the dry-run family. A composite run job REBUILDS a composite (linked) table for real; `qctl get dry-run-jobs` and `qctl describe dry-run-job` preview a rule against a table and write nothing. `get` and `describe` here are read-only, but `run composite-run-job` queues a real rebuild of one linked table; most runs are not started that way and appear as a side effect of the operation named in their `reason`.
- `describe composite-run-job <id> -o table` renders the backend Markdown in `status.msg`, `status.error_message`, and log messages before embedding `status` in its JSON cell, then prints complete `Message` and `Error Message` sections when those fields are present. JSON/YAML behavior is unchanged, and `-vv` remains the untouched wire response.
- The `reason` values a run can carry are a closed set of twelve, validated where the run is queued and not on this read route: upstream_ingestion_finished, clean_value_overwrite, clean_to_quarantine, quarantine_to_clean, undo, redo, revision_enabled, operator_retry, override_apply, file_ignored, composite_quarantine_release, period_unlocked. `--reason` is an exact string match the server does not check, so a misspelling is an empty page at exit 0, not an error. [backend]
- `--reverse` reverses the SERVER's default, and that default is descending: the route declares `reverse: bool = FastApiQuery(True, ...)`, so the unflagged listing is newest-first and `--reverse` asks for oldest-first. It is the same inversion ~10 other qctl list commands carry. [backend]
- `--state` accepts every value of the shared job-state enum, because that is the type the route's query parameter declares — but a composite run job only ever reaches eight of them: created, pending, running, requeued, finished, stopped, error and killed. Passing blocked, partitioned, file_missing, version_id_mismatch or staged is accepted and matches nothing. [backend]
- `--table` / `--table-id` filter on the CHANGED table — the one whose change triggered the recompute — not on the composite table being rebuilt. A run is anchored by exactly one of a changed table or a set of spec groups, so the group-scoped runs (`--reason revision_enabled`) carry no changed table at all and `--table` hides every one of them; reach those with `--group-id`. [backend]
- `--group-id` matches a run that NAMED that composite spec group at creation and a run that merely resolved it as a target while rebuilding something else, because it tests both the identity set and the resolved-targets set. So it is wider than "runs for this group", and it is the only flag that scopes to a group at all -- `--table` cannot reach a group-scoped run, which has no changed table. [backend]
- A run you can see in the list can still be a 403 on `describe`. The listing narrows a non-ops, non-admin caller to runs anchored in their assigned tables; the detail route additionally refuses unless the caller can read EVERY table the response names, including each downstream table in `target_results` — which the listing never consulted. [backend]
- Composite run job ids reach you from two places today: this listing, and `qctl describe row`'s rule-evidence header, which labels the run behind a verdict `composite-run <id>` beside the `ingestion` and `dry-run` alternatives. A grep of the tree finds no third printer.
- `describe composite-run-job` renders a manifest by default: `spec` is what the invalidation asked for, `status` is what the run has done. `-vv` is the untouched API response. `-v` adds nothing, exactly as on `describe table-rule`.
- `run composite-run-job` QUEUES the rebuild; it does not create the run. The composite run job — reason `operator_retry`, its target the table's composite spec group — is created only when the scheduler claims the queue message, which the server's outbox relay puts on the queue after the command's transaction commits, so it can be missing from `get composite-run-jobs` for a while after the command returns. The `run_job_id` in `-o json`/`-o yaml` is the id of the outbox row that carries that message, a row the relay deletes once it is delivered — not a queue message id and not a composite run job id, so `describe composite-run-job <run_job_id>` shows an unrelated run or none. A retry that lands while another whole-group rebuild of the same table is still waiting in the queue coalesces onto that message when it is relayed: each call still returns its own outbox row's id, and whichever of them reaches the queue last names the run's `reason`. [backend]
- A tripped breaker keeps the table from being rebuilt at all: a composite run drops a blocked table from its targets before building anything (a run left with none ends `stopped`). It trips when the table's failed builds in a row reach its dataset's `max_retry_count` (5 if that cannot be read), and a successful build zeroes the count. A failed build of a table whose breaker had not tripped queues a retry of the same run on its own, 30 seconds later after a first failure and twice as long after each further one, at most 5 minutes. Besides `run composite-run-job`, enabling a revision that is not already enabled resets the breaker and queues its own rebuild, and resolving its alert with `qctl alert resolve` (ops-only when it is a `bug` alert) or deleting its error incident (`qctl explain error-incident`) resets it too and, when it had tripped, queues an `operator_retry` rebuild. [backend]
- `run composite-run-job` resets the breaker whether or not it had tripped — zeroing the failure count — and resolves the table's open `composite_recompute` error incidents and the alerts they raised, before it looks for anything to build. The 409 for a table with no enabled revision or runnable target is raised AFTER that reset is committed, so exit 2 there does not mean nothing happened; the 409 for a table with no active composite source, like a 404 or 403, is raised before any reset. [backend]

## SEE ALSO

- `qctl explain linked-table`
- `qctl explain composite-dry-run-job`
- `qctl explain dry-run-job`
- `qctl explain error-incident`
- `qctl guide link-tables`
