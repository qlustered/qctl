---
summary: a named spreadsheet sent to a receiver every period, filled from table data
aliases:
kind: Deliverable
schema: manifest:deliverable
guide: deliverables
signature: 705dc2b
---

## USAGE

    qctl apply -f <file.yaml>   # kind: Deliverable
    qctl delete deliverable <ref>
    qctl deliverable completeness <ref>
    qctl describe deliverable <ref>
    qctl diff -f <file.yaml>   # kind: Deliverable
    qctl get deliverable <ref>
    qctl get deliverables
    qctl undelete deliverable <ref>

## EXAMPLES

    # list the deliverables outside the trash, most recently updated first
    qctl get deliverables

    # find the ones whose name or slug contains "bordereau"
    qctl get deliverables --search bordereau

    # list the ones that read a table
    qctl get deliverables --table "September reports"

    # list the trash
    qctl get deliverables --archived

    # read one by its slug
    qctl get deliverable monthly-bordereau

    # show its releases and its latest run
    qctl describe deliverable monthly-bordereau

    # show missed rows and where each goes next
    qctl deliverable completeness monthly-bordereau

    # read the whole response with a larger missed-row cap
    qctl deliverable completeness monthly-bordereau --limit 5000 -o json

    # the API records behind describe
    qctl describe deliverable monthly-bordereau -o yaml

    # obtain one reusable reference per deliverable
    qctl get deliverables -o name

    # pass a reference back to describe
    qctl describe deliverable deliverable/550e8400-e29b-41d4-a716-446655440000

    # move a deliverable to the trash
    qctl delete deliverable monthly-bordereau --yes

    # restore it from the trash
    qctl undelete deliverable monthly-bordereau --yes

    # export its manifest: the basic fields plus the enabled release, else the only one
    qctl get deliverable monthly-bordereau -o yaml

    # round trip: export, edit, list the requests apply would send (exit 1 when there are any), apply
    qctl get deliverable monthly-bordereau -o yaml > monthly-bordereau.yaml
    qctl diff -f monthly-bordereau.yaml
    qctl apply -f monthly-bordereau.yaml

    # create quarterly-claims with a draft release v1 from a template beside the file; `qctl explain deliverable` lists the keys
    qctl apply -f quarterly-claims.yaml

    # the release apply created is a draft: turn it on
    qctl enable deliverable quarterly-claims --release v1

    # every manifest key, including nested column, row_order and cell total fields
    qctl explain deliverable --recursive

    # list one sheet binding's keys and a command to explain a child
    qctl explain 'deliverable.spec.sheets.<sheet>'

    # one key at any depth, with its limits
    qctl explain 'deliverable.spec.sheets.<sheet>.columns.<column>.run_param'

    # fixed text for each kept row
    qctl explain 'deliverable.spec.sheets.<sheet>.columns.<column>.literal'

    # inspect a mirror total's fields
    qctl explain 'deliverable.spec.cells.<cell>.total.same_rows_as'

    # apply a manifest whose sheets, columns or cells remove bindings, confirming the removal
    qctl apply -f monthly-bordereau.yaml --yes

    # set a sheet's row filter: add spec.sheets.<sheet>.row_filter; diff shows the rows it keeps, apply saves it
    qctl diff -f monthly-bordereau.yaml
    qctl apply -f monthly-bordereau.yaml

## GOTCHAS

- Apply and diff leave matching flagged cells unchanged and print a `deliverable confirm-cells` command for the draft. Select the exact sheet name and each intended A1 reference with `--cell`, repeated or comma-separated. Cells planned for PATCH or DELETE are omitted from the preview's confirmation notes.
- `deliverable completeness` is the UI's "Missed rows" view: live clean rows kept by a delivered sheet's filter, or every clean row when it has none, that no delivered manifest of that period's re-issue group or a later group holds on a sheet of the same name and table. Each row names the newest delivered run of the first group that misses it, visiting groups newest first, then sheet name, table id and row id; the same sheet/table/row is reported once. An undecided row is also listed when a delivered run of that group or a later one, or the carrier, waived it; `waived_on_run_id` is the highest such run, and `undecided_reason` says why the filter cannot decide it. [backend]
- Completeness `carried_into_run_id` names the lowest-id new-period run in `open`, `rendered`, `stale` or `signed_off`: for a live carrier, only rows with neither a waiver nor a pending re-issue; for a signed-off carrier, only rows already in its manifest on that sheet. `reissue_run_id` is the lowest-id open, rendered, stale or signed-off run with the missed period's parameters. With neither destination nor a waiver, the UI says the row will be carried into the next period's run. [backend]
- Completeness `can_reissue` is true for the latest delivered period, whose earliest delivered run has the highest id, when no new-period run is signed off. It can remain true with a pending re-issue or a waived row; qctl's Re-issue column says `eligible` only for an unwaived row with that flag and no pending re-issue. Creating a run still applies its usual gates, including refusing a deliverable in the trash. [backend]
- Completeness `--limit` defaults to 1000 and takes 1 to 10000; `truncated` is true even at exactly the limit, so qctl says additional missed rows may exist. The request can be expensive, and qctl waits up to 45 seconds unless `--request-timeout` is given. [backend]
- Completeness answers 400 (exit 2) for a filter that does not validate or compile against its table, for an undelivered undecided row no counting run waived, or for a `ValueError` during computation. One such failure prevents a report. Fix the data/filter, or waive an eligible row on the live carrier before sign-off; a signed-off carrier cannot accept a waiver. [backend]
- On a completeness 400, qctl preserves the server's diagnostic and gives `get deliverable-runs`, `deliverable-run readiness` and `deliverable-run waive <carrier-run-id> --table-id <table-id> --row-id <row-id> --reason "<reason>"` templates. Use the table and row ids of a blocked or `carried_unbound` readiness entry, or a structured `waivable_row` target on a `carried_rows` error. Older servers may supply no such target. If readiness supplies neither, qctl's waiver check refuses the row; fix its data/filter first. qctl never guesses identities from error prose.
- Completeness text prints Sheet, Table (id), Row, Problem, What happens next and Re-issue; a waiver takes precedence over a carrier, then a pending re-issue. It prints integer run ids with state-neutral `Carried into` wording instead of fetching run states, names or parameters. JSON/YAML print the server's whole response and `-o name` exits 2. Completeness includes the trash when resolving a slug, name or UUID prefix.
- A 403 from a deliverable API request keeps the server message and exits 4, with the permission that operation requires, the roles that hold it and `qctl auth me` to check your role. The added advice describes the endpoint requirement; the server message explains the refusal.
- `apply -f <file> -o name` prints `deliverable/<uuid>`, including an unchanged result. Several applied documents print one reference per line in file order; prose goes to stderr.

- Integer fields in a YAML manifest reject fractional numbers during parsing (exit 2): `2.7` is refused instead of becoming `2`. Whole-valued YAML numbers such as `2.0` remain accepted.
- A reference matches a full UUID, else an exact slug, else an exact name, else a UUID prefix of at least 8 hex digits; nothing else matches. A slug picks its own deliverable even when it is another deliverable's name. Names are not unique, so a name or prefix that two deliverables share exits 2 and lists each one's slug and UUID. A reference that matches nothing exits 3; an empty one exits 2 before authentication. `get deliverable` and `describe deliverable` match deliverables in the trash too.
- The server generates the slug from the name at create and keeps it when the name changes: ASCII-folded, stripped of punctuation other than `-`, `_` and `+`, lowercased, with spaces turned into `-` and outer `-` trimmed, prefixed `d-` when it does not start with a letter, and cut to 59 characters. A name with no ASCII letter, digit, `_` or `+` gets an empty slug, which no reference can name. A collision appends `-1`, `-2` and so on, and a deliverable in the trash still holds its slug. [backend]
- `--search` matches a substring of the name or the slug, ignoring case. [backend]
- `--table` keeps deliverables whose enabled revision reads the table, through a sheet or an aggregate cell, and deliverables with a run that is not cancelled reading it. A sheet or cell bound to a series names the series' reference table, so `--table` on that table matches it; another member of the series is matched only once a run reads it. [backend]
- `--archived` lists only the trash and `--include-archived` adds it; passing both exits 2. Either one adds an `archived_at` column to the table view, as does a deliverable in the trash under `get deliverable`.
- `--reverse` sorts ascending. The default is descending, so `--order-by name` alone lists Z to A. [backend]
- `enabled` says whether a revision is enabled; the UI's Setup badge shows it as "Active" or "Draft". `latest_run` is the state of the run with the highest ID, cancelled runs included, and `latest_run_params` is its parameters as the server's exact text. [backend]
- `describe deliverable -o json|yaml` emits `{deliverable, revisions, latest_run}`: `deliverable` is the API record, which has no latest run, and `revisions` is every revision, newest first. `get deliverables -o json|yaml` list items carry their own `latest_run`. [backend]
- Every deliverable route these commands call requires `DELIVERABLE_ADMIN`, held by ops, admin, member, editor and agent roles; read_only and external roles lack it. A 403 preserves the server's permission message. [backend]
- UI words: "Receiver", "Recipient" and "To" all name `receiver_label`; "Period question" is `period_param`; "Delete" moves a deliverable to the trash, setting `archived_at`, and "Restore from trash" clears it.
- `delete deliverable` moves it to the trash and keeps its revisions; its enabled revision stays enabled. A run in open, rendered, stale or signed_off state prevents deletion with 409 (exit 2); deliver or cancel those runs first. `undelete deliverable` clears `archived_at`. [backend]
- In the trash, the server refuses deliverable metadata edits, revision creation, revision enablement and run creation with 409 (exit 2). It still permits draft binding edits, draft template uploads, draft revision deletion, validation, preview and row-filter reports, subject to their usual checks. [backend]
- `delete deliverable` resolves slugs, names and UUID prefixes outside the trash, then checks the record even for a full UUID; an already trashed UUID exits 2 without changing its timestamp. `undelete deliverable` resolves including the trash and refuses an untrashed record with exit 2. JSON/YAML print the server's updated deliverable record and `-o name` prints `deliverable/<uuid>`; confirmation and lifecycle prose go to stderr for those formats.
- Both trash and restore require confirmation (`--yes` skips it). On a protected context, name it with `--context`; `--yes` does not bypass the context gate.
- A `Deliverable` manifest has `apiVersion: qluster.ai/v1`, `metadata.slug`, and in `spec` the required `name` and `filename_pattern`, the optional `receiver_label` and `period_param`, and at most one `release` with its optional `description`, `template`, `acknowledge_lossy`, `sheets` and `cells`. Any other key exits 2. `qctl explain deliverable.spec.<key>` describes one key.
- `metadata.slug` names the deliverable apply writes, and `spec.name` is an ordinary field, so changing it renames that deliverable. A deliverable in the trash exits 2 before any write, naming `qctl undelete deliverable`. With no deliverable holding the slug, apply creates one only when `metadata.slug` is exactly the slug `spec.name` generates, and otherwise exits 2 naming that slug; so apply cannot create a deliverable whose name generates a slug another deliverable already holds, in the trash or not.
- `name`, `receiver_label`, `filename_pattern` and `period_param` belong to the deliverable, not to a release: the PATCH that changes them takes effect at once, with no draft to review or enable. [backend]
- `spec.release` names one release, matched exactly. A release the deliverable lacks is created as a draft, which apply never enables (`qctl enable deliverable <slug> --release <release>` does). Of an existing release, apply changes only a draft's sheet, column and cell bindings, to match `spec.sheets` and `spec.cells`; an enabled or disabled release whose bindings differ exits 2 before any write, so bump `spec.release` for a new one. A manifest without `spec.release` writes only the basic fields.
- A created release copies the enabled release when there is one — its template, lossy acknowledgement, sheet and cell bindings, sort keys and row filters, but not its description — and starts empty otherwise. A `spec.template` replaces the copied template; the copied bindings are re-anchored to it, and apply prints the anchor report, only when the enabled release had a different template. Other drafts are left alone: a release label is unique per deliverable, and only one release can be enabled. [backend]
- A release's description is set when the release is created, and no route changes it. So apply sends `spec.description` only when it creates the release; on an existing release a different `spec.description` exits 2 before any write (bump `spec.release`, or delete the draft and apply again), and a manifest without the key is not compared. [backend]
- `spec.template` is a path relative to the manifest file, uploaded only when apply creates the release and before any other write. On an existing release it is not compared: apply and diff print a `Note:`, which for a draft names the `qctl upload deliverable-template` command that replaces its template. `diff -f` never uploads it.
- The template finalize refuses a lossy template with `SKELETON_LOSSY` even when no release is named, so a lossy `spec.template` without `spec.acknowledge_lossy: true` exits 2 with no deliverable or release written. A release the server creates starts as a draft. [backend]
- `get deliverable -o json|yaml` emits a `kind: Deliverable` manifest with the enabled release, else the only release, else no `spec.release`, and never `spec.template`. When that release has sheet bindings it adds `spec.sheets`, with each `table` as its numeric ID, `series` by name, `row_order` by position, `instruction_row_count`, every sheet's `columns` by letter, and `format` only where one is set; it adds `spec.cells` when there are cell bindings, with keys `Sheet!A1`, table IDs, series names and mirror sheet names. A sheet, or a total with its own `table`, carries its stored `row_filter` when it has one, and a release with no sheet or cell bindings omits the corresponding map, leaving those bindings unmanaged. Its `status` (the deliverable's id, and the release's id and state) is ignored by apply, and applying an unedited export of a deliverable outside the trash reports `unchanged`.
- An export carries its release's `spec.description`, so bumping `spec.release` in it without editing or deleting that key creates the new release with the old description.
- `spec.sheets` maps a template sheet name to its binding: `table` (a table name or ID, as a string or integer in YAML and JSON; with `series`, the series' reference table), `header_rows` (at most 3 rows above `data_start_row`, `[]` for none) and `data_start_row` are required; both row fields are 1-based and bounded by Excel's last row, 1048576; `series` (a name or UUID, `null` to clear it), `instruction_row_count` (0–10), `row_order` (a list of `{column, descending}`, `[]` to clear it) and `row_filter` (`null` to clear it) keep the stored value when omitted; and `columns` maps a column letter (A to XFD) or 1-based index (1 to 16384, without leading zeros) to exactly one of `column` (a table column), `run_param` or `literal` (fixed text, including `""`), plus an optional `format`. A null literal does not select a source. `qctl explain deliverable --recursive` lists every nested key, and `qctl explain 'deliverable.spec.sheets.<sheet>'` lists a sheet's child keys. `qctl explain 'deliverable.spec.sheets.<sheet>.<key>'` describes one key with its limits, a deeper one such as `columns.<column>.run_param` or `row_order.column` included.
- `spec.sheets` is the release's full set of sheet bindings: apply deletes a sheet binding the map does not name, with its column bindings, so `{}` deletes them all, while a manifest without `spec.sheets` leaves every sheet alone. A sheet's `columns` works the same way within that sheet, so without the key the column bindings saved in the UI or by `qctl deliverable accept-column-suggestions` are kept. Sheets are matched by name, so changing a sheet's key is a delete plus a create, not a rename. [backend]
- A planned sheet, column or cell binding DELETE asks first, as a `TableSeries` member removal does: a y/N prompt lists the removals and every request, and `--yes` skips it; without a terminal, or under `-o json|yaml`, apply exits 2 unless `--yes` is passed, and on a protected context the removal also requires a named `--context`. A sheet that already has column bindings and whose `columns` the manifest lists asks too when its `header_rows` or `data_start_row` changes, or when `spec.template` replaces the template of a release apply creates by copying, because the server may move its column bindings first and apply then deletes any left outside the listed columns. Managing cells copied into a release with a replacement `spec.template` asks too, since the template may move their targets and apply removes cells outside `spec.cells`. All of this happens before any write, and declining writes nothing.
- A column binding is keyed by its target column, so apply saves a changed one with a PATCH at the same index rather than a delete and a create. That PATCH replaces the whole binding: apply sends `format_override` null when `format` is absent, so omitting `format` clears a stored one, and the save sets the binding's `origin` to `user` and re-reads its `target_label` from the template header. [backend]
- When a sheet, column or cell create or PATCH is refused with a 422 that lists issues, `BINDING_INVALID` or the request's own validation, apply translates known request-body locations to manifest keys, such as `spec.sheets.Data.row_order[0].column`, `spec.sheets.Data.columns.C.column` or `spec.cells.Summary!B1.literal`; unknown locations keep the server's spelling. A sheet PATCH issue on an omitted `row_order`, `series` or `instruction_row_count` says `from stored binding`, since apply resent that binding's value.
- Changing a sheet's `header_rows` or `data_start_row` makes the server re-anchor its column bindings by their header labels: a labelled binding whose label is no longer over its column moves to the one other column under that label, and is reported `broken` where it is when no column or several carry the label or that column is taken; a binding with no label stays and is not flagged. Apply prints that PATCH's anchor report and plans the sheet's column writes again from the response, as it plans every binding write again from a release it creates; `diff -f` notes the PATCH may re-anchor. [backend]
- Every sheet binding create or PATCH is refused with 422 `BINDING_INVALID` (exit 2) while any cell of that template sheet at or below row `data_start_row` + `instruction_row_count` is non-empty, so a footer or totals row under the data region must move above it or onto another sheet first. Apply checks no template cell before writing, so such a refusal stops it at that sheet, keeping the writes before it. [backend]
- The server refuses with 409 every sheet, column and cell binding write to a release that is not a draft, every one but a DELETE to a release with no template, and the DELETE of a sheet binding a cell binding totals (`same_rows_as_sheet_binding_id`, `ERR_STILL_REFERENCED`). Apply and diff check these against the release and bindings they plan from and exit 2 before any write: the first says to bump `spec.release`, the second names `qctl upload deliverable-template` for an existing draft or `spec.template` for a release apply creates, and the third names any unmanaged mirror cells. When `spec.cells` manages those mirrors, apply deletes them before their sheet and recreates the ones still wanted after sheet writes. [backend]
- The server refuses a sheet binding create or PATCH naming a sheet the release's template lacks with 422 `BINDING_INVALID`, and a column binding create or PATCH under such a sheet with 404 `SHEET_NOT_FOUND`; sheet names match exactly, case included, and neither DELETE is checked. Apply and diff check every other binding write against the template's sheets and exit 2 before any write, listing them; for a release apply creates with `spec.template`, diff skips the check, and apply makes it against the created release after its POST and stops there, before any binding write. [backend]
- A column binding create or PATCH is refused with 422 `BINDING_INVALID` when a template merged range covers its target column and reaches `data_start_row` or below, except a range wholly inside the instruction rows. A range starting above `data_start_row` and entering instruction rows is still refused. Apply and diff check planned column saves, including saves a layout re-anchor may add, and exit 2 before any write when the planned template inventory is available. For a release created with `spec.template`, diff skips this inventory check and apply checks the created release after its POST, before binding writes. [backend]
- A sheet binding create or PATCH checks every `row_order` column it sends, including a stored order kept when the key is omitted; a column binding create or PATCH checks its source `column`. Names match the table's non-deleted applied columns exactly: a draft-only addition is unavailable, and a pending deletion remains available until applied. Apply and diff read each needed table schema once for `spec.sheets` and refuse an unavailable column with exit 2 before any write, including column saves a layout or template re-anchor may add. A metadata-only manifest reads no table schema, and a cell-only one reads only the tables of totals whose stored `row_filter` no save sends or keeps, to check the columns that filter reads. [backend]
- The server sets a column binding's `needs_confirmation` only when the template is replaced or the sheet renamed, and only on a binding with no stored header label; any save of the binding clears it, and validation, so `enable deliverable`, reports an error for each flagged binding until then. Apply and diff never save a matching binding just to clear the flag: for a draft they print one `Note:` per sheet naming its flagged columns, with the `qctl describe deliverable-revision` command that shows them and the `qctl deliverable confirm-columns` command that confirms them, and a planned PATCH of a flagged binding notes that it also confirms it. [backend]
- `row_filter` on a `spec.sheets` entry or a `spec.cells` `total` picks the rows of its table that fill the sheet or feed the total. Omitted, it keeps the stored filter (for a total, only while the cell stays a total with its own `table`), and a sheet or cell apply creates has none; `null` clears it; a mapping sets it, with `conditions`, a `predicate` or both, and a `description` of 1 to 200 characters, which the server stores with its outer whitespace trimmed. A mapping beside `total.same_rows_as` exits 2, since a mirror totals its sheet's rows. Every sheet create or PATCH compiles the filter it sends or keeps against the sheet's `table` (with `series`, the series' reference table), and the server refuses it with 422 (exit 2) when the filter no longer fits, for example after `table` changes. `qctl explain 'deliverable.spec.sheets.<sheet>.row_filter'` lists the filter's keys. [backend]
- A `row_filter` that keeps different rows from its binding's stored filter, through other conditions or another predicate, while repeating its description (compared with outer whitespace trimmed) exits 2 before any write: describe which rows the new filter keeps. The stored filter is the draft's, or the enabled release's when apply creates the release by copying it. A filter that matches the stored one but for its description's outer whitespace is no change.
- `diff -f` and `apply -f` count the rows kept by each `row_filter` a sheet or cell save sends or keeps, on the table the server compiles it against: the total's `table`, or the sheet's `table`, which for a sheet with `series` is the series' reference table rather than the member a period selects. Each count prints as `Row filter <key> on table <id>:` over a `Keeps M of T rows` line, where M and T cover the table's live clean and quarantined rows, as `qctl table-rule test-filter` counts them, while `inspect deliverable` counts only clean rows; diff prints the counts after its requests, and apply before its first write. A count refused with 422, because the filter does not compile against that table, exits 2 before any write and names the key, including a stored filter that a save changing another field keeps. Any other failure, such as a 403, a 404, a 5xx or a network error, prints `Warning: could not count rows for <key>: <reason>`, and the save goes ahead. [backend]
- For a sheet in `spec.sheets`, or a total with its own `table` in `spec.cells`, whose stored `row_filter` no planned save sends or keeps, `diff -f` and `apply -f` read that table's applied columns and print a `Warning:` naming each column the filter reads, in a condition or the predicate's `columns`, that the table's non-deleted applied schema lacks, even when nothing else changes; a failed read prints a `Warning:` too, and neither stops the plan. Revision validation reports such a filter as an error, so `enable deliverable` and `inspect deliverable` refuse the release with 422 `REVISION_INVALID` (exit 2) until `row_filter` is set to a filter that fits the table or to `null`. [backend]
- Counting a filter calls the table route `qctl table-rule test-filter --row-filter-file` calls, which requires `DATASET_RULE_EDIT` and access to that table: ops and admin roles reach every table of the organization, while member, editor and agent roles need a grant on it that is not `read_only`. The deliverable binding routes check `DELIVERABLE_ADMIN` and no table grant, so a member, editor or agent without such a grant on the table saves the filter while its count gets a 403, printed as a `Warning:`. [backend]
- A sheet with `series` needs the deliverable's `period_param`: validation, so `enable deliverable`, reports an error while any sheet or cell binding of the release follows a series and the deliverable has none, and apply and diff print a `Note:` for each such sheet. `table` must be one of the series' members, which the server checks when the sheet is written (422 `BINDING_INVALID`, exit 2). [backend]
- UI words for `spec.sheets`: on the Sheets step "Rows from" is `table` and "Follow a table series" is `series`; the "Header rows" step sets `header_rows`, `data_start_row` and `instruction_row_count`; "Sort column" and "Sort direction" are `row_order`; on the Columns step "Run value" is `run_param`, "Number format" is `format`, "Leave empty" removes the column from `columns`, and "Keep it here" is `qctl deliverable confirm-columns`.
- `spec.cells` requires `spec.release` and maps `Sheet!A1` to exactly one of `run_param` (non-blank), `literal` (fixed text, including `""`) or `total`, plus optional `label_cell` and `format`. `total` has `table` (name or ID), optional `series` (name or UUID), `func` (`sum`, `count`, `min` or `max`), `column` (required except for `count`) and optional `row_filter`; a mirror replaces `table`, `series` and `row_filter` with `same_rows_as`, the exact bound sheet name. Absolute markers, lower case and leading row zeros normalize to canonical A1 within `A1:XFD1048576`, including label references; duplicate normalized keys exit 2. Sheet names retain their case. `qctl explain 'deliverable.spec.cells.<cell>.<key>'` describes the fields.
- Present `spec.cells` is the complete set: apply deletes cells the map omits, and `{}` deletes all of them; omitting the map leaves cells unmanaged. A cell is identified by its sheet and canonical A1 reference, so a changed source at the same address uses PATCH, while a changed map key is a DELETE and a POST. A mirror must name a sheet retained or created by `spec.sheets`, or an existing sheet when sheets are unmanaged. Sheets are saved before cells, so mirrors use the IDs of newly created or copied bindings. Unmanaged mirrors block removal of their sheets.
- The server requires an aggregate cell's own table to belong to the organization, and with a series to be one of that series' members; the table need not be bound to a sheet. A mirror instead totals its bound sheet's rendered rows, with no table, series or filter of its own. Validation requires the deliverable's `period_param` while any sheet or cell follows a series, and apply and diff print a note for a manifest cell with a series while it is unset. [backend]
- A cell save replaces its source, format and row filter. So that an omitted `total.row_filter` keeps an ordinary total's stored filter, apply resends that filter whenever it saves the total, even if its `table` changes, and the server compiles it against the new table, refusing it with 422 when it no longer fits. Switching to a mirror, run value or fixed text clears that filter and every source field that no longer applies. An omitted or null `format` clears it on save. The label cell must have text on the same template sheet and differ from the bound cell; omitted or null `label_cell` keeps the stored label on PATCH, and a new cell defaults to the nearest text to its left, then above. Apply also retains the old label when it normalizes an existing address or temporarily deletes a mirror before removing its sheet; to preserve it across an interrupted DELETE/POST, include `label_cell` explicitly. Cell saves inside a bound sheet at or below its `data_start_row` are refused with 422, even in instruction rows; apply and diff reject such planned saves before writing. [backend]
- `diff -f` prints every request apply would send, in order: the template upload, then the deliverable's POST or PATCH, the PATCH preceded by `field: old → new` lines, then the release's POST, then the binding writes — cell DELETEs first, then each sheet DELETE, then sheet by sheet in name order its POST or PATCH followed by its column DELETEs, PATCHes and POSTs in column order, then cell PATCHes and POSTs sorted by sheet and A1 reference — then the row filter counts and `Warning:` lines, and after them any `Note:` lines. `<created-deliverable-id>`, `<uploaded-template-id>`, `<created-revision-id>` and `<created-sheet-binding-id>` stand for ids the server assigns, and `<copied-sheet-binding-id>`, `<copied-column-binding-id>` and `<copied-cell-binding-id>` for the ids of the bindings a created release copies. It exits 0 when nothing would change, printing any `Warning:` and `Note:` lines on stderr and nothing on stdout, 1 when it prints requests, and 2 for an invalid manifest or a plan apply would refuse.
- `apply -f` prints `deliverable/<slug> created`, `updated` (any write to an existing deliverable, a new release or a binding write included) or `unchanged`, then the release it created, the template it attached, the created release's anchor report, a `binding writes:` line counting the sheets, columns and cells it deleted, created and updated, the anchor report of each sheet PATCH whose report lists a column binding, any column-write warnings labelled with their sheet and column, and any `Note:` lines, the confirmation notes last, read back from the release after the writes. The confirmation notes and exported bindings share one revision read after the writes. `-o json|yaml` print the deliverable and the named release as a manifest, `spec.sheets` and `spec.cells` read back unless a write or the read-back failed. Under `-o json|yaml` these result lines and warnings go to stderr. It sends one request at a time. A failed write or read-back still prints, before the error, those lines but the first for what was written, with the confirmation notes only once the release was read back; the error of a write past the first request and the template upload names the step, the sheet, column or cell of a binding write, and how many completed. `diff -f` then shows what is left, and applying the same manifest again sends only that.

- A manifest writes `{literal: "Acme"}` or `{literal: "0.25", format: "0%"}` under a sheet's `columns`. Diff shows `source_type: literal` and `literal_value`; export preserves the value and format.
- `diff -f` and `apply -f` refuse a column or cell `literal` longer than 32767 characters, or holding a control character below U+0020 other than tab, newline and carriage return, with exit 2 and the manifest key before any write: an Excel cell cannot hold that text.

## SEE ALSO

- `qctl explain deliverable-revision`
- `qctl explain deliverable-template`
- `qctl explain table-series`
