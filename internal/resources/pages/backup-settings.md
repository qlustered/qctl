---
summary: object-storage credentials and targets for table backups
aliases: []
kind: BackupSettings
schema: openapi:BackupSettingsSchemaFull
guide:
signature: cdadb16
---

## USAGE

    qctl apply -f <file.yaml>   # kind: BackupSettings
    qctl apply backup-settings -f <file.yaml>
    qctl describe backup-settings <id>
    qctl get backup-settings

## EXAMPLES

    # every backup configuration in the org
    qctl get backup-settings

    # narrow the list by bucket or backup type
    qctl get backup-settings --search archive

    # a manifest targeting one named or unnamed configuration by ID
    qctl describe backup-settings 7

    # update by metadata.id, or create/update by metadata.name without an ID
    qctl apply backup-settings -f backup-settings.yaml

    # the same manifest through the generic dispatcher
    qctl apply -f backup-settings.yaml

## GOTCHAS

- `apply -f <file> -o name` prints `backup-settings/<id>`, including an unchanged result. Several applied documents print one reference per line in file order; prose goes to stderr.

- Integer fields in a YAML manifest reject fractional numbers during parsing (exit 2): `2.7` is refused instead of becoming `2`. Whole-valued YAML numbers such as `2.0` remain accepted.
- `metadata.id`, when supplied, must be a positive integer and selects an update in the current organization; a failed ID lookup stops apply without a name lookup or create. With an ID, omit or leave `metadata.name` empty to preserve the stored name, or set it to name or rename that configuration. Without an ID, `metadata.name` is required: an exact existing name is updated, and a new name creates another configuration; the name lookup includes configurations beyond the first 200.
- `describe` includes `metadata.id` for named and unnamed configurations. Keep it when editing or naming an unnamed configuration; to copy a configuration, remove `metadata.id` and choose a new `metadata.name`. IDs belong to the selected server and organization, so verify them before applying an exported manifest elsewhere. The manifest's `status.id` and `status.version_id` are readback data: apply ignores them and reads the current version before updating.
- `describe backup-settings` omits access keys, secret keys, private keys, passphrases, and service-account keys from every output format; applying that emitted manifest preserves the stored credentials unless you add those fields explicitly.
- `spec.backup_type` accepts `s3`, `s3_compatible`, or `gs`; qctl requires `backup_s3_bucket` for `s3` and `s3_compatible`, `backup_s3_endpoint_url` for `s3_compatible`, and `backup_gs_bucket` for `gs` before making a request. Creating `s3` or `s3_compatible` backup settings also requires non-empty `backup_s3_access_key` and `backup_s3_secret_key` (exit 2 before POST), including after `${VAR}` expansion; updates may omit them to preserve stored credentials. `s3_compatible` was called `rustfs`, which is no longer accepted.
- The server requires both an access key and a secret key for `s3` and `s3_compatible` backup settings on creation and every update: a configuration still missing either key after an update is refused with HTTP 400 while setting bucket CORS. Supply both keys to repair a keyless configuration. This S3 key requirement does not apply to `gs`. [backend]
- a string field under `spec`, including a credential, reads an environment variable when its entire value is `${VAR}`; every other value keeps its dollar signs literally.
- `--search` matches bucket or backup type and rejects a query shorter than three characters locally; `describe backup-settings <id>` likewise rejects a non-positive or non-numeric ID before any API request.
- there is no `delete backup-settings` command; the CLI exposes only `apply`, `get`, and `describe` for this resource.

## SEE ALSO

- `qctl explain table`
- `qctl explain destination`
- `qctl guide onboard-table`
