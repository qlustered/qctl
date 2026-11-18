# Get a file in, and back out again

A **file** in qctl is a *stored item*: a raw file uploaded to backup storage
and published for ingestion into a table. This guide walks its whole
lifecycle — upload, watch the ingestion job pick it up, inspect what landed,
pull the original back down, and, if it turns out to be wrong, take its rows
back out while keeping the raw file.

## Upload

    qctl upload file ./orders.csv --table orders --yes

`--table` (or `--table-id`) is required unless a series cloud source and period
are given directly as shown below. A cloud source is required only if the
table has more than one — with a single cloud source configured, `--cloud-source`
/ `--cloud-source-id` can be omitted and qctl resolves it for you. A new table
starts with exactly one, the upload source the server creates with it. Upload
prints an organization/table/cloud-source/file summary and asks for
confirmation before it does anything; pass `--yes` to skip the prompt. On
success it prints the new `stored_item_id` — that's the `<id>` every file
command below takes — and the `ingestion_job_id` of the job it queued.

For a series cloud source whose `routing_mode` is `uploader_chooses`, choose
a period directly with its source ID and the exact period key:

    qctl upload file ./q3.csv --cloud-source-id 456 --period 2026-Q3 --yes

qctl resolves the period's table before confirmation. With `--table` or
`--table-id` instead, it uses that table's own period, and a `--period` that
differs exits 2. Other series routing modes take no `--period` and route the
file after upload.

When the upload would replace a file already loaded for its period, qctl says
which one after the bytes are up and asks before publishing; a yes means
ingestion does not hold the file for that replacement. `--yes` does not answer
that question: the file is published unconfirmed and held on an alert. To
confirm in a script, name the loaded file it replaces:

    qctl upload file ./q3.csv --cloud-source-id 456 --period 2026-Q3 --yes --replaces 812

A `mirror` table with `key_columns` can also take a *partial* file — one
change window from a CDC feed such as Airbyte — instead of a full snapshot:

    qctl upload file ./delta.csv --table orders --delta --delta-cursor 1042 --yes

Only the keys in the file change: a row with a non-empty `_ab_cdc_deleted_at`
deletes its key, every other row is an upsert, and keys the file does not carry
are left alone. The cursor must be greater than the cloud source's
`delta_applied_cursor` (shown by `qctl describe cloud-source <id>`), which moves
up only when a delta job finishes — so deliver windows in cursor order.
A series cloud source keeps that cursor per period instead, and checks the
period the file goes to.
`qctl explain file` lists what is refused before the upload and what only at
ingest.

## Watch it land

Uploading only queues the file; an ingestion job does the actual work of
turning it into rows, and `upload file` returns without waiting for it. Its
last line names the job it queued:

    qctl describe ingestion-job <ingestion-job-id>

To see that job among the table's others, and the file once it lands, both
commands below take the table either way — `--table <name>` for the name,
`--table-id <id>` for the ID. The name is never parsed as an ID, so a table
literally named `42` is reachable:

    qctl get ingestion-jobs --table orders

    qctl get files --table-id <table-id>

`get ingestion-jobs` shows the job's `state`; add `--watch --timeout 10m` to
poll it. `finished`, `killed`, `stopped` and `file_missing` are final;
`blocked` waits on an alert (`qctl guide run-jobs`). Once the job finishes,
the file shows up in `get files` with `bad_rows_count` and `clean_rows_count`
filled in.

## The first upload to a new table

A new table has no schema yet, so its first ingestion job does not load rows
straight away. This is normal, not a failure:

1. The upload raises a `no_current_migration` alert and the job goes
   `blocked`: the file has to be profiled first.
2. Profiling runs on its own and proposes a schema. It may raise
   `initial_fields` (map the file's column names) and then
   `needs_review_migration` (approve the schema; a mirror or append table
   chooses its key columns there, with `--key-columns` or `--no-key-columns`).
3. Resolve those alerts (`qctl guide triage-alerts`; for the schema,
   `qctl guide inspect-schema`). The blocked job is re-queued by itself a few
   minutes after its alerts clear.

If profiling the first file fails, the table's other files stay blocked. Pick
another of them to profile instead (`qctl explain profiling-job`):

```bash
qctl get ingestion-jobs --table orders --state blocked --columns id,stored_item_id,file_name,alert_issue_type
qctl run profiling-job --table orders --file-id 456 --yes
```

```bash
qctl get latest-alert --table orders
qctl describe table orders
```

`describe table` lists what blocks the table under BLOCKING. If an alert
offers no action that fits, report it to the user rather than deleting the
file or the table.

## Inspect

    qctl describe file <id>

Default output is a YAML `kind: File` document with fields like
`is_trashed`, `backup_key`, and the CSV parsing settings (`csv_delimiter`,
`csv_escapechar`, `csv_quotechar`, `header_line_number_for_file`) that were in
effect when it was ingested. It is read-only: `qctl apply` refuses
`kind: File` (exit 2). `delete file` and `undelete file` below are how to
change `is_trashed`.

A new file can ask whether it replaces an older file of the same cloud source
that never loaded. `describe file` then shows the question under
`status.replacement_question` and names the two commands that answer it, on
the NEW file's id:

    qctl file replace-older <id>   # the older file is discarded as replaced
    qctl file keep-both <id>       # the older file is left as it is

Ask the user which one is meant; `replace-older` needs `--context` on a
protected context.

## Send a file's issues report

    qctl file send-issues-report <id>

Queues an email of the file's issues report to the broker who emailed it, or
for a portal upload to its uploader. Every run sends another email, so run it
once per email you mean to send. A refusal exits 2 with a `Hint:` saying
whether the file is not ready, its table series' rules lack the MGA's current
approval, or its sender cannot be emailed (`qctl explain file --grep
send-issues-report`).

## Download a file's issues report

    qctl download issues-report <id>

Builds the file's issues report workbook on the server and writes it to the
current directory once it is ready, waiting up to `--wait-timeout`. A file
that is not ready is refused with exit 2 and a `Hint:`, whether the server
refuses the request or the report when it runs; `--no-wait` queues it and
returns (`qctl explain file --grep issues-report`).

## Get the original back

    qctl download file <id>

Downloads the original raw file from backup storage to the current directory
under its original filename. Use `--output <path>` to choose where it lands,
and `--force` to overwrite a file already there.

## Ignore / restore

If a file turns out to be bad, `delete file` marks it ignored and
**hard-deletes** the rows it produced, clean and quarantined, from the
destination table — a row another file also produced stays — and resolves
each of their alerts that no remaining quarantined row carries. Edits made to
those rows are lost. Only the raw file is kept, in backup storage:

    qctl delete file <id> --yes

`undelete file` reverses the flag — it sets the file back to not-ignored:

    qctl undelete file <id> --yes

Deleted files drop out of `qctl get files`, which lists active files only. List
them with:

    qctl get files --deleted

That returns the deleted files only — the two sets never come back together —
which is how you find the ID to undelete.

The ignored flag is the one thing these two commands exactly invert. The rows
are rebuilt, not restored: un-ignoring a file makes the server queue a fresh
ingestion job that loads the raw file again, so you do not re-run ingestion by
hand. That job can land
blocked (e.g. the table still needs profiling), so if the rows do not come
back, look at `qctl get ingestion-jobs --table-id <table-id>`.

## See also

- `qctl explain file`
- `qctl guide onboard-table`
- `qctl guide run-jobs`
