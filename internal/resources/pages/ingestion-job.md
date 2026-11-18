---
summary: a run that loads a file's rows into a table
aliases:
kind:
schema:
guide: run-jobs
signature: 8622a9c
---

## USAGE

    qctl describe ingestion-job <id>
    qctl get ingestion-jobs
    qctl kill ingestion-job [id...]
    qctl run ingestion-job [id...]

## EXAMPLES

    # full detail for one ingestion job
    qctl describe ingestion-job 123

    # every ingestion job in the org
    qctl get ingestion-jobs

    # only the running jobs for one table
    qctl get ingestion-jobs --table-id 5 --state running

    # the same filter by table name (--table is a name, --table-id an ID)
    qctl get ingestion-jobs --table orders --state running

    # the dry runs of one file and of its parts
    qctl get ingestion-jobs --file-id 101 --kind dry_run --kind composite_dry_run

    # stop one job that is stuck or loading the wrong thing
    qctl kill ingestion-job 123

    # stop several in one call
    qctl kill ingestion-job 123 456 789

    # stop everything currently running on one table, after confirming
    qctl kill ingestion-job --filter table-id=5,state=running

    # see exactly which jobs that would hit, without killing them
    qctl kill ingestion-job --dry-list --filter table-id=5,state=running

    # same kill, unattended
    qctl kill ingestion-job --yes --filter table-id=5,state=running

    # put one existing job back on the queue -- run takes job IDs, never a table
    qctl run ingestion-job 123

    # queue several in one call
    qctl run ingestion-job 123 456 789

    # drain a table's pending backlog, after confirming
    qctl run ingestion-job --filter table-id=5,state=pending

    # see which jobs that would re-queue, without queueing anything
    qctl run ingestion-job --dry-list --filter table-id=5

    # same drain, unattended
    qctl run ingestion-job --yes --filter table-id=5,state=pending

## GOTCHAS

- A watch cancels an HTTP poll in flight when its timeout or SIGINT/SIGTERM ends the watch, including a request waiting to retry; a successful response arriving after cancellation is discarded.

- `describe ingestion-job <id>` and every positional ID passed to `run` or `kill ingestion-job` must be numeric; the action commands reject a bad ID locally as bad usage (exit 2), before config, organization, or credentials are loaded.
- `describe ingestion-job <id> -o table` renders `status.message` and its log messages before embedding `status` in its JSON cell, then prints the complete status value under `Message` and rendered log entries in the dedicated `Logs` table. JSON/YAML behavior is unchanged.
- `describe ingestion-job` prints `Next: qctl get dropped-lines <file-id>` on stderr, naming the job's `stored_item_id`, when `status.deleted_lines_count` is positive; the listing is the file's lines as of its latest finished load, not this job's. [backend]
- `run ingestion-job` and `kill ingestion-job` each require job IDs **or** `--filter` (never both, never neither); invalid mode combinations, filter syntax, filter keys, numeric filter IDs, and an `is-dry-run` value that is not a boolean are all bad usage (exit 2) checked before config or credentials.
- `state=` and `states=` inside `--filter`, and `get ingestion-jobs --state`/`--states`, accept only the job states `qctl get ingestion-jobs --help` lists; any other value is bad usage (exit 2) before a request is sent, and the error names the flag or filter key and lists the valid states.
- Action filters accept `table-id`, `cloud-source-id`, `file-id` (or `stored-item-id`), `state`, `states`, `kind`, `kinds` (pipe-separated, like `states`), `is-dry-run`, and `created-by=<user-uuid>`. A filter that names no kind matches only `file`, `dry_run` and `composite_dry_run` jobs, and one naming `quarantine_reprocess` or `composite_carrier` is bad usage (exit 2), checked before config or credentials.
- A server whose `GET /ingestion-jobs` ignores the `kind` and `stored_item_id` query parameters (an atlas from before they were added) would let an action filter match every job its other keys allow, so `run`/`kill ingestion-job --filter`, `--dry-list` included, read every listed job's kind and exit 5 with nothing run or killed when one is outside the filter's kinds, or, for `file-id`, when a listing for file 0 is not empty. Name the jobs by ID on such a server. `get ingestion-jobs --kind`/`--file-id` are not checked and list unfiltered there. [backend]
- `kind` says what a job processes: `file`, `dry_run`, `composite_dry_run`, `quarantine_reprocess` or `composite_carrier`. `file_name` is null for every kind but `file` — blank in the table, `null` under `-o json`/`-o yaml` and in `describe ingestion-job`'s `spec.file_name`, which then names the job `ingestion-job-<id>`. `--file-id <id>` (`file-id=` in an action filter) matches the jobs of that file and of its parts. [backend]
- A `quarantine_reprocess` or `composite_carrier` job can't be run again or stopped: `run` or `kill ingestion-job <id>` on one answers 409 with the server's reason (exit 2), and a request of two or more IDs that names one is refused whole with 409, before any job is run or stopped. [backend]
- `--table <name>` and `--table-id <id>` are `get ingestion-jobs` flags; `table-id=` inside `--filter` is a different thing — a `--filter key=value` payload key, and it takes an ID only. There is no `--filter table=orders`.
- `run ingestion-job` returns as soon as the backend accepts the job onto its queue, not once ingestion finishes — the response is just an acknowledgment; poll `qctl describe ingestion-job <id>` for its actual state. [backend]
- `run ingestion-job <id>` refuses a job whose cloud source is a draft: 400 `Activate the draft source before running ingestion.` (exit 2). Two or more IDs, or a `--filter` matching two or more jobs, go in one bulk request. Unless one is a kind it refuses (above), the server attempts every requested job; if any fail, it answers 500 (exit 1) listing each failed ID and its reason. Successful jobs before and after refusals are queued. [backend]
- `get ingestion-jobs --watch` writes the page again only when it changes, as one stream: `-o json` writes one compact List per line, `-o yaml` one document after each `---`, `-o name` the `ingestion-job/<id>` of each job added, changed or gone from the page, and the table is redrawn in place on a terminal and appended elsewhere; after a poll retry notice, an HTTP retry notice under `--retries`, or verbose HTTP diagnostics under `-vvvvvvv` and above, the next changed table starts below it and later updates redraw there; a json line holds the whole page, so a large `--limit` can outgrow a line reader's buffer, such as the 64 KiB `bufio.Scanner` starts with.
- `get ingestion-jobs --watch` stops after `--timeout`, a duration with a unit (`90s`, `10m`; `0` means no limit) — a bare `60` is bad usage, unlike `--watch-interval`, which counts whole seconds. Left unset it is no limit, from a script or an agent as from a terminal, unless `QCTL_WATCH_TIMEOUT`, in the same syntax, sets a default; an explicit `--timeout` beats it. Running out of time is a clean end: exit 0. `--timeout` without `--watch` (even `--timeout 0`), a negative `--timeout`, and a malformed or negative `QCTL_WATCH_TIMEOUT` when no `--timeout` is given are bad usage (exit 2).
- `get ingestion-jobs --watch` writes its own lines to stderr, so stdout carries only the pages: before the first poll one such as `watching ingestion jobs every 5s, for up to 10m0s; resume with: qctl get ingestion-jobs --timeout 10m0s --watch` under `--timeout 10m` (no `for up to` clause when there is no limit), then `timed out after 10m0s; resume with: …` when time runs out, or `interrupted; resume with: …` on a signal. The resume command repeats every flag you set, global ones such as `--org` and `--output` included, and `--timeout` too, so the resumed watch gets the same limit again.
- `get ingestion-jobs --watch` ends on SIGINT (Ctrl-C) or SIGTERM by re-raising that signal, which a shell reports as exit 130 or 143. A failed first poll ends it with that failure's exit code. A later failure ends it with its own exit code only when retrying cannot help — exit 2, 3, 4 or 5, which is any 4xx but a 429, or an incompatible API; any other, such as a 5xx, a 429 or a network failure, is written to stderr as `poll failed (<error>); retrying in <wait>` and retried, first after twice `--watch-interval`, doubling on each further failure up to 30s (or the interval itself when that is longer), and back on the interval once a poll succeeds.
- states: `created`, `pending`, `requeued` and `running` are in flight; `finished`, `killed`, `stopped` and `file_missing` are final: a user can re-queue `finished`, `stopped` and `file_missing`, as `run ingestion-job` does, but nothing moves `killed` — `run ingestion-job` refuses one with 409 `A killed ingestion job cannot be rerun.` (exit 2); `error`, `blocked` and `version_id_mismatch` are retried by the backend once the cause clears; `partitioned` and `staged` belong to a partitioned mirror upload and settle when its parts merge. `qctl guide run-jobs` has more. [backend]
- `blocked` on a `no_current_migration` alert is normal for an upload made before the table's first schema is applied: profiling must infer the schema first. That alert offers no action; it clears when the first schema is confirmed — `qctl alert resolve <id> --action apply_migration` on the `needs_review_migration` alert profiling raises, or on its own on a table that already holds `migration_policy: apply_asap`, which a client cannot set — and the job then re-queues itself. [backend]

## SEE ALSO

- `qctl explain profiling-job`
- `qctl explain dry-run-job`
- `qctl guide run-jobs`
