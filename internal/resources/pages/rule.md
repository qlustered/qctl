---
summary: a Python data-quality rule in the org catalog
aliases:
kind: Rule
schema:
guide: submit-rules
signature: d4711f6
---

## USAGE

    qctl apply -f <file.yaml>   # kind: Rule
    qctl delete rule <name-or-id>
    qctl delete rules
    qctl describe rule <name-or-id>
    qctl disable rule <name-or-id>
    qctl enable rule <name-or-id>
    qctl get rule <name-or-id>
    qctl get rules
    qctl set-default rule <name-or-id>
    qctl submit rules

## EXAMPLES

    # add a new rule, or a new release of an existing one, to the catalog (ops role only)
    qctl submit rules -f rules.py

    # submit several files in one call
    qctl submit rules -f rules1.py -f rules2.py -f rules3.py

    # submit the .py files directly inside a directory (not its subdirectories)
    qctl submit rules -f rules/

    # resubmit over an existing version; still won't overwrite a locked enabled/disabled revision
    qctl submit rules -f rules.py --force

    # skip the confirmation prompt, e.g. in CI
    qctl submit rules -f rules.py --yes

    # ask whether the server would accept the file, and what it would do with each rule; writes nothing
    qctl submit rules -f rules.py --check

    # list every rule family in the org catalog
    qctl get rules

    # show one rule; all its releases if the name has more than one
    qctl get rule email_validator

    # pick one release when the name matches several
    qctl get rule email_validator --release 1.0.0

    # short ID resolves too, not just the name
    qctl get rule 550e8400

    # apply-compatible kind: Rule manifest, for round-tripping (needs --release if the name has several)
    qctl get rule email_validator -o yaml

    # raw Python source, not a manifest (also needs --release when the name has several)
    qctl get rule email_validator -o code

    # diagnostic view of one release when the name has several
    qctl describe rule email_validator --release 1.0.0

    # --release is optional when the name has exactly one release
    qctl describe rule email_validator

    # show matching table kinds and the standard field bound to each rule field
    qctl describe rule email_validator -o json

    # how each rule field would bind on table orders if attached with no column_mapping
    qctl describe rule email_validator --release 1.0.0 --table orders

    # the same preview for an upgrade: fields keep the columns my_email_check maps them to
    qctl describe rule email_validator --release 2.0.0 --table-id 42 --upgraded-from my_email_check

    # short ID also resolves
    qctl describe rule 550e8400

    # full UUID works too
    qctl describe rule 550e8400-e29b-41d4-a716-446655440000

    # full source instead of the truncated preview
    qctl describe rule email_validator --show-code

    # flips state to enabled for the matching revision
    qctl enable rule email_validator

    # disambiguate when the name has multiple releases
    qctl enable rule email_validator --release 1.0.0

    # resolve by ID when you already have one — no name or --release lookup needed
    qctl enable rule 550e8400

    # promotes this revision to the family's current one (a Default tag in get rule/get rules)
    qctl set-default rule email_validator

    # pick which release becomes default when several exist
    qctl set-default rule email_validator --release 1.0.0

    # same shortcut — go straight from an ID (e.g. saved from get rule) without re-resolving the name
    qctl set-default rule 550e8400

    # flips state to disabled for the matching revision
    qctl disable rule email_validator

    # disambiguate when the name has multiple releases
    qctl disable rule email_validator --release 1.0.0

    # ID form skips name/--release resolution entirely
    qctl disable rule 550e8400

    # blocked if this revision is enabled, or referenced by an ENABLED table rule
    qctl delete rule email_validator

    # target one release when the name has several
    qctl delete rule email_validator --release 1.0.0

    # ID form works the same way here — no name or --release lookup needed
    qctl delete rule 550e8400

    # full UUID; --yes skips the confirmation prompt
    qctl delete rule 550e8400-e29b-41d4-a716-446655440000 --yes

    # the reverse of submit: remove revisions matching this file's definitions from the catalog
    qctl delete rules -f rules.py

    # skip the confirmation prompt, e.g. in CI
    qctl delete rules -f rules.py --yes

    # apply a kind: Rule YAML manifest through the generic dispatcher (.py files are rejected — see GOTCHAS)
    qctl apply -f rule.yaml

## GOTCHAS

- `apply -f <file> -o name` prints `rule/<revision-uuid>`, including an unchanged result. Several applied documents print one reference per line in file order; prose goes to stderr.

- `submit rules`, `submit rules --check` and `delete rules -f` need `ops:rule:create`, which only the `ops` role (Qluster operations staff) holds; any other role, `admin` included, gets a 403 (exit 4). `enable`, `disable`, `set-default`, `delete rule`, dry runs and attaching a revision to a table need other permissions, not that one. [backend]
- `submit rules` and `submit rules --check` reject files above 500,000 Unicode code points with exit 2 before sending them; their Size preview counts code points, including combining marks separately.
- Server preflight for `submit rules` and `submit rules --check` also caps each file at 500,000 UTF-8 bytes, so a file within qctl's character limit can still receive a 422 for its byte size. [backend]
- Empty positional rule references exit 2 before authentication or requests.
- On an empty list, `get rules --search` writes any search suggestions to stderr in every format; JSON and YAML stdout carry a List with empty `items`.

- a `SHORT-ID` cannot be looked up by ID on the server: the rule-revision search matches name, slug, release, `description`, `business_summary`, `limitations`, the column arrays and the param schema but never `id`. qctl uses that search only to let an exact hex-only name or slug win, then expands an exact-name miss from `~/.qctl/cache/aliases.db` — seeded by every `qctl get rules` / `qctl get rule-revisions` — and falls back to listing the catalog when the cache is cold. `qctl cache aliases clear` empties it; the next listing refills it. [backend]
- a deleted revision's `SHORT-ID` still resolves from that same cache — nothing evicts an entry when the revision it names is deleted — so a lookup that should print a clean "no rule found" instead reaches the backend with a UUID that no longer exists and surfaces as a downstream 404, until `qctl cache aliases clear` empties the cache and the next lookup falls back to a catalog listing that no longer contains it.
- every non-UUID spelling gets an exact name-or-slug lookup first, so a hex-only name such as `decade` or `2024` wins even when an identically spelled UUID prefix is cached or appears in the first 1,000 revisions. After an exact-name miss, an input made only of hex digits and dashes, with at least one hex digit, takes the `SHORT-ID` path: the local cache, then a cache-cold scan of only the first 1,000 revisions, which names that bound when nothing in those rows matches. Non-hex names and slugs keep using every matching rule-revision page before qctl decides between a fuzzy match, an ambiguity or no match, and post-submit draft-review breadcrumbs use the same exhaustive lookup.
- `get rules` lists rule **families**, not revisions: the table and `-o name` show one row per family (two when a newer-than-default revision exists), and `-o json|yaml` one item per family with its revisions nested under `primary_revision` and `secondary_revision`. Use `get rule-revisions` to see every one.
- `get rules -o json`/`-o yaml` carry the API's family objects: `family_id`, and each revision's full `id` under `primary_revision` and `secondary_revision`; there is no `short_id`. `--columns id` shows the revision id in table format, so a short ID from the table can be paired with its full UUID.
- `get rules -o name` prints `rule/<revision UUID>` for each row, as `get rule-revisions -o name` does; every command that takes a rule takes that line back.
- `submit rules --check` runs the checks submit runs and writes nothing: a file submit would refuse receives the same status and body from the check route, with one exception — submit first applies the organization's change review policy, and when that policy does not let your role submit rules it refuses every file with a 403 `CHANGE_REVIEW_POLICY_DENIED` (exit 4), which the check route never applies, so `--check` can pass a file that submit then refuses. A rule it reports `updated in place` is one whose `draft` or `in_review` revision at that release submit rewrites, and submit's own report lists such a rule as neither added nor unchanged. [backend]
- `submit rules --check` checks each `-f` file on its own against the catalog as it stands, while `submit rules` writes each file before it sends the next — so two files that define the same rule can both pass `--check` and still leave the second one refused, or updated in place, when submitted together. Like submit, it stops at the first file that fails.
- `submit rules` and `submit rules --check` suggest bumping `RuleMetadata.release` only when a rejection carries a code diff; other rejections show the server's reason without that hint.
- against a server that predates the check route, `submit rules --check` exits 5, saying the server does not support it, and never falls back to submitting.
- `submit rules -f <dir>` submits the regular `.py` files directly inside the directory, in name order, as if each had its own `-f`; its subdirectories, its other files and its `__init__.py` are skipped, and a directory with no other `.py` file exits 2. An `__init__.py` named by its own `-f` is sent. A path that does not exist, file or directory, exits 1 before anything is sent. A shell glob after one `-f` (`-f rules/*.py`) leaves every file after the first as a stray argument and exits 2 naming the fix: repeat `-f`, or pass the directory; a file given with no `-f` at all exits 2 naming the same fix. A file that is not `.py` exits 2, with a pointer to `qctl apply -f` only for a `.yaml`, `.yml` or `.json` file.
- When authoring issue messages, format every row value with `self.value_for_msg(column, value)` (`Rule.value_for_msg`), passing the rule's logical column name; never interpolate raw row values directly into f-strings. See `qctl guide submit-rules` for the example.
- Python rule files are **submitted**, not applied — `qctl apply -f x.py` fails immediately (a client-side extension check) with a pointer to `qctl submit rules -f x.py`.
- under `-o json` or `-o yaml`, `delete rules -f` prints the server's `{message, deleted, not_found, skipped}`, each list `[]` when empty, and `submit rules` its `{message, added, not_changed}`, each rule a `[name, release]` pair, or `{message, rules}` under `--check`; several files, counted after each `-f` directory is expanded, print one List, and the prose moves to stderr. `delete rules -o name` prints `rule/<revision-id>` for each deleted revision; `submit rules` has no name view and exits 2 on `-o name`.
- `get rule <name> -o code` prints the raw Python source (no headers); `-o yaml`/`-o json` print an apply-compatible `kind: Rule` manifest — all three error and ask for `--release` when the name has multiple releases (every other output format shows every release at once).
- `describe rule --table <name>` or `--table-id <id>` adds a `Table bindings` block: the server's preview of how a `kind: TableRule` create with no `column_mapping` would bind each rule field on that table — required or not, `bound`, `ambiguous` or `unbound`, the column, and its source (`inherited`, `concept`, `original_name`, `normalized`), with the ranked candidate columns under a field left unbound or ambiguous; `-o json|yaml` carry it as `field_column_candidates`. `--upgraded-from <table-rule>` previews an upgrade of that rule, whose fields inherit its columns, and exits 2 without a table. `qctl explain table-rule` has the binding order.
- `describe rule` shows matching table kinds on a `Works with:` line and standard-field bindings under `Columns:`; fields without a standard field show `-`. `get rule -o yaml|json` carries `problem_domains` and `field_concepts` under read-only `status`, while its table view remains a release summary.
- In Python rule files, `column_concepts={"Gross Amount": "gross_amount"}` inside `RuleMetadata(...)` binds an affected rule field to an organization standard field; the value must be an existing slug, the key cannot be `*`, and the metadata must be an inline literal. An unbound field stays generic. The SDK also requires `column_field_types` for every read-side field. See `qctl guide submit-rules` for a complete example. [backend]
- `-o code` is a byte-exact passthrough, so it is the one `get rule` format that does **not** turn terminal control characters into visible `\xNN` escapes: it exists to be redirected into a `.py` file, and escaping would corrupt the source. Page it through `less -R`, or read it with `describe rule`, rather than catting an untrusted revision straight to a terminal.
- `enable`, `disable`, and `set-default` act on one rule **revision**; when the name matches multiple releases, the bare name is ambiguous and `--release` becomes required to pick one.
- `qctl apply -f` patches only `state` and `is_default` in a Rule manifest. It ignores the server-computed `params_renderability`, `params_renderable_in_basic_ui`, and `renderability_ruleset_version` fields, so a saved manifest still applies after those values change. Edits to author-defined fields such as `description`, `default_severity`, `affected_column_types`, and optional columns exit 2 with a pointer to `qctl submit rules`.
- `describe rule` and `get rule -o yaml` show the revision's `default_severity`, the rule author's `RuleMetadata.default_severity`: a creation-time default that decides a `kind: TableRule` instantiate which omits `spec.severity` (`qctl explain table-rule` has the resolution rule), and describes no table rule that already exists. The backend's rule-revision PATCH ignores the key, so the value moves only by resubmitting the rule file. [backend]
- `describe rule` lists the field types each rule field accepts in an `Accepted types:` block under `Columns:`, and `qctl apply -f` annotates the instantiate pre-flight's column mapping the same way (`email -> email_col [String, Text] (explicit)`) — the placement the web UI already uses. Both are display only: qctl never fetches the table's own column types, so it does not refuse a mapping over a type mismatch, and the server stays the judge.
- a rule field with no `Accepted types:` entry is one the revision typed nothing for, not one that accepts anything — the revision's accepted types combine the author's column type declarations with the declared types of enriched columns not already typed, so an untyped field is absent from the map and a revision stored before types were declared has an empty one. `any` is a value the author declares, and the schema allows no non-list value besides it. [backend]
- `--search` needs **3 or more characters**: the server drops a shorter query and returns the unfiltered list, so qctl refuses it (exit 2) rather than print the whole catalog under a heading that claims a filter. What it does match is a case-insensitive substring of the family's name **or** slug — `requir` finds `required_field`, `field_required` finds nothing. [backend]
- the organization's own change review policy, never a table's, governs `submit rules` (operation `rule_revision_submit`), `delete rule` and `delete rules` (`rule_revision_delete`; `delete rules` through unsubmit), and `apply -f Rule`, `enable rule`, `disable rule` and `set-default rule` (`rule_revision_patch`, one key per field sent, `state` or `is_default`, and `apply -f` sends only the fields that differ from the live revision). A refusal is a 403 `CHANGE_REVIEW_POLICY_DENIED` (exit 4) whose `Hint:` names the operation and the refused keys and points at `qctl get change-review-policy` with no selector; no key of these operations can be proposed, so `CHANGE_PROPOSAL_REQUIRED` never answers them. [backend]

## SEE ALSO

- `qctl explain rule-revision`
- `qctl guide rule-lifecycle`
- `qctl guide submit-rules`
