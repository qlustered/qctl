---
summary: a table composed from other tables
aliases: [composite-dataset]
kind: LinkedTable
schema: openapi:CompositeDatasetApplyManifest
guide: link-tables
signature: 6740ae2
---

## USAGE

    qctl apply -f <file.yaml>   # kind: LinkedTable
    qctl apply linked-table -f <file.yaml>
    qctl diff -f <file.yaml>   # kind: LinkedTable
    qctl enable linked-table [table]
    qctl get linked-table [table]
    qctl rekey linked-table [table]

## EXAMPLES

    # create or update a linked table from a manifest
    qctl apply linked-table -f linked-table.yaml

    # check the manifest server-side without persisting anything
    qctl apply linked-table -f linked-table.yaml --validate-only

    # the same manifest through the generic dispatcher
    qctl apply -f linked-table.yaml

    # apply, then wait for the dry run and the enable it decides
    qctl apply linked-table -f linked-table.yaml --wait

    # the same through the generic dispatcher, waiting at most ten minutes
    qctl apply -f linked-table.yaml --wait --wait-timeout 600

    # preview what the apply would change, after the server validates the manifest
    qctl diff -f linked-table.yaml

    # enable the linked table's latest spec revision
    qctl enable linked-table orders_enriched

    # roll the table back to an older revision, by table ID
    qctl enable linked-table --table-id 12 --revision 550e8400-e29b-41d4-a716-446655440000

    # show a linked table's latest spec revision
    qctl get linked-table orders_enriched

    # export it as a manifest to edit and apply again
    qctl get linked-table orders_enriched -o yaml > orders_enriched.yaml

    # export an older revision of the table, by table ID
    qctl get linked-table --table-id 12 --revision 550e8400-e29b-41d4-a716-446655440000 -o yaml

    # after editing the exported manifest's natural keys and setting spec.release
    qctl rekey linked-table orders_enriched -f orders_enriched.yaml --yes

## GOTCHAS

- `apply -f <file> -o name` and `apply linked-table -f <file> -o name` print `linked-table/<table-id>` from the response, including an unchanged result. A valid `--validate-only` result, or a successful response without a decoded table ID, prints `linked-table/<manifest-name>` instead; this name form identifies the manifest target and is not an ID reference for other commands. Control characters in that fallback name are escaped. Several documents print one reference per line in file order; prose and warnings go to stderr.

- Integer fields in a YAML manifest reject fractional numbers during parsing (exit 2): `2.7` is refused instead of becoming `2`. Whole-valued YAML numbers such as `2.0` remain accepted.
- A pending auto-enable on a later page of unfinished dry runs is included when `get linked-table -o yaml|json` chooses `spec.desired_state`.

- Apply and diff reject duplicate manifest keys and field-name case changes in JSON as in YAML (exit 2), including keys inside a `join_fields` clause.
- `rekey linked-table` requires `spec.release`, checks that the manifest still names the selected table and its existing primary source, and refuses `spec.dry_run`; it sends the manifest's natural keys, sources and output as one re-key request. It prompts for acknowledgement unless `--yes` is given.

- the manifest `kind` may be written as `LinkedTable` or `CompositeDataset` — `apply linked-table`, `apply -f` and `diff -f` all accept either spelling, but the wire request sent to the backend always has `kind: CompositeDataset`, regardless of which alias you wrote.
- `--validate-only` sets `validate_only=true`, and the backend then runs the *same* apply pipeline — resolution, the permission branch, the frozen-identity check, every validator — inside a savepoint it always rolls back, so nothing is persisted either way. A content problem (a bad reference, a failed validator, a change to a frozen field) still answers `200`, with `valid: false` and the problem listed under `errors`, so qctl reads that verdict and exits 2 listing every error — the exit code a real apply's refusal of the same manifest gives; a 403/404 does not come back that way — the pipeline re-raises those as an HTTP error instead, same as a non-`--validate-only` apply would. What the flag skips is the linked table's change review policy check and everything after content validation: no convergence, so no dry run is launched, no ingestion job queued and no run event published. [backend]
- the flag only exists for `LinkedTable`/`CompositeDataset` manifests — it's registered on `apply linked-table` and separately on the top-level `apply` (for `apply -f`), and nowhere else: `apply -f --validate-only` refuses a file holding a document of any other kind `apply -f` accepts, exiting 2 before any document is applied and naming each one it would otherwise have applied for real, while `apply table` / `apply destination` / `apply cloud-source` reject it outright as an unknown flag; it's also distinct from `spec.dry_run`, which only sets `max_spine_rows` and `seed` for the composite dry run a real apply launches.
- a validation issue — a `--validate-only` error or warning, or an entry in the issue list a real apply's `400`/`409` refusal carries — prints as `<loc>: <msg> (<code>)`, errors inside the exit-2 message and warnings on stderr as `Warning:` lines that do not fail the check, with the server's `loc` joined by dots exactly as sent: apply's own checks point into the manifest (`spec.destination`, `spec.sources.<alias>.dataset`, `metadata.name`), but the validators apply shares with the web wizard answer in the wizard's request shape — `revision.output.<column>.field` for `spec.output.<column>.field`, `group.natural_key_columns` for `spec.spine.natural_key_columns`, `dataset.table_name` for `spec.table.name` — and a `loc` or `code` the server left empty is left out. [backend]
- a real apply prints `linked-table/<name> created`, `configured` (the server's `revision_added`) or `unchanged`, then the revision's release and its state after the apply, the composite dry run the apply launched (if any) and whether that run will enable the revision, and a `Next:` line; the apply's warnings print to stderr as `Warning:` lines, and `-o json` prints the server's answer verbatim instead.
- `--wait` polls the composite dry run the apply launched, as `create composite-dry-run-job --wait` does (`qctl explain composite-dry-run-job`), and then prints how it ended — the revision's state and auto-enable outcome and a `Next:` line that replaces the apply's own; it exits 1 when the dry run ends without finishing or the backend withholds the auto-enable or fails to enable, with the reason, and 124 when `--wait-timeout` (seconds, default 900) runs out first, which stops only the wait. An apply that launched no dry run returns at once. On `apply -f` it also waits for the validation of a table rule a `TableRule` document creates (`qctl explain table-rule`), and every other kind waits for nothing; in a multi-document file each document's wait ends before the next document is applied, a failed wait failing that document. `--wait` beside `--validate-only`, and `--wait-timeout` without `--wait`, exit 2 before any document is applied. Under `-o json` stdout is the server's answer, as without `--wait`, and how the dry run ended goes to stderr.
- each wait first prints one line naming it to stderr, for a linked table's dry run `waiting for composite dry run <id> to settle, for up to 15m0s; check with: qctl describe composite-dry-run-job <id>` at the default `--wait-timeout`. Ctrl-C (SIGINT) during a wait exits 130, and SIGTERM 143, printing `interrupted; check with: qctl describe composite-dry-run-job <id>` on stderr; only the wait stops and the dry run carries on, but in a multi-document file the signal ends the whole apply at that document: no later document is applied, and no `<n> documents:` summary line follows.
- `unchanged` means the manifest's spec hashed to a revision the table already has — possibly an older, disabled one, which is how re-applying an old manifest rolls back — and a manifest `release` that differs from that revision's is ignored; a revision that is not yet enabled gets a composite dry run when it has no current successful one, and with `desired_state: enabled` that run enables it when it finishes if the enable gates pass (a revision that already passed one is enabled during the apply itself), while `desired_state: draft` never disables a revision that is already enabled. [backend]
- converging a manifest usually launches a composite dry run (a revision that is already enabled, or that already has a current successful dry run, converges without launching one), so an apply can be refused with `429` when you already have 3 dry runs *executing* in this organization (`DRY_RUN_RATE_LIMIT`; only `pending`/`running` count, queued runs are free) — `apply linked-table` and `apply -f` absorb it the way `create dry-run-job` does, making up to 5 attempts and waiting the server's `Retry-After` (15s) between them, with each wait printed to stderr so `-o json` stdout stays parseable; the refusal rolls the whole apply back, so retrying re-sends the identical manifest safely, and `--validate-only` never launches a dry run and so never hits this at all. [backend]
- `qctl diff -f` first sends a linked-table manifest to the apply route with `validate_only=true`, the same check as `apply linked-table --validate-only`, so it needs what a real apply needs — `dataset:create` for a new table, `dataset:config:edit` for an existing one, and access to every source table and to the existing table itself — and a 403 or 404 exits with its own code before anything prints; a manifest that fails the check exits 2 listing the server's errors, with its warnings on stderr, and nothing is compared. [backend]
- a linked-table preview of an existing table starts with `linked-table/<name>: compared with its latest revision, <release> (<state>)` and then one `path: old → new` line per change; a create has no header and lists every spec field the manifest sets as `<absent> → value`, except `spec.dry_run` and an output column the check reports as `deleted_field_pruned`. The check's warnings follow the changes on stdout as `Warning:` lines, so stderr stays empty on exit 1, and the request body apply would send comes last. With no differences diff exits 0 and prints nothing, warnings included.
- against an existing table the preview compares what an apply would change in its spec: each source's table, `arity` and `join_fields` in the form the server hashes (so a shorthand and the clause it abbreviates are a difference), the `output` mappings, and `description` only when the manifest sets one or the spec changed, since a new revision takes the manifest's description, absent included. The frozen fields — spine, table, destination, backup settings — are not compared, because changing one fails the check (exit 2); nor are `release` and `spec.dry_run`, because a matched revision keeps its own release and a new one takes the manifest's or the next `vN` across all the table's revisions. An output column the check reports as `deleted_field_pruned` is left out of the manifest side only, as apply leaves it out of the spec, so a live mapping that reads a deleted field shows as a removal. [backend]
- when sources and output already match the latest revision, the preview adds one `spec.desired_state: "<state>" → "enabled"` line if that revision is not `enabled` and the manifest wants `enabled` (the default), since apply would converge it toward enabled; `desired_state: draft` never adds a line, since apply never disables a revision, and neither shows the composite dry run an apply may launch. [backend]
- the preview's baseline is the table's latest spec revision, but apply matches the manifest's spec hash against every revision the table has, whatever its state, so a manifest matching an older revision — the rollback path — previews as differences from the latest while apply reports `unchanged` and converges the older one; no route lists a table's revisions, so qctl cannot find the one apply would match. [backend]
- The preview identifies an existing linked table by the slug of `metadata.name`, as apply does. It checks an exact-name table first, then scans visible tables across pages and reads their latest revision groups until the slug matches; a renamed table, including a series member, therefore compares with its existing revision. A latest-revision 404 is skipped; other read failures stop the preview. Apply restores a non-series table's display name to `metadata.name`, while a series member keeps its series-derived display name. [backend]
- a join `constant` that holds among its values, at any depth, an integer outside -9223372036854775808..18446744073709551615 — decimal, `0x`, `0o`, `0b` or leading-`0` — or a `+`-signed one above 9223372036854775807 fails the manifest with exit 2 before anything is sent, `apply` and `diff -f` alike, since it would be read as a float or a string instead of the integer written; quote it to send a string. So does a leading-`0` decimal, which YAML reads as a float, when that float would be sent with other digits, such as `09007199254740993` (sent as `9007199254740992`), and a `0b` or `0o` literal with its sign after the prefix, such as `0b-1`. A float is sent as a float — `2.0` sends `2.0` and `2e19` sends `20000000000000000000.0` — while `09`, which YAML reads as a float, is sent as the integer `9`.
- a join `constant` also fails the manifest with exit 2 when it holds, at any depth, an infinity or NaN such as `.inf`, `-.inf` or `.nan`, which JSON cannot carry; a float beyond the float64 range, such as `1e400`, which YAML reads as a string; or a mapping key YAML does not read as a string, such as the `5` in `{5: x}` or a `true`, `~` or date key — quote the value or key to send a string. A value YAML reads as a date or timestamp, such as `2024-01-01`, is sent as the text written, `"2024-01-01"`.
- a join `constant` that holds, at any depth, an integer from 9223372036854775808 to 18446744073709551615 passes qctl's own check but is refused by the server with 422, so `apply`, `apply --validate-only` and `diff -f` all exit 2 with its message: `constant integers must be between -9223372036854775808 and 9223372036854775807`. [backend]
- With no `--revision`, `enable linked-table` enables the table's latest spec revision — the newest one, whatever its state — which is not always the revision your last apply touched: an apply whose spec hashes to an older revision reuses that one, so name it with `--revision` (the apply's `Next: qctl enable linked-table` line does). [backend]
- `--revision` must be a revision of the linked table's current spec group; one of another table, or one from before the table was re-keyed, exits 2 before the enable is sent.
- Enabling a revision disables the one enabled before it and queues a rebuild of the table. A `draft`, `in_review` or `disabled` revision can be enabled — enabling a `disabled` one is how a table rolls back to an older spec — but the backend refuses to enable an `enabled` one, so for a revision that is already enabled qctl sends nothing, prints `already enabled` and exits 0. [backend]
- To rebuild the table from the revision already enabled, use `qctl run composite-run-job <table>` — see `qctl explain composite-run-job`.
- `get linked-table -o yaml|json` writes the spec revision as a `kind: LinkedTable` manifest that `apply -f` and `diff -f` accept, plus a `status:` block they parse but do not use (table id, spec group id, slug, revision id, release, state, backup settings id); by default it prints one row, and `-o name` prints `linked-table/<table id>`. The spec has no `release`, so an edited export gets the next `vN` on apply instead of being refused as `release_conflict`, a release the table already has with a different spec; `desired_state` is `enabled` when the exported revision is enabled or an unfinished dry run of that revision has a pending auto-enable, and `draft` otherwise, since apply converges a revision toward `enabled` unless the manifest says `draft`. [backend]
- An export's `metadata.name` is the composite spec group's name, which can differ from the table's display name after a series rename. Apply identifies the group by the slug derived from `metadata.name`; series members keep their series-derived display names. A group created with a custom or collision-suffixed slug that its name does not derive still needs `metadata.name` edited to derive `status.slug` before apply or diff can target it. [backend]
- `get linked-table` names the backup settings through `GET /backup-settings/{id}`, which needs `backup:list` — a permission the `agent` role lacks although it may apply linked tables — so when that read answers 403, or the backup settings have no name, `spec.backup_settings` is left out with a `Warning:` on stderr and the export still succeeds; apply resolves an absent `backup_settings` to the organization's only backup settings and refuses the manifest when there are several, and `status.backup_settings_id` keeps the table's id. [backend]
- With no `--revision`, `get linked-table` shows the table's latest spec revision — the newest, whatever its state — which is not the one the table is built from once an apply with `desired_state: draft` has added a revision, since that leaves the enabled revision in place; `status.state` tells them apart, and `--revision` shows another revision of the table's current spec group (one of another table, or one from before the table was re-keyed, exits 2). [backend]
- The backend enables a revision only when its latest composite dry run exists, has `finished`, ran against the revision's current spec, sampled at least one spine row and found no activation blockers; its other refusals include a table whose composite output is not active, a revision whose sources no longer resolve to tables of this organization, and one that would close a dependency cycle. Each refusal is a 400 carrying the reason, and the command exits 2 with it. For the dry-run gate, launch one with `qctl create composite-dry-run-job` and wait for `finished`. [backend]
- A manual enable cancels the pending auto-enables on the table's other revisions: a dry run that an apply with `spec.desired_state: enabled` launched for another revision is marked `auto_enable_state: withheld` ("superseded by manual enable") at once, so it no longer enables that revision over yours, while one pending on the revision you enabled is left pending. Every successful apply of the table cancels all of them, the one on the revision it converges included ("superseded by newer apply"); `qctl describe composite-dry-run-job <id>` shows a dry run's `status.auto_enable_state` and `auto_enable_detail`. [backend]
- a linked table is built from its spine table's CLEAN, undeleted rows only, and each joined source is matched against its clean rows only: a quarantined spine row produces no linked row, and a quarantined joined row counts as no match, so `exactly_one` or `at_least_one` on that source fails the whole build. A composite dry run files that spine row under `blocked_zero_match` and lists the quarantined ids in `quarantined_upstream_row_ids` (`qctl inspect composite-dry-run-job`). Release or fix the rows upstream first. [backend]
- each `spec.output` entry is `{source, field}` — a straight copy of one source column — and nothing else: no expression, constant or cast, and an extra key is refused before anything is sent. Join clauses can use expressions; output cannot. To derive a value, have an enrichment rule write it into an upstream table's column and map that column. [backend]

- Updating an existing linked table, including `--validate-only` and the validation performed by `diff`, needs the role's `dataset:config:edit` permission. Internal callers other than ops/admin also need an editor or owner grant on that table; a `read_only` grant is refused with 403 (exit 4). [backend]
- the change review policy of the linked table governs `apply -f LinkedTable` when it converges an existing linked table (operation `composite_dataset_apply`, key `spec`), `enable linked-table` (`composite_spec_revision_enable`) and `rekey linked-table` (`composite_spec_group_rekey`, key `natural_keys`). A refusal is a 403 `CHANGE_REVIEW_POLICY_DENIED` (exit 4) whose `Hint:` names the operation and points at `qctl get change-review-policy --table <table>`; no key of these operations can be proposed. Creating a linked table is not checked, and neither is `diff -f`, which runs the server's `validate_only` pipeline without the policy, so it can pass a manifest that `apply -f` is then refused. [backend]

## SEE ALSO

- `qctl explain table`
- `qctl explain composite-dry-run-job`
- `qctl explain composite-run-job`
- `qctl guide link-tables`
