---
summary: one issue of a deliverable for a set of run parameters, with its render, sources and readiness
aliases:
kind:
schema: openapi:DeliverableRunResponse
guide: deliverables
signature: 3756b25
---

## USAGE

    qctl create deliverable-run <deliverable-ref>
    qctl deliverable-run cancel <id>
    qctl deliverable-run drift <id>
    qctl deliverable-run mark-delivered <id>
    qctl deliverable-run override-source <id> [<source-id>]
    qctl deliverable-run readiness <id>
    qctl deliverable-run render <id>
    qctl deliverable-run sign-off <id>
    qctl deliverable-run unwaive <id> <waiver-id>
    qctl deliverable-run waive <id>
    qctl describe deliverable-run <id>
    qctl download deliverable-run <id>
    qctl get deliverable-run <id>
    qctl get deliverable-runs [<deliverable-ref>]

## EXAMPLES

    # create a run for period 2026-09: a new period, or a re-issue when a delivered run has exactly these parameters
    qctl create deliverable-run monthly-bordereau --period 2026-09

    # every run parameter from a YAML mapping, and let the run render with no rows
    qctl create deliverable-run monthly-bordereau --params-file params.yaml --allow-empty

    # re-issue the period delivered with parameters {period: 2026-08} on the enabled release, printing the server's answer
    qctl create deliverable-run monthly-bordereau --period 2026-08 --use-enabled-revision -o json

    # create a run for period 2026-09, make its draft file, and wait until the file is made or the render fails
    qctl create deliverable-run monthly-bordereau --period 2026-09 --render --wait

    # make, or re-make, run 42's draft file ("Make the draft file" in the web app) and wait for it
    qctl deliverable-run render 42 --wait

    # only request the render, printing the run as it was before a worker picked it up
    qctl deliverable-run render deliverable-run/42 -o json

    # review the rendered report's parameters, totals and left-out count, then confirm sign-off
    qctl deliverable-run sign-off 42 --context prod

    # sign off the reviewed render and print the server's signed-off run
    qctl deliverable-run sign-off deliverable-run/42 --context prod --yes -o json

    # record how the final file was delivered ("Mark as delivered…" in the web app)
    qctl deliverable-run mark-delivered 42 --note "Emailed to the carrier" --context prod

    # record delivery without an interactive prompt, printing the server's updated run
    qctl deliverable-run mark-delivered deliverable-run/42 --note "Sent through the portal" --context prod --yes -o json

    # abandon an eligible run permanently ("Cancel run" in the web app)
    qctl deliverable-run cancel 42 --context prod

    # cancel without an interactive prompt, printing the server's updated run
    qctl deliverable-run cancel deliverable-run/42 --context prod --yes -o json

    # download the rendered spreadsheet using its server filename and verify its SHA-256
    qctl download deliverable-run 42

    # choose the destination and overwrite an existing file
    qctl download deliverable-run deliverable-run/42 --output reports/bordereau.xlsx --force

    # list runs across the organization, including deliverables in the trash
    qctl get deliverable-runs

    # the second page of rendered runs, oldest first, with deliverable ids and names in JSON
    qctl get deliverable-runs --state rendered --reverse --page 2 --limit 5 -o json

    # list one deliverable's runs, newest first
    qctl get deliverable-runs monthly-bordereau

    # leave a blocked row out before rendering ("Leave out…" in the web app); save the printed waiver id
    qctl deliverable-run waive 42 --table claims --row-id 7 --reason "Duplicate claim"

    # print only the created waiver id for a script to save
    qctl deliverable-run waive deliverable-run/42 --table-id 103 --row-id 7 --reason "Late adjustment" -o name

    # remove waiver 15 so its row returns to readiness ("Include again" in the web app)
    qctl deliverable-run unwaive 42 15 --context dev

    # make run 42's Claims sheet read a corrected table ("Change table…" in the web app)
    qctl deliverable-run override-source 42 --sheet Claims --table claims-2026-09-corrected --reason "Corrected file from the carrier"

    # make the total in cell B4 of sheet Summary read table 103
    qctl deliverable-run override-source 42 --cell 'Summary!B4' --table-id 103 --reason "Totals from the restated table"

    # override source 7 by its id, printing the run the server answers with
    qctl deliverable-run override-source deliverable-run/42 7 --table-id 103 --reason "Corrected file" -o json

    # list only the runs in state rendered ("Draft ready" in the web app)
    qctl get deliverable-runs monthly-bordereau --state rendered

    # read one run's API record, including the table each binding reads
    qctl get deliverable-run 42 -o json

    # show a run's render status, totals, sources and readiness summary
    qctl describe deliverable-run 42

    # list the rows that fail rules and so block the run's sign-off
    qctl deliverable-run readiness 42 --bucket blocked_failing_rules

    # show waived rows, their stored notes and any undecided row filters
    qctl deliverable-run readiness 42 --bucket waived

    # the whole readiness response, with the first page of rows
    qctl deliverable-run readiness 42 -o json

    # show how delivered run 42's rows differ from the live rows, and the remedy
    qctl deliverable-run drift 42

    # the complete drift report as JSON, up to the server's 10000-row maximum
    qctl deliverable-run drift 42 --limit 10000 -o json

## GOTCHAS

- With no reference, `get deliverable-runs` lists runs across the current organization, including archived deliverables, and names each run's deliverable using its current name. The organization listing requires `deliverable:admin`. [backend]
- `get deliverable-runs [<deliverable-ref>]` lists one page, newest first; `--reverse` lists oldest first in either scope. `--state` accepts `open`, `rendered`, `stale`, `signed_off`, `delivered` or `cancelled`; `--page` starts at 1 and `--limit` defaults to 20. Table output adds `DELIVERABLE` when no reference is given; `--columns deliverable,deliverable_id,id` selects the name and ids. JSON/YAML print a List whose organization-wide items include `deliverable_id` and `deliverable_name`; `next_page` names the next page or is null. Table and name output print a more-pages hint on stderr when another page exists. Pass a slug, name or UUID to keep the listing to one deliverable, including one in the trash.
- `deliverable-run mark-delivered` requires a non-blank `--note` describing how the final file was sent, trims it, and records delivery without sending the file. `deliverable-run cancel` sends no body. Both ask for y/N confirmation on a terminal and require `--yes` in a script; `--yes` does not satisfy the protected-context gate. Each sends one POST without predicting state eligibility or retrying a refusal. Table output acknowledges the change, JSON/YAML preserve the server's updated run, and name output prints `deliverable-run/<id>`.
- Marking delivered requires `deliverable:admin` and state `signed_off`. The server records `delivery_note`, `delivered_by_id` and its own clock's `delivered_at`; it refuses an older-period re-issue with 409 `REISSUE_NOT_LATEST`, a new-period run while a latest-period re-issue is pending with 409 `REISSUE_PENDING`, and an invalid state with 409 without an error code (all exit 2). Delivery cannot be reversed, joins the delivered period groups used by later runs and completeness, and keeps the run's source tables locked against deletion, series removal and period changes, except that `table-series delete-period` and `recreate-period` still delete or replace the table's period and list the run as one to re-issue. [backend]
- Cancellation requires `deliverable:admin` and state `open`, `rendered` or `stale`, or a `signed_off` re-issue of a delivered period that is no longer the latest; other states return 409 without an error code (exit 2). Unlike the web UI, qctl allows the server to decide the signed-off case. Cancellation cannot be reversed, keeps the rendered file and the run's place in issue numbering, and removes the run from the in-flight checks and carrier selection. It releases this run's source-table locks; other non-cancelled runs and enabled bindings may still lock those tables. [backend]
- `download deliverable-run` needs both the `deliverable:admin` and the `pii:view` permission — the `admin`, `ops`, `member` and `editor` roles hold both, while the `agent` role holds only `deliverable:admin` — and a temporary PII access grant does not stand in for `pii:view`: without it the request is refused with 403 (exit 4) before any 409. It can download a rendered file even from a cancelled run. No rendered file, no signature, no template on the rendered revision, an invalid filename pattern or missing organization backup settings is a 409 refusal (exit 2). [backend]
- The download name comes from the run's saved filename pattern and parameters, with `.xlsm` or `.xlsx` from the rendered revision's template; it gains `-DRAFT` unless the run is `signed_off` or `delivered`. Parameter text can put `/` in the name, and the storage URL's Content-Disposition defaults to the storage key's basename, so qctl uses the response's `filename`, reduced to one path element in the working directory. [backend]
- Every successful download-URL request records a download audit before any bytes are transferred. The presigned GET uses `DOWNLOAD_URL_VALID_FOR_MIN` (240 minutes by default), but its response includes neither the expiry nor the file signature. [backend]
- `download deliverable-run` reads the run's `rendered_file_signature` before minting its URL and compares it with the downloaded file's SHA-256. A mismatch exits 1 and removes the file; a concurrent render can replace the file between those requests, so retry the download. A missing or malformed signature after a successful mint exits 1 before downloading. DRAFT notes and progress go to stderr; only success goes to stdout.
- `download deliverable-run --output <path>` chooses the destination path, preserving directories the user supplies; without it qctl uses the sanitized server filename. An existing destination exits 2 unless `--force` is given; an explicit path collision is refused before any request. Newly created files have permissions `0600`.

- `deliverable-run sign-off` reads the run and refuses a state other than `rendered` with exit 2, then reads live readiness. Before confirming it prints the render attempt, parameters, last-render totals, the waived row count and readiness summary to stderr, even with `--yes`. The left-out count sums the sheets' waivers, waived unbound carried rows and `waived_without_sheet`; filter exclusions are separate per-sheet counts. A terminal gets a y/N prompt; a script needs `--yes`. The protected-context gate runs first: `--yes` does not substitute for `--context`. Table output acknowledges the locked report; `-o json|yaml` preserve the server's signed-off run, and `-o name` prints `deliverable-run/<id>`.
- Sign-off needs `deliverable:run:signoff`, held by member, admin and ops roles; editor and agent roles cannot sign off (403, exit 4). Signing off freezes the run's rendered artifact and has no reversal route. A signed-off new-period run cannot be cancelled and blocks creation of another new period or any re-issue until it is delivered. [backend]
- qctl sends the previewed `attempt_id` as `expected_attempt_id` without refreshing it after confirmation. A mismatch is 409 `STALE_ATTEMPT` (exit 2): re-read the run and review its current render before retrying. A 409 `SIGNOFF_BLOCKED` (exit 2) prints the refusal's aggregate bucket counts when the server supplies them and points at `deliverable-run readiness`; those counts may all be zero when drift or another check refused sign-off, so the server's message still matters. qctl does not automatically retry the sign-off POST. [backend]

- `deliverable-run waive` requires exactly one of `--table` (an exact name) or `--table-id`, a positive `--row-id`, and a non-blank `--reason`, which it trims. Before posting, qctl scans live readiness in pages of 500 until the exact table/row appears in `blocked_awaiting_review`, `blocked_failing_rules`, `blocked_indeterminate` or `carried_unbound`, or matches a structured `errors[].waivable_row` target on a `carried_rows` error. The response must name the requested run in `open`, `rendered` or `stale`. A row without either form of eligibility is refused with exit 2. A row can appear on several sheets; an eligible entry on any one permits the waiver. Every page recomputes readiness, so a large run can take several requests and a concurrent change can move rows between pages.
- Structured `waivable_row` evidence contains positive `member_dataset_id` and `row_id` for an undecided older-table row that may be absent from readiness's rows. qctl prints the exact waiver command beside that error; JSON/YAML retain the original response. A server that supplies no target retains the existing refusal for an unlisted row. qctl never extracts identities from error prose, and evidence on a warning or another binding kind permits no waiver.
- A waiver names a table and row on one run, across its sheets. It leaves blocked and unbound carried rows out of that run; it also counts for completeness when the run is the carrier and an older delivered sheet's filter cannot decide that clean, undelivered row. It does not remove included or carried-forward rows from the run's readiness. It changes no table data. Completeness also counts waivers on delivered runs of the missed period's group or a later group. The server rechecks eligibility before creating a waiver, returning 409 `WAIVER_NOT_ELIGIBLE` (exit 2) for a row no longer listed as blocked, `carried_unbound` or a `carried_rows` error's `waivable_row`. Both create and delete require state `open`, `rendered` or `stale`; a later state is 409 (exit 2), and a duplicate create is 409 `WAIVER_EXISTS` (exit 2). [backend]
- Keep the created waiver id: there is no waiver list route, and readiness carries a waiver reason but no waiver id. `deliverable-run waive -o name` prints only the numeric waiver id; JSON/YAML print the whole create response. `deliverable-run unwaive <run-id> <waiver-id>` needs that saved id. [backend]
- `deliverable-run unwaive` hard-deletes the waiver and its reason, leaving no history entry; the row returns to readiness, where its current rules and review status decide whether it is included or blocked. qctl supports table output only for this command and requires an explicit `--context` on a protected context because the waiver record is permanently removed. [backend]
- `deliverable-run override-source` names the source with exactly one of `--sheet` (an exact sheet name), `--cell Sheet!A1` (a total that reads a table of its own) or the source id from `get deliverable-run -o json`, and the new table with exactly one of `--table` (an exact name) or `--table-id`; `--reason` must be non-blank and is trimmed. qctl reads the run and its revision on every call, resolving `--sheet` and `--cell` through the revision's bindings rather than a saved source id, and refuses with exit 2 before sending: a run that is not `open`, `rendered` or `stale`, a sheet or cell the release lacks, a cell that is not a total, a total of a sheet's rows (override that sheet instead), and a table the source already reads. An unknown source id exits 3. The table view prints the binding, the new, previous and resolved table, the reason, the source's warning and whether the run is stale; under `-o json|yaml` stdout is the server's whole run and under `-o name` it is `deliverable-run/<id>`, with the warning and stale lines on stderr.
- An override accepts any table of the organization that is not deleted (otherwise 404, exit 3), with no series, schema or period check. A table that holds another period than the run's, or is in no series while the run has a period, only sets the source's `warning`; a column a binding needs that the table lacks is a readiness error. [backend]
- An override needs the `deliverable:admin` permission and a run in state `open`, `rendered` or `stale` (any other state is 409, exit 2). It turns a `rendered` run `stale` with `stale_reason` `source_overridden`; an `open` run stays open and a `stale` run keeps its `stale_reason`. The source keeps `resolved_dataset_id`, the table resolution picked, and records the reason, author and time. Nothing clears an override: pointing the source back at `resolved_dataset_id` is another override, and so is pointing it at the table it already reads, which qctl refuses. [backend]
- A table that a non-cancelled run's source reads, delivered runs included, cannot be deleted, leave its series or change its period (409 `DATASET_BOUND_TO_DELIVERABLE`), so an override locks the new table until its run is cancelled. It releases the original table only from this run's source: an enabled revision whose sheet or total names that table still blocks its deletion, and every other non-cancelled run that reads it still blocks all three. The ops-only delete or recreate of the table's series period, which soft-deletes the table, is refused (409 `SERIES_PERIOD_CHANGE_BLOCKED`) by such a revision or an `open`, `rendered`, `stale` or `signed_off` run, but not by a delivered run, which it lists in `runs_to_reissue` instead. [backend]
- Enabling a revision moves each `open`, `rendered` or `stale` run in mode `new_period` or `reissue_enabled` onto it and recreates the run's sources, so source ids change. An overridden source keeps its table, reason, author and time on the new revision's sheet of the same name, or on its total at the same sheet and cell that totals no sheet's rows; when the new revision has no such sheet or total, the enable is refused with 409 naming the run and nothing moves. `reissue_as_delivered` runs never move. [backend]
- A run is named by its integer id, or by the `deliverable-run/<id>` that `get deliverable-runs -o name` prints; `0`, a negative number or anything else exits 2 before authentication.
- `describe deliverable-run` reads no readiness for a cancelled run. When the run reads but its readiness does not, it still prints the run, without readiness (`readiness` is null under `-o json|yaml`), and exits with the readiness failure's code.
- UI words: the run states In progress, Draft ready, Draft out of date, Signed off, Delivered and Cancelled are `open`, `rendered`, `stale`, `signed_off`, `delivered` and `cancelled`; "issue #N" is `sequence`; a run's "setup version" is its revision, named by its release; the revision modes New period, "Re-issue with the setup it was delivered with" and "Re-issue with the current setup" are `new_period`, `reissue_as_delivered` and `reissue_enabled`; "As of sign-off" is a frozen readiness (`is_live` false, which qctl prints as `as of sign-off`); and the readiness buckets Needs review, Failing rules, Unreadable value, Left out, Carried forward and "Carried, sheet not in this setup" are `blocked_awaiting_review`, `blocked_failing_rules`, `blocked_indeterminate`, `waived`, `carried_forward` and `carried_unbound`; "+ New run" on the Runs tab and "Create re-issue" on Missed rows are `create deliverable-run`, and its "Allow an empty report (nil bordereau)" is `--allow-empty`; "Make the draft file" and "Re-make draft" are `deliverable-run render`; and "Change table…" on a run's source is `deliverable-run override-source`.
- The readiness of an open, rendered or stale run is recomputed from its tables on every call, `describe deliverable-run` included, and `--bucket`, `--page` and `--limit` only cut the finished row list, so none of them makes it faster. The server gives up after its `REQUEST_TIMEOUT_SEC`, 30 seconds by default, answering 504 `ERR_TIMEOUT` (exit 1); qctl waits up to 45 seconds per attempt unless a nonzero `--request-timeout` is given. `--retries N` (default 0) retries this GET after a 504, repeating the server computation for up to `N+1` attempts with separate deadlines, plus retry backoff. [backend]
- A cancelled run has no readiness: 409 (exit 2). A `signed_off` or `delivered` run's readiness is frozen rather than recomputed: `is_live` and `is_signoff_blocked` are false and `errors` is empty, and it lists each sheet binding of the run's revision by sheet name, with blocked and out-of-scope counts of 0 and the `row_filter_report` its last render recorded, null when that render recorded none. Its rows are the run's manifest (`included`, `carried_forward`) and its waivers (`waived`), with `quarantined` and `row_filter_undecided` null; waived rows retain their `waiver_reason`. `warnings` still lists each source that has a `warning`. [backend]
- Each readiness row's `waiver_reason` is its stored waiver note for bucket `waived`, otherwise null. `row_filter_undecided` says whether the filter used to classify a live row could not decide it, including waived rows; carried rows use the earlier delivered sheet's filter. Frozen rows have `row_filter_undecided` null. [backend]
- `deliverable-run readiness`'s table view shows `WAIVER REASON` separately from the row's diagnostic `REASON`, and `FILTER UNDECIDED` as `yes`, `no` or `-` for null. A frozen row's `-` means no live filter outcome was computed; `no` means the live filter decided the row. JSON/YAML preserve the server's whole response, including both nullable fields.
- Readiness masks by the reader, live and frozen alike: for a caller without `pii:view` in the table's organization and with no active PII access grant on the table, each sheet's `row_filter_report` carries its left-out sample's PII values masked (`•••••` plus the last 4 characters at 9 or more, else `•••••`) and names them in its `masked_fields`, which `deliverable-run readiness`'s table view prints as a `Masked fields:` line under the sheet; the row-filter reasons of undecided rows and the live `errors` messages mask those values too, with nothing naming them. [backend]
- In live readiness, a sheet whose binding does not hold for the run has no `sheets` entry and none of its rows are listed; each reason is an `errors` entry instead. That covers no table or several for the sheet, a deleted table, a bound, sort or row-filter column that the table lacks or holds in another type family than the reference table, a row filter that does not compile against the table, and a run-value column whose parameter the run leaves unset or blank. [backend]
- `is_signoff_blocked` (qctl's `sign-off blocked`) is true for a live run with any `errors` entry or any blocked row no waiver covers, carried rows awaiting review included. False (`no errors or blocked rows`) is not the whole sign-off check: sign-off also needs, among other checks, state `rendered`, a last render made with the run's own revision, and live included and carried-forward rows that still match that render's, so rows released or removed since the render mean a re-render first. [backend]
- `get deliverable-runs -o json|yaml` list items carry no `sources`, which the list route leaves out; `get deliverable-run <id> -o json|yaml` and `describe deliverable-run` show the table each of the run's bindings reads. [backend]
- `create deliverable-run` reads `--params-file` and `--period` as `inspect deliverable` does (`qctl explain deliverable-revision`) and always sends them as `run_params_typed`, each value with the type its YAML wrote. It prints `deliverable-run/<id> created` with the issue number, the revision mode the server chose (`new period`, `re-issue as delivered` or `re-issue on the enabled revision`), the release and the parameters; `Note:` lines go to stderr.
- The server makes the run a re-issue when its parameters equal a delivered run's as text, and a new period otherwise: it compares the whole key set and each value's Python `str()`, with null distinct from blank and the type ignored. So integer `2026` and float `2026.0` are different periods, string `"2026"` and integer `2026` are the same one, and a parameter the release never uses still makes a new period. [backend]
- A re-issue takes the revision and filename pattern of the newest delivered run with its parameters (`reissue_as_delivered`), or the enabled revision and the deliverable's filename pattern with `--use-enabled-revision` (`reissue_enabled`); a new period takes the enabled revision (`new_period`). The issue number (`sequence`) is one more than the runs already holding those parameters, cancelled ones included. [backend]
- Only the latest delivered period, the one whose earliest delivered run has the highest id rather than the highest period value, can be re-issued: any other is 409 `REISSUE_NOT_LATEST`. A new-period run is one whose parameters match no delivered run's; creating one is 409 `NEW_PERIOD_IN_FLIGHT` while another is `open`, `rendered`, `stale` or `signed_off`, and so is a re-issue while one is `signed_off`. qctl predicts neither refusal and adds a `qctl get deliverable-runs <ref> --state <state>` hint. Nothing refuses a second re-issue of a period while one is pending, so qctl prints a `Note:` naming the pending run. [backend]
- At create the server checks no parameter against the release except the period parameter, which missing or blank is 422 `SCOPE_ERROR` only when a sheet or total of the release follows a series. Any other missing parameter surfaces later: a run-value column's is a readiness error, which refuses the render, a cell run value's refuses the render, and a filename `{token}`'s refuses the download with 409. [backend]
- For a new period qctl requires every parameter the enabled release declares, derived as `inspect deliverable` derives them, and exits 2 before creating anything when one is missing, null or blank, or when the deliverable has no enabled release; a parameter the release does not use prints a `Note:` and is still sent. A re-issue's parameters are exactly a delivered run's, so they are not checked, but with `--use-enabled-revision` each name the enabled release declares that they lack prints a `Note:`, since adding it would make the run a new period. `--use-enabled-revision` on a new period has no effect and prints a `Note:`.
- `deliverable-run render` only queues the render, for a run that is `open`, `rendered` or `stale` (any other state is 409, exit 2): it answers with the run as it was before any worker picked the render up, so without `--wait` it returns at once, and repeated requests for a run are coalesced. The queue entry commits together with the request, so a 200 means the render is queued; the server retries handing it to a worker rather than dropping it. [backend]
- A failed or refused render changes no state; it sets `render_error`, which the next attempt clears when a worker picks it up. A failed first render leaves the run `open`, and a failed re-render keeps the previous draft file and the run's state. Sign-off never reads `render_error`, so a `rendered` run whose re-render failed can still be signed off with that older file. [backend]
- A worker refuses the render and records why in `render_error` when the run's readiness is sign-off blocked (fix or waive those rows first), when it would have no rows and was created without `--allow-empty`, when the setup does not hold for the run, when the revision has no template, when the template's labels do not match the bindings, and when the organization has no backup settings to store the file. [backend]
- A successful render deletes the files of the run's earlier attempts, so a download URL minted before it stops working. A render that succeeds while a table the run reads is overridden, its revision is superseded or its rows drift lands the run `stale`, not `rendered`. A render is stopped after `MAX_COMPOSITE_RUN_JOB_RUNTIME`, 60 minutes by default. [backend]
- `--wait` on `deliverable-run render` and `create deliverable-run --render` settles on the first render attempt after the request: `attempt_id` above the request's answer and either `rendered_at` moved or `render_error` set, or the run reaching `signed_off`, `delivered` or `cancelled`. It exits 0 only for `rendered` with no `render_error`, 1 otherwise with the render error or the run's message, and 124 when `--wait-timeout` (default 900 seconds) runs out, saying whether a worker ever picked the render up; under `-o json|yaml|name` stdout stays the request's answer and the outcome goes to stderr.
- `computed_totals`, describe's `Totals (last render)`, are what the run's last successful render recorded, and null before one; only a successful render replaces them, so a failed render and data changed since leave them as they were. A sheet's entry counts the rows it rendered, included plus carried forward; a `sum`, `min` or `max` cell over a money or percent column, or one with `display_decimals` set, records the displayed text, such as `$1,234.57`, as its `value`. [backend]
- `deliverable-run drift` only reports: it writes nothing and creates no correction entries. It needs the `deliverable:admin` permission (403, exit 4, without it), and a run in any state but `delivered` answers 409 with no error code (exit 2). [backend]
- Drift `remedy` is `none` when no row drifted or the run is superseded; `superseded_by_run_id` is the highest-id delivered run with the same run parameters when that is not this run, and a signed-off re-issue supersedes nothing. Otherwise `remedy` is `reissue` for the latest delivered period, the parameters group whose earliest delivered run has the highest id, and `restatement` for an older period, which an operator arranges as a restatement or contra entry. `can_reissue` does not depend on the rows: it is true for the latest period when nothing supersedes the run and no new-period run is signed off, so `reissue` with `can_reissue` false means a signed-off new-period run exists. `reissue_run_id` is a re-issue already in flight: the lowest-id open, rendered, stale or signed-off run with the same run parameters. [backend]
- Drift `--limit` defaults to 1000 and takes 1 to 10000; qctl refuses anything else with exit 2 before sending. Rows are ordered by table id, sheet name and row id, and `truncated` is true exactly when more rows drifted than `limit`. A small limit saves work only when many rows drift: the server loads the run's whole manifest and compares it a whole table at a time until more than `limit` rows have drifted, all under its `REQUEST_TIMEOUT_SEC`, 30 seconds by default, after which it answers 504 `ERR_TIMEOUT` (exit 1); qctl waits up to 45 seconds per attempt unless a nonzero `--request-timeout` is given. `--retries N` (default 0) retries this GET after a 504, repeating the server computation for up to `N+1` attempts with separate deadlines, plus retry backoff. [backend]
- A drift row's `sheet_name`, `sheet_binding_id`, `member_dataset_id` and `row_id` are where the row was delivered; nothing in the response says which sheet the row belongs to now. Of the row's identity fields only `live_row_version_id` and `live_history_ids` are live, and both are null when the row is missing or its table unavailable. The scope reasons test the row filter of the binding the row was delivered with, not the current revision's. No later run's manifest is read, so a row that a change moved to another sheet stays listed on its delivered sheet as `no_longer_in_scope` after a later run carries it onto the new one: carrying does not reverse the copy already delivered. [backend]
- Drift `reasons`, several per row possible: `row_missing` (no live row has the id), `row_version_changed` (the live version differs from the delivered one), `history_changed` (the latest active history ids differ), `quarantined` (the live row is in quarantine, held or released), `no_longer_in_scope` (the delivered binding's row filter now excludes it), `filter_indeterminate` (the filter cannot decide the row, or cannot be applied to the table at all, which marks every live row delivered on that sheet) and `table_unavailable` (the table is missing or deleted), which is then the row's only reason. `filter_reason` says why, and is set only with `filter_indeterminate`. [backend]
- UI words for `deliverable-run drift`: it is a delivered run's "Changes after delivery" panel, which asks for 500 rows; the panel's reasons Row no longer exists, Row changed since delivery, Active history changed since delivery, Row is now quarantined, "Row no longer matches the original sheet's filter", "The row filter can't tell whether this row belongs in the original sheet" and Original table is unavailable are `row_missing`, `row_version_changed`, `history_changed`, `quarantined`, `no_longer_in_scope`, `filter_indeterminate` and `table_unavailable`.
- `deliverable-run drift`'s table view leaves out each row's `sheet_binding_id` and history ids; `-o json|yaml` prints the server's whole response, and `-o name` is refused (exit 2).

## SEE ALSO

- `qctl explain deliverable-run`
- `qctl explain deliverable`
- `qctl explain deliverable-revision`
