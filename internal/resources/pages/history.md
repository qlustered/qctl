---
summary: the org's ledger of who changed what, and whether it was undone
aliases:
kind:
schema: openapi:HistoryTinySchema
guide:
signature: 7eb992c
---

## USAGE

    qctl get history
    qctl redo cloud-source <id>
    qctl redo destination <id>
    qctl redo table [table-id-or-name]
    qctl undo cloud-source <id>
    qctl undo destination <id>
    qctl undo table [table-id-or-name]

## EXAMPLES

    # every change in the organization, newest first
    qctl get history

    # what changed on one table in the last day
    qctl get history --table orders --since 24h

    # your own changes
    qctl get history --mine

    # who changed a rule's thresholds, and to what
    qctl get history --model DatasetRule --field max

    # changes somebody has since undone
    qctl get history --undo-status undone

    # one person's configuration changes over a date range
    qctl get history --author dana@example.com --since 2026-08-01 --until 2026-08-31

    # reverse your most recent eligible change to a table, after previewing it
    qctl undo table orders

    # put it back
    qctl redo table orders

    # the raw records, with the untruncated values and the full path
    qctl get history --limit 5 -o json

    # each recorded value beside its unit tag: `decimal` marks a number
    qctl get history --model TableValueOverwrite --columns id,field,old_value,old_value_type,value,value_type

    # which of your CLI sessions made each change; compare with `qctl auth sessions`
    qctl get history --mine --columns id,created_at,operation,field,delegation_id

## GOTCHAS

- the undo/redo preview reports how many of your changes the step skipped because someone else changed their fields afterwards or the fields no longer hold the expected values, and names later authors when available. Undo counts newer changes passed over; redo counts undone changes passed over. When no eligible change remains, the same notice accompanies the 404 (exit 3). [backend]
- a successful cell undo or redo can leave cells untouched because they changed since; the result message counts those cells. qctl prints that message after the `applied` line in table output; `-o json` and `-o yaml` carry it in `result.msg`. [backend]
- on a money or percent column a cell edit's history item records the number you typed in the column's stored units — the `$`, grouping commas and `%` are dropped and a percent is divided by 100 — and `old_value` is the text the cell held, so setting a converted percent cell shown as `12.50%` to `13` records `0.125 → 0.13`. Neither side carries a display format, and `--search` matches both texts exactly as recorded: `--search 0.13` finds that item and `--search 13` does not. `-o json` and `-o yaml` carry a unit tag for each side, `old_value_type` and `value_type`: `decimal` marks a stored number, so a percent cell whose source text never converted reads `150` tagged `str` beside `0.13` tagged `decimal` — but `str` does not prove source text, because an item recorded before the server began tagging stored numbers — a cell edit, or a composite table's `CellOverride` — can be `str` too. [backend]
- the listing is narrowed to the tables you can see even when `--model-id` is supplied. An ops user's listing also includes global history; an external user can read only `DataSourceModel` history for their associated sources. [backend]
- `--model Destination` and `--model BackupSettings` are a 403 for every role but ops — not an empty list. [backend]
- external users can read only `--model DataSourceModel` history; anything else, including no `--model` at all, is a 403. [backend]
- a `read_only` role cannot read history at all — it is the one role without `HISTORY_READ`, so every `qctl get history` is a 403 for it. [backend]
- `--model DataSet` and `--model SettingsModel` are the same filter: either one matches BOTH models, so they cannot be told apart from the CLI. With either, and no `--table`/`--table-id`, `--model-id` is read as a table id (`DataSet`) or resolved to one (`SettingsModel`), and the table's own access checks then apply. [backend]
- `--field` matches ONE segment of the path, not the dotted path the FIELD column prints: a change to `params.max` is found with `--field max`, and `--field params.max` matches nothing. [backend]
- `--author` with an email address needs the `org:users:list` permission, which the `editor` and `external` roles lack even though both can read history; `--author <uuid>` and `--mine` work for them. [backend]
- the listing is newest-first already; `--reverse` asks for oldest-first, because the endpoint's own `reverse` parameter defaults to true. [backend]
- `--undo-status partial` means an undo or redo half-applied and needs retrying — it is not a third steady state. [backend]
- `--search` matches the full value or old value; for model and operation it accepts an exact enum member or a shorter substring of one, and table names are looked up loosely. [backend]
- a `--search` the server cannot use is DISCARDED and the listing comes back UNFILTERED, not empty — qctl prints the server's "was not used" note on stderr, and `-o json`/`-o yaml` also carry it in `msg`. [backend]
- `--since 2026-08-30` means midnight UTC, not midnight local; the wire parameters are UTC and qctl does not shift them. `--until` takes the same two forms but not a duration.
- `-o json` and `-o yaml` re-encode each item from qctl's generated API types, under the wire names, rather than pass the server's bytes through — so a field newer than qctl's client is dropped, and a null field the types mark `omitempty` is left out. They are the only place the raw `path`/`new_path` arrays survive, and the only place a tag the table folds is still readable; the table's `field`, `change` and `status` columns are qctl's own projection, and on a terminal the table cuts any cell wider than 80 columns unless `--max-column-width` is passed.
- the table's CHANGE column shows list additions as `→ value` and list removals as `value →`; it hides the `unknown___` old-value placeholder, in the optional OLD-VALUE and OLD-VALUE-TYPE columns too, so the two never disagree about whether an old value was recorded. `-o json` and `-o yaml` keep the original values and tags.
- the unit tags are columns of their own, off by default: `--columns value,value_type,old_value,old_value_type` puts each recorded value beside its tag. The table prints the values exactly as recorded either way — `--search` matches that text, so reformatting one into the column's display units would stop the printed value and the argument that finds it lining up. `qctl undo table` is where a change is described in display units.
- `delegation_id` and `change_proposal_id` are columns of their own, off by default: `--columns` prints each as a full UUID or an empty cell, and `-o json`/`-o yaml` carry it as a UUID or `null`. `delegation_id` is the delegation a bearer-token request acted through — every qctl login mints a new one, so on your own changes it is the session ID `qctl auth sessions` shows until that session is revoked — and it is empty for a change made through a browser's cookie session or outside a request. [backend]
- `change_proposal_id` names the change proposal whose execution made the change: it is set on the changes written while an approved proposal runs its operation — an alert or warning resolve, or a table-rule patch — as the approver, and empty on every other item. [backend]
- `version_id` is empty — a blank cell under `--columns version_id`, `null` in `-o json` — on a composite table's `CellOverride` items, on `DatasetRuleRowIgnore`, `ConditionalActionItem` and `RowDelete` items, and on the item recording that a `ReviewPolicy`, `DataReviewPolicy` or `ChangeReviewPolicy` was removed: those writes record no model version. Nothing else about the item is missing and `--model`/`--field` find it as usual, so a script reading `version_id` has to accept `null`. [backend]
- `qctl undo` and `qctl redo` act only on your own changes, whatever your role: another user's edit is never next, a change recorded without a user is on nobody's stack, and an external user's `undo table` and `redo table` see only their own `TableValueOverwrite` changes. Any change but a cell edit or a `ConditionalActionItem` also leaves your stack while another user's newer change to one of the fields it changed stands — two `ExtendedContextAwareModel` changes that each add a different value do not count — and comes back once that change is undone. For `undo table` and `redo table`, a DatasetRule change that a change recorded without a user followed on the same rule is excluded. [backend]
- `qctl undo table` walks the table's eligible change chain, so a settings change cannot be reversed without first reversing eligible data edits made after it. Background DatasetRule changes and earlier user changes to the same rule that they followed are excluded. [backend]
- an undo or redo is refused with a 409 (exit 2), and nothing in the step is applied, when the step would change the table's `data_loading_process` — change that with `qctl apply` instead; the refused step stays next, so `qctl undo table` can never reach anything recorded before it — when it would bring a deleted table-series cloud source back, or when it would leave a cloud source `active` whose type disagrees with its table's process: a `composite` source on a table that is not composite, or any other source on a composite one. [backend]
- `qctl undo table` and `qctl redo table` describe the change in the column's display format where `qctl get history` shows stored text: a cell edit, or a `map_value_once` mapping limited to one field, that `get history` lists as `0.125 → 0.13` is previewed as `“12.50%” changed to “13.00%”` — the same values in different units. Only a stored number is reformatted: a cell whose source text never converted is shown as recorded, so `12.5%` or `150` can precede a formatted `13.00%`. [backend]
- if the selected history ids change between the preview and your confirmation, undo or redo is refused with a 400 (exit 2) — qctl always sends the previewed ids. An undo refusal describes the newly selected change; a redo refusal says only that the history changed. An intervening edit that leaves the selected ids unchanged does not trigger this check. [backend]
- undoing needs authorization for the kind of change involved, not just history access. For internal callers, settings need `dataset:config:edit`, rules `rule:edit`, cell changes `dataset:data:write`, cloud sources `source:edit`, and destinations `destination:create`. External callers instead have a separate ownership check: only their own source changes and cell fixes on assigned sources may be undone. [backend]
- an undo can half-apply: rows locked by a running ingestion job, rows you are not authorized to change, rows in a cloud source you may not modify, and more matching rows than the sync row limit moves all stop it short; the error names each cause except the per-row authorization one, which arrives as a bare row count naming nothing, and it never says which rows; it leaves the item in the `partial` state that `qctl get history --undo-status partial` lists only when some rows did move — when none did, the item keeps the state it had. [backend]
- an undo or redo step that turns a cloud source back to `active` — from `undo cloud-source` or `redo cloud-source`, or from `undo table` or `redo table`, whose steps include the table's source changes — runs the connection check and files guard `enable cloud-source` runs (a `qluster_ui` source has nothing to check), and a failure is a 400 that rolls the whole step back. A `composite` source gets neither, so undoing a disable of one turns it back on. The step writes the state directly and queues no sensor deploy, so a scheduled source it turns back on does not poll until something next writes the source through the ORM, such as an `apply` that changes a field, or a disable and enable; that part is reasoned from the handlers, not observed. [backend]
- there is no `qctl undo` for backup settings, and none for a file — the API accepts `stored_item_id` but no filter ever reads it, so a file undo would be indistinguishable from `qctl undo table`.
- `-o json` and `-o yaml` do not switch confirmation off: the preview and question go to stderr while stdout remains one machine-readable document. Pass `--yes` to skip the question in a script or whenever stdin is not a terminal.

- For internal callers other than ops/admin, undo/redo checks the table grant when the change requires a table write permission. A `read_only` grant is then refused with 403 (exit 4), even when the organization role permits that write. [backend]
- a cell edit on a PII column reaches a caller who sees the table's PII masked — no `pii:view` and no active PII access grant on the table, which among the roles that can read history is the `agent` role — with its value and old value masked: the VALUE, OLD-VALUE and CHANGE columns print `•••••`, or `•••••` and the value's last 4 characters when it has 9 or more, as sent and as text whatever `value_type` says, and `-o json|yaml` name the column in the item's `masked_fields` (every masked column of the table when the item names none). `qctl undo table` and `qctl redo table` print those values masked the same way in their preview. [backend]
- `undo` and `redo` are subject to the change review policy of each table whose history items they revert or reapply (operations `history_undo` / `history_redo`), under one key per model of those items, such as `DataSet`, `SettingsModel`, `DatasetRule` or `StoredItem`; items of no table are not checked. While a policy is in force, a batch holding an item of a model outside that key set (it leaves out `CellOverride` and `DatasetRuleRowIgnore`, among others) is refused for every role, and its `Hint:` says the change maps to no key the policy can list. A refusal is a 403 `CHANGE_REVIEW_POLICY_DENIED` (exit 4) whose `Hint:` points at `qctl get change-review-policy --table <table>`; no key can be proposed, so `CHANGE_PROPOSAL_REQUIRED` never answers them. [backend]

## SEE ALSO

- `qctl explain table`
- `qctl explain table-config`
- `qctl explain rule-revision`
