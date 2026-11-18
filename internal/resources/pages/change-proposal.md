---
summary: a proposed alert or warning resolution, or rule-parameter edit, waiting for, or past, an approver's decision
aliases:
kind:
schema: openapi:ChangeProposalDetailSchema
guide: propose-changes
signature: e3fe31e
---

## USAGE

    qctl alert propose <alert-id>
    qctl change-proposal approve <proposal-id>
    qctl change-proposal edit <proposal-id>
    qctl change-proposal reject <proposal-id>
    qctl change-proposal request-changes <proposal-id>
    qctl change-proposal rerun-dry-run <proposal-id>
    qctl change-proposal submit <proposal-id>
    qctl change-proposal withdraw <proposal-id>
    qctl describe change-proposal <proposal-id>
    qctl get change-proposals
    qctl table-rule propose <name-or-id>
    qctl warning propose <warning-id>

## EXAMPLES

    # the live proposals on the tables you can see, newest first
    qctl get change-proposals

    # the proposals waiting for a decision you are allowed to make
    qctl get change-proposals --reviewable

    # the ones you wrote or edited, as the raw wire response
    qctl get change-proposals --mine -o json

    # finished proposals on one table (--table is a name, --table-id an ID)
    qctl get change-proposals --table orders --state executed --state execution_failed

    # every proposed warning resolution in any state on table 42
    qctl get change-proposals --table-id 42 --operation warning_resolve --state draft --state pending_review --state changes_requested --state executing --state executed --state execution_failed --state rejected --state withdrawn --state obsolete

    # the next page of a full one
    qctl get change-proposals --page 2 --limit 50

    # one proposal: its target issue now, its payload as a resolution manifest, and the steps you may take
    qctl describe change-proposal 0190bbbb-1111-2222-3333-444444444444

    # the same proposal as the raw API response
    qctl describe change-proposal 0190bbbb-1111-2222-3333-444444444444 -o json

    # propose an alert resolution for an approver instead of applying it
    qctl alert propose 9 --action not_anomaly

    # propose discarding just one file of an alert
    qctl alert propose 9 --action trash_file --file-id 123 --comment "sent twice"

    # write the manifest for an action that needs input, fill it in, then propose it as a draft that also resolves alert 10
    qctl alert propose 9 --action map_value --dry-run
    qctl alert propose -f fix.yaml --draft --also-alert 10

    # propose a warning resolution
    qctl warning propose 55 --action not_anomaly

    # replace the payload after a request for changes and send it back to review in one step
    qctl change-proposal edit 0190bbbb-1111-2222-3333-444444444444 -f fix.yaml --submit

    # keep the payload and only change the alerts it also resolves
    qctl change-proposal edit 0190bbbb-1111-2222-3333-444444444444 --also-alert 10 --also-alert 11

    # empty either or both addressed lists while keeping the payload
    qctl change-proposal edit 0190bbbb-1111-2222-3333-444444444444 --clear-also-alerts --clear-also-warnings

    # propose a resolution that an approver can approve only once another proposal of the table is executed
    qctl alert propose 9 --action not_anomaly --depends-on 0190aaaa-1111-2222-3333-444444444444

    # make an existing proposal wait for another one, or drop that dependency
    qctl change-proposal edit 0190bbbb-1111-2222-3333-444444444444 --depends-on 0190aaaa-1111-2222-3333-444444444444
    qctl change-proposal edit 0190bbbb-1111-2222-3333-444444444444 --remove-depends-on

    # tell the approver why, when proposing
    qctl alert propose 9 --action not_anomaly --note "Broker confirmed these codes are valid"

    # replace the proposer's note, or drop it
    qctl change-proposal edit 0190bbbb-1111-2222-3333-444444444444 --note "Re-checked against the October file"
    qctl change-proposal edit 0190bbbb-1111-2222-3333-444444444444 --remove-note

    # the dependency column, which the default table leaves out
    qctl get change-proposals --columns id,state,target,depends_on

    # send a draft to review, or withdraw a live proposal you wrote
    qctl change-proposal submit 0190bbbb-1111-2222-3333-444444444444
    qctl change-proposal withdraw 0190bbbb-1111-2222-3333-444444444444

    # approve: print the payload and the server's review, confirm, then run the resolution as you
    qctl change-proposal approve 0190bbbb-1111-2222-3333-444444444444

    # approve from a script: no prompt, the executed proposal as JSON on stdout
    qctl change-proposal approve 0190bbbb-1111-2222-3333-444444444444 --yes -o json

    # send it back to its authors, or reject it outright
    qctl change-proposal request-changes 0190bbbb-1111-2222-3333-444444444444 --reason "map 14-20 to 1420, not 142"
    qctl change-proposal reject 0190bbbb-1111-2222-3333-444444444444 --reason "the anomaly is real"

    # propose a rule-parameter change: the edits merge into the rule's live params, and the proposal carries them all
    qctl table-rule propose amount_range --table orders --set-param max=500 --unset-param legacy_max

    # change the params a rule-parameter proposal stores
    qctl change-proposal edit 0190bbbb-1111-2222-3333-444444444444 --set-param max=450

    # stage a new validation dry run of a rule-parameter proposal whose run ended killed, error or stopped
    qctl change-proposal rerun-dry-run 0190bbbb-1111-2222-3333-444444444444

## GOTCHAS

- A `403 PII_ACCESS_REQUIRED` (exit 4) prints commands to request temporary PII access and check the decision, with a reminder to wait for approval before retrying. See `qctl explain alert` for the header and sheet restrictions and the separate refusal of mask-shaped header names.

- You see a proposal only through its table: an ops or admin caller sees every proposal in the organization, any other internal role only the ones on tables assigned to it, and an `external` caller only the ones on cloud sources granted to it. A caller with no table assignments gets an empty list, not an error. [backend]
- `describe change-proposal` exits 3 (404) for an id that is not in the current organization, but exits 4 (403) for a proposal in it whose target issue or rule sits on a table not assigned to you (for an `external` caller and an issue, a cloud source not granted to you), so a 403 here means the proposal exists. [backend]
- `--reviewable` keeps the `pending_review` proposals that pass the approver check: your role holds the operation's permission and may approve every change the proposal carries under the code's rules and the table's change review policy, and, when that policy requires a distinct approver, you authored no version of it. The server applies that check before paging, so every page but the last is full, at the cost of loading every visible `pending_review` proposal that matches the other filters server-side. [backend]
- Reading never turns a proposal obsolete. An alert or warning resolve, and the other user requests that resolve issues on a table (a file ignore, a row delete, a rule, migration or suggestion change, a history undo and others), obsoletes every live, non-executing proposal holding a resolved alert or warning of that table (its target or an addressed issue), not only those holding the issues it resolved; but a draft holds no issue and ingestion and other background jobs resolve issues without that step, so a live proposal can list with its target already resolved or gone. The next edit, submit, withdraw, rerun of its dry run, approve, reject or request-changes on such a proposal turns it obsolete and answers 409 `CHANGE_PROPOSAL_STATE_CONFLICT` (exit 2), and so does any step on a live proposal whose target's issue type is no longer the one it was written for, or whose target rule is deleted or gone; `describe change-proposal` prints a `Note:` line when the target issue is resolved or gone, and its REVIEW reads `obsolete` in all of these cases. [backend]
- A proposal left `executing` past the server's execution timeout is settled only when it is read on its own or a step is attempted on it: `describe change-proposal` (any `-o`) can find it `executed` or `execution_failed` while `get change-proposals` still lists it `executing`. A timed-out execution that failed records `execution_result.partial` as the string `unknown`, not a boolean, alongside `settled_after_timeout: true`, because the server cannot tell whether any of the change was committed. [backend]
- The listing has no total: the response is `{items}` alone, so qctl cannot say how many proposals match. A full page means more may exist, which qctl says on stderr, and the end of the list is the first short page. [backend]
- Only `alert_resolve`, `warning_resolve` and `dataset_rule_patch` (a rule-parameter edit, which `table-rule propose` creates) can be proposed, so `--operation` takes those three and refuses every other change-review operation with exit 2 before any request. [backend]
- The ALLOWED column and NEXT ACTIONS are the server's `allowed_actions`, computed for you from the proposal's state, your role, the table's change review policy, whether you are an author (only an author may withdraw) and, for `rerun_dry_run`, how the proposal's dry run ended, so two people see different lists for one proposal. They do not look at the target, so a listed step can still answer 409 when the target was resolved or deleted since. [backend]
- `get change-proposals` lists only the live states (draft, pending_review, changes_requested, executing) unless you pass `--state`, which is repeatable and replaces that default. `--reviewable` alone needs no `--state`; with a `--state` set that leaves out `pending_review` it exits 2.
- NEXT ACTIONS, and the `Next:` lines every `change-proposal` verb prints after it, run as printed: one per step the server allows you now, then `describe change-proposal`. `approve` carries `--yes` when stdin is not a terminal, since it prompts otherwise.
- `alert propose` and `warning propose` send the body their `resolve` twin would send, built by the same code, and check it the same way first; unlike `alert resolve --action`, `alert propose --action` also refuses an action that needs request fields (exit 2), since nothing would notice the gap until an approver ran it. `--draft`, `--also-alert` or `--also-warning` with `--dry-run` exits 2, and so does `--also-alert` or `--also-warning` with a file of several documents, which makes one proposal per document.
- `alert propose --action trash_file --file-id <id>` stores the file as `stored_item_id` in the proposal payload. The ID must be positive; `--file-id` with an action that does not accept `stored_item_id`, with `-f`, or with `--dry-run` exits 2 before any request. With `-f`, set `spec.stored_item_id` instead. A trash action also needs `--comment` (`spec.comment` with `-f`), stored in the payload as `comment`.
- `table-rule propose` reads the rule's live params once, applies `--set-param` and `--unset-param` to them, and proposes the complete result as the payload `{params: ...}`: approving replaces the rule's params as a whole, as `apply -f` with `spec.params` does. A `--set-param` value is JSON when it parses as one (`5`, `true`) and a plain string otherwise, so quote a string that looks like JSON (`--set-param mode='"5"'`). Neither flag, a `--unset-param` key the rule lacks, or a key both set and unset exits 2 before the proposal is created; an edit that leaves the params as they are is sent, and qctl says the params are unchanged. It has no `--dry-run`; `--draft`, `--also-alert`, `--also-warning` and `--depends-on` work as on `alert propose`.
- `change-proposal edit` reads the proposal first and sends the version it read. With `-f` the document must be for the proposal's own operation and target issue, or it exits 2 before the edit; with neither `-f` nor `--action` it sends no payload, and the server keeps the stored one. On a `dataset_rule_patch` proposal `-f` and `--action` exit 2, and `--set-param` and `--unset-param` edit the params the proposal stores, not the rule's live ones (on any other proposal they exit 2); when its REVIEW carries `rule_params_changed`, qctl adds a `Note:` that the edit did not merge the rule's newer params. `--also-alert` and `--also-warning` each replace the list they name; `--clear-also-alerts` and `--clear-also-warnings` empty the matching list. Each clear flag conflicts with its matching replacement flag (exit 2 before any request); a list that is neither replaced nor cleared is kept. `--depends-on` replaces the dependency and `--remove-depends-on` drops it; an edit with neither keeps it. `--note` and `--remove-note` work the same way on the proposer's note. A blank `--note`, after trimming, exits 2 on every command that takes it, and so does `--note` under `--dry-run`.
- `change-proposal edit --set-param` or `--unset-param` on a proposal whose response declares a non-empty `masked_fields` exits 2 with no PATCH, because the stored params they merge into are mask text. Every other edit works for that viewer: it sends no payload, or a complete replacement with `-f` or `--action` on an alert or warning proposal. An omitted, null or empty `masked_fields` counts as unmasked; qctl does not infer masking from a value's text.
- A viewer who sees the table's PII masked gets a proposal's values for those columns masked: an alert or warning proposal's `new_value`, `another_field_new_value` and `field_name_to_value` entries, and a `dataset_rule_patch` proposal's params and base params when its rule maps or filters on a masked column (any masked column for a custom rule). `masked_fields` names the columns. For that viewer `review.impact_digest` and `impact_diff` are null, `allowed_actions` omits `approve` and `request_changes`, and the server refuses both with a plain 403; reject and withdraw stay allowed. `approve` exits 4 before any POST and names the masked fields, and `describe` prints the digest and the change since submit as withheld. [backend]
- A proposal's version moves on every write to it, state changes included, so an edit that crossed a submit, a request for changes or another person's edit is refused with 409 `CHANGE_PROPOSAL_VERSION_MISMATCH` (exit 2): re-read it with `describe change-proposal` and edit again. Anyone allowed to propose the new payload may edit a draft, `pending_review` or `changes_requested` proposal, not only its authors, and becomes an author — except that, when the table's policy requires a distinct approver, a non-author whose role may decide the proposal is refused editing or submitting a `pending_review` or `changes_requested` one with 403 `CHANGE_REVIEW_POLICY_DENIED` (exit 4) and must request changes instead; only an author may withdraw one (anyone else gets 403, exit 4). [backend]
- Submitting a `dataset_rule_patch` proposal (`table-rule propose` without `--draft`, `submit`, `edit --submit`), and every edit that leaves one `pending_review`, records the rule's live params as its base params and stages a validation dry run of the live rule against the proposed params, killing the proposal's previous run; that step is refused with 429 over the dry-run budget (exit 1) or 409 when the table has no active data source (exit 2). `approve` is blocked with `dry_run_not_finished` until the run is `finished`, and with `dry_run_rule_errors` when it recorded rule errors. Once the rule's params differ from the base params, REVIEW reads `stale` with `rule_params_changed` and `approve` is refused with `CHANGE_PROPOSAL_STALE`; an edit that leaves it `pending_review` clears that by re-recording the base params, without merging the rule's newer params into the proposal, so approving still replaces them all. A params patch equal to the rule's live params is accepted. [backend]
- `change-proposal rerun-dry-run` stages a new validation dry run of a `pending_review` or `changes_requested` `dataset_rule_patch` proposal, killing its previous run, as you when you are an author and as its creator otherwise; only an author or an approver may (403, exit 4). The server takes a proposal with no run, one whose run ended `killed`, `error` or `stopped`, or one whose REVIEW carries `rule_configuration_changed` (the rule or the stored form of the proposed params moved since the run), and refuses any other with 409 `CHANGE_PROPOSAL_STATE_CONFLICT` (exit 2). `allowed_actions` lists `rerun_dry_run` only when the run ended `killed`, `error` or `stopped`, so the other two cases have no NEXT ACTIONS line but still run. An alert or warning proposal has no dry run (422, exit 2); the dry-run budget and a table with no active data source refuse it as they refuse a submit. [backend]
- A `dataset_rule_patch` proposal's params come in two units. Its payload (what `table-rule propose` and `edit --set-param` write, and PAYLOAD prints) is in the units `get table-rule -o yaml` shows, while its base params, REVIEW's params change, and the rule's params on `approve`'s `Target rule/<uuid> now:` line and in `execution_result.target_state` are in the units the rule stores. The two differ only for a `range_rule`'s `min` and `max` on a column the table formats as a percent, where the stored value is the shown one divided by 100: PAYLOAD's `max: 12` is REVIEW's `0.12`. [backend]
- A draft holds no issue; a `pending_review` or `changes_requested` proposal holds its target issue and every addressed issue (a `dataset_rule_patch` proposal, whose target is a rule, holds only the addressed ones), and an executed proposal whose change was staged as a rule suggestion (a params patch, or an alert or warning resolve's new keyword) keeps holding them until the suggestion is applied or dismissed. A step that leaves a proposal holding issues (creating it with submit, submitting it, or editing it) is refused with 409 `CHANGE_PROPOSAL_ACTIVE` (exit 2) when another proposal holds one of them, and qctl prints a `describe change-proposal` line for each holder. An addressed issue must be an unresolved issue of the target's table that you can see (for a `dataset_rule_patch` proposal, raised by the target rule and, for a warning, not on a live clean row), or the request is a 422 naming it. [backend]
- `change-proposal approve` runs the proposed change as you, the approver, not as its author: your role is checked against the table's change review policy under the approve verb, your access to the target is checked as a resolve of the issue or a PATCH of the rule would check it, an action that also resolves the same issue elsewhere reaches other cloud sources unless you are an `external` user, and the history items it writes carry the proposal's id in `change_proposal_id`. A `dataset_rule_patch` approval may stage the change as a rule suggestion instead of applying it, and an applied one may also record a new approval of the table's series rules by you, which `approve_reapproves_series_rules` predicts. [backend]
- Approving, rejecting and requesting changes need a role the table's change review policy lets approve every change the proposal carries; when the policy requires a distinct approver, anyone who wrote or edited any version of the proposal is refused as well. Both refusals are 403 `CHANGE_REVIEW_POLICY_DENIED` (exit 4); a role without the operation's permission at all, or without the right a choice in an alert payload needs (a new field, a `dataset`-scope mapping, key columns, rule suggestion decisions), gets a plain 403. [backend]
- `approve` sends the version and the `review.impact_digest` it read, and the server reviews the proposal again before anything runs: a moved version is 409 `CHANGE_PROPOSAL_VERSION_MISMATCH`, a stale review `CHANGE_PROPOSAL_STALE`, a different digest `CHANGE_PROPOSAL_IMPACT_CHANGED`, and a review with blockers `CHANGE_PROPOSAL_BLOCKED` (each exit 2, nothing run, qctl prints the fresh reasons or blockers), while an obsolete one is stored obsolete with `CHANGE_PROPOSAL_STATE_CONFLICT`. The digest covers the review's bound impact: for an alert or warning proposal the operation, target, issue type, payload, addressed issues, each policy key's catalog description and any field-mapping, key-column or suggestion decisions; for a `dataset_rule_patch` proposal the payload, base params and their diff, the series rule approval status, the dry run and its rule errors, and the addressed issues. It leaves out the linked files, the same-issue sweep and the dry run's progress, so those can change between your read and the approval without a refusal. [backend]
- An `apply_migration` proposal carries the table's key choice in its payload — `alert propose --key-columns a,b` or `--no-key-columns`, or `spec.key_columns` / `spec.no_key_columns: true` with `-f` — and `alert propose` checks it against the table's key step as `alert resolve` does (`qctl explain alert`), exit 2 before creating anything. A key choice the step refuses later, or that slipped past a failed key-step read, makes the review `stale` with the finding `key_columns_refused`, and `approve` answers `409 CHANGE_PROPOSAL_STALE`. While the table is at its key step the review binds `key_columns` (`current`, `new`, `no_key_columns`), which `describe change-proposal` prints as a `Key columns:` line, and its resource keys include `dataset:<id>/key_columns`. [backend]
- An `apply_migration` proposal that explicitly accepts a carried rule suggestion whose columns do not fit the draft migration makes REVIEW `stale` with `suggestion_misfit_accepted`; `approve` answers `409 CHANGE_PROPOSAL_STALE` (exit 2). The server checks fit without suggestions named in `spec.dismiss_suggestion_ids`, so dismissing a producer can make a dependent carried suggestion unfit. Edit the proposal to remove the misfit from `spec.accept_suggestion_ids` before submitting again. [backend]
- When the resolution fails before it commits anything, `approve` answers with the resolution's own error (a 409 `PERIOD_LOCKED`, a 422, a 403) and the proposal is back in `pending_review` with the approval cleared and its version moved, so another approval must read it again, which `approve` always does. When it fails after committing part of its change, the proposal ends `execution_failed` with `execution_result.partial: true` and the committed part stays. [backend]
- `approve` exits 0 when the proposal ends `executed`, 1 when it ends `execution_failed` (after printing the error and a `qctl get history --table-id` line), 2 for every `CHANGE_PROPOSAL_*` 409 (printing the proposal's fresh state, version and your next steps), and the relayed error's own code otherwise, with a line from a re-read saying whether the proposal is still `pending_review`. It never retries. Its preview goes to stderr, next to the prompt. A proposal in any state but `pending_review` is sent without the preview or the prompt, and the server's 409 is the answer; a `pending_review` proposal the server sends no review for (a server older than impact digests) exits 2 without approving.
- An executed `dataset_rule_patch` proposal prints its outcome instead of the response, on `approve` and in `describe change-proposal`'s EXECUTION: `applied` (the rule has the proposed params) or `staged` (the PATCH became a rule suggestion, which applies once its suggestion is accepted or applied with a draft migration, or its revalidation candidate is promoted, named with a `qctl describe rule-suggestion <id> --table-id <n>` line); `staged_as` when recorded (draft migration apply or revalidation candidate promotion); the suggestion's `application`, `pending` until it is applied (`applied`) or dismissed or otherwise not applied (`dismissed`); whether the approval re-recorded the table series' rule set approval (its id) or why not (`staged`, `not_in_series`, `approver_role`, `approver_table_access`, `no_approval`, `approved_params_differ`, `rule_set_differs`); and the rule's state and params. `-o json` and `-o yaml` carry the whole `execution_result`, its response included. There is no series line when the execution was settled after the server's execution timeout, or when recording the series approval failed. [backend]
- An approved `validation_add_a_new_keyword` resolution can leave the alert or warning open while its keyword change is staged: `state: executed` does not mean the keyword is live. For a staged result carrying `staged_as`, `approve` and `describe change-proposal` explain whether the change waits on the table's draft migration (`draft_migration`) or validation and promotion (`revalidation_candidate`), and whether its `application` is pending, applied or dismissed. A candidate cannot be manually accepted. `approve` uses the recorded kind without reading the suggestion and names `qctl describe rule-suggestion <id> --table-id <n>`. Older executions have no backfill; the field is also absent when the suggestion is gone. Without it, `describe` makes no staging-kind claim, and `approve` falls back to reading the suggestion and issue for its keyword guidance: a failed suggestion read names the describe command, and a failed issue read reports uncertainty. Applying the suggestion resolves the issue when the keyword is the issue's own value and the rule then allows it, and a `new_value` naming a different keyword leaves it open: once the keyword is live, re-read the issue and, if still open, use `qctl alert resolve -f <file.yaml>` or `qctl warning resolve -f <file.yaml>` with the same PAYLOAD shown in the approval preview, including `new_value`. JSON/YAML keep the raw result; approval guidance goes to stderr under `-o json|yaml|name`. [backend]
- A `delete_*`, `discard_*` or file-ignore action in the payload makes `approve` subject to the protected-context gate, as `alert resolve` is for that action: on a protected context it must be named with `--context`, and `--yes` does not count. Other actions only prompt. `reject` and `request-changes` need a non-blank `--reason` (exit 2 before any request) and never prompt.
- The proposer's note is stored with the proposal, separate from the approver's `decision_reason`, and `describe change-proposal` and `change-proposal approve` print it as `Proposer note:`. Only creating and editing a proposal write it: an edit that sends a note replaces it, one that sends null clears it, and one that leaves the key out keeps it, while submit, withdraw and the decisions never touch it. An edit that changes only the note still moves the version, makes you an author and, on a `pending_review` proposal, re-notifies its reviewers. [backend]
- `describe change-proposal`'s `Authors:` line follows an author with `(via session <name>)` for each CLI session (delegation) through which they created the proposal, wrote its payload or note, or became an author, first used first, by its id when the session has no name; the server reads them from the proposal's history, which records a session for every bearer-token request — qctl's own included — and none for a cookie session (the web app), so an author with no `via` wrote only through cookie sessions, and a revoked session is still named. `-o json|yaml` carry them as `authors[].delegations`. [backend]
- `--depends-on` must name a draft, `pending_review` or `changes_requested` proposal of the same table that you can see and that does not already depend on this one, directly or through others; anything else is a 422 (exit 2), as is any dependency for a proposal with no table. Approving the dependent proposal is refused until its dependency is `executed` or `obsolete`: while the dependency is in any other live state REVIEW lists a `dependency_not_executed` blocker (409 `CHANGE_PROPOSAL_BLOCKED`), and once it is `rejected`, `withdrawn` or `execution_failed` the review reads `stale` with a `dependency_failed` reason (409 `CHANGE_PROPOSAL_STALE`) until an edit drops or replaces it. [backend]
- OVERLAPS lists the other draft, `pending_review` and `changes_requested` proposals of the same table that you can see and that share at least one of REVIEW's resource keys, oldest first, each with the keys it shares. The server computes it only alongside a review, so a proposal in any other state shows none. An overlap refuses nothing: two overlapping proposals can both be approved, in either order, unless one depends on the other. [backend]
- Proposal ids are full UUIDs: a malformed `<proposal-id>` exits 2 before any credential is read or request is made.
- On `get change-proposals`, `--table` is a name and `--table-id` an ID; neither accepts the other, passing both is an error, and there is no `--series`.
- People are named through the current org's user directory in the table and in the text payload; when it cannot be read qctl warns once and prints UUIDs. `-o json` and `-o yaml` skip that lookup.
- PAYLOAD shows the stored payload as the `AlertResolution` or `WarningResolution` manifest it maps onto, and a `dataset_rule_patch` proposal's as the params it would patch the rule with; a payload that does not map, or whose target issue was deleted, is printed as the raw body, with the reason. REVIEW, which `approve` prints too, is the server's review computed for you as of the read: for an alert or warning proposal its blockers are the refusals a resolve by you would raise; for a `dataset_rule_patch` proposal it shows the params change from the rule's live params, the base params, the series rule approval and the validation dry run.

## SEE ALSO

- `qctl explain alert`
- `qctl explain warning`
- `qctl explain rule-review`
- `qctl explain table-rule`
- `qctl guide propose-changes`
