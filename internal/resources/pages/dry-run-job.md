---
summary: a preview run of a rule against a table, writing nothing to the table
aliases:
kind:
schema:
guide: dry-run-jobs
signature: a5e68e2
---

## USAGE

    qctl create dry-run-job
    qctl describe dry-run-job <id>
    qctl get dry-run-job <id>
    qctl get dry-run-jobs [table]
    qctl inspect dry-run-job <id>

## EXAMPLES

    # start a dry run from a spec file, against a table by name
    qctl create dry-run-job -f spec.yaml --table orders

    # run it on a period table of a series cloud source instead of the source's own table
    qctl create dry-run-job -f spec.yaml --cloud-source-id 456 --table sales_2026_09

    # the readable view of one run: state, rules, timestamps, and why a failed run failed
    qctl describe dry-run-job 123

    # add the metrics, sampling metadata, structured summary and rule load stack traces
    qctl describe dry-run-job 123 -v

    # raw dump of the whole API response, when the summary hides what you need
    qctl describe dry-run-job 123 -vvv

    # the same detail as a payload a script can parse
    qctl describe dry-run-job 123 -o json

    # the job record straight through the generic printer, no curated view
    qctl get dry-run-job 123

    # same record as JSON, for a script polling for completion
    qctl get dry-run-job 123 -o json

    # stream state updates over a WebSocket until the run ends; exit 0 only if it finished
    qctl get dry-run-job 123 --watch

    # the table's dry-run history -- this listing has no org-wide form, see GOTCHAS
    qctl get dry-run-jobs --table-id 123

    # same, addressing the table positionally instead of via a flag
    qctl get dry-run-jobs orders

    # same, addressing the table by name
    qctl get dry-run-jobs --table orders_2024

    # re-poll the listing every --interval seconds while runs finish (the single-job --watch is a WebSocket instead)
    qctl get dry-run-jobs --table-id 123 --watch

    # same, stopping after ten minutes with exit 0 and a resume line on stderr
    qctl get dry-run-jobs --table-id 123 --watch --timeout 10m

    # the listing as a payload for a script
    qctl get dry-run-jobs --table-id 123 -o json

    # the actual answer: which rows the rule would have changed
    qctl inspect dry-run-job 123

    # the whole preview rather than the trimmed one
    qctl inspect dry-run-job 123 --view full

    # wait for the run to end, then show its preview if it finished (exit 1 and a Next: line if not)
    qctl inspect dry-run-job 123 --follow

    # same, but give up after five minutes (exit 124; the run keeps going)
    qctl inspect dry-run-job 123 --follow --timeout 5m

## GOTCHAS

- A null `sampling` stays null in raw JSON/YAML and is omitted from the `describe -v` manifest.

- Integer fields in a YAML manifest reject fractional numbers during parsing (exit 2): `2.7` is refused instead of becoming `2`. Whole-valued YAML numbers such as `2.0` remain accepted.
- `--watch` and `--follow` announce the wait before the initial REST read, even for a job already terminal. Their timeout and SIGINT/SIGTERM cancel that read and any REST lookup after a terminal frame or a normal stream close. The list watch likewise cancels in-flight polls and retry waits; a successful response arriving after cancellation is discarded.

- `get`, `describe`, and `inspect dry-run-job <id>` reject a non-numeric ID locally as bad usage (exit 2), before any API request.
- `describe dry-run-job <id> -o table` renders the Markdown `status.summary` and `status.error_message` onto one line before embedding `status` in its JSON cell, then prints each complete plain-text value under `Summary` and `Error Message`. `status.rule_load_errors` gets no section of its own, so read it whole in the default YAML view or `-o json`, which carry both messages as plain text; `-vvv` remains the untouched wire response.
- `describe dry-run-job` shows a failed run's `status.error_message` and `status.rule_load_errors` at the default verbosity and leaves both keys out when the run has none; each rule load error's `stack_trace` appears only with `-v`. `get dry-run-job` prints both as the server sent them, `error_message`'s Markdown and every `stack_trace` included.
- the server sets `error_message` only on an `error` or `killed` run that has neither `rule_load_errors` nor `metrics`, copying the message of the ingestion job that carried the run (`status.ingestion_job_id`); a run whose rules failed to load reports them in `rule_load_errors` instead, and one that reached `metrics` lists its rule exceptions under `metrics.exceptions` (`-v`). [backend]
- a dry run writes nothing to your destination table — no clean rows, no bad rows; the backend does persist a job record, a synthetic stored item, and the row-level preview artifacts, which is what makes it a safe way to see what a rule would do before wiring it up for real. [backend]
- `spec.rule_run_specs` takes exactly one or two entries — qctl rejects zero or more than two client-side before ever sending the request; each entry's position in the array (not an explicit field) is what tells the backend the two runs apart.
- `create dry-run-job` rejects unknown manifest keys at every level with exit 2, so a misspelled field cannot be silently ignored; the retired `spec.rule_run_specs[].treat_as_alert` key gets a tailored error naming its `severity` replacement.
- `create dry-run-job` can auto-detect a table's only cloud source when no source is named; if the table has several, it exits 2 and asks for `--cloud-source` or `--cloud-source-id`.
- `create dry-run-job` runs on the table it names (`--table`/`--table-id`, else spec metadata `table`/`table_id`), and on the cloud source's own table when it names none. A period table of a series cloud source's series is sent as `dataset_id`; `--cloud-source-id` with a table that is neither the source's own table nor a period of its series exits 2 before any launch.
- a dry run on a period table is recorded on that table, so `get dry-run-jobs <period-table>` lists it and the source's own table does not. Naming the period checks only your grant on that table, so a user granted only a period table can dry-run there, while a launch naming no table checks the source's own table and is refused with 403 (exit 4). A named table deleted since answers 404 `The cloud source target table is unavailable.` (exit 3), one you hold no grant on 403 (exit 4), and one outside the source's series 400 `The target table does not belong to this cloud source.` (exit 2). [backend]
- a launch on a series cloud source kills your own queued or executing exploratory dry run on every period table of that source, not only the one it names — validation, revalidation-candidate, rule-run-replay and change-proposal runs are spared, and a refused launch rolls the kill back. [backend]
- `create dry-run-job` prints `{"dry_run_job_id": N}` as JSON when no `-o` is given, the same document as YAML under `-o yaml`, and `dry-run-job/<id>` under `-o name`; `-o table` exits 2.
- `spec.rule_run_specs[].severity` decides what the run does with the severity its findings declare: `warning` makes every finding of that rule a warning, `blocker` makes every one a blocker (the class that quarantines a row in a real run), and `as_declared` leaves each finding on the severity the rule itself gave it. Omit the key and the backend resolves it to `as_declared` when it loads the rule — the dry run inherits neither the severity of the live rule named by `dataset_rule_id` nor the rule revision's `default_severity`, so state it explicitly whenever the preview has to mirror a live rule. It replaced the boolean `treat_as_alert` in the 2026-09-10 spec, where `true` meant today's `as_declared` and `false` meant today's `warning`; qctl refuses a manifest still on the old key with exit 2 rather than dropping it silently. [backend]
- `nondeterminism_check` and `nondeterminism_sample_size` are optional in the manifest — leave them unset and the backend defaults to `sample` with a 25-row sample; only `nondeterminism_check: "off"` skips the check entirely. [backend]
- `get dry-run-jobs` **requires** a table (`--table-id` or `--table`, never both) and has no org-wide form — unlike `get ingestion-jobs` and `get profiling-jobs`, where the table filter is optional; there is no way to list every dry run in the org.
- `get dry-run-jobs` prints one page, `--limit` rows (default 100) of `--page` (default 1); `-o json|yaml` print a `List` whose `total` and `next_page` say whether another page exists, and table and name output say so on stderr.
- `inspect dry-run-job` renders the row-level preview/diff via a separate preview endpoint — that response also carries the job's `state`, so it isn't blind to whether the run finished; `describe dry-run-job` reads a different endpoint and additionally surfaces config and sampling metadata the preview response omits.
- `structured_summary` (in `describe dry-run-job -v`, in `get dry-run-job -o json|yaml`, and in `inspect dry-run-job`'s compact view) carries only the keys the server set, so a stat whose rate is valid has NO `rate_valid` and NO `not_estimable_reason` key — read an absent `rate_valid` as `true`. A server newer than 2.118.0 sets `mode` (`single` or `pair`) on both shapes, but a 2.118.0 server sends no `mode`, so where it is missing tell them apart by their keys: a single-run summary has `clean`/`warning`/`blocker`, a two-run one `blockers`/`warnings`. qctl prints the object as received and fills in none of these keys. [backend]
- `rate_valid: false` is decided per job, so every stat in the summary is invalid together, and its `rate` is not a measurement: 0.0 when a stratum with positive matching population share has no eligible results (which makes `clean` read 1.0), an estimate with no confidence interval otherwise. In a single-run summary the stat's `count` is then the OBSERVED row count rather than an estimate from the rate (except on a job finalized before the backend recorded observed counts); in a two-run summary it stays the estimate. [backend]
- in a two-run summary `delta_valid: false` means `delta_rate` is not a measured difference and `is_significant` is forced to `false`, so that `false` is not evidence the runs agree. `observed_delta` (rows flagged by the candidate only minus rows flagged by the baseline only, counted, not estimated) holds up either way, but it is `null` for an `independent` comparison, where `delta_rate` is always 0.0 and `is_significant` always `false`. [backend]
- `not_estimable_reason` is free text, not an enum, and it describes the job's sampling rather than the stat it sits on, so every invalid stat in a summary carries the same string: `<stratum> stratum not sampled`, `<a>, <b> strata not sampled`, or `insufficient observations for a confidence interval`. Branch on `rate_valid`, never on the reason. [backend]
- the backend caps you at 3 dry runs *executing* per user per organization and answers the next launch with 429 (`DRY_RUN_RATE_LIMIT`) — only `pending` and `running` count, so queued runs are free, and rule validations, revalidation-candidate runs and rule-run replays are excluded from this budget entirely, while a change proposal's dry runs and composite dry runs count; `create dry-run-job` absorbs the 429: it makes up to 5 attempts (the first, plus 4 retries), honouring the server's `Retry-After` verbatim while that is 30s or under — the backend sends `Retry-After: 15`, so about 60s of waiting in practice — and falling back to 2s doubling to a 16s max wait (30s total) only when the header is missing or out of range, printing each wait to stderr so stdout stays a parseable JSON document; if the cap is still full when the budget runs out the command fails with the backend's own message and exit code 1. [backend]
- a launch first kills *your own* active dry runs on the same cloud source before the cap is counted, so what actually blocks you is dry runs you started on other sources, plus a change proposal's dry run run as you on this one, which the launch spares but the cap counts — and a refused launch is rolled back whole, which is why retrying it is safe. [backend]
- `get dry-run-jobs --watch` writes the page again only when it changes, as one stream: `-o json` writes one compact List per line, `-o yaml` one document after each `---`, `-o name` the `dry-run-job/<id>` of each run added, changed or gone from the page, and the table is redrawn in place on a terminal and appended elsewhere; after a poll retry notice, an HTTP retry notice under `--retries`, or verbose HTTP diagnostics under `-vvvvvvv` and above, the next changed table starts below it and later updates redraw there; a json line holds the whole page, so a large `--limit` can outgrow a line reader's buffer, such as the 64 KiB `bufio.Scanner` starts with.
- `get dry-run-jobs --watch` announces itself on stderr — `watching dry-run jobs every <interval>, for up to <d>; resume with: <command>`, with no `for up to` clause when there is no limit — and polls until `--timeout`, where 0 means no limit; unset, it is no limit unless `QCTL_WATCH_TIMEOUT` (a Go duration such as `90s`) sets one. Running out is a clean end: exit 0, after `timed out after <d>; resume with: <command>` on stderr. SIGINT exits 130 and SIGTERM 143, and `--timeout` without `--watch`, or a negative one, exits 2. A failed first poll ends the watch with that failure's exit code, as does a later one that would exit 2, 3, 4 or 5; any other failed poll prints `poll failed (<error>); retrying in <wait>` on stderr and is retried after a wait that starts at twice `--interval` and doubles up to 30s (or `--interval`, if longer).
- `get dry-run-job <id> --watch` writes one record per state it learns: the job's state when a REST read finds it already terminal, each state the WebSocket reports, and, when the stream closes before a terminal state, the state a re-read finds if it moved. `-o json` writes the frame as one compact line (`{"state":"running"}`), `-o yaml` one document after each `---`, `-o name` `dry-run-job/<id>`, and the default view `dry-run-job <id>: <state>`; `-o json` and `-o yaml` skip a frame equal to the last one written, the other two do not. `inspect dry-run-job <id> --follow` writes the same `dry-run-job <id>: <state>` lines to stderr, so its stdout carries only the preview.
- `get dry-run-job <id> --watch` and `inspect dry-run-job <id> --follow` read the job over REST before they open the WebSocket, so a missing job exits 3 without dialing, and a job already in a terminal state is judged at once without dialing. They end at the first of `finished`, `error`, `killed`, `stopped`, `version_id_mismatch`, `file_missing`, `blocked` or `partitioned`, and exit 0 only for `finished`, the one state after which `--follow` prints the preview; any other terminal state exits 1 with an error that opens `dry-run-job <id> ended in state <state>`, adds the run's rule load errors and error message when it has them, and ends in a `Next:` line, `qctl describe dry-run-job <id>` for every state but `blocked`.
- A terminal state here ends the current progress watch; it does not guarantee the job can never run again. `describe rule-run-replay` uses the same stop states for its carrier's current attempt, reporting `blocked` and `partitioned` as `failed` while preserving the raw state.
- a `blocked` run's error names the alert holding it — `dry-run-job <id> ended in state blocked: alert <n> blocks it`, then `alert message: …` when the alert has one — and ends `Next: qctl describe alert <n>`. qctl reads that alert from the run's ingestion job, because the dry-run record carries `alert_item_id` only for an `error` or `killed` run with neither `rule_load_errors` nor `metrics`; when the ingestion job names no alert or cannot be read, the Next line is `qctl describe ingestion-job <ingestion-job-id>`. An `error` or `killed` run that does carry `alert_item_id` lists `alert: <n> (qctl describe alert <n>)` above its Next line. [backend]
- `get dry-run-job <id> --watch` and `inspect dry-run-job <id> --follow` announce the wait on stderr — `waiting for dry-run-job <id> to finish, for up to <d>; resume with: <command>`, with no `for up to` clause when there is no limit — and wait at most `--timeout`, where 0 means no limit. Unset, it is no limit for `--watch` and 10m for `--follow`, from a script or an agent as from a terminal, unless `QCTL_WATCH_TIMEOUT` (a Go duration such as `30m`) replaces that default. Running out exits 124 with `timed out after <d>: dry-run-job <id> is still <state>; the job was not stopped` and `Resume with: <command>`, the same command line, `--timeout` included; SIGINT exits 130 and SIGTERM 143 after `interrupted; resume with: <command>`. `--timeout` without `--watch` or `--follow`, or a negative one, exits 2 before any request.
- the first WebSocket dial of `--watch` and `--follow` is not retried: a dial the server answers with 401 or 403 exits 4, with 404 exits 3, with any other 4xx but 429 exits 2, and any other failure exits 1. Once the stream is open, a dropped connection is redialed after a wait from 1s doubling to 30s, each attempt announced on stderr as `connection lost (<cause>); reconnecting in <wait> (attempt <n>)`, until `--timeout` runs out. A close with code 1008 ends the wait with exit 4, quoting the server's reason with the `qctl auth login` hint; a close with 1002, 1003, 1007, 1009, 1010 or 3000–4999 exits 1; a redial refused with a 4xx other than 429 exits as a first dial would. A close with 1000 before a terminal state makes qctl re-read the job and judge what it finds: a state that is not terminal exits 1 with `the server closed the stream while dry-run-job <id> is still <state>` and `Resume with: <command>`.
- the server answers every progress-stream upgrade it refuses with a bare HTTP 403 — an expired or revoked token, a role without the `rule:list` permission, a job that does not exist or is outside your org, a table you have no grant on — so a refused stream exits 4 with `websocket handshake failed: HTTP 403 Forbidden` and none of the server's reason; it is the REST read before the dial that answers a missing job with exit 3 and a forbidden one with the server's message. [backend]
- the dry-run progress stream sends the job's current state as soon as it accepts the socket, then one frame per state change, and closes itself after the first of the eight terminal states above, the set qctl ends on — so the first record of a watch is the state at connect time, and every reconnect sends the current state again. It never re-checks the token once open, so unlike the socket behind `get table <t> --job-activity --watch` it is not closed with 1008 when the token expires mid-watch. [backend]

- `spec.rule_run_specs[].row_filter` accepts the same conditions and Starlark predicate as `TableRule.spec.row_filter`, with an optional description. Omitted or null means no filter; to preview a live rule's filter, write it explicitly in the run spec. Custom rules accept conditions but refuse a predicate. Saved filters need a description when later applied to a table rule. [backend]
- for a caller who sees the table's PII masked, `describe dry-run-job` shows each `spec.rule_run_specs[].row_filter` with its literals masked, and the `-vvv` raw dump each run spec's params too, by the rule `qctl explain table-rule` gives for a table rule; a run spec copied from that output into a `create dry-run-job` manifest runs the rule against the mask text. [backend]
- `describe dry-run-job` includes `status.row_coverage` at default verbosity when the server supplies sample coverage: each entry names its position, `checked_rows`, and `total_rows`. The full structured summary remains under `-v`; old responses without coverage leave `row_coverage` absent.
- A single-rule summary's `total_eligible` is the number of checked rows processed, while `checked_rows` counts sampled rows the filter matched and `total_rows` counts all sampled rows. Pair summaries carry `baseline_checked_rows`, `candidate_checked_rows`, and `both_checked_rows`; `total_eligible` is the processed rows either rule checked, and a rule is treated as clean on a row its filter skipped. `transitions.unblocked_by_filter` and `newly_blocked_by_filter` identify changes due to a filter. Counts and rates use `total_eligible`, so read the coverage before comparing rates. [backend]
- for a caller who sees the table's PII masked — no `pii:view` and no active PII access grant on the table — `inspect dry-run-job` prints a PII column's values masked, as `•••••` or `•••••` and the last 4 characters: in the `--view full` rows' `left` and `right` `values`, and in the compact exemplars' `changed_fields` `before`/`after` and issue `field_values`; `masked_fields` names the columns it masked. The compact summary's reason patterns and `describe dry-run-job`'s error message quote PII values masked the same way. [backend]

## SEE ALSO

- `qctl explain ingestion-job`
- `qctl explain profiling-job`
- `qctl explain table-rule`
- `qctl guide dry-run-jobs`
