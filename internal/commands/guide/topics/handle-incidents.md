# Handle ingestion and processing failures

    qctl get error-incidents
    qctl describe error-incident <id>
    qctl get ingestion-jobs --table-id <table-id> --state error
    qctl run ingestion-job --dry-list --filter table-id=<table-id>,state=error
    qctl run ingestion-job --filter table-id=<table-id>,state=error --yes
    qctl delete error-incident <id>

An error incident is the platform failing to *do* something, such as a sensor
erroring out or an ingestion job blowing up, as opposed to a table rule finding
something wrong with the data. It has no severity and no resolved state: a
record of a failure and a count of how many times it happened. Find it, trace
it to the file or job behind it, retry the work, then delete the record.

All three error-incident commands require the ops-only debug-view permission;
any other account gets an error from each, not an empty list.

## See what failed

    qctl get error-incidents --columns id,module,msg,job_name,dataset_name,count,created_at
    qctl get error-incidents --module sensor
    qctl get error-incidents --job-name sensor-1
    qctl get error-incidents --search "AccessDenied"

The default columns are module, message, job name, recurrence count and
creation time; `dataset_id` and `dataset_name` exist but must be asked for.
`--deleted` lists only incidents someone already closed out.

## Read one and trace it

    qctl describe error-incident <id>
    qctl describe file <stored-item-id>
    qctl get ingestion-jobs --table-id <dataset-id> --states error,killed

`describe error-incident` prints the error class (`spec.error`), message,
module and count, plus `spec.dataset_id` (the table) and `spec.stored_item_id`
(the file) when the platform recorded them; a sensor error may have neither.
`describe file` shows the file's cloud source, delimiter, encoding and backup
key; `get ingestion-jobs --table-id` lists the table's jobs, narrowed to the
failed ones by `--state` or `--states`.

## Retry the work

    qctl run ingestion-job --dry-list --filter table-id=<id>,state=error
    qctl run ingestion-job --filter table-id=<id>,state=error --yes
    qctl run ingestion-job <job-id> --yes

`get ingestion-jobs` takes the table as its own `--table`/`--table-id` flag,
but `run ingestion-job` has neither: the table is a `table-id=<id>` pair
inside `--filter`, and that key takes an ID only. Preview with `--dry-list`
first. Job IDs and `--filter` are mutually exclusive. A `killed` job cannot
be re-run: the run refuses it.

A `composite_recompute` incident records a failed rebuild of a linked table,
and its `spec.dataset_id` is that linked table. Fix what made the build fail,
then queue the rebuild by that ID:

    qctl get error-incidents --module composite_recompute --columns id,dataset_id,dataset_name,msg,count
    qctl run composite-run-job --table-id <dataset-id>

`run composite-run-job` also resolves the table's open `composite_recompute`
incidents and their alerts, so there is nothing left to close out. The build
circuit breaker that repeated failures trip: `qctl explain composite-run-job`.

## Close it out

    qctl delete error-incident <id>

Closing an incident out means deleting the record. The delete also resolves
the incident's linked alert and, when that alert is on a linked table, resets
the table's build circuit breaker; it launches no job, so fix the failure
first. `get error-incidents --deleted` still lists it.

## See also

- `qctl explain error-incident`
- `qctl explain file`
- `qctl explain composite-run-job`
- `qctl guide run-jobs`
