---
summary: a deliverable release with its template and sheet, column and cell bindings
aliases:
kind:
schema: openapi:DeliverableRevisionResponse
guide: deliverables
signature: 1ce989d
---

## USAGE

    qctl delete deliverable-revision <ref> --release <release>
    qctl deliverable accept-column-suggestions <ref> --sheet <sheet>
    qctl deliverable confirm-cells <ref> --sheet <sheet> --cell <A1>
    qctl deliverable confirm-columns <ref> --sheet <sheet> --column <column>
    qctl deliverable test-filter [<ref>] --sheet <sheet>
    qctl describe deliverable-revision <ref>
    qctl enable deliverable <ref>
    qctl get deliverable-revisions <ref>
    qctl inspect deliverable <ref>

## EXAMPLES

    # list every setup version of a deliverable
    qctl get deliverable-revisions monthly-bordereau

    # show the enabled revision's bindings and template inventory
    qctl describe deliverable-revision monthly-bordereau

    # select a release explicitly
    qctl describe deliverable-revision monthly-bordereau --release 2026.2

    # select by revision UUID within the named deliverable
    qctl describe deliverable-revision monthly-bordereau --revision 550e8400-e29b-41d4-a716-446655440000

    # inspect the first 30 template rows with spreadsheet coordinates
    qctl describe deliverable-revision monthly-bordereau --release 2026.2 --sheet Premium --limit 30

    # preserve blank cells and merged ranges in a structured row response
    qctl describe deliverable-revision monthly-bordereau --sheet Premium -o json

    # obtain reusable revision references
    qctl get deliverable-revisions monthly-bordereau -o name

    # use one of those references as a revision selector
    qctl describe deliverable-revision monthly-bordereau --revision deliverable-revision/550e8400-e29b-41d4-a716-446655440000

    # validate the only draft without enabling it: errors exit 2, warnings go to stderr
    qctl enable deliverable monthly-bordereau --dry-run

    # enable the only draft, replacing the enabled release
    qctl enable deliverable monthly-bordereau

    # roll back by enabling an earlier, now disabled, release again
    qctl enable deliverable monthly-bordereau --release 2026.1

    # enable a release and print the server's revision tree
    qctl enable deliverable monthly-bordereau --release 2026.2 -o json

    # permanently delete a draft release and its bindings
    qctl delete deliverable-revision monthly-bordereau --release 2026.2 --yes

    # preview the enabled revision with live table data
    qctl inspect deliverable monthly-bordereau --period 2026-09

    # preview a draft with a YAML mapping of parameter names to scalar values
    qctl inspect deliverable monthly-bordereau --release 2026.2 --params-file params.yaml --max-rows 100

    # read parameters from stdin and keep the complete server response
    qctl inspect deliverable monthly-bordereau --params-file - -o json

    # accept confident template-column matches on the only draft's Premium sheet
    qctl deliverable accept-column-suggestions monthly-bordereau --sheet Premium

    # select a draft release and preserve the complete suggestions response
    qctl deliverable accept-column-suggestions monthly-bordereau --release 2026.2 --sheet Premium -o json

    # confirm the flagged columns C and D on the only draft's Premium sheet
    qctl deliverable confirm-columns monthly-bordereau --sheet Premium --column C,D

    # confirm flagged cells on a sheet that need not have a sheet binding
    qctl deliverable confirm-cells monthly-bordereau --sheet Summary --cell B2,D2

    # select a draft and preserve the returned cell bindings
    qctl deliverable confirm-cells monthly-bordereau --release 2026.2 --sheet Summary --cell B2 -o json

    # report the enabled revision's saved Premium row filter: kept / left out / blocked and left-out rows
    qctl deliverable test-filter monthly-bordereau --sheet Premium

    # report a series sheet's filter on the member table of one period
    qctl deliverable test-filter monthly-bordereau --sheet Claims --period 2026-09

    # keep the complete report response of a draft release
    qctl deliverable test-filter monthly-bordereau --release 2026.2 --sheet Premium -o json

    # count a manifest's unsaved Premium row filter before applying it
    qctl deliverable test-filter -f deliverable.yaml --sheet Premium

    # count a series sheet's unsaved filter on a period's table instead of the reference table
    qctl deliverable test-filter -f deliverable.yaml --sheet Claims --table-id 42

## GOTCHAS

- `deliverable confirm-cells` selects its draft as `confirm-columns` does. `--sheet` is an exact cell-binding sheet name; it need not have a sheet binding. Required `--cell` takes A1 references, repeated or comma-separated; lowercase letters, absolute markers and leading row zeros are normalized. Missing, malformed or duplicate selectors exit 2 before authentication; a selected cell not bound on that sheet exits 2 before any write. Flagged bindings are re-saved in reference order with their stored source, row filter, format and label-cell reference; unflagged bindings are reported `already confirmed` without a write. Table output reports each selected cell; JSON/YAML emit the selected bindings on stdout and the report on stderr; `-o name` exits 2. A failed save preserves its exit code, names prior successful confirmations and stops further writes.
- Re-saving a cell binding clears `needs_confirmation` and re-reads its stored label cell's text. When no label cell is stored, the server chooses the nearest text to the left, else above, else no label. A stored label cell that is now empty, or an aggregate source or row filter the server no longer accepts, can prevent the save; confirmation does not bypass binding validation. [backend]
- Apply/diff notes name flagged cells that matching manifests leave unchanged and provide a `deliverable confirm-cells` command. A planned cell PATCH or DELETE suppresses that cell's note; apply reads the draft back for remaining flags after its writes, including when only `spec.cells` is managed.
- A 403 from a deliverable API request keeps the server message and exits 4, with the permission that operation requires, the roles that hold it and `qctl auth me` to check your role. The added advice describes the endpoint requirement; the server message explains the refusal.
- `deliverable accept-column-suggestions` selects the only draft unless `--release` or `--revision` names one. It checks the selected revision's current draft state before writing. `--sheet` is a required exact bound sheet name, not a sheet-binding UUID; an empty sheet exits 2 before authentication and a missing binding exits 3. Reads and apply do not accept suggestions implicitly; the UI accepts them when a sheet's Columns tab opens.
- Accepting suggestions requires a draft revision with an attached template (409 otherwise). It saves `same_name` and `standard_field` matches as `origin: suggested`, leaves `origin: user` bindings untouched, and returns `similar_name` matches only as hints. Matching excludes already-bound targets and source columns, and targets intersecting merged ranges that reach `data_start_row` or below unless the range lies wholly within instruction rows. [backend]
- Suggestions re-arm every labelled target when no basis is stored or the bound table changes, otherwise only targets whose labels changed. Suggested bindings on re-armed or now-unlabelled targets are removed before matching. Once the basis is stored, a suggested mapping you delete stays deleted until its target label or bound table changes. The response contains every current column binding with its match reason and formula warnings, plus unbound labelled columns that can receive a binding and their optional hints; `replaced_count` counts removed suggested bindings, including ones without a new match. [backend]
- The suggestions text report shows created and replaced counts, every current mapping with its origin and match reason, and empty-column hints marked `not applied`. JSON/YAML print the original `{created_count, replaced_count, column_bindings, empty_columns}` response and send the report to stderr; `-o name` exits 2.
- `deliverable confirm-columns` selects its draft and `--sheet` as `accept-column-suggestions` does; `--column` takes letters or 1-based indexes, repeated or comma-separated, and a duplicate, or a column the sheet does not bind, exits 2 before any write. It re-saves each named binding whose `needs_confirmation` is set with its stored source, target column and format, and that save is what clears the flag; it also sets the binding's `origin` to `user` and re-reads its `target_label` from the template header. A named binding without the flag is reported `already confirmed` and not sent; a failed save keeps its exit code and names the columns confirmed before it. Only a binding with no stored `target_label` is ever flagged, when the template is replaced or its sheet renamed, and validation, so `enable deliverable`, reports an error for each one until it is saved again. A binding whose source column has since been deleted from the bound table cannot be re-saved (422 `BINDING_INVALID`, exit 2). [backend]
- UI "setup version" means revision; the API's "skeleton" means template; the "Turn on this version" and "Turn on — replaces <release>" buttons are `enable deliverable`. The positional reference names the deliverable, by the same slug, name or UUID rules as `get deliverable`, including deliverables in the trash.
- `--release` matches an exact, case-sensitive release label, then a full revision UUID; `--revision` accepts a full UUID or `deliverable-revision/<uuid>`. Both selectors are checked against the named deliverable's revisions. A missing match exits 3, and passing both or an empty selector exits 2 before authentication.
- With neither selector, describe chooses the enabled revision, or the sole revision when none is enabled. Multiple revisions without an enabled one require a selector (exit 2); no revisions exits 3.
- `get deliverable-revisions` returns every revision, newest first, without pagination. A release is unique per deliverable and cannot be edited; the lifecycle is draft, enabled, disabled, and a disabled revision can be enabled again. At most one revision is enabled. [backend]
- The text tree sorts sheets by name, columns by index, sort keys by position and cells by sheet name then cell reference. Each sheet, and each total with its own table, prints its row filter as an indented block: its description, each condition on a line of its own, and the predicate's columns and full source, or `(no filter: keeps every row)`; a fixed-text, run-value or mirror cell prints none, and JSON/YAML carry the complete filter.
- Column bindings carry `origin` (`suggested` or `user`); both column and cell bindings carry `target_label` and `needs_confirmation`. The column-binding `warnings` list on a revision GET is empty; validation is a separate operation. [backend]
- Template inventory includes only the first 10 rows in `header_rows`. `--sheet` fetches template rows in any revision state; `--limit` is 1–100, defaults to 10, and requires `--sheet`. A missing template returns 409 (exit 2); an unknown sheet returns 404 `SHEET_NOT_FOUND` (exit 3). [backend]
- A version-1 fidelity report uses raw zip part paths in `lost_parts`; version 2 itemizes losses in `losses`. The text view switches on the report version and shows notes separately. [backend]
- `get deliverable-revisions -o json|yaml` emits the API array. `describe deliverable-revision -o json|yaml` emits the revision API record, or the template row response when `--sheet` is passed, preserving null cells and inventory headers. `-o name` prints `deliverable-revision/<uuid>`: get prints one per revision, and `enable deliverable` prints the chosen revision's when it succeeds; describe and delete refuse it with exit 2.
- The server deletes only draft revisions; an enabled or disabled revision returns 409 `REVISION_NOT_DRAFT` (exit 2). Deletion permanently removes the draft's sheet, column and cell bindings and works while its deliverable is in the trash. [backend]
- `delete deliverable-revision` requires a nonblank `--release` (exact release label or full revision UUID) within the named deliverable and checks its current draft state before confirmation. It supports table output only. Confirmation is required (`--yes` skips it); on a protected context, also name it with `--context`.
- With neither selector, `enable deliverable` chooses the deliverable's only draft revision: no draft, or several, exits 2 pointing at `get deliverable-revisions`, and a deliverable with no revisions exits 3. A disabled release is enabled again only by naming it with `--release` or `--revision`.
- For a deliverable in the trash, `enable deliverable` exits 2 before sending the enable, naming `qctl undelete deliverable`, even when the chosen release is already enabled. Otherwise it sends no enable for a revision that is already enabled: it says `already enabled` and exits 0, and `-o json|yaml` prints the revision read back. `--dry-run` still validates in both cases.
- `--dry-run` POSTs the validation and never the enable. A valid revision exits 0 and an invalid one exits 2 listing each error; either way each warning prints on stderr as a `Warning:` line, and `-o json|yaml` prints the server's `{valid, errors, warnings}` before exiting. Only `--dry-run` reports warnings, so run it before enabling.
- The following-runs lines come from a run listing read just before the enable POST, or under `--dry-run` after a valid validation of a revision that is not enabled: one `run <id> (<revision_mode>): <state>` line per open, rendered or stale run in `new_period` or `reissue_enabled` mode on another revision, a rendered run's line ending `→ stale (revision_superseded; re-render required)`.
- Under `-o json|yaml|name`, all of `enable deliverable`'s prose, the following-runs and `Next:` lines included, goes to stderr. A validation issue prints as `<loc joined by dots>: <msg>`, or `<msg>` alone when it has no loc, and a run that cannot move prints as `run <id>: <reason>`.
- Enabling runs the same validation as `--dry-run` and refuses a revision with errors with 422 `REVISION_INVALID`, whose `context.errors` lists them and which carries no warnings (exit 2). Otherwise the revision becomes enabled and the deliverable's previously enabled revision becomes disabled. [backend]
- Enabling moves every open, rendered or stale run of the deliverable in `new_period` or `reissue_enabled` mode that is on another revision onto the new one, and a rendered run becomes `stale` with `stale_reason` `revision_superseded`. Each moved run's sources are recreated with new ids, an overridden table keeping its override on the new revision's binding of the same sheet or aggregate cell. Runs in `signed_off`, `delivered` or `cancelled` state, and `reissue_as_delivered` runs, keep their revision, and the enable answers with the revision tree, which names no moved run. [backend]
- A following run that cannot move, because its tables do not resolve for the new revision or an overridden sheet or aggregate cell is missing from it, makes the enable answer 409 with no error code and a `context.runs` entry (`run_id`, `reason`) per problem; nothing is enabled or moved (exit 2). [backend]
- Validation saves nothing and runs in any revision state. It, and so the enable, reads the template file, downloading it into the server's cache when it is not already cached, so it can answer 409 when the organization has no backup settings (exit 2); a revision with no template reports one error at `skeleton_id` instead. [backend]
- Validation's only warnings are a source column bound to two target columns of one sheet, formulas in a bound column's data region, which rendering overwrites with values, and one at `due_days` for each series a sheet or total of the release follows whose broker due days, or an active intake's, exceed the deliverable's `due_days`. Every other finding is an error. [backend]
- UI "Preview" is `inspect deliverable`: it selects a revision as describe does, creates no run or output file, and takes table, JSON or YAML output. The table view shows sampled rows, resolved cells and each sheet's kept / left out / blocked counts; JSON/YAML preserve the server's `{sheets, cells}` response and write counts to stderr. `-o name` exits 2.
- `--params-file` reads exactly one YAML mapping, for example `period: "2026-09"` and `carrier: "Acme"` on separate lines; `-` reads stdin. Values are strings, integers (-2**63 through 2**64 - 1), finite floats, booleans or null. Quote dates to keep them as strings. An untagged plain integer with a leading zero, such as `01234`, exits 2; so does a leading-zero `!!int` value, even if quoted. Use a quoted string or `!!str` for text, or drop the leading zero for a number. Duplicate or non-string keys, blank names, nested values, aliases and unsupported YAML tags exit 2 before authentication. Strings keep their whitespace, integers keep their digits and floats stay floats even when whole.
- `--period` supplies the deliverable's `period_param` as a string; it is mutually exclusive with `--params-file` and exits 2 if the deliverable has no period parameter. Preview requires every declared parameter: period first, then cell run values, column run values and filename `{tokens}`, deduplicated case-sensitively in that order. A missing, null or blank required value exits 2 before the preview POST; zero and false are valid. Extra parameters are passed through.
- Preview validates the revision first, refusing errors with 422 `REVISION_INVALID` and listing `context.errors`; it does not return validation warnings. A series binding resolves its table from `run_params[period_param]`, and a missing period or a period with no series member returns 422 `SCOPE_ERROR`. Missing or blank column run values, missing cell run values and row-filter scope errors also return 422 `SCOPE_ERROR` (exit 2). [backend]
- `--max-rows` is 1–1000, default 50, per sheet. Preview samples only kept clean rows, ordered by the sheet's sort keys then row ID, using the table's display formats; the kept, left out and blocked counts cover all live clean rows rather than the sample. Run-value columns use `$run.<name>` keys and can repeat in `columns`. Aggregate cells use all kept rows rather than the sample; a cell filter that cannot decide a row refuses the preview with 422 `SCOPE_ERROR`. Unless the caller holds `pii:view` in the table's organization or an active PII access grant on the table, each bound PII column's values come back masked in `rows`, listed in the sheet's `masked_fields`, and a `sum`, `min` or `max` cell over a PII column comes back masked with its own `masked_fields` naming that column; `-o json` keeps those lists, and the table view prints them as a `Masked fields:` line above the sheet's rows and a `(masked: <column>)` suffix on the cell. [backend]
- `deliverable test-filter` reads a sheet's saved row filter report when given a deliverable, or counts a manifest's unsaved filter with `-f`; giving both or neither exits 2. With a deliverable it selects the revision as `describe deliverable-revision` does, in any state, and `--sheet` is an exact bound sheet name, a missing one exiting 3. A sheet that follows a table series needs `--period <period key>`, naming the member table to read, and `--period` on a fixed-table sheet exits 2; both checks need the revision's bindings, so they run after authentication, while `--table`, `--table-id` and a blank `--period` exit 2 before it.
- The saved report requires `DELIVERABLE_ADMIN` and no grant on the table, and reads a revision in any state without a run. Its counts are exact, never capped, and cover every live clean and quarantine row of the sheet's table, or of the series member whose period key is exactly `--period`, before release gates and waivers: kept and left out add up the two tables' rows, while blocked counts distinct row ids across both. `inspect deliverable` counts only clean rows. [backend]
- The report's left-out sample holds at most five rows, lowest row ids first across the clean and quarantine tables, each carrying the values of only the columns the filter reads. Unless the caller holds `pii:view` in the table's organization or an active PII access grant on the table, the values of its PII columns come back masked and are listed in the report's `masked_fields`; `-o json` keeps that list, and the table view prints it as a `Masked fields:` line after the sample. With no row filter every row is kept, so nothing is left out or blocked. [backend]
- The report answers 409 `SCOPE_ERROR` for a period the series has no member for and for a deleted table, and 409 `TABLE_SETUP_IN_PROGRESS` while the table's data storage is not ready. A saved filter that no longer compiles against the table, such as one reading a deleted column, and a filter column whose type family (number, date, or text including UUID and Boolean) differs between a period's table and the series' reference table, answer 422 `SCOPE_ERROR`. Each exits 2. [backend]
- `deliverable test-filter -f` reads one `kind: Deliverable` document and counts its `spec.sheets.<sheet>.row_filter`, which must be set and not null, without saving it: on `--table` or `--table-id` when given, such as a series member, else on the sheet's `table`, which for a sheet with `series` is the reference table. It calls the count route `apply -f` calls and prints apply's `Row filter <key> on table <id>:` heading over `Keeps M of T rows`; a 422 exits 2 naming the key, and any other failure, which apply only warns about, keeps its exit code. `--release`, `--revision` and `--period` exit 2 with `-f`.
- The saved report prints `deliverable/<id> release "<release>" sheet "<sheet>" on table <id>` (a series sheet: `on period "<period>" of series <uuid>`), the row filter as `describe deliverable-revision` prints it, a `K kept / L left out / B blocked` line, a `Blocked:` line when B is not 0, and one `row <id>: column=value, ...` line per sampled row, columns sorted and a quarantine row's id followed by `(quarantined)`; a table with no live rows prints `No live rows: the table is empty.` instead of the counts. `-o json|yaml` prints the report response, or with `-f` the count response, as the server sent it, with the prose on stderr; `-o name` exits 2.

- Literal column sources appear as `literal "<value>"` in the binding tree, suggestion report and column confirmation report, including `literal ""` for empty text. Confirming a flagged literal column preserves its value and format.

## SEE ALSO

- `qctl explain deliverable`
- `qctl explain deliverable-template`
- `qctl explain table-series`
