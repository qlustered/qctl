---
summary: a run that profiles a table's columns
aliases:
kind:
schema:
guide: run-jobs
signature: 36cf1f7
---

## USAGE

    qctl describe profiling-job <id>
    qctl get profiling-jobs
    qctl kill profiling-job [id...]
    qctl run profiling-job

## EXAMPLES

    # full detail for one profiling job
    qctl describe profiling-job 123

    # every profiling job in the org
    qctl get profiling-jobs

    # only the jobs for one table
    qctl get profiling-jobs --table-id 5

    # the same filter by table name (--table is a name, --table-id an ID)
    qctl get profiling-jobs --table orders

    # stop one in-flight profiling run
    qctl kill profiling-job 123

    # stop several at once; kill takes job IDs, unlike run
    qctl kill profiling-job 123 456 789

    # stop every profiling run on one table, after confirming
    qctl kill profiling-job --filter table-id=5

    # see exactly which jobs that would kill, without killing them
    qctl kill profiling-job --dry-list --filter table-id=5

    # same kill, unattended
    qctl kill profiling-job --yes --filter table-id=5

    # start a fresh profile of one table -- run takes a table, never a job ID
    qctl run profiling-job --table-id 123

    # same, naming the table instead of numbering it
    qctl run profiling-job --table orders

    # same, without the confirmation prompt
    qctl run profiling-job --table-id 123 --yes

    # the files held for a new table's first schema: blocked jobs on a no_current_migration alert
    qctl get ingestion-jobs --table orders --state blocked --columns id,stored_item_id,file_name,alert_issue_type

    # profile one of them instead -- it becomes the table's only file enabled for training
    qctl run profiling-job --table orders --file-id 456

    # filter mode collects the TABLES behind the matched jobs and starts a NEW profile for each
    qctl run profiling-job --filter table-id=5

    # list those table IDs first, launching nothing
    qctl run profiling-job --dry-list --filter table-id=5

    # same fan-out, unattended
    qctl run profiling-job --yes --filter table-id=5

## GOTCHAS

- A watch cancels an HTTP poll in flight when its timeout or SIGINT/SIGTERM ends the watch, including a request waiting to retry; a successful response arriving after cancellation is discarded.

- Bulk `run profiling-job --filter` and `kill profiling-job` attempt every selected target even if one fails. Any failure makes the command exit nonzero, including partial success; the exit code follows the last failure (for example, 3 for a missing target or 4 for forbidden access). Successful targets are reported individually and are not rolled back.
- `describe profiling-job <id>` and every positional ID passed to `kill profiling-job` must be numeric. For the run/kill action commands, malformed `--filter` payloads — and malformed job IDs for kill — are rejected locally as bad usage (exit 2), before qctl loads config or credentials or sends an API request.
- `describe profiling-job <id>` renders Markdown in `status.message`, `status.analysis_tasks[].msg`, and log messages. Table output embeds the rendered fields in its JSON cell, then prints the complete status value under `Message` and rendered log entries in the dedicated `Logs` table; JSON and YAML render the same fields as plain text.
- profiling (called a *training job* backend-side) is a separate run from ingestion — a successful ingest into an already-profiled table does not re-profile it, so bump the profile yourself with `qctl run profiling-job`. Two cases queue one for you instead. A table with no schema and fewer than 4 files uploaded so far blocks the ingestion job and queues profiling for the file in its place — there is no schema yet to ingest against. An ingest that raises an alert requiring learning does more than that: it queues profiling *and* re-queues the same file for a later ingestion attempt, so it is not blocked out of ingesting the way the no-schema case is. Either way, a freshly uploaded file can turn up under `get profiling-jobs` without you asking. [backend]
- `run profiling-job` takes a table (`--table <name>` or `--table-id <id>`) directly, not a job ID, because the backend launches profiling per table rather than per existing job record — unlike `kill profiling-job`, which (like `run`/`kill ingestion-job`) takes job IDs. [backend]
- `run profiling-job` is refused with 409 `NO_TRAINING_FILES` (exit 2), and nothing is queued, when the table has no file enabled for training; `alert resolve <id> --action rerun_training` is refused the same way. The server enables a file for training when it first adds it, as a side effect of other work — an upload to a table with no schema and fewer than 4 files, or an ingest that raises an alert needing learning, among others — and profiling switches a file off when its analysis comes out empty or repeats another file's. The one request that picks a file is `run profiling-job --table <table> --file-id <id>`, which sends it as `stored_item_id`: it makes a file held for the table's first schema the only one enabled and profiles it, and is refused, exit 2, with 409 `PERIOD_LOCKED` on a locked period, 409 once the table has a schema, 409 `ERR_MIGRATION_CONFLICT` while a profiling job of the table is queued, running or blocked, and 422 for a file not held that way or whose content repeats another training file's. [backend]
- `run profiling-job` and `kill profiling-job` each require a target (a table for `run`, job IDs for `kill`) **or** `--filter`, never both and never neither — supplying neither errors before any request is sent.
- `--table <name>` and `--table-id <id>` are flags; `table-id=` inside `--filter` is a different thing — a `--filter key=value` payload key, and it takes an ID only. There is no `--filter table=orders`.
- `get profiling-jobs --watch` writes the page again only when it changes, as one stream: `-o json` writes one compact List per line, `-o yaml` one document after each `---`, `-o name` the `profiling-job/<id>` of each job added, changed or gone from the page, and the table is redrawn in place on a terminal and appended elsewhere; after a poll retry notice, an HTTP retry notice under `--retries`, or verbose HTTP diagnostics under `-vvvvvvv` and above, the next changed table starts below it and later updates redraw there; a json line holds the whole page, so a large `--limit` can outgrow a line reader's buffer, such as the 64 KiB `bufio.Scanner` starts with.
- `get profiling-jobs --watch` stops after `--timeout`, a duration with a unit (`90s`, `10m`; `0` means no limit) — a bare `60` is bad usage, unlike `--watch-interval`, which counts whole seconds. Left unset it is no limit, from a script or an agent as from a terminal, unless `QCTL_WATCH_TIMEOUT`, in the same syntax, sets a default; an explicit `--timeout` beats it. Running out of time is a clean end: exit 0. `--timeout` without `--watch` (even `--timeout 0`), a negative `--timeout`, and a malformed or negative `QCTL_WATCH_TIMEOUT` when no `--timeout` is given are bad usage (exit 2).
- `get profiling-jobs --watch` writes its own lines to stderr, so stdout carries only the pages: before the first poll one such as `watching profiling jobs every 5s, for up to 10m0s; resume with: qctl get profiling-jobs --timeout 10m0s --watch` under `--timeout 10m` (no `for up to` clause when there is no limit), then `timed out after 10m0s; resume with: …` when time runs out, or `interrupted; resume with: …` on a signal. The resume command repeats every flag you set, global ones such as `--org` and `--output` included, and `--timeout` too, so the resumed watch gets the same limit again.
- `get profiling-jobs --watch` ends on SIGINT (Ctrl-C) or SIGTERM by re-raising that signal, which a shell reports as exit 130 or 143. A failed first poll ends it with that failure's exit code. A later failure ends it with its own exit code only when retrying cannot help — exit 2, 3, 4 or 5, which is any 4xx but a 429, or an incompatible API; any other, such as a 5xx, a 429 or a network failure, is written to stderr as `poll failed (<error>); retrying in <wait>` and retried, first after twice `--watch-interval`, doubling on each further failure up to 30s (or the interval itself when that is longer), and back on the interval once a poll succeeds.
- for a caller who sees the table's PII masked — no `pii:view` and no active PII access grant on the table — each PII value a profiling job's message quotes prints as `•••••`, or `•••••` and its last 4 characters: in `get profiling-jobs`' MSG column, and in `describe profiling-job`'s message, log lines and analysis-task messages. [backend]

## SEE ALSO

- `qctl explain ingestion-job`
- `qctl explain dry-run-job`
- `qctl guide run-jobs`
