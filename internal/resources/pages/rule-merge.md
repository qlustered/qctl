---
summary: one table's rules merged into another — a stored plan per source rule, applied with your resolutions and settled as the new rules validate
aliases: [rule-merges]
kind:
schema: openapi:RuleMergeSchema
guide:
signature: c616b77
---

## USAGE

    qctl describe rule-merge <id>
    qctl merge table-rules [table]

## EXAMPLES

    # preview merging orders_eu's rules into orders and stop; an open conflict adds a fill-in -f document after the plan
    qctl merge table-rules orders --from orders_eu --dry-run

    # check a resolution document against a fresh preview, applying nothing
    qctl merge table-rules orders --from orders_eu -f resolutions.yaml --dry-run

    # apply with it, without a prompt, and wait until the merge settles
    qctl merge table-rules orders --from orders_eu -f resolutions.yaml --yes --wait

    # by table IDs; a plan with no conflict needs no -f
    qctl merge table-rules --table-id 42 --from-id 12 --yes

    # the stored preview itself, for a script (the plan goes to stderr)
    qctl merge table-rules orders --from orders_eu --dry-run -o yaml

    # read a merge back: its id, and the TARGET table it was merged into
    qctl describe rule-merge 7 --table orders

    # the same merge as the API returns it
    qctl describe rule-merge 7 --table-id 42 -o yaml

## GOTCHAS

- `--wait-timeout` and SIGINT/SIGTERM cancel HTTP polls in flight, including requests waiting to retry; a successful response arriving after cancellation is discarded.

- An `edit_source` entry's `row_filter` replaces the source rule's filter in the shape of a `TableRule`'s `spec.row_filter` (`qctl explain table-rule.spec.row_filter`): qctl requires its `description`, refuses an unknown key and exits 2 before the prompt. `row_filter: null` removes the source rule's filter and leaving the key out keeps it; RESOLUTIONS shows `edit_source (row_filter)` or `edit_source (row_filter removed)`.
- An `edit_source` `row_filter` is compiled against the TARGET table's columns when the apply is requested, so one naming a column the target lacks or has deleted, in a condition or in the predicate's `columns`, or a predicate on a custom rule, refuses the whole apply with 422 (exit 2). A filter the merge carries over unchanged — by `take_source`, `keep_both`, or a `created` or `replaced` item — is checked only as its rule is created, and one that does not compile fails just that item, with the reason in its note. [backend]
- A `-f` resolution document rejects duplicate keys and field-name case changes in JSON as it does in YAML (exit 2). An unquoted YAML date in an `edit_source` `params` map is sent as the date text written, so `cutoff: 2024-01-01` stays `"2024-01-01"` rather than gaining a time and zone.
- `merge table-rules` names the TARGET table — the one whose rules change — with the positional, `--table` or `--table-id`, and the SOURCE with `--from` (always a name, even one that looks like a number) or `--from-id`, exactly one of them; the same table on both sides exits 2 before the preview. `describe rule-merge` takes the merge id as its positional and the TARGET table by `--table` or `--table-id` only, because a merge is read through the table it was merged into.
- Every run of `merge table-rules`, `--dry-run` included, stores a NEW `previewed` merge on the server — no run reuses an earlier preview, and nothing expires or cleans one up — and no route lists merges, so a merge is reached only by the id the command prints, with `qctl describe rule-merge <id> --table <target>`. [backend]
- Resolutions are keyed by SOURCE rule, never by item id, because item ids change with every preview. An entry's `rule` names a rule of the source table and its `target_rule` a rule of the target table, each by instance name, SHORT-ID, `r-` alias or UUID; qctl looks both up in the two tables' rule lists and maps each entry onto this run's plan. An entry whose rule has no item in the plan, two entries on one item, or a document with no entries exits 2 before anything is applied.
- Before the prompt qctl checks every entry against its item and exits 2 on a misfit, as the server would with a 400: a `created` or `replaced` item takes only `keep_target`, which declines it; a `kept` or `skipped` item takes no entry; `target_rule` goes only on an ambiguous conflict (TARGET RULE reads `ambiguous (N candidates)`), must be one of its candidates, and is required there for `take_source`, `edit_source` and `keep_target` but refused with `keep_both`; `column_mapping`, `params`, `severity` and `row_filter` — `row_filter: null` included — go with `edit_source` only. Whether params or columns are valid is left to the server (400 or 422, exit 2).
- An `edit_source` entry whose `params` or `row_filter` holds a string shaped like a masked value — `•••••` alone or followed by 4 characters, at any depth — exits 2 before the prompt, naming the path, so params copied from a masked viewer's `get table-rule -o yaml` are never sent back as values.
- Before an apply, qctl checks the combined plan once all conflicts are resolved: a target rule waiting for validation cannot be replaced, and two items cannot replace the same target rule (exit 2). This includes an automatic `replaced` item unless declined with `keep_target`. A `--dry-run -f` makes the same check; a bare dry run stays a preview.
- `--dry-run` previews, prints the plan and exits 0 even with conflicts open, and then prints a fill-in `RuleMergeResolution` for them starting at the line `# Set choice on every entry`. The plan above it is not YAML, so copy the document out from that line rather than saving the whole output; under `-o json` or `yaml` both go to stderr and stdout is the stored preview. With `-f` a dry run maps the document onto the fresh plan and fails as qctl's own checks before an apply would; the server can still refuse the apply itself. Without `--dry-run` an open conflict prints the same fill-in document after the plan and exits 2 naming each one; `--dry-run` with `--yes` or `--wait` exits 2.
- Applying asks first, on stderr, after the plan; `--yes` (`-y`) skips the question and is required whenever stdin is not a terminal — `-f -` included, since the document then arrives on stdin — or the command exits 2 before the server stores a preview. Declining exits 1.
- The apply plans the merge again and refuses with 409 (exit 2) when the plan moved since the preview: "The rules of either table changed since the preview; preview the merge again." A rule changed on either table while the prompt waited means running the command again. [backend]
- A source rule is paired by LINEAGE when an earlier `applied` or `partially_applied` copy or merge between the two tables, in either direction, created, replaced or kept it — a `qctl copy table` that carried the rules counts, so a copy has lineage with its source from the start. The newest such record gives the base, and both rules are followed through later upgrades. Any other source rule is matched by rule family plus an identical column mapping against the target's rules that are not deleted, not `validation_failed`, not superseded by an upgrade and not already paired by lineage. [backend]
- With lineage the plan is three-way against the base, comparing states as enabled or not: source unchanged, or already equal to the target → `kept`, whatever the target did; source changed and target not → `replaced`; both changed → `conflict` ("It changed in both tables."). A paired target rule since deleted gives `skipped` when the source is unchanged and `conflict` when it changed; one still `pending_validation` gives `skipped` ("Waiting for validation."). So disabling a rule in the source plans a `replaced` whose new rule is created disabled and disables the target's. [backend]
- Without lineage there is no base, so a match is `kept` when the two configurations are equal and a `conflict` otherwise — never `replaced` — and a source rule with no match is `created`. A rule family and column mapping held by more than one rule on EITHER side (two target rules, or two source rules sharing one target rule) makes each of those source rules an ambiguous conflict whose candidates are the target rules, and no two items may replace the same target rule (400, exit 2). [backend]
- A source rule that is `pending_validation`, `validation_failed` or `error` is `skipped`, with notes "The source rule is waiting for validation.", "The source rule failed validation." or "The source rule has an error.", respectively; one upgraded to a newer rule is also `skipped` ("It was upgraded to the rule ...", or "Waiting for validation." while the upgrade validates); a deleted source rule gets no item at all. [backend]
- Every rule the merge creates is named after its source rule, with ` (2)`, ` (3)`, ... appended while the target has a rule of that name in ANY state — a deleted rule and a retired one keep theirs, and a replacement is created before the rule it replaces is retired — so replacing a same-named rule makes `NAME (2)`. `keep_both` adds an independent rule beside the target's, not an upgrade of it. [backend]
- Params in the plan's snapshots — `rule_snapshot`, `base_snapshot`, `target_snapshot`, and the CONFLICTS block that shows them — are STORED values, but an `edit_source` entry's `params` are read in DISPLAY units through the target's column formats. Today that differs only for `range_rule` `min`/`max` on a percent column, where display 5 is stored 0.05, so snapshot params copied into an entry unchanged are off by a factor of 100 there. An entry's `params` and `column_mapping` replace the source rule's wholesale, not key by key. [backend]
- The apply answers 202 with the merge `applying`, always. The rules are created in that call: a disabled source rule gives a disabled rule, and an enabled one gives a `pending_validation` rule when the target has an active data source or the rule has produced fields, and an `enabled` rule at once otherwise. A replacement retires the target's rule — disables it and takes its position — in that call when the new rule is not pending, and otherwise only when the new rule is enabled; a new rule that fails validation leaves the target's rule in service. [backend]
- A merge stays `applying`, with no deadline, while any rule it created is `pending_validation`, and that item's note says what it waits for: "Waiting for validation.", "Waiting for review of the draft migration that adds its columns." (a rule whose produced fields add columns is enabled only when that draft migration is applied), or "It waits for validation until the target has an active data source.". Meanwhile every other apply into the same table is refused with 409 (exit 2), naming the merge that is applying. [backend]
- On a target with an active data source but no rows yet, a validation dry run has nothing to evaluate, and a run that evaluates no eligible row fails: each rule the merge created pending goes to `validation_failed`, its item ends `failed` ("The new rule failed validation."), and the target rule it would have replaced stays in service. [backend]
- While the target has a draft migration, an apply that would create any rule with produced fields — from a created or replaced item not declined, or a conflict resolved with `take_source`, `edit_source` or `keep_both` — is refused with 409 (exit 2): apply or discard the draft migration first. [backend]
- An `applying` merge moves on only when the server settles it: when it is read (`describe rule-merge`, each `--wait` poll), when the same two tables are previewed again, or on the scheduler's periodic sweep. It ends `applied` when no item failed, `failed` when none was created, replaced or kept, and `partially_applied` otherwise. After the apply a conflict item shows the action it took and its `conflict_choice`, and a declined item reads `skipped` (it was created) or `kept` (it was replaced). [backend]
- `summary` is prose — "Rules: 1 created, 2 kept." counting every item by action, skipped and kept included, or "The merge carried no rules." — and is null until the merge is final; a script should read `items[].action` instead. [backend]
- After an apply, a created or replaced item's `target_object_id` is the rule the merge CREATED, while its `target_snapshot` stays the configuration of the target rule it was paired with — the one a replacement retires — so TARGET RULE in `describe rule-merge` names the new rule (`NAME (2)`) where the preview named the old one. [backend]
- Every route — preview, read and apply — needs both `dataset:rule:edit` and `rule:instantiate`, plus access to both tables (403, exit 4). A composite (linked) table on either side is refused with 400 (exit 2), and so is a table merged into itself, which qctl refuses first. Once either table is deleted, its merges are not found (404, exit 3), to read and to apply alike. [backend]
- `--wait` polls every 2 seconds, then every 5 once it has waited 30, until the merge is `applied` (exit 0) or `partially_applied` or `failed` (exit 1, with the summary). Running out of `--wait-timeout` (seconds, default 900) exits 124, lists the created and replaced items that carry a note — among them what the merge waits for — and leaves the merge applying: only the wait stopped. A poll refused outright (403, 404) ends the wait with that exit code and the same `Next:` line. Each change of state, or of an item's action or note, prints to stderr; under `-o json` or `yaml` stdout is the last successful poll. `--wait-timeout` without `--wait` exits 2.
- Before its first poll `--wait` prints one line to stderr, `waiting for rule merge <id> to finish applying, for up to 15m0s; check with: qctl describe rule-merge <id> --table-id <target-id>` at the default `--wait-timeout`. Ctrl-C (SIGINT) ends the wait with exit 130, and SIGTERM with 143, printing `interrupted; check with: qctl describe rule-merge <id> --table-id <target-id>` on stderr; as on a timeout, only the wait stops and the merge goes on applying.
- Under `-o json`, `yaml` or `name` the plan and outcome go to stderr and stdout carries one document: the stored preview with `--dry-run`, the accepted merge (`applying`) after an apply, the last poll with `--wait`, or `rule-merge/<id>` for `-o name`. `describe rule-merge` takes `-o table`, `json` or `yaml` only (`-o name` exits 2) and names rules by listing both tables' rules, showing a SHORT-ID where that lookup fails.
- `merge table-rules` previews the merge unchecked, then applies it under the target table's change review policy (operation `rule_merge_apply`, key `rules`), so `--dry-run` passes where the apply is refused; a refusal is a 403 `CHANGE_REVIEW_POLICY_DENIED` (exit 4) whose `Hint:` names the operation and points at `qctl get change-review-policy --table <table>`. The key cannot be proposed, so `CHANGE_PROPOSAL_REQUIRED` never answers it. [backend]

## SEE ALSO

- `qctl explain table-copy`
- `qctl explain table-rule`
- `qctl explain table-schema`
