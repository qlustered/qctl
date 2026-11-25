---
summary: how many approvals a rule needs before it can go live
aliases:
kind: RuleReviewPolicy
schema: manifest:rule-review-policy
guide: request-review
signature: 04a3082
---

## USAGE

    qctl apply -f <file.yaml>   # kind: RuleReviewPolicy
    qctl delete rule-review-policy
    qctl get rule-review-policy

## EXAMPLES

    # apply a rule review policy manifest through the generic dispatcher; spec.dataset_id present = table override, absent = org policy
    qctl apply -f rule-review-policy.yaml

    # remove one table's override by ID; the table falls back to the org policy, or the system default if there is none
    qctl delete rule-review-policy --table-id 42

    # same, addressed by table name instead of ID
    qctl delete rule-review-policy --table orders

    # the effective org-level policy (no table override to check)
    qctl get rule-review-policy

    # the effective policy for one table: its own override, or the org policy if it has none
    qctl get rule-review-policy --table orders

    # apply-compatible manifest, for editing and re-applying
    qctl get rule-review-policy --table-id 42 -o yaml

## GOTCHAS

- `apply -f <file> -o name` prints `rule-review-policy/<policy-uuid>` for the saved policy; `get` and `delete` still select policies by scope with `--table` or `--table-id`, rather than by policy UUID. Several applied documents print one reference per line in file order; prose goes to stderr.

- Integer fields in a YAML manifest reject fractional numbers during parsing (exit 2): `2.7` is refused instead of becoming `2`. Whole-valued YAML numbers such as `2.0` remain accepted.
- `--table` is a name and `--table-id` is an ID; neither accepts the other. A table whose name is a number (`42`) is reachable with `--table 42`, which older builds resolved as ID 42.
- the table selector is optional on `get` (omit it for the organization policy, set it for the effective policy at that table) but **required** on `delete`, which refuses before sending anything: only a table-level override can be deleted.
- a table-level override must be at least as **strict** as the org policy on every field, or the apply is rejected 422 — you can tighten a table (more reviewers, dry-run required) but never loosen one below the org setting; nothing in `--help` or the schema says so, and it is the first wall most overrides hit. [backend]
- `-o yaml` prints an apply-compatible manifest, so read (`get -o yaml`) → edit → `qctl apply -f` is a round trip.
- `delete rule-review-policy` removes only the **table-level override**, never the organization policy — the table then inherits the org policy or, absent one, the system default (`requires_review: false`), so deleting an override can leave that table ungated rather than reverting to something equally strict. [backend]

## SEE ALSO

- `qctl explain data-review-policy`
- `qctl guide review-rules`
