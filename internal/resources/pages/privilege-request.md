---
summary: a break-glass request to grant or revoke a user's ops eligibility (is_ops)
aliases:
kind:
schema: openapi:PrivilegeRequestDetailSchema
guide:
signature: 1dc4b74
---

## USAGE

    qctl describe privilege-request <request-id>
    qctl get privilege-audit
    qctl get privilege-requests
    qctl privilege approve <request-id>
    qctl privilege reject <request-id>
    qctl privilege request --action <action> --target-user <target-user>

## EXAMPLES

    # every request on the platform, newest first
    qctl get privilege-requests

    # only the ones still waiting for a decision (the status matches in any case)
    qctl get privilege-requests --status pending

    # every request that targets one user, as the raw wire response
    qctl get privilege-requests --target-user tom@example.com -o json

    # one request in full: the people on it, its justification, and its audit rows
    qctl describe privilege-request 0190bbbb-1111-2222-3333-444444444444

    # the same request as the raw API response
    qctl describe privilege-request 0190bbbb-1111-2222-3333-444444444444 -o json

    # the ledger of every applied change, newest first
    qctl get privilege-audit

    # every change applied to one user
    qctl get privilege-audit --target-user tom@example.com

    # the audit row one request wrote, in the stable machine-readable form
    qctl get privilege-audit --request-id 0190bbbb-1111-2222-3333-444444444444 -o json

    # ask for another user to be made an ops user (the action matches in any case)
    qctl privilege request --target-user tom@example.com --action escalate --justification "INC-4411: on-call needs ops access to replay the stuck ingestion queue"

    # revoke it, reading the justification from stdin
    qctl privilege request --target-user 0190cccc-1111-2222-3333-444444444444 --action demote --justification-file -

    # print only privilege-request/<id>, to capture with id=$(...) and hand to an approver
    qctl privilege request --target-user tom@example.com --action escalate --justification-file why.md -o name

    # approve it as a second ops user: qctl shows the request and asks before it acts
    qctl privilege approve 0190bbbb-1111-2222-3333-444444444444

    # approve from a script or an agent, which has no terminal to answer the prompt
    qctl privilege approve 0190bbbb-1111-2222-3333-444444444444 --yes

    # check that the approval was applied: the audit row it wrote
    qctl get privilege-audit --request-id 0190bbbb-1111-2222-3333-444444444444

    # reject a pending request, or withdraw one you made
    qctl privilege reject 0190bbbb-1111-2222-3333-444444444444

## GOTCHAS

- Every route behind these commands requires the ops role: an admin or editor gets a 403 (exit 4) carrying the server's message, and is_ops on your user is an eligibility flag that grants nothing on its own. [backend]
- The request listing and the audit are platform-wide: they return every request and every applied change on the platform, whichever organization the current context points at. The org only decides where your ops role is checked. [backend]
- A Pending request is not necessarily approvable. The server checks a request's age against a TTL the deployment sets (24 hours by default) only when someone tries to approve it, and only then marks it Expired, so a days-old request still lists as Pending. The TTL is not on the wire and qctl never computes one. [backend]
- The audit has a row only for an APPLIED change -- an approval, a request the server applied at once because its requestor was the sole eligible ops user, or a change the server applied by itself during an identity-provider ops bootstrap or user deletion. A rejection or an expiry leaves no row, so `describe privilege-request` shows an empty AUDIT section for both. [backend]
- The server also files and decides requests by itself. When identity-provider reconciliation finds no eligible ops user and grants a configured bootstrap user eligibility, or the identity provider deletes a user who is ops, the server rejects every Pending request for that user and records the change as an already Approved request with its audit row. The HQ org's automation user (`automation+<org-id>@system.qluster.internal`) is the requestor and approver of that request, and the approver of those rejections. So a request can turn Rejected with nobody running `privilege reject`, and an audit row whose requestor and approver are the same user is not a two-person-rule breach. [backend]
- A rejected request's approver is the user who rejected it: the server records both decisions in the one approver field, so the listing's APPROVER column names the rejector too, and `describe privilege-request` labels that person "Rejected by". An expired request has a decision time and no approver. [backend]
- Audit rows name every person by bare UUID. The `get privilege-audit` table names them through the current org's user directory and falls back to the UUIDs, after one warning, when that directory cannot be read. `-o json` and `-o yaml` skip that lookup, and `-o name` exits 2, since an audit entry's id is no request's; only a name-typed `--target-user` still reads the directory, once, to resolve itself. [backend]
- `privilege request` can come back already applied: when you are the only eligible ops user, the server approves your request in the same call, and the result line reads "created and applied" instead of waiting for an approver. [backend]
- `privilege request` is refused with a 400 (exit 2), not a 404, when you target yourself, when the target does not exist, when an escalation target is already ops or is not an active admin of the HQ org, when a demotion target is not ops, and when the target already has a Pending request -- that message names the existing request's id. [backend]
- The justification must be 10 to 4000 characters. qctl leaves that check to the server, whose 422 (exit 2) names the bound. [backend]
- `privilege approve` does more than flip is_ops. The server writes the audit row first. An escalation then makes the target's HQ membership an ops membership and deactivates their standing memberships in customer orgs, so they reach those orgs only by switching in. A demotion deactivates the memberships they made by switching in and their HQ membership, and revokes all of their sessions. Either way, each customer-org membership it deactivates also loses the table grants given through that organization and, when it was external, its cloud-source assignments. [backend]
- `privilege approve` keeps the two-person rule: approving a request you made, or one that targets you, is a 403 (exit 4). `privilege reject` refuses only the target: rejecting a request that targets you is a 403 (exit 4) too, while a requestor can reject, and so withdraw, their own request. Both refusals share exit 4 with lacking the ops role, so read the message. [backend]
- Approving a request past its TTL fails with a 400 (exit 2) and still changes it: that failed call is what marks the request Expired, so the next `privilege approve` of it is refused locally as already Expired. `privilege reject` never checks the age, so a stale request can still be rejected until an approval attempt marks it Expired. [backend]
- `privilege approve` and `privilege reject` read the request before deciding it, because the decision routes answer an unknown id with a 400, not a 404. Through qctl an unknown id exits 3, and a request that is no longer Pending is refused at exit 2 without a decision being sent. [backend]
- `privilege reject` has no `--reason`: the server accepts a rejection reason and discards it, and a rejection writes no audit row, so nothing records why a request was rejected. [backend]
- `privilege approve` asks for confirmation, so a script or an agent must pass `--yes`. Without a terminal it exits 2 after reading the request, and approves nothing.
- `-o json` on `get privilege-audit` is the wire schema verbatim and the stable, machine-readable contract. The table columns are presentation and may change.
- `--target-user` resolves an email or a "First Last" name against the CURRENT org's members only, so from a customer org pass the UUID. A UUID is sent as typed, with no directory request and no membership check.
- Request ids are full UUIDs: a malformed `<request-id>` or `--request-id` exits 2 before any credential is read or request is made. The detail route answers an unknown id with 404, so `describe privilege-request` exits 3 for it. [backend]

## SEE ALSO

- `qctl explain session`
- `qctl explain org`
