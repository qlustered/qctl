# Submit rule files to the org catalog

A **rule** is Python-defined validation / correction / enrichment logic that
lives in your organization's rule catalog, independent of any table. You author
rules in a `.py` file and **submit** them with `qctl submit rules`; the file is
parsed server-side and only the extracted rule *versions* are kept.

Submitting adds a rule to the catalog. Attaching one to a table is a separate
step — see `qctl guide instantiate-rule`.

## 1. Write a rule file

Each rule is a class subclassing `Rule` from the `qluster_sdk.rule` SDK. It
declares `metadata`, a one-line `business_summary`, and an `apply(self, row)`
method that returns a `RuleResult` — empty for a clean row, or carrying an
`Issue` to flag one.

```python
# rules.py
from qluster_sdk.rule import Rule, RuleMetadata, RuleResult, Issue, RowProxy


class GrossAmountPositiveRule(Rule):
    """Gross Amount must be present and a positive number."""

    metadata = RuleMetadata(
        release="1.0.0",
        validates_columns=["Gross Amount"],
        column_field_types={"Gross Amount": "any"},
        column_concepts={"Gross Amount": "gross_amount"},
    )
    business_summary = "Flags rows where Gross Amount is missing or not positive."

    def apply(self, row: RowProxy) -> RuleResult:
        raw = row.get("Gross Amount")
        try:
            amount = float(raw)
        except (TypeError, ValueError):
            amount = None
        if amount is None or amount <= 0:
            return RuleResult(issues=[Issue(
                dataset_rule_id=self.dataset_rule_id,
                issue_reason=(
                    "Gross Amount must be positive, got "
                    + self.value_for_msg("Gross Amount", raw)
                ),
                field_names=["Gross Amount"],
            )])
        return RuleResult()  # clean row
```

Rules that take configuration declare a pydantic params model and read
`self.params` inside `apply`; those params are validated later, when the rule is
instantiated on a table.

### Format row values in issue messages

Format every row value in an issue message with `Rule.value_for_msg`, called
as `self.value_for_msg(column, value)`. Pass the rule's logical column name
and the row value, as the example does with `"Gross Amount"` and `raw`.
The helper writes Qluster message markup and marks values of PII columns
so readers can mask them. Never interpolate a raw row value directly into
an f-string, including with `!r`; that leaves it without the PII marker.
An f-string may include the helper's result.

### Bind rule fields to standard fields

`column_concepts` maps a rule field to a standard field slug in the organization. In
the example, the `Gross Amount` field binds to the built-in `gross_amount`
standard field. A table column tagged with that standard field can make the rule a match for
the table and supply a column mapping when it is the only matching column.
An explicit mapping still takes precedence. Fields omitted from
`column_concepts` remain generic and match by field type only.

Use `qctl get standard-fields` to find available slugs. An organization admin must
create a custom standard field before a rule can bind to it; submitting a rule with
an unknown slug fails with a 422. Write `column_concepts` as an inline literal
in `RuleMetadata`, with keys from `input_columns`, `validates_columns`,
`corrects_columns`, or `enriches_columns` (never `*`). Slugs start with a
lowercase letter and use lowercase letters or numbers, with single hyphens or
underscores between groups. Declare `column_field_types` for every read-side
field, even one bound to a standard field; use `"any"` only when the rule accepts any
field type.

## 2. Check, then submit it

```bash
qctl submit rules -f rules.py --check
qctl submit rules -f rules.py --yes
```

Run `--check` until it passes, then submit with `--yes` — only the files the
user asked you to submit.

- Only `.py` files are accepted (YAML rule manifests go through `qctl apply -f`).
- The file is parsed **server-side**. qctl caps each file at 500,000 Unicode code points; server preflight also caps it at 500,000 UTF-8 bytes.
- Submit several at once by repeating `-f`:
  `qctl submit rules -f rules.py -f more_rules.py`.
  Or pass a directory to submit the `.py` files directly inside it, other
  than `__init__.py`: `qctl submit rules -f rules/`. A shell glob after one
  `-f` exits 2.

Submitting prints a confirmation summary (Organization / File / Size) and asks
before sending. Once you confirm, it sends the file and reports what changed:

```
Importing rules from rules.py...

Successfully submitted 1 rule(s)

NAME                        VERSION
gross-amount-positive-rule  1.0.0
```

The `NAME` column is the rule's slug: `GrossAmountPositiveRule` becomes
`gross-amount-positive-rule`. Commands that take a rule accept either spelling.

Flags:

- `--force` — skip, instead of rejecting, a rule whose `enabled`/`disabled`
  revision at the same release has different code.
- `--yes` — skip the confirmation prompt (useful in scripts / CI).
- `--check` — ask whether the server would accept the file; write nothing.

### Check before you submit

`--check` sends the request submit sends and gets submit's verdict back, but
writes nothing and asks no confirmation:

```bash
qctl submit rules -f rules.py --check
```

```
Checking rules from rules.py...

Submit would accept this file: 1 added, 0 updated, 0 unchanged, 0 skipped

NAME                        VERSION  ACTION
gross-amount-positive-rule  1.0.0    added

Check only: nothing was submitted.
```

A file submit would refuse exits non-zero with the error submit prints, so
`--check` works as a CI step or a pre-commit hook. `updated in place` means
the release exists as a `draft` or `in_review` revision whose code submit
would rewrite; a rule `--force` would skip is listed as `skipped`, with the
reason.

### What the server refuses

Each refusal names the rule class and exits non-zero; `--check` reports the
same ones.

- **Every `RuleMetadata` keyword must be a literal written inline**: no
  variables, constants or function calls. Otherwise: `<Class>: metadata
  keyword(s) <names> are not literals; declare them inline so they can be
  validated here`. A negative number counts as not a literal.
- **Only allow-listed imports**: `Non-allowlisted module: <name>`. Import
  from `qluster_sdk` and the standard modules the server allows.
- **No `if __name__ == "__main__":` block.**
- **Each rule class is stored on its own.** It may use helper functions and
  constants from the file, but not another rule class in it.
- **Changing code needs a new release once a revision is enabled or
  disabled**: `… cannot be modified. Bump the release to create a new
  revision.` A `draft` or `in_review` revision is updated in place.

## 3. Confirm the rules landed

List the catalog and inspect what you just submitted:

```bash
qctl get rules                                 # rule families (grouped by name)
qctl get rule GrossAmountPositiveRule          # this rule and its revisions
qctl get rule-revisions                        # every revision in the catalog
```

Two handy output formats for a single rule:

```bash
qctl get rule GrossAmountPositiveRule -o code  # the raw Python back
qctl get rule GrossAmountPositiveRule -o yaml  # an apply-compatible manifest
```

## Manage a rule

```bash
qctl enable rule <name>        # set state to enabled
qctl disable rule <name>       # set state to disabled
qctl set-default rule <name>   # mark a revision the default
```

Add `--release <version>` to disambiguate when a rule has multiple releases.

## Editing vs. re-submitting

- To change **mutable** fields, apply a `kind: Rule` YAML manifest with
  `qctl apply -f rule.yaml` — it computes a smart diff and patches only what
  changed. Immutable fields (such as the rule code) are rejected with a hint to
  re-submit.
- To remove rules, pass the Python file to `qctl delete rules -f rules.py`
  (it un-submits the versions found in that file).

## Next step

Submitting only adds a rule to the catalog. To make it run against real data,
attach a revision to a table:

```bash
qctl guide instantiate-rule
```

For the full path from a Python file to a rule enabled on a table — submit,
review, dry run, enable, instantiate — in one place, see
`qctl guide rule-lifecycle`.
