---
summary: which tables still run an older revision of a rule, and re-notifying about it
aliases: [rule-upgrades]
kind:
schema: openapi:RuleFamilyUpgradeDetailsSchema
guide: rule-lifecycle
signature: dfe0a07
---

## USAGE

    qctl get rule-upgrades <rule-name-or-id>
    qctl rule-upgrade notify <rule-name-or-id>

## EXAMPLES

    # which tables still run an older revision of phone_format
    qctl get rule-upgrades phone_format

    # the same, with the ids you act on
    qctl get rule-upgrades phone_format --columns table,table_id,rule,id,on

    # the raw object — the only view with new_default_revision_id
    qctl get rule-upgrades phone_format -o json

    # table-rule/<id> for each rule instance, ready to pipe into describe table-rule
    qctl get rule-upgrades phone_format -o name

    # re-send the upgrade notice to everyone who dismissed it
    qctl rule-upgrade notify phone_format

    # say which release explicitly, and replace the notice text
    qctl rule-upgrade notify phone_format --release 3.0.0 --message "please upgrade before Friday"

    # scripted, no prompt
    qctl rule-upgrade notify phone_format --yes

## GOTCHAS

- the listing is scoped to YOU, not to the org: you see rules you created or last edited, plus rules on tables you hold access to if you are an editor or admin, so two people can legitimately get different answers for the same rule. [backend]
- only ENABLED rule instances are listed, so an instance sitting in error, disabled or pending_validation on the old revision does not appear even though it is outdated. [backend]
- the read is gated on the ALERT_LIST permission, not RULE_LIST, so a 403 here is about your alert access rather than your rule access — today every role that can list rules also holds ALERT_LIST, so the two only come apart if that map changes. [backend]
- upgrading is not done from here — instantiate the new revision with a TableRule manifest whose `spec.upgradedFrom` is the old instance's `id`, rather than disabling the old instance yourself; `qctl explain table-rule.spec.upgradedFrom` says when the old one is retired.
- the positional argument is a rule NAME, a rule-revision short ID or a rule-revision UUID — the same vocabulary `enable rule` takes — and a rule FAMILY uuid, such as the `family_id` that `get rules -o json` carries, is not accepted.
- upgrade_to repeats the family's new default release on every row on purpose, so a single row is self-describing and a --columns slice keeps it.
- `notify` addresses a FAMILY, so naming one of its non-default revisions by id lands on the default anyway and says so on stderr — while `--release` naming that same non-default release exits 2, because there the release is what you asked for rather than how you found the family.
- `notify --release ""` exits 2 rather than falling back to the default, so a script whose release variable never expanded stops instead of re-notifying for a release it did not name.
- `notify` reaches only users who DISMISSED the notice; someone who has not read it yet is untouched, because they already have it. [backend]
- `notify` clears BOTH dismissed and read, so the notice comes back as unread, and its COUNT in `qctl get notifications` goes up by one. [backend]
- `notify` works only on the family's DEFAULT revision, and the backend reports any other as a 404 — qctl rejects it client-side as exit 2 instead, so a live revision is never reported as missing. [backend]
- only the user who CREATED the revision may `notify`, but a 403 is not proof of it: the route checks RULE_INSTANTIATE and org access first, so qctl calls the creator rule the usual cause rather than the cause. [backend]
- `notify` with no --message leaves each notice's existing text alone; --message replaces it on every notice it re-sends. [backend]
- a second `notify` straight after the first reactivates 0, because the first one un-dismissed them all — that is a no-op and exits 0, not a failure. [backend]
- `notify` prints the server's `{message, reactivated_count}` under `-o json` and `-o yaml` and moves its prose to stderr; `-o name` exits 2.

## SEE ALSO

- `qctl explain table-rule`
- `qctl explain rule-revision`
- `qctl explain notification`
- `qctl guide rule-lifecycle`
- `qctl guide instantiate-rule`
