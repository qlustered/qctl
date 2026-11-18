---
summary: a copy of a table's configuration into a new table, and the record of what that copy carried
aliases: [dataset-copy, copy-history]
kind:
schema: openapi:DatasetCopySchema
guide:
signature: 5b6d2d6
---

## USAGE

    qctl copy table [table]

## EXAMPLES

    # the plan: the new table's names, each option with its item count, the data sources; copies nothing
    qctl copy table orders --dry-run

    # the plan as one YAML document: what the server offered, and the request a copy would send
    qctl copy table orders --dry-run -o yaml

    # copy under chosen names without a prompt
    qctl copy table orders --name "Orders EU" --table-name orders_eu --yes

    # put the copy into an existing series as an explicitly chosen period
    qctl copy table orders --series "Monthly orders" --period-key 2026-10 --yes

    # preview the names the series gives the copy, and the membership it would request
    qctl copy table orders --series "Monthly orders" --period-key 2026-10 --dry-run -o yaml

    # copy everything but the learned values and the access grants, without a prompt
    qctl copy table --table-id 42 --exclude learned-values,access --yes

    # copy the settings and the rules only
    qctl copy table orders --include rules

    # also copy the data sources eu-sources.yaml lists, each entry resupplying its source's secrets
    qctl copy table orders --data-sources-file eu-sources.yaml --yes

    # the same list from stdin, checked against the plan and copying nothing
    qctl copy table orders --data-sources-file - --dry-run < eu-sources.yaml

    # into another destination and schema
    qctl copy table orders --destination-id 3 --schema-name staging --yes

    # copy, and keep the copy record: dataset_copy lists every item the copy created or skipped
    qctl copy table orders --yes -o yaml

    # only table/<new id>, for a script
    qctl copy table orders --yes -o name

## GOTCHAS

- No route lists copies or reads one back: a copy's record is the `dataset_copy` object in the copy's own response, so `-o yaml` or `-o json` is the only way to keep it, and the default view prints only its items. It holds the source and new table ids, the options carried (`options.include`, sorted, `settings` always among them) and items with `source_object_id`, `target_object_id` and `note`. A `table_series_member` item has action `created`, no source object, and the existing series UUID as its target, with the series name and period in its note. `qctl describe rule-merge` answers 404 for a copy's id: that route reads rule merges only. [backend]
- A copy is also the history a later rule merge between the same two tables reads, in either direction: each rule item the copy `created` records the source rule's configuration, and the merge compares both sides against it (the merge item's `base_snapshot`), so a rule only one side has changed since the copy is not reported as a conflict. A rule with no such record is matched by rule family and columns instead, and a rule the copy `skipped` is paired with nothing. [backend]
- A copy's writes form one transaction: the new table and everything its record lists are committed together. A failure before commit, including a failure to create its destination tables, rolls back the copy. A missing series or one outside the source table's organization returns 404; a period key not in the series frequency's format (`YYYY-MM` monthly, `YYYY-QN` quarterly) returns 422 `SERIES_PERIOD_KEY_INVALID`; a period already held by another table in that series returns 409. Each refuses the entire copy. The member's period dates are derived by the server from the key under the series frequency. The record is always `state: applied` and every item is `created` or `skipped`; `replaced`, `kept`, `conflict` and `failed` are rule-merge actions. [backend]
- A copy carries configuration, never rows or files. The new table's destination tables are created empty, or not at all when the source has no current schema: its `migration_model` item is then `skipped` and the new table learns its schema from its first upload. It has no files enabled for training either, so `qctl run profiling-job` on it, and `qctl alert resolve <id> --action rerun_training`, answer 409 `NO_TRAINING_FILES` until a file of its own is. [backend]
- A copy carries every table-kind tag and the standard fields assigned to live columns that survive in the copy; there is no include option for them. When the copier lacks `dataset:read`, `problem_domain:read`, or `dataset:concept:edit`, the copy succeeds without those assignments and only the settings item's note explains why. If the source has no schema or the copier lacks `migration:list`, column standard fields are skipped. [backend]
- An option you lack a permission for is not an error: its objects are recorded `skipped`, with a note naming the permission. Without `rule:instantiate` every rule and pending suggestion is skipped; without `dataset:third_party:invite` every other user's grant is; a rule or data review-policy override is skipped without `review:policy:edit` or `data_review:policy:edit` respectively, which the member and agent roles do not have. The plan's ITEMS counts already leave these out, so `rules` reads 0 without `rule:instantiate`. [backend]
- You become the new table's owner. `access` carries the other users' grants given through this organization to its active members other than external users — an owner's as `read_only` — when your effective level on the source table is `owner` (always, for admin and ops). It also carries rule and data review-policy overrides that are at least as strict as the organization's policy, and the change-review override when you have `change_review:policy:read`; any grant or override it leaves out is recorded `skipped` with the reason. [backend]
- Rules are copied without a validation dry run: the copy of an `enabled` rule starts `enabled` (or `disabled`, with the reason as its note, when enabling it on the new table fails), every other rule is copied `disabled`, and a rule the new table refuses is `skipped` with the error. Pending suggestions travel with `rules`, except a revalidation candidate, an update or delete of a rule that was not copied, a create that duplicates a copied rule or an earlier suggestion, and a suggestion whose row filter fails validation against the copied schema. [backend]
- The plan's ITEMS counts assume every option is carried and every rule copies: `settings` counts the new schema's columns including the output columns of the rules the copy would create, so leaving `rules` out — or a rule the new table refuses — drops those columns too, and the `anomaly_model` with them when it was trained on one. The `rules` count includes the pending suggestions it carries. [backend]
- Data sources are copied only when `--data-sources-file` lists them; the plan offers every source of the table except deleted, upload (`qluster_ui`), `composite` and table-series ones, and a linked table cannot be copied at all (400). A copied source keeps its source's state unless `state:` says otherwise, and when any copied source ends up `active` the new table's upload source is disabled. The same-files check compares each copy with every ACTIVE source in the organization at the same location — the source it copies and the earlier copies in the same file included — whatever state the copy asks for, and a match is a 400 that copies nothing. For a `simple_url` source the location compared is the URL the entry supplies. [backend]
- A copied source that is not a draft has its connection checked before anything is saved, with only the secrets its entry supplies, and a failure is a 400 that copies nothing. A copy of an S3 source keeps `s3_access_key`, so without its `s3_secret_key` the check signs with an empty secret and fails even on a public bucket; only a source holding neither key is checked unsigned. A non-draft `simple_url` copy needs a HEAD of its URL to answer 200 (a 307 to googleusercontent.com also passes). A draft is never checked, but an invalid `schedule`, or a `simple_url` that does not start with `http` or `https`, is a 400 for a draft too. [backend]
- A copied data source carries its source's bucket, prefix, folder, host, port, region and endpoint, its `s3_access_key` and `sftp_user`, its file options and settings overrides, and its value-map overrides only when `value_maps` is carried. It does not carry the source's `simple_url` or any secret — `s3_secret_key`, `sftp_password`, `sftp_ssh_key`, `sftp_ssh_key_passphrase`, `gs_service_account_key`, `dropbox_access_token`, `gpg_private_key`, `gpg_passphrase`, `file_password` — nor its delta cursor, last-sync time or row counts. [backend]
- The server rewrites `table_name` before storing it: lowercased, spaces, hyphens and dots turned into underscores, other special characters spelled out as words, and cut to 41 characters — `--table-name Orders-EU2025` is stored as `orders_eu2025`. `--schema-name` and `--database-name` are rewritten the same way, up to 63 characters, except that `--schema-name` also gets an underscore between letters and digits. The plan shows the name as sent; read the stored one from the result's `table_name` (`-o yaml`) or `qctl describe table <id>`. A copy into a series sends no names to rewrite: the series sets both. [backend]
- A copy into a series is named by the server, which ignores any `name` and `table_name` the request carries: `<series> · <label>`, the label `Oct 2026` for a monthly key and the key itself, such as `2026-Q4`, for a quarterly one, with ` (2)`, ` (3)` and so on added when an active or disabled table of the organization has that name; and `<series slug>_<key>`, such as `monthly_orders_2026_10`, the slug ASCII-folded and lowercased with every other run of characters an underscore, cut to 37 characters, with `_2`, `_3` and so on added when the copy's destination, database and schema already have that table name. Its upload source is named `<that name> Source`. The copy options read that the plan comes from checks the table name at the source table's location; the copy checks it at its own. A series renames its tables later on its own (`qctl explain table-series`), but never changes their `table_name`. [backend]
- Both the plan and the copy need `dataset:create`, which the editor role does not have, so an editor's `--dry-run` is refused with 403 as well. Copying a data source also needs `source:create`; without it the whole copy is refused with 403 rather than the sources skipped. Adding the copy to a series needs `deliverable:admin`; every current role holding `dataset:create` also holds that permission. [backend]
- `--series` and `--period-key` are required together. The series resolves by exact, case-sensitive name or full UUID in the current organization; `table-series/<uuid>` references are accepted too. The period key is explicit, 1–200 characters on one line without surrounding whitespace. It is not inferred from the table name or advanced automatically. Invalid flags exit 2 before authentication. There are no period-date flags; `--period-start` or `--period-end` exits 2 as an unknown flag. The server names the new table from the series and its period, so `--name` or `--table-name` with `--series` exits 2 before authentication, and the request sends `name` and `table_name` as null. The plan shows the names the server previews for that period, marked `(set by the series)`; the table name is previewed at the source table's location, so with `--destination-id`, `--database-name` or `--schema-name` the copy's `_N` suffix may differ. The plan includes the series UUID and key, and `--dry-run -o json|yaml` includes the same `request.series` object (`series_id` and `period_key`) that a copy would send. The preview also stops `--dry-run` where the copy would stop: a series outside the organization exits 3, a missing `deliverable:admin` exits 4, and a period key the series frequency rejects or a period another table holds exits 2. Dry run reserves nothing, so the period can still be taken before the copy.
- qctl always sends `include` as an explicit list worked out from the plan — with no flag, every option marked DEFAULT or ALWAYS — so the plan's INCLUDED column is exactly what the request carries; `--dry-run -o yaml` shows that request. `--include` with `--exclude`, and `--exclude` of an option the plan marks ALWAYS, exit 2.
- `--data-sources-file` is one YAML document holding a list, one entry per copy, and `-` reads stdin. Each entry needs `source_id`; the other keys are `name`, `state`, `schedule`, the file-selecting keys and the eleven secret keys (`s3_secret_key`, `sftp_password`, `sftp_ssh_key`, `sftp_ssh_key_passphrase`, `gpg_private_key`, `gpg_passphrase`, `file_password`, `gs_service_account_key`, `dropbox_access_token`, `dropbox_authorization_code`, `simple_url`). An unknown or repeated key, a non-list document, a second document and an empty list exit 2. A key set to null counts as left out; `""` clears a non-secret field and is refused for `name`, `state` and every secret.
- A secret whose whole value is `${VAR}` is read from the environment, and an unset, empty or non-UTF-8 `VAR` exits 2 naming it; anything else is taken as written, and a secret holding the mask character `•` exits 2. Quote a secret that starts with `*`, `&` or `!`, holds ` #`, or is `null` or `~` in any case: YAML reads those as an alias, an anchor, a tag, a comment or null. An unknown alias, an anchor no alias uses and a tag other than `!!str` exit 2; the others silently change the secret or drop it out. No secret value reaches the plan (`KEY=(supplied)`), the `--dry-run -o json|yaml` request (`••••••••`, whatever `--allow-plaintext-secrets` says) or an error message.
- Each entry is checked against the plan before any prompt, and a failure exits 2: `source_id` must be a source the plan lists, a file-selecting key (`pattern`, `archive_pattern`, `s3_prefix`, `gs_prefix`, `sftp_folder`, `excel_sheet_name`) must be one the plan lists among that source's DISTINGUISHING FIELDS, and a copy of an ACTIVE source must change at least one of them whatever `state:` it is given — except a `simple_url` source, whose URL the plan shows as `(withheld)` and qctl cannot compare. Names must also differ from earlier copies and the new table's `<name> Source` upload source, where `<name>` is the plan's Name — with `--series`, the name the series sets. A later copy of the same source cannot read the same files as an earlier active copy, even if the later copy is draft or disabled; for a `simple_url` source that means both supply the same `simple_url`.
- An entry whose copy is active or disabled — by its `state:`, or else its source's state — must supply every secret its source holds, which the plan's SECRETS column lists; `dropbox_access_token` stands in for `dropbox_authorization_code`. The plan lists each copy's supplied and missing secrets, then the command exits 2 naming every entry short of one, `--dry-run` included. This is stricter than the server, which saves a copy missing `gpg_private_key`, `gpg_passphrase` or `file_password` that then fails at ingest. Set `state: draft` to save a copy without its secrets.
- When the server rejects one entry, qctl names it as `--data-sources-file entry N (source_id ID)`, counted from 1, with the field under `Field:`; the server's `custom_api_file_url` is shown as `simple_url`, the key the file takes. After the copy, the default view adds a SECRETS TO RESUPPLY column when a copied source lacks one, and for each draft copy a `Next:` line ending in `qctl enable cloud-source <id>`, which first says to add any missing secret to the spec `qctl get cloud-source <id> -o yaml` prints and `qctl apply -f` it.
- Without `--yes` the command asks before copying, and exits 2 when stdin is not a terminal; `--dry-run` with `--yes` exits 2. Under `-o json`, `yaml` or `name` the plan goes to stderr, so stdout carries only the result: the server's response as received, or `table/<new id>`. `--dry-run` refuses `-o name` with exit 2, since no table has an id yet.

## SEE ALSO

- `qctl explain table`
- `qctl explain rule-merge`
- `qctl explain table-rule`
- `qctl explain cloud-source`
- `qctl explain table-series`
