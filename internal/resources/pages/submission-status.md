---
summary: where a table-series cloud source's submission for one reporting period stands on the MGA's broker board
aliases:
kind:
schema: openapi:SubmissionBoardRowSchema
guide: deliverables
signature: b3fae6d
---

## USAGE

    qctl get submission-statuses
    qctl submission-status unwaive --cloud-source-id <cloud-source-id> --period <period>
    qctl submission-status waive --cloud-source-id <cloud-source-id> --period <period> --reason <reason>

## EXAMPLES

    # how many intakes of each series are in each status, for the period each series is collecting
    qctl get submission-statuses

    # one series' board for the period being collected: one row per cloud source expected for it
    qctl get submission-statuses --series "Monthly bordereau"

    # the same board for an earlier period, by series UUID
    qctl get submission-statuses --series 550e8400-e29b-41d4-a716-446655440000 --period 2026-09

    # one intake across every period it is expected for, newest first
    qctl get submission-statuses --cloud-source-id 42

    # which intakes are waived, when, and why
    qctl get submission-statuses --series "Monthly bordereau" --columns cloud_source,status,declared_at,reason

    # why a file on the board has not loaded and who fixes it
    qctl get submission-statuses --series "Monthly bordereau" --columns cloud_source,status,file,file_id,alert_id,fix_owner,hold_reasons

    # the board as the API returns it: header fields, counts and rows
    qctl get submission-statuses --series "Monthly bordereau" -o json

    # each intake on the board as a reusable reference
    qctl get submission-statuses --series "Monthly bordereau" -o name

    # each series as a reference --series takes back
    qctl get submission-statuses -o name

    # waive one intake's period, so the board shows it as waived
    qctl submission-status waive --cloud-source-id 42 --period 2026-09 --reason "Broker closed for the month"

    # withdraw that waiver
    qctl submission-status unwaive --cloud-source-id 42 --period 2026-09

    # waive a quarter and print the period's status as the API returns it
    qctl submission-status waive --cloud-source-id cloud-source/42 --period 2026-Q3 --reason "No business written" -o json

## GOTCHAS

- Three scopes, picked by flag: `--series <name-or-id>` is one series' board for one period, one row per active cloud source expected for that period; `--cloud-source-id <id>` is one series cloud source across the periods it is expected for, one row per period; neither is one row per visible series with its status counts. `--series` with `--cloud-source-id` exits 2, and so does `--period` without `--series`. A blank `--series`, a blank or whitespace-padded `--period`, and a non-positive `--cloud-source-id` exit 2 before authentication.
- `--period` is sent as typed and never checked against the series frequency or computed locally; without it the server picks the period. A `--series` name resolves through the series listing, the same read `get table-series` makes, before the board is read; a full UUID or `table-series/<uuid>` goes straight to the board.
- Under the default table output, `--series` writes the series, its frequency, the period and its dates, the series' broker due date, the MGA deadline and whether the period is locked to stderr, then the rows to stdout, then a count line such as `9 intakes: 2 late, 1 on_hold, 6 clean` to stderr. `--cloud-source-id` writes the source's name to stderr above its rows. When a row has an alert stopping its file, a `Next: qctl describe alert <id>` line follows on stderr.
- `-o json|yaml` re-encodes the API records from qctl's generated API types, under the wire names, rather than pass the server's bytes through, so a field newer than qctl's client is dropped: the whole board object (`series_id`, `period_key`, `broker_due`, `mga_deadline`, `is_locked`, `counts`, `rows`) for `--series`, and a `kind: List` envelope of rows or per-series counts for the other two scopes. `-o name` prints `cloud-source/<id>` per `--series` row, `table/<id>` per `--cloud-source-id` period that has a table, and `table-series/<uuid>` per series, which `--series` accepts back.
- `STATUS` is the row's `board_status`; the wire's `status` is the coarser submission status without `late` or `on_hold`. `FILE` and `FILE-ID` name, while the status is `on_hold` or `received_not_loaded`, the row's file waiting for its period (`waiting_file`) or else its file that has not loaded, and the newest counted file otherwise. `ALERT-ID`, `FIX-OWNER` and `HOLD-REASONS` come from that waiting or unloaded file and are `-` on every other row. `DAYS-LATE`, `BAD-ROWS` and `LAST-REMINDED` show `-` when the server sends null. `DECLARED-AT` and `REASON`, which the defaults leave out, are `-` except on a `waived` or `nil` row, and `REASON` is `-` on a `nil` row too. The two boards share one set of columns, with different defaults; `--columns` naming a counts column on a board, or a board column on the counts, exits 2 before authentication.
- When `--period` is omitted, and always for the per-series counts, the period is the one being collected on today's date in the organization's time zone: the earliest period that ended before today whose MGA deadline is today or later, else the latest period that ended before today. A `--period` that is not a key of the series frequency returns 422 `SERIES_PERIOD_KEY_INVALID` (exit 2). [backend]
- All three reads need `dataset:read`. Admin and ops see every series of the organization; any other role sees only series with an undeleted table it holds a grant on. An unknown or unseen series returns 404 (exit 3), and so does a `--cloud-source-id` that is deleted, belongs to no series of the organization, or belongs to a series the caller does not see. [backend]
- Only active cloud sources are on a board: a series board and the counts leave out a series source that is disabled or a draft, and `--cloud-source-id` of one returns an empty list rather than 404. A source is expected from the later of the series' `first_period_key` and its own `start_period_key`, whichever are set, through the earlier of its `end_period_key` and the period holding today, so `--cloud-source-id` lists the unfinished current period too, and a series board leaves out a source not expected for its period. With neither first period set, a source is expected for no period. [backend]
- A waiver in effect makes a period `waived`. Otherwise a loaded file makes it `received` while the source has bad rows on the period's table, else `clean`; any other counted file makes it `received_not_loaded`, or `on_hold` when the alert stopping it holds the file before loading (`preload_check_held`, `inbound_email_held`, or a `series_file_routing` alert waiting for someone to name the file's period); a nil return makes it `nil`; nothing makes it `expected`, or `late` once the source's broker due date has passed. An `expected` or `received_not_loaded` period that is not locked is `on_hold` instead, and not late, while a file of the source waiting for its period could belong to it; the row's `waiting_file` names the newest such file. `days_late` is set while a period is `late`, `on_hold` or `received_not_loaded` past that date with no `waiting_file`. [backend]
- Rows are ordered by board status (`late`, `on_hold`, `received_not_loaded`, `received`, `expected`, `clean`, `nil`, `waived`), then most days late or most bad rows first, then by cloud source name ignoring case. A row's `broker_due` is that source's own due date, with its `broker_due_days` override applied, while the board's `broker_due` is the series'; `last_reminded_at` is the latest deadline reminder sent to the broker for that source and period. [backend]
- The per-series counts list every series the caller sees, ordered by name, each for its own period being collected, and a series with no active source has a row of zeros. [backend]
- `submission-status waive` and `unwaive` both require `--cloud-source-id <id>` (or `cloud-source/<id>`) and `--period <key>`; a non-positive id and a blank, whitespace-padded or multi-line `--period` exit 2 before authentication. `waive` also requires `--reason <text>`: a missing or blank one exits 2 before authentication, and it is sent with surrounding whitespace trimmed. The key is sent as typed, as one path segment, and never checked against the series frequency. Each makes one request with no prompt, and neither needs `--context` on a protected context.
- The waiver verbs' table view is one line with the period's status after the change, such as `cloud-source/42 period "2026-09" (2026-09-01 to 2026-09-30): waived, declared 5 seconds ago, table 314`; `-o json|yaml` print the response body as it arrived, fields newer than qctl included, and move that line to stderr; `-o name` exits 2.
- Waiving and withdrawing a waiver need `deliverable:admin` and no grant on the cloud source. A `--cloud-source-id` that is deleted or is not a series cloud source of the organization returns 404 (exit 3), checked before the period key; a key that names no period of the series frequency returns 422 `SERIES_PERIOD_KEY_INVALID` (exit 2). [backend]
- `waive` returns 409 `SUBMISSION_PERIOD_NOT_EXPECTED` (exit 2) for a period the cloud source is not expected for, and 409 `SUBMISSION_ALREADY_DECLARED` (exit 2) only while a waiver is in effect: a nil return or a file that has arrived does not block a waiver, and the waiver makes the period `waived` over either. [backend]
- A locked period refuses both `waive` and `unwaive` with 409 `PERIOD_LOCKED` (exit 2); a period with no table is never locked. `unwaive` with no waiver in effect returns 404 (exit 3) before the lock is checked. [backend]
- `unwaive` withdraws the waiver in effect whoever declared it, and keeps its record with who withdrew it and when rather than deleting it; the period's status is then worked out without it, so a nil return still in effect makes the period `nil` again. When the cloud source is no longer expected for the period the server answers 204 with no body, and qctl prints `cloud-source/<id> waiver withdrawn; it is no longer expected for period "<key>"` (on stderr under `-o json|yaml`, with stdout empty) and exits 0. [backend]
- a waiver's `--reason` comes back as `declaration.reason` on every `waived` row of `get submission-statuses` and in the `waive` response, because those reads and `waive` are internal-only; the nil-return routes answer an external caller with `reason` null, and a nil return carries no reason of its own. [backend]
- qctl has no commands for nil returns (the `.../periods/{period_key}/nil` routes): the server takes one only from an account with `dataset:ingest:upload` whose own source grants include the cloud source, with no bypass for admin or ops, and an internal account also needs the table access an upload to that period would, so an internal account would first need a source grant. A nil return a broker declared still shows as `nil` on the board. [backend]

## SEE ALSO

- `qctl explain table-series`
- `qctl explain cloud-source`
