# Connect a cloud bucket as a data source

    qctl apply -f cloud-source.yaml --yes
    qctl describe cloud-source <id>

A **cloud source** is where one table's files come from: S3 or an
S3-compatible store, SFTP, Google Cloud Storage, Dropbox, or a plain URL. It is
applied declaratively, and re-applying the same manifest converges the source
rather than duplicating it. Every field and edge case: `qctl explain
cloud-source`.

## Write the manifest

    # cloud-source.yaml
    apiVersion: qluster.ai/v1
    kind: CloudSource
    metadata:
      name: s3-orders
    spec:
      dataset_id: 11                  # required: the table this source feeds
      data_source_type: s3            # required
      s3_bucket: raw-orders           # required for s3/s3_compatible
      s3_prefix: incoming/
      s3_region_name: us-east-1
      s3_access_key: ${S3_ACCESS_KEY}
      s3_secret_key: ${S3_SECRET_KEY}
      schedule: "@hourly"

The table must already exist (`qctl guide onboard-table`): the source names
its table, never the reverse. Credentials expand `${ENV_VAR}`, so the file can
be checked in. `data_source_type` is one of `s3`, `s3_compatible`, `sftp`,
`gs`, `dropbox`, `simple_url`, `qluster_ui`; `qctl explain cloud-source` says
which fields each one requires. Leave `settings_model_id` out.

## Apply and confirm

    qctl apply cloud-source -f cloud-source.yaml --yes
    qctl get cloud-sources --table-id 11
    qctl describe cloud-source <id>

A missing required field is refused locally, before any request. A new source
is created without a question; changing one previews and asks, so pass
`--yes` only for a change the user asked for.

## Create it without the credentials

    spec:
      state: draft                    # create only

If you do not hold the secrets, add `state: draft` to a new source's spec and
leave the credentials out. The server still checks the schedule and the URL
format, but it does not connect to the source. `apply` prints the two steps
that follow: remove `state`, add the secrets and apply again, then run
`qctl enable cloud-source <id>`. An update whose manifest carries
`spec.state` exits 2.

## Update it

    qctl get cloud-source 42 -o yaml > src.yaml
    qctl apply -f src.yaml --yes

Export, edit, apply — the export carries no credentials. `diff -f` does not
take a `CloudSource`; without `--yes` and off a terminal, `apply` prints the
change list and exits 2 without writing, which is the preview. The file is the
source's whole desired state, so always start from a fresh export:

- An override or `csv_delimiter`/`csv_quotechar`/`csv_escapechar` the file
  leaves out is **cleared**; every other field it leaves out (credentials and
  `schedule` included) is kept. `schedule: ""` stops polling.
- A credential other than `s3_access_key` reads back masked, so a file that
  carries one always shows it as a change. Once the source exists, leave
  credentials out of files you re-apply.
- The update is pinned to the version `apply` just read; if the source
  changed in between, nothing is written — run it again.

Per-source overrides of the table's settings go under `settings_overrides`,
keyed without the `_override` suffix:

    spec:
      settings_overrides:
        null_values: ["N/A"]
        null_values_per_column:
          notes: ["-"]

A list is added to the table's list, a map is merged one key deep, and a
`*_per_column` entry replaces the table's entry for that column.

## Turn it on, off, and kick it

    qctl disable cloud-source 42
    qctl enable cloud-source 42
    qctl run sensor-job 42

`disable` stops polling, uploads and dry runs on the source; `enable` restores
them. Both refuse a composite source (exit 2), naming the linked table that
owns it. Enabling a source that is already active changes nothing. To redeploy a
schedule that stopped firing, disable the source, wait a minute, enable it,
then a minute later confirm with `qctl describe cloud-source 42` that it is
still active, and enable it again if it is not: the scheduler applies a
disable asynchronously, and if it gets to it after the enable it turns the
source off again.
`run sensor-job` takes a cloud source id and checks for new files now. An
upload source (`qluster_ui`) has no sensor: `enable` turns it on for uploads,
and `run sensor-job` refuses it (exit 2).

Removal is soft:

    qctl delete cloud-source 42 --yes
    qctl get cloud-sources --state deleted

Deleting resolves the source's open alerts and keeps its ingested rows and
stored files. Recovery depends on server support: `qctl undelete cloud-source
42` uses the sensor endpoint, which current servers reject with 404 for a
deleted source. Older servers may accept it, returning the source active
without reopening its alerts. Uploading and ingesting through the source:
`qctl guide manage-files`.

## See also

- `qctl explain cloud-source`
- `qctl guide onboard-table`
- `qctl guide manage-files`
