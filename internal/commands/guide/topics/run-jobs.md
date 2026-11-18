# Run and watch jobs

Find the job, preview what a filter matches, run or kill it with `--yes`, and
watch with a `--timeout`. Re-run a job only if the user asked: an ingestion
job in `error` or `blocked` is retried automatically once what blocks it
clears.

## Which job is this

| Job | Started by | Follow it with |
|-----|-----------|----------------|
| ingestion | an upload, the source's schedule, `qctl run ingestion-job` | `qctl get ingestion-jobs`, `qctl describe ingestion-job <id>` |
| profiling | the first upload, `qctl run profiling-job` | `qctl get profiling-jobs` |
| sensor (check a source for new files; upload and composite sources have none) | the schedule, `qctl run sensor-job <cloud-source-id>` | the ingestion jobs it queues |
| rule dry run | `qctl create dry-run-job` | `qctl guide dry-run-jobs` |
| rule-run replay | `qctl run rule-run-replay <rule-run-id>` | `qctl describe rule-run-replay <id>`; its dry run as above |
| composite (linked table) dry run | `qctl apply linked-table`, `qctl create composite-dry-run-job` | `qctl get composite-dry-run-jobs`, `qctl inspect composite-dry-run-job <id>` |
| composite (linked table) build | enabling a linked table, `qctl run composite-run-job <table>` | `qctl get composite-run-jobs` |

## Ingestion states

Final: `finished`, `killed`, `stopped`, `file_missing`. The backend never
retries them; `qctl run ingestion-job` re-queues any of them except `killed`,
which it refuses.

`error` and `blocked` are not final. `blocked` means the job waits on an alert:
its own, or a blocker on its table, source or file — `qctl get latest-alert
--table <table>` names it. About five minutes after the alert is resolved, the
scheduler re-queues the job by itself (while the source is active and the job
is under its retry limit), so resolving the alert is usually the whole fix.

## Run ingestion jobs

Preview a filter, then run it:

```bash
qctl run ingestion-job --dry-list --filter table-id=5,state=error
qctl run ingestion-job --yes --filter table-id=5,state=error
qctl run ingestion-job 123 456 --yes
```

Filter keys: `table-id`, `cloud-source-id`, `file-id` (or `stored-item-id`;
the file's jobs and its parts' jobs), `state`, `states` (pipe-separated,
`states=pending|error`), `kind`, `kinds` (pipe-separated,
`kinds=file|dry_run`), `is-dry-run`, `created-by` (a user UUID). A filter
that names no kind matches only `composite_dry_run`, `dry_run` and `file`
jobs; a `quarantine_reprocess` or `composite_carrier` job can't be run again
or stopped, and a filter naming either kind is bad usage (exit 2), refused
before config or credentials are loaded. IDs and `--filter` cannot be
combined. A run re-queues every job it is given to reprocess its file,
whatever its state — `finished` included — except a `killed` job, which it
refuses (409, exit 2), and a `quarantine_reprocess` or `composite_carrier`
job given by ID, which it refuses (409, exit 2) and which makes a request of
two or more IDs run none of them, so always narrow a filter with `state=` or
`states=`.

## Run profiling jobs

Profiling runs per table:

```bash
qctl run profiling-job --table orders --yes
qctl run profiling-job --dry-list --filter table-id=5
```

A filter (`table-id`, `search`) matches existing profiling jobs and starts one
new run per table they belong to.

## Stop jobs

```bash
qctl kill ingestion-job 123 --yes
qctl kill profiling-job --dry-list --filter table-id=5
```

## Watch progress

```bash
qctl get ingestion-jobs --table orders --watch --timeout 10m
qctl get profiling-jobs --watch --timeout 30m
```

Always pass `--timeout`: a list watch has no end of its own. It reprints the
list on each change and exits 0 at `--timeout` after `timed out after
<duration>; resume with: <command>` on stderr.

## See also

- `qctl guide dry-run-jobs`
- `qctl guide unblock-table`
- `qctl guide handle-incidents`
