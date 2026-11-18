# Resolve alerts and warnings

An **alert** needs a decision and holds the rows it names in quarantine. A
**warning** is advisory. Most alerts come from ingestion (an unreadable header,
a datetime format, a schema migration to review); the rest come from table
rules, whose `spec.severity` decides which kind each finding raises
(`qctl explain table-rule.spec.severity`).

## Find the table or series

For organization-wide triage, start with standalone tables and series totals:

    qctl get tables-overview --view needs-attention

A series's top alert names its affected table and period. The `PERIOD` column
shows the latest selected period, which can be different. To inspect the
series's individual periods and then one table's blockers:

    qctl get tables --series 11111111-2222-3333-4444-555555555555 --needs-attention
    qctl describe table 42

## Do this

    qctl get alerts --table orders        # unresolved alerts (the default)
    qctl get warnings --table orders      # unresolved warnings
    qctl describe alert 9                 # its actions, and the exact commands to run
    qctl alert resolve 9 --action apply_migration --yes

1. List what fired. `--resolved` shows the resolved ones instead; row-level
   alerts need `--is-row-level`.
2. Describe one. Its `spec.actions` (`warning_actions_list` on a warning) are
   the only values `--action` takes, filtered by your role, so two alerts of
   one issue type can differ.
3. Run the action the user asked for, with `--yes`: without a terminal a
   prompt exits 2. Act only on the alerts in the request.
4. qctl re-reads the alert afterwards and says `resolved`, or
   `accepted, but the alert is still unresolved` — a 200 is not a resolution.
5. `validation_add_a_new_keyword` can instead report `keyword change staged as
   rule suggestion <id>; the alert stays open`: the keyword waits on the
   table's draft migration or on a validation run. Follow the step it prints —
   `qctl suggestion accept`, or wait for a revalidation candidate's run — then
   re-read the alert once the keyword is live. Applying the suggestion
   resolves the alert when the keyword is the alert's own value; if it is
   still open, re-run the same resolve. Stage one keyword at a time: two
   keywords staged on one rule overwrite each other. The same holds for a warning, and a
   compound `validation_add_a_new_keyword__*` action is refused with a 409
   instead; its error names the plain keyword action to send first.

## Read the NEXT ACTIONS footer

`describe alert` ends with runnable commands. An action that needs request
fields is listed as the command that writes its manifest, never as a bare
`--action` line that would fail. Under each command is the backend's catalog
entry for the action on this issue type: what it does, its scope (`file`,
`source`, `table`, `rows` or `none`), whether it modifies data, whether it can
be undone, and the follow-on work it triggers. Choose by those lines, not by
the action's name:

    qctl describe alert 9

    # NEXT ACTIONS
    #   qctl alert resolve 9 --action resolve_sniff_header --dry-run
    #     needs extra input (cant_sniff_header_params) that --action alone cannot supply, so the
    #     runnable form is the generator above — edit what it writes, then:
    #       qctl alert resolve -f <file>
    #     Sets where the header and data start, and optionally the column names and
    #     CSV dialect, for this alert's files or for the whole data source, then
    #     resolves the alert and requeues the files.
    #     scope: file, source; modifies data: yes; reversible: no
    #     follow-on: requeues_file, starts_learning
    #   qctl alert resolve 9 --action increase_chunk_size_to_read_for_header
    #     Increases how much of each file the data source reads when looking for the
    #     header row by 8096 bytes, resolves the alert and requeues the file.
    #     scope: source; modifies data: yes; reversible: yes
    #     follow-on: requeues_file, starts_learning
    #   qctl alert resolve 9 --action identify_header_by_column_names --dry-run
    #     needs extra input (cant_sniff_header_params) that --action alone cannot supply, so the
    #     runnable form is the generator above — edit what it writes, then:
    #       qctl alert resolve -f <file>
    #     Tells the data source to find the header row by the given column names,
    #     resolves the alert and requeues the file.
    #     scope: source; modifies data: yes; reversible: yes
    #     follow-on: requeues_file, starts_learning
    #   qctl alert resolve 9 --action trash_file --comment <text>
    #     Ignores every file linked to the alert, removing rows they already loaded,
    #     and resolves the alert; with stored_item_id, discards only that file and
    #     leaves the alert open while it links another kept file.
    #     scope: file; modifies data: yes; reversible: yes

`-o json` has no footer; read `spec.actions` there. The catalog is offline:
`qctl explain alert.alert_actions_list` lists every action's general entry,
and `qctl explain alert.issue_type` gives each issue type's label and who
usually fixes it.

## Actions that need input

Generate the manifest, fill it in, send it. The warning side is the same loop
with `qctl warning resolve`, and there `--action` refuses such an action
outright (exit 2), so the manifest is the only path.

    qctl alert resolve 9 --action map_value --dry-run > fix.yaml
    qctl alert resolve -f fix.yaml --yes

The skeleton carries the alert's values as comments, every required key bare,
the optional ones commented out, and the backend's note for the action:

    # alert 9 on table orders (id 42)
    # field_name: amount
    # field_value: 1,420.00
    #
    # Fill in every key below, then:  qctl alert resolve -f <file>
    apiVersion: qluster.ai/v1
    kind: AlertResolution
    metadata:
      id: "9"
    spec:
      action: map_value
      new_value:
      # another_field_new_value:
      # map_value_once:
    #
    # note from the backend: ... map_value_once records a one-off mapping instead of a learned one, and is REQUIRED to map a blank value ...

- An unedited skeleton is refused with exit 2: a bare key is YAML null, and
  `new_value` is never prefilled.
- A one-of group (`# set exactly one of: ids, initial_signatures`) needs exactly
  one member uncommented.
- An unknown key, the other verb's `kind`, or a missing `metadata.id` is exit 2.
- One file may hold several `---` documents, applied in order; the run goes on
  past a failure unless `--fail-fast`.
- `map_value_once` is alert-only; `qctl warning resolve -f` refuses it.

### Column mappings during onboarding

An `initial_fields` or `unexpected_field_name` alert accepts column mappings
without an action. Generate its template, decide what to do with each incoming
column, then submit it:

    qctl alert resolve 9 --dry-run > columns.yaml
    qctl alert resolve -f columns.yaml --yes

The template includes incoming names from the alert's suggestions. Fill in
`field_name`, `operation`, and `scope` for each entry; keep `spec.action` absent.
For example:

    apiVersion: qluster.ai/v1
    kind: AlertResolution
    metadata:
      id: "9"
    spec:
      field_names_resolution:
        - incoming_field_name: Customer Name
          field_name: customer_name
          operation: new
          scope: dataset
        - incoming_field_name: Unused
          field_name: ""
          operation: delete
          scope: stored_item

`new` introduces a column, `map` names an existing column, and `delete` ignores
the incoming column. `field_name: ""` keeps the normalized incoming name with
`new`; it is also valid for `delete`. `scope` is `dataset`, `data_source_model`,
or `stored_item`; `expand` optionally requests flattening of nested data.
The template comments each entry with the server's recommendation
(`# recommended: map customer_name`) and with `# blank in this file` when every
row leaves the column empty, and ends with the table's columns, marking the
required ones.

Not every scope is open to every role. The template lists the scopes in the
alert's `field_mapping_scopes` and prefills `scope` with the widest of them.
Editors and other roles without `dataset` scope cannot `delete`, `map` to a
column or `expand` at `dataset`, and `qctl alert resolve -f` refuses that before
sending (exit 2); `new` needs ops or admin whatever the scope. To ask an
approver for a `dataset`-scope change, use `qctl alert propose -f` instead.

On `unexpected_field_name`, a required column that ends up with no data from
the file is a 422 naming the column. That happens when no file column maps to
it, or when the only one that does is blank. The template flags a required
column the file lacks when no column with data is recommended for it.

The backend checks mapping permissions and target names. Submission may park
the alert until its schema migration completes; re-read the alert and follow
its NEXT ACTIONS footer before applying the migration.

### Where `ids` comes from

`ids` names a different set of rows per action, and a wrong id is often silent:

| action | rows | a stranger id |
|---|---|---|
| `overwrite_value`, `validation_add_a_new_keyword__overwrite_value` | quarantined bad rows | matches nothing, still succeeds |
| `ignore_rule_for_row` | bad rows of the alert's data source | 404, the whole request fails |
| `ignore_rule_for_row` on a warning | clean or quarantined rows of the warning's data source | 404, the whole request fails |
| `reapply_cell_override` | composite table: rows with a live cell override; any other table: quarantined rows carrying the conflict | composite: 404, the whole request fails; otherwise skipped, and 400 when no id qualifies |
| `discard_cell_override` | the same | composite: skipped silently, the alert still resolves; otherwise as `reapply_cell_override` |

Read them from the rows the alert holds; each row's `id` is what `ids` takes,
and `initial_signature_` is what `initial_signatures` takes:

    qctl get data orders --is-bad-data --alert-item-id 9 -o json

For a warning, `--warning-item-id 55` lists its clean rows; add
`--is-bad-data` for its quarantined ones.

### Actions that reach further

Value mappings, datetime formats, `accept_gtin_prefix` and some parameterless
actions also resolve matching issues on the table's other data sources and
store settings for future data. External users resolve matching issues only
within the named issue's cloud source. These actions do not prompt; the footer
marks their reach. `apply_migration` prompts (pass `--yes`) and applies the
table's whole draft migration; on a mirror or append table's first migration it
also needs `--key-columns <a,b>` or `--no-key-columns`, and `qctl describe
table-schema <table>` shows the key step and key candidates. `ignore_column_for_future_anomalies` also prompts: it
permanently ignores the column and resolves unresolved anomaly alerts on the
same column and settings model. Pass `--yes` to confirm it without a terminal.

## Act on a warning

    qctl describe warning 55

    warning_actions_list:
      - not_anomaly
      - ignore_column_for_future_anomalies

    # NEXT ACTIONS
    #   qctl warning resolve 55 --action not_anomaly
    #     Records the warning's value as not an anomaly for this column in the
    #     table, resolves the warning, and may start anomaly-model retraining.
    #     scope: table; modifies data: yes; reversible: yes
    #     follow-on: reprocesses_quarantined_rows, retrains_anomaly_model
    #   qctl warning resolve 55 --action ignore_column_for_future_anomalies
    #     Stops anomaly detection on this column for the table and resolves every
    #     open anomaly warning on the column across the table's data sources,
    #     reprocessing their quarantined rows.
    #     scope: table; modifies data: yes; reversible: yes
    #     follow-on: reprocesses_quarantined_rows, resolves_matching_issues
    #     BULK — permanently ignores the column and resolves unresolved anomaly
    #     warnings on the same column and settings model; prompts unless --yes
    # resolving republishes this warning's quarantined rows: qctl get data 42 --is-bad-data

    qctl warning resolve 55 --action not_anomaly --yes

Resolving a warning republishes its quarantined rows, so some may leave
quarantine; nothing in the response says which.

A `rule_validation` warning also offers `ignore_rule_for_row`, sent through a
manifest with `ids` (see "Where `ids` comes from"). It removes the warning
only from those rows: the warning stays open while another row still carries
it.

## When the policy says propose

A table's change review policy can let your role resolve an action, only
propose it, or neither, and the server reports which for each offered action.
The footer follows it: a `propose` command where you may only propose, and the
action's name with `not open to you` where you may do neither.

    # NEXT ACTIONS
    #   qctl warning propose 55 --action not_anomaly
    #     the table's change review policy lets you propose this, not resolve it;
    #     an approver applies it
    #     Records the warning's value as not an anomaly for this column in the
    #     table, resolves the warning, and may start anomaly-model retraining.
    #     scope: table; modifies data: yes; reversible: yes
    #     follow-on: reprocesses_quarantined_rows, retrains_anomaly_model
    #   ignore_column_for_future_anomalies: not open to you
    #     the table's change review policy lets you neither resolve nor propose it
    #     Stops anomaly detection on this column for the table and resolves every
    #     open anomaly warning on the column across the table's data sources,
    #     reprocessing their quarantined rows.
    #     scope: table; modifies data: yes; reversible: yes
    #     follow-on: reprocesses_quarantined_rows, resolves_matching_issues
    # resolving republishes this warning's quarantined rows: qctl get data 42 --is-bad-data

- Propose what the footer says to propose, then stop: a human approves it
  (`qctl describe change-proposal <id>` shows where it stands). Do not retry
  it as a resolve. `resolve` there is refused with 403
  `CHANGE_PROPOSAL_REQUIRED`, and an action not open to you with 403
  `CHANGE_REVIEW_POLICY_DENIED` (both exit 4).
- `propose` takes the same `--action`, `-f` and `--dry-run` inputs as
  `resolve`, never prompts and has no `--yes`.
- While a live change proposal holds the issue, `describe` names it under
  HELD BY CHANGE PROPOSAL and submitting another proposal for it is refused
  with 409 `CHANGE_PROPOSAL_ACTIVE`; follow that proposal instead.
- An action that is not open to you is a decision for someone else: say so
  and name the action rather than looking for another way to run it.

## Flag a cell yourself

`qctl alert submit` flags a cell of a QUARANTINED row and holds the row there.
`qctl warning submit` adds an advisory note that holds nothing. To flag a
clean row, park it in quarantine first, wait for it to settle, then flag it:

    qctl rows send-to-quarantine orders --row-id 1042 --yes
    qctl get data orders --is-bad-data --awaiting-processing=false --filter-by-field-name id --filter-by-field-value 1042
    qctl alert submit --table orders --row 1042 --field amount --comment "wrong FX rate" --yes

The row keeps its id across the move. Until the second command returns the
row, a submit answers `row_not_found`; retry once it settles.

To put the row back, clear the flag, then release the row:

    qctl alert retract 8817 --row 1042 --yes      # the flag was wrong
    qctl rows set-cell orders --row-id 1042 --field amount --value 1420.00 --yes   # or: the value was wrong
    qctl rows release orders --row-id 1042 --yes

`send-to-quarantine` puts a manual hold on the row, and neither retracting the
flag nor fixing the value lifts it: `release` does. Release last, because an
edit after a release revokes it. When the table's data review policy requires
review or its `quarantine_rows_until_approved` is on, `release` records your
approval instead and skips a row you edited, so after a `set-cell` someone else releases it. `reset-hold` is not the same:
on a table that holds rows for approval it leaves the row in quarantine.

The `--comment` also opens a comment thread on the flagged cells, and submit
prints its id with a `qctl describe comment-thread` line to read it. A
`[@label](mention:<uuid>)` link in the comment notifies that person; a bare
`@name` does not. `qctl describe alert <id>` names who flagged its rows, with each
flag's thread.

Retract means "I flagged the wrong cell"; resolve means "the value is fixed".
Editing the thread's first comment changes the discussion, not the flag: to
reword the flag, retract and submit again. Per-row outcomes, `row_not_found`
and the warning form: `qctl explain alert --grep row_not_found`.

## Wait for the next one

    qctl get latest-alert --table orders --watch --exit-on-change --timeout 10m

Exits 0 with the change on stdout, or 124 with stdout empty when nothing
changed in time. Never run `--watch` without `--timeout`: it does not end on
its own. Full walkthrough: `qctl guide unblock-table`.

## The rows behind it, and the rule

    qctl get data orders --is-bad-data --include-dataset-rule-ids   # quarantined rows, with the rule that tripped
    qctl describe table-rule r-1                                    # an r-* alias from that column
    qctl get table-rules orders                                     # every rule, with its Severity policy
    qctl download alert-report orders                               # a workbook of the backlog, for a person

Fix the values themselves with `qctl guide edit-data`.

## Turn a noisy rule down

Patch the table rule's `spec.severity` (`warning`, `blocker` or
`as_declared`). Both ids come from `qctl describe table-rule <id> --table orders`:

    # table-rule.yaml
    apiVersion: qluster.ai/v1
    kind: TableRule
    metadata:
      id: <table-rule-id>
    spec:
      dataset_id: <table-id>
      severity: warning

    qctl diff -f table-rule.yaml
    qctl apply -f table-rule.yaml --yes

Change a rule's severity only when the user asked for it.

## See also

- `qctl explain alert --gotchas`, `qctl explain warning --gotchas` — every field and gotcha
- `qctl guide edit-data` — fix the values in quarantined rows
- `qctl guide inspect-data` — read the rows
- `qctl guide unblock-table` — a table that stopped ingesting
