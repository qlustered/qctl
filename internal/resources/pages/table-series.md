---
summary: a named set of tables, one for each explicit reporting period
aliases:
kind: TableSeries
schema: openapi:TableSeriesSchema
guide: deliverables
signature: 1456126
---

## USAGE

    qctl apply -f <file.yaml>   # kind: TableSeries
    qctl delete table-series <name-or-id>
    qctl describe table-series <name-or-id>
    qctl diff -f <file.yaml>   # kind: TableSeries
    qctl get table-series [name-or-id]
    qctl get table-series-suggestion [file-name]
    qctl table-series approve-rules <name-or-id> [--fingerprint <sha256>]
    qctl table-series delete-period <name-or-id> --period <key> --period <period>
    qctl table-series lock <name-or-id> --period <key> --period <period>
    qctl table-series recreate-period <name-or-id> --period <key> --period <period>
    qctl table-series unlock <name-or-id> --period <key> --period <period>

## EXAMPLES

    # list the current organization's series and period tables
    qctl get table-series

    # discover series upload targets, including their cloud-source IDs and selectable periods
    qctl get table-series --uploadable

    # export upload targets and their API fields in a List envelope
    qctl get table-series --uploadable -o json

    # upload through a listed uploader_chooses source to a listed period
    qctl upload file reports.csv --cloud-source-id 42 --period 2026-10

    # suggest a series and period from a file name, with the names its period's table would get
    qctl get table-series-suggestion "Orders export 2026-11-03.csv"

    # pick the period key for a copy from that file, keeping your own series name
    qctl get table-series-suggestion "Orders export 2026-11-03.csv" --series-name "Monthly orders" -o json

    # read one series by its exact name
    qctl get table-series "Monthly bordereau"

    # show its description, frequency, first period, timestamps, members, who locked each period, and the deadline schedule of the period being collected
    qctl describe table-series "Monthly bordereau"

    # show the deadline schedule with the dates of one period instead
    qctl describe table-series "Monthly bordereau" --period 2026-09

    # show each series' frequency and first expected period
    qctl get table-series --columns name,frequency,first_period_key

    # inspect each period's lock state, locker and lock time
    qctl get table-series "Monthly bordereau" --columns period_key,table_id,is_locked,locked_by,locked_at

    # list accessible submission periods without series-admin or table-detail reads
    qctl get table-series 550e8400-e29b-41d4-a716-446655440000 --periods

    # export period keys, dates, table IDs and lock state in a List envelope
    qctl get table-series table-series/550e8400-e29b-41d4-a716-446655440000 --periods -o json

    # lock a period after its jobs finish
    qctl table-series lock "Monthly bordereau" --period 2026-09 --yes

    # reopen a period to changes and submissions
    qctl table-series unlock "Monthly bordereau" --period 2026-09 --yes

    # list the files a recreate of a period can reload (Qluster ops)
    qctl get table-series "Monthly bordereau" --reloadable-files --period 2026-09

    # recreate a period from its own table and reload every file (Qluster ops)
    qctl table-series recreate-period "Monthly bordereau" --period 2026-09 --reload-all-files --yes

    # recreate a period whose layout changed, learning its schema from the first reloaded file
    qctl table-series recreate-period "Monthly bordereau" --period 2026-09 --schema learn-from-file --reload-all-files --yes

    # delete a period; its table is soft-deleted and recoverable under the printed table name (Qluster ops)
    qctl table-series delete-period "Monthly bordereau" --period 2026-09 --yes

    # check whether the latest approval still matches the series' rules
    qctl get table-series "Monthly bordereau" --rule-approval

    # read the full rule set and every approval, newest first
    qctl get table-series "Monthly bordereau" --rule-approval -o yaml

    # approve the rule set shown in the preview, after a y/N prompt
    qctl table-series approve-rules "Monthly bordereau" --context prod

    # approve only the fingerprint you reviewed, without a prompt
    qctl table-series approve-rules "Monthly bordereau" --fingerprint 3f2a9c8e1b7d4f6a0c5e9b2d8f1a7c3e6b0d4f9a2c8e5b1d7f3a6c0e9b4d2f8a --context prod --yes

    # export a manifest, including a null description and full table IDs
    qctl get table-series "Monthly bordereau" -o yaml

    # export a manifest with the deadline schedule's overrides and switches, to edit spec.deadline_schedule
    qctl get table-series "Monthly bordereau" -o yaml > table-series.yaml

    # apply the edited schedule: one PATCH of the changed keys, sent after every other write
    qctl apply -f table-series.yaml

    # inspect its API record plus deadline_schedule
    qctl describe table-series "Monthly bordereau" -o json

    # preview the ordered series, membership and schedule writes, and the schedule values and dates they change
    qctl diff -f table-series.yaml

    # apply a manifest, confirming any omitted members' removal
    qctl apply -f table-series.yaml --yes

    # delete an empty series with no deliverable bindings
    qctl delete table-series "Unused series" --yes --context dev

    # list complete records in a List envelope
    qctl get table-series -o json

    # obtain one reusable reference per series
    qctl get table-series -o name

    # pass a reference back to describe
    qctl describe table-series table-series/550e8400-e29b-41d4-a716-446655440000

## GOTCHAS

- `get table-series --uploadable` takes no series reference and conflicts with `--periods` and `--rule-approval`. Its table shows `name`, `frequency`, `cloud_source_id`, `routing_mode`, `period_key`, `label` and `table_id`; `--columns id,...` also shows the series UUID. Each selectable period is a row, with `-` for a table the upload will create; a source with automatic routing gets one row with `-` for the period, label and table ID. JSON/YAML preserve the API records in a List envelope, with `data_source_model_id` for the cloud-source ID and `dataset_id` for a period's nullable table ID. `-o name` prints one `table-series/<uuid>` per series. Use `upload file <path> --cloud-source-id <cloud_source_id> --period <period_key>` for `uploader_chooses`, and omit `--period` for other routing modes.
- The uploadable listing requires `dataset:ingest:upload`, orders series by name and selects one active series source per series. For `uploader_chooses`, it returns selectable unlocked periods newest first, including periods whose table the upload creates (`dataset_id: null`), and omits a series with no selectable periods. Other routing modes have an empty `periods` list. [backend]
- `get table-series-suggestion [file-name]` sends only the text after the argument's last `/` or `\`, never reads the file, and writes nothing. `--series-name`, `--frequency` and `--period-key` replace what the file name suggests; with no file name and none of them it exits 2 before authentication, as it does for a frequency other than `monthly` or `quarterly`, a padded or multi-line period key, an empty series name or one over 200 characters. The table is one row of `series_name`, `frequency`, `period_key`, `period_source`, `name` and `table_name`, `-` for a null; JSON/YAML print the server's object and `-o name` exits 2.
- The series suggestion requires `dataset:create` and `deliverable:admin`; a refusal exits 4. The frequency is `--frequency`, else the one `--period-key` is a key of, monthly first, else the one the file name's period tokens suggest (quarterly when every one names a quarter), else monthly. A period key that is no key of that frequency, or of either frequency without one, returns 422 `SERIES_PERIOD_KEY_INVALID` (exit 2). In the file name, a full date is an export date and suggests the period before the one holding it, and a month or quarter without a year is its most recent occurrence that started by today's date in the organization's time zone; tokens that disagree suggest no period. `period_source` is `export_date` when only export dates named the period, `file_name` otherwise, and null for a given `--period-key`. `name` and `table_name` are null unless the series name is non-blank and there is a period; `name` gets the ` (N)` suffix of a taken table name, `table_name` never does. [backend]

- `apply -f <file> -o name` prints `table-series/<uuid>`, including an unchanged result. Several applied documents print one reference per line in file order; prose goes to stderr.

- Names match exactly and case-sensitively; there is no substring or UUID-prefix matching. A full UUID goes straight to the detail route. A missing name exits 3; an empty or whitespace-only reference exits 2 before authentication.
- `get table-series` prints one table row per member and keeps empty series visible. `--columns id` shows the series UUID; `table_id` is the member table's numeric ID. `-o name` prints each series reference once, regardless of its member count. The default columns include the series' `frequency`. `first_period_key` is not a default column: name it in `--columns`, which replaces the default set; it shows `-` when unset.
- The member grids in `get table-series [name-or-id]` and `describe table-series <name-or-id>` include `is_locked`, `locked_by` and `locked_at`. Locker names fall back to email, then UUID; missing lock details and unlocked periods show `-`. Lock timestamps use UTC RFC3339. A selected series's JSON/YAML manifest omits lock metadata; use `describe -o json|yaml` for the API lock details.
- Without `--periods`, `get table-series -o json|yaml` emits a `kind: List` envelope of API records. With a name or UUID, it exports an apply-compatible `kind: TableSeries` manifest keyed by `metadata.name`, with numeric table IDs as `spec.members[].table`. `describe` emits the API record plus one top-level `deadline_schedule` key, the schedule read's response, in JSON/YAML; it defaults to text details, the member grid and a Deadlines section, and `-o name` prints one reference without reading the schedule.
- `describe table-series` prints `Period column: <name> (<kind>)` when the series has a period column and omits the line otherwise. A listing's and `describe`'s JSON/YAML carry `period_column` and `period_column_kind`, both `null` when unset. A selected series's manifest exports both as `spec.period_column` and `spec.period_column_kind`, including nulls. Omit both to preserve the setting; supply both to set it or both as `null` to clear it. A missing partner, mixed null and non-null values, or a kind other than `period_key` or `date` exits 2 before authentication.
- Apply sends a changed period-column pair in a separate PATCH after all membership changes, including on a newly created series. Diff shows that same order. A failed final PATCH leaves earlier changes applied; inspect the result, correct the manifest and reapply.
- `period_column` names the schema field holding each row's period, and `period_column_kind` is `period_key` or `date`. The two are always both set or both null. [backend]
- A PATCH changing the effective period-column pair returns 400 `SERIES_PERIOD_COLUMN_INCOMPLETE` if only one value would be null, or 409 `SERIES_PERIOD_COLUMN_NOT_IN_SCHEMA` if the column is absent from any undeleted member table's schema. These refusals exit 2. An unchanged pair is not revalidated, and membership writes do not recheck the setting. [backend]
- `get table-series <uuid> --periods` reads only the submission-periods endpoint. A full UUID or `table-series/<uuid>` reference is required; names and missing IDs exit 2 before authentication. Find `table_series_id` in `describe cloud-source <id>`. Default columns are `period_key,dataset_id,period_start,period_end,is_locked`; `--columns` can add `creatable`, true for a period with no table yet, whose `dataset_id` shows `-`. JSON/YAML emit a List of period API records, including lock metadata when supplied. `-o name` prints `table/<dataset_id>` per period that has a table. Use the listed key with `upload file --cloud-source-id <id> --period <key>` or `file set-period <file-id> <key>`.
- A `TableSeries` manifest uses `apiVersion: qluster.ai/v1`, `metadata.name`, and `spec.members` entries with `table` (exact name or numeric ID) and `period_key`. A member's `period_start` or `period_end` is an unknown field and exits 2 before authentication: the dates are read-only output. `spec.members` must be a list; `[]` removes all members. Duplicate period keys or resolved table IDs exit 2 before writes. Numeric table names require their ID.
- A member named by its table name stops resolving once an apply has added it, moved it or changed its `period_key`, because the server renames the table then: the next apply or diff of the same file exits 3 before any write, naming the table it cannot find. Apply prints a `Note:` on stderr for each such member, naming its table ID. Name members by numeric table ID, as the export does.
- `describe table-series` prints `Frequency: <frequency>`, then `First period: <key>` only when the series has one.
- `describe table-series <name-or-id> [--period <key>]` reads the deadline schedule after the series and prints, after the member grid and also for an empty series, `Deadlines: period <key> (<start> to <end>), time zone <zone>`, ending `, the period being collected` when `--period` is absent. A `STEP DAYS ENABLED DATE` table and an `ESCALATION THRESHOLD ENABLED` table follow; a value reads `<n> (default)` or `<n> (override, default <d>)`, and a cell a row lacks shows `-`. `--columns` and `--no-headers` apply only to the member grid.
- `describe table-series --period` sends the key as typed and never computes one; an empty, padded, multi-line or over-200-character key exits 2 before authentication. When the series reads but its schedule does not, the view prints without the Deadlines section, `deadline_schedule: null` in JSON/YAML, and the command exits with the schedule error's code: a 422 with 2, a 5xx with 1.
- `spec.frequency` (`monthly` or `quarterly`) and `spec.first_period_key` follow `spec.description`: an export carries both, `first_period_key` as `null` when unset, and an omitted key preserves the server value; on create an omitted frequency is monthly. Explicit `first_period_key: null` clears it. `frequency: null`, any other frequency value, or a `first_period_key` that is empty, longer than 200 characters, multi-line or padded with whitespace exits 2 before authentication; qctl does not check the key's format against the frequency.
- Create sends `frequency` and `first_period_key` on the series POST. On update, a changed `description`, `frequency` or `first_period_key` goes in one series PATCH: before the membership writes, or, when the frequency changes, after every member removal and before any addition or move. Diff shows that same order. A frequency change exits 2 before any write when `spec.members` keeps any of the series' current tables, naming their IDs, or when the series has a `first_period_key` the manifest omits or leaves unchanged; set it to a key of the new frequency or `null`. Otherwise apply removes every current table, changes the frequency and adds the listed tables, provided the series' cloud sources were deleted first. A refused removal or PATCH stops the apply with the earlier removals done, which can leave the series empty on its old frequency.
- `spec.deadline_schedule` holds the series' deadline schedule under the schedule PATCH's own keys, flat: seven day values (`mga_deadline_days`, `broker_due_days`, `first_reminder_days`, `late_notice_days`, `late_repeat_days`, `issues_open_days`, `at_risk_days`), four escalation thresholds (`still_failing_files`, `still_failing_days`, `failing_share_percent`, `not_loaded_days`) and twelve `*_enabled` switches. `get table-series <name-or-id> -o json|yaml` reads the schedule and exports all eleven day values and thresholds, each stored override or `null` where none is stored, and all twelve switches, never the values in effect, the defaults or the dates; a failed schedule read fails the export with nothing on stdout. A read-shaped key such as `mga_deadline: {days: 40}`, an unknown key, or a value that is not a whole number or a boolean exits 2 before authentication; qctl leaves the ranges to the server.
- Omit `spec.deadline_schedule` to leave the schedule alone: apply and diff then neither read nor write it. Inside the block, a key left out keeps its stored value, a day value, threshold or switch alike; a day value or threshold set to `null` goes back to its default. `deadline_schedule: null`, an empty value, an empty block `{}`, which would change nothing, and a switch set to `null` exit 2 before authentication. An export re-applies with no writes.
- Apply and diff plan the schedule as one `PATCH /table-series/<id>/deadline-schedule` carrying only the keys that differ from the stored schedule, after every series, membership and period-column write. On a create it targets `<created-series-id>` and carries every non-null key of the block, a `null` there being the new series' default already; a block with none sends no schedule request. A refused schedule PATCH stops the apply with the earlier writes kept. `apply -o json|yaml` carries `spec.deadline_schedule` only when the apply read or wrote the schedule.
- A 422 `SERIES_DEADLINE_SCHEDULE_INVALID`, from a schedule PATCH or preview, a frequency change or a cloud-source apply, ends with a `Hint:`: the broker due date must not fall after the MGA deadline, the message cannot name the cloud source whose `broker_due_days` it checked, `qctl describe cloud-source <id>` shows a source's `Broker due:` and `qctl describe table-series <series>` the series' Deadlines; lower the broker due days or raise `mga_deadline_days`.
- A frequency change refused with 422 `SERIES_DEADLINE_SCHEDULE_INVALID` because a stored override fits only the old frequency stops before the schedule PATCH that would fix it, since that PATCH runs last. Apply the schedule change alone first, then the frequency change.
- An omitted `spec.description` preserves the existing value; explicit `null` clears it. `status.id` in an export is informational: apply selects the series by its exact name, so changing `metadata.name` creates or updates that name's series.
- Apply moves a table already in another series with PATCH. Members omitted from the manifest are removed, requiring confirmation or `--yes`; removal also requires a named `--context` on a protected context. Confirmation happens before any write. A replacement period holder is removed before its replacement is added; other removals run last, except under a frequency change, when every removal runs first. Period-key swaps park one member on a vacant temporary key of the series frequency, counting back from `9999-12` (monthly) or `9999-Q4` (quarterly), shown in diff.
- `diff -f` lists ordered request methods, paths and JSON bodies; it exits 0 without output for no changes, 1 for changes, and 2 for invalid input. `<created-series-id>` stands for the UUID returned by a planned create. A planned schedule PATCH is first sent to `POST /table-series/<id>/deadline-schedule/preview` with the `period_key` of the stored schedule diff read; a refused preview exits with its error and nothing on stdout, a 422 with 2. Otherwise the requests are followed by `Deadline schedule for period <key> (<time zone>), stored → after apply:` and one `<step> <column>: old → new` line per day value, threshold, switch or date that changes. A create or a frequency change is not previewed and ends with a `Note:` line instead. It does not validate deliverable locks. Apply sends requests sequentially; a failure can leave earlier changes or a temporary period key in place. Run diff and reapply to finish.
- After a partial apply, `-o json|yaml` prints the series read back with its completed changes and exits with the write error. If that read fails, it prints the last successful series create/patch response when available; that snapshot can omit later membership changes. A failed first write prints no result.
- `delete table-series` requires confirmation or `--yes` and is subject to the protected-context gate. It deletes the series itself; it does not remove its members or deliverable bindings.
- `table-series delete-period`, `table-series recreate-period` and `get table-series --reloadable-files` are Qluster ops only, and any other account, admins included, gets 404 (exit 3), as for an unknown series or a period with no table. A period key is sent as one escaped path segment and never format-checked; `delete-period` and `recreate-period` first read the series and exit 3 without a write when it has no such period. Both are destructive: confirmation or `--yes`, and on a protected context `--context` must name it. [backend]
- `delete-period` and `recreate-period` are refused with 409 `SERIES_PERIOD_CHANGE_BLOCKED` (exit 2) while the period is locked, its table has active jobs, or a linked table, an enabled deliverable revision or a deliverable run not yet delivered (`open`, `rendered`, `stale` or `signed_off`) reads it. A locked period comes as this code, not `PERIOD_LOCKED`, and active jobs not as `PERIOD_HAS_ACTIVE_JOBS`; the error's `Hint:` gives one command per blocker. Delivered runs never block and are listed as runs to reissue. [backend]
- `delete-period` soft-deletes the period's table, keeping its files, and moves its destination tables to a new table name it prints, under which the table can be recovered. A series cloud source templated on the table moves to the latest other period's table; with no other active period the delete is refused with 409 `SERIES_SOURCE_TEMPLATE_TABLE`. The period then shows as missing until a file for it creates it again. [backend]
- `recreate-period` copies the period's table, or `--copy-from`'s, into a new table that takes the period's place, name, table name, cloud sources and files, and soft-deletes the replaced one, whose alerts it resolves and whose comment threads, reviews, row ignores, duplicate groups and cells holding a steward's fix stay with it; the printed summary counts those cells, or says `steward fixes not counted` when the server could not count them. A rule of the copied table that reads a column the new schema lacks is skipped and printed. `--reload-file` ids that repeat or are not reloadable are refused with 422 before anything changes, and the blockers are checked next. [backend]
- `recreate-period` reloads files only after the recreate is committed, each on its own, in upload order: a failed reload is printed with its reason and a stderr warning but does not undo the recreate and exits 0. `reloaded` means an ingestion job was queued, not that the file loaded. With `--schema learn-from-file` the new table has no schema, schema overrides, key columns or rules: the first file that reloads is profiled into the table's first migration review (`qctl get alerts --table-id <id>`), the others report `held_until_first_migration` and load once that migration is applied, and the copied table's enabled rules come back as `recreated_period` rule suggestions on the draft. [backend]
- `get table-series <series> --reloadable-files --period <key>` lists every file of the period's table but phantom, partition, trashed, held and turned-down ones, in upload order, unpaginated; `--reload-all-files` reads the same list just before the confirmation and sends every id. [backend]
- `table-series lock|unlock <name-or-id> --period <key>` selects one exact period key and requires confirmation or `--yes` when changing its state. An already requested state succeeds without writing. JSON/YAML print the member API record with lock metadata; `-o name` prints `table/<dataset_id>`, with prose on stderr. These commands change only `is_locked`; a TableSeries manifest has no writable lock field.
- Changing a period lock requires `deliverable:admin`. A new lock is refused with 409 `PERIOD_HAS_ACTIVE_JOBS` while active ingestion, worker, deduper or migrate jobs prevent it; wait for them to finish, inspect `qctl get table <id> --job-activity` or `qctl get ingestion-jobs --table-id <id>`, and retry. Either direction can return 409 `ERR_DB_CONCURRENCY_CONFLICT` while the table is busy; retry after the other change finishes. Both refusals exit 2. Locking records the caller and time, and unlocking clears them. [backend]
- `get table-series <name-or-id> --rule-approval` resolves the series, then reads only its rule-set approval. The table is one row: `approved`, the current `fingerprint`, member `tables`, `rules` across them, the latest approval's `approved_by` UUID and `approved_at` (UTC RFC3339, `-` without one) and the `approvals` count; a `--columns` list naming `id` shows the series UUID. JSON/YAML print the server's record with integers exact, including the full `rule_set` and every approval, newest first; `-o name` prints `table-series/<uuid>`. A missing reference, `--periods` alongside, or an unknown column exits 2 before authentication.
- `table-series approve-rules <name-or-id>` reads the approval state, prints the fingerprint, each table's rule count and the latest approval on stderr, even under `--yes`, then asks for confirmation or `--yes` and POSTs that previewed fingerprint once. `--fingerprint <sha256>` (64 hex digits, either case) pins the fingerprint you reviewed: a different current fingerprint exits 2 before any write. A series with no tables exits 2, and rules already approved succeed without writing. The command is subject to the protected-context gate. A 409 ends with the `get table-series <uuid> --rule-approval` command to re-review. JSON/YAML print the server's approval record and `-o name` prints `table-series/<uuid>`, with prose on stderr.
- A series' rules are approved while its latest approval's fingerprint equals the fingerprint of its current rule set, which must contain at least one undeleted table. The rule set lists each undeleted member table with its rules' revision and revision state, code hash, position, severity, params, row filter, state, column mapping and produced fields. The fingerprint ignores table IDs, rule-instance IDs and instance names, and identical table configurations count once, so adding a period table configured like the others keeps the approval current. [backend]
- Broker report mail for a series table needs a current approval: `file send-issues-report` is refused with 409 `BROKER_REPORT_APPROVAL_REQUIRED`, automatic issues reports are not queued, report updates are held, and a queued report email is skipped once its approval is no longer the current one. A new approval records the approver, the time, the fingerprint and a snapshot of the rule set, and schedules the held report updates. Approving rules already approved at the current fingerprint records nothing. An approval POST returns 409 when the series has no undeleted table or its current fingerprint differs from the one sent (exit 2). No route removes an approval. [backend]
- Reading the approval needs `review:list`. Approving needs `review:approve` and an admin or member role; any other role, ops included, gets 403. Both need a grant on every undeleted table of the series, read-only grants included; admin and ops roles need none. Refusals exit 4. [backend]
- Approving a rule-params change proposal on a series table also records a series approval, carrying the proposal's `change_proposal_id`, when the approver may approve the series' rules and the latest approval matches the current rule set with that rule at the proposal's base params. The proposal's `approve_reapproves_series_rules` says whether an approve will. [backend]
- Source-blocker hints print the names and IDs from the error's `context.data_sources`. A `SERIES_REFERENCED_BY_SOURCE` hint offers `qctl delete cloud-source <id>` for each blocker; a `SERIES_SOURCE_FILES_IN_TABLE` hint offers `qctl describe cloud-source <id>` for each file source. Both refusals exit 2.
- A table can belong to only one series, and a series can have only one table per `period_key`. Adding a table already in a series or taking an occupied period key returns 409; adding a foreign or deleted table returns 404. [backend]
- The server names a series' tables and renames them on its own. Adding a table, moving it into another series or changing its `period_key` names it `<series> · <label>`, the label `Oct 2026` for a monthly key and the key itself, such as `2026-Q4`, for a quarterly one, with ` (2)`, ` (3)` and so on added when another active or disabled table of the organization has that name; renaming a series renames every table in it the same way; removing a table names it `<series> · <label> (removed)`, or ` (removed 2)` and up when that name is taken. The history records these renames with no author, so undo never reverts them. A table's `table_name` is never changed by any of them. [backend]
- Removing a table from a series, moving it, changing its `period_key` and renaming the series each lock every table row they rename, retrying for about a second, and return 409 `ERR_DB_CONCURRENCY_CONFLICT` (exit 2) while another change holds one, locked period or not; retry once the other change finishes. [backend]
- Unlocking the period of a `mirror` or `append` table re-applies the table's duplicate-group decisions once the unlock is saved; a failure there leaves the period unlocked. [backend]
- Moving a member, changing its period key, or removing it returns 409 `DATASET_BOUND_TO_DELIVERABLE` while any non-cancelled deliverable run reads the table, including delivered runs. [backend]
- Moving a table to another series, or removing it, returns 409 `SERIES_SOURCE_TEMPLATE_TABLE` while it is the template of an undeleted cloud source of its series, and 409 `SERIES_SOURCE_FILES_IN_TABLE` while it holds a non-phantom file of one; a period-key change is not checked for either. Adding or moving a composite table into a series an undeleted cloud source uses returns 409 `SERIES_COMPOSITE_TABLE_WITH_SOURCE`. Each message names the sources. [backend]
- Deleting a series returns 409 while it has members, while a sheet binding or aggregate cell binding in any deliverable revision references it, or once it has a rule-set approval record. Bindings on enabled and disabled revisions are frozen, and nothing deletes an approval record, so a series whose rules were ever approved cannot be deleted. [backend]
- Deleting a series also returns 409 `SERIES_REFERENCED_BY_SOURCE` while an undeleted cloud source references it. The error's `Hint:` prints one `qctl delete cloud-source <id>` per source the server lists; deleting those sources clears this refusal, since a deleted source does not count, but the series' other delete refusals still apply. A `SERIES_REFERENCED_BY_DELIVERABLE` refusal's `Hint:` says only a draft revision can drop the binding. [backend]
- A table in a locked period refuses writes with 409 `PERIOD_LOCKED` (exit 2). That covers row edits, uploads, undo and redo, data-review decisions, and suggestion and rule changes. An account with `deliverable:admin` can run `qctl table-series unlock <series> --period <key> --yes`; the error's `Hint:` names this command. `get table-series <uuid> --periods` shows `is_locked`. [backend]
- The series list and detail routes require `DELIVERABLE_ADMIN`, held by ops, admin, member, editor and agent roles; read_only and external roles lack it. A 403 preserves the server's permission message. [backend]
- The list is unpaginated and includes every series with its members and their table names. Series are ordered by name; members are ordered by `period_start`, then by `added_at` and member ID. [backend]
- `--periods` requires `dataset:ingest:upload`. An active series source reaches every undeleted period; without one, only periods fed by active per-table sources are listed. External users see only periods reached by sources they are granted. Internal non-admins also need a table grant that is not read-only; ops and admin skip that grant filter. An empty list can mean no accessible periods or an unknown series. [backend]
- Submission periods are unpaginated and ordered oldest first by period start, members in the member ordering above. Locked periods remain listed, and so do the periods with no table yet that the caller's upload would create, as `creatable` with a null `dataset_id`. The response has no table state: a listed table can be inactive. External callers receive no locker identity. [backend]
- Membership is explicit: a deliverable binding that follows a series picks the member whose `period_key` equals its run's named period parameter converted to text. Period keys are not derived from table names, and the binding does not match on period dates. [backend]
- A member's `period_key` names one period of the series frequency: `YYYY-MM` for monthly, the default, or `YYYY-QN` for quarterly. The server derives `period_start` and `period_end` from it (atlas `cettings/cettings/series_periods.py` `parse_period_key`), and no member request carries dates. Adding a member, changing its key, or moving it into a series under a key not in that series' format returns 422 `SERIES_PERIOD_KEY_INVALID` (exit 2). [backend]
- A series' frequency changes only while no table belongs to it, a deleted table included: otherwise a PATCH changing it returns 409 `SERIES_FREQUENCY_HAS_PERIODS`, whose `Hint:` says how to empty the series. While an undeleted cloud source of the series exists, its template table cannot be removed (409 `SERIES_SOURCE_TEMPLATE_TABLE`), nor can a table holding one of its files (409 `SERIES_SOURCE_FILES_IN_TABLE`), so the series cannot be emptied until its sources are deleted; a table a non-cancelled deliverable run reads cannot be removed either (409 `DATASET_BOUND_TO_DELIVERABLE`). `table-series delete-period` is checked differently: a delivered run or a source's files in the table do not stop it, and it moves a source templated on the table to another period's table, refusing only when none is left. A create, or a PATCH changing `frequency` or `first_period_key`, that leaves a first period key outside the frequency's format returns 422 `SERIES_PERIOD_KEY_INVALID`. A frequency change also returns 422 `SERIES_DEADLINE_SCHEDULE_INVALID` when, under the new frequency, the MGA deadline would fall before the series' broker due date or an undeleted series source's `broker_due_days`. All these exit 2; an unchanged frequency is not rechecked. [backend]
- The MGA deadline defaults to 30 days after a monthly period ends and 45 after a quarterly one. Every other day value, `still_failing_days` and `not_loaded_days` default from the MGA deadline in effect; `still_failing_files` defaults to 3 and `failing_share_percent` to 10, and every switch defaults to on. A day value or threshold sent as `null` follows its default again. [backend]
- A schedule PATCH returns 422 `SERIES_DEADLINE_SCHEDULE_INVALID` (exit 2) when the broker due date in effect would fall after the MGA deadline in effect, or when the MGA deadline falls before the largest `broker_due_days` of the series' undeleted cloud sources, drafts and disabled ones included; equal days are allowed. Both checks run on every schedule PATCH, a switch-only one included, and the message does not name the cloud source. [backend]
- Day values range from 0 to 366, except `mga_deadline_days`, `late_repeat_days` and `issues_open_days`, which start at 1. `still_failing_files` ranges from 1 to 100, `still_failing_days` and `not_loaded_days` from 1 to 366, and `failing_share_percent` from 0 to 99. An out-of-range value, or a switch sent as `null`, is refused with a request-validation 422 that carries no error code (exit 2). [backend]
- The schedule preview writes nothing and answers what a schedule PATCH of the same body would return, the unsaved edits showing in `override_days`, `override_value` and `enabled`. Its body is checked as the PATCH's is — ranges, null switches, and both broker-due checks (422 `SERIES_DEADLINE_SCHEDULE_INVALID`) — except that an empty body skips the broker-due checks. Without `period_key` it picks the period being collected under the previewed schedule, which can differ from the one a GET picks, so diff passes the stored read's period. [backend]
- A schedule read gives the dates of one period. The MGA deadline and the broker due date count from the period's end, and either one on a weekend moves to the Friday before; `first_reminder` counts back from the moved due date, `late_notice` forward from it and `at_risk` back from the moved MGA deadline, and each of those three on a weekend moves to the Monday after. `due_day_reminder` is the moved due date. `late_repeat` and `issues_open` have no date, and a step's date is given whether or not it is enabled. [backend]
- Without `period_key`, a schedule read gives the dates of the period being collected on today's date in the organization's time zone: the earliest ended period whose MGA deadline under the stored schedule is today or later, else the latest ended period. A `period_key` that is not a key of the series frequency returns 422 `SERIES_PERIOD_KEY_INVALID` (exit 2). [backend]
- `first_period_key` is the first period files are expected for: an active series source is expected for the periods from the later of it and the source's `start_period_key`, whichever are set, through the earlier of the source's `end_period_key` and the current period. With neither start set, no period is expected. [backend]
- `apply -f TableSeries` is subject to the change review policy of every member table of the series (the organization's policy for a series with none) when it changes `spec.frequency` or the period column (operation `table_series_patch`), and to the member table's policy when it adds a member, changes a member's `period_key` or removes one (`table_series_member_add`, `table_series_member_patch`, `table_series_member_remove`); `table-series lock` and `unlock` are member patches of `is_locked`. A refusal is a 403 `CHANGE_REVIEW_POLICY_DENIED` (exit 4) whose `Hint:` names the operation and the refused keys and points at `qctl get change-review-policy --table <table>`; no key of these operations can be proposed. Other series fields, `spec.deadline_schedule`, creating a series and `delete table-series` are not subject to the policy. [backend]

## SEE ALSO

- `qctl explain table`
