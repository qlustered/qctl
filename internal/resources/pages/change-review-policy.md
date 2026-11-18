---
summary: which roles may resolve, propose and approve each governed change, for the organization or one table
aliases:
kind: ChangeReviewPolicy
schema: manifest:change-review-policy
guide: propose-changes
signature: cec0d0a
---

## USAGE

    qctl apply -f <file.yaml>   # kind: ChangeReviewPolicy
    qctl delete change-review-policy
    qctl get change-review-policy

## EXAMPLES

    # apply a change review policy manifest; spec.dataset_id present = table policy, absent = organization policy
    qctl apply -f change-review-policy.yaml

    # remove one table's override by ID; the table falls back to the organization policy, or to none
    qctl delete change-review-policy --table-id 42

    # same, addressed by table name instead of ID
    qctl delete change-review-policy --table orders

    # the organization policy, one row per key
    qctl get change-review-policy

    # the policy in force for one table, with the keys it does not list
    qctl get change-review-policy --table orders

    # apply-compatible manifest of a table's effective policy
    qctl get change-review-policy --table-id 42 -o yaml

    # every registered key granted to the roles allowed it, as a manifest to edit and apply
    qctl get change-review-policy --template > change-review-policy.yaml

## GOTCHAS

- the policy is a whitelist: while one is in force (the organization or the table has a policy), a key it does not list is denied to every role, for resolve, propose and approve alike. Applying a policy that lists a handful of keys locks everything else, so start from `--template` and narrow it. [backend]
- on a table policy an entry replaces the organization's **whole** entry for that key, not verb by verb; keys the table does not list keep the organization's entry. [backend]
- `apply` replaces the whole policy at its scope: a key dropped from the manifest is dropped from the stored policy, which under a policy in force means denied. [backend]
- `spec.require_distinct_approver` is required on the organization policy, and qctl refuses one without it before sending (exit 2); omitted on a table policy, the table inherits the organization's value. [backend]
- qctl checks operation names, role names, the three verbs and nesting before sending (exit 2); the server checks the rest and its 422 names the dotted path, e.g. `operations.alert_resolve.<issue_type>.<action>.approve cannot hold agent: the key modifies data`. It also refuses an unregistered key, a client-side action, a non-empty `propose` or `approve` on a key that cannot be proposed, and any role the code would not let perform that verb. [backend]
- `get --table ... -o yaml` prints the **effective** policy: applying it back writes every inherited entry and the effective `require_distinct_approver` into the table's override, which then no longer follows later organization changes to those keys.
- `get` exits 3 when there is nothing to show: without a selector when the organization has no policy, with one when no policy is in force for the table (every change is then allowed). `delete` exits 3 when the table has no override.
- `--template` prints YAML unless `-o` is given, and cannot be combined with `--table` or `--table-id` (exit 2). Its `require_distinct_approver` is `false`.
- YAML anchors and aliases in a manifest are expanded when it is read, so the server stores, and `get` prints, every key explicitly, sorted.
- reading a policy needs `change_review:policy:read`, which every role but `external` holds; `apply` and `delete` need `change_review:policy:edit`, held by `admin` and `ops` only. [backend]
- `apply -f <file> -o name` prints `change-review-policy/<policy-uuid>`; `get` and `delete` still select a policy by scope with `--table` or `--table-id`, not by UUID.

## SEE ALSO

- `qctl guide propose-changes`
- `qctl explain change-proposal`
- `qctl get history --model ChangeReviewPolicy`
