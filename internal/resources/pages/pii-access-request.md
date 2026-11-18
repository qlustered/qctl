---
summary: request temporary unmasked access to a table's personal data, or decide a request
aliases:
kind:
schema: openapi:PiiAccessRequestSchema
guide: inspect-data
signature: c3d6afb
---

## USAGE

    qctl get pii-access-requests [table]
    qctl pii-access-request approve <request-id>
    qctl pii-access-request deny <request-id> --reason <reason>
    qctl pii-access-request request --reason <reason>
    qctl pii-access-request withdraw <request-id>

## EXAMPLES

    # ask the table's admins for an hour of unmasked access
    qctl pii-access-request request --table orders --duration 1h --reason "reconcile refunds" --yes

    # poll your requests for their state, denial reason and grant expiry
    qctl get pii-access-requests orders -o json

    # ask about a table shared from another organization (ask is an alias of request)
    qctl pii-access-request ask --shared --table-id 42 --duration 8h --reason "audit" --yes

    # keep the shared provider when checking the decision
    qctl get pii-access-requests --shared --table-id 42 --columns id,state,deny_reason,grant_expires

    # withdraw your own open request
    qctl pii-access-request withdraw pii-access-request/7 --table-id 42 --yes

    # as an admin, every requester's requests on the table, with the grant an approval created
    qctl get pii-access-requests orders --all

    # approve for the requested duration, or shorten it
    qctl pii-access-request approve 7 --table orders --yes
    qctl pii-access-request approve pii-access-request/7 --table-id 42 --duration 1h --reason "INC-4411" --yes

    # deny with a reason the requester sees
    qctl pii-access-request deny 7 --table orders --reason "use the masked export" --yes

## GOTCHAS

- Requests are for your own user, including the agent role. The requester routes require dataset:data:read; use `--shared` for a table shared from another organization. [backend]
- The server refuses a new request with 400 (exit 2) if you already see the table's personal data unmasked, are not an eligible grantee, or the table has no personal-data columns; an existing open request yields 409 (exit 2), and a denial within the past 24 hours yields 429 (exit 1) with Retry-After. [backend]
- Without `--all`, listings show only your own requests, newest first. State is open, approved, denied, withdrawn or expired; an undecided request expires after 168 hours. An approved request remains approved after its grant expires: `grant_expires_at` is the grant's expiry, not proof of current access. [backend]
- Withdrawal is refused with 404 (exit 3) for an absent request or one belonging to another user or table, and with 409 (exit 2) once decided or expired. [backend]
- `--all` lists every requester's requests on the table, newest first, with the requester, who decided and the grant an approval created. It needs the pii:grant permission, which only the admin and ops roles hold (a 403, exit 4, otherwise), and only exists for your own organization's tables, so qctl refuses it with `--shared` (exit 2). [backend]
- Approving creates a pii-grant for the requester, listed by `get pii-grants`, for the requested duration or a shorter `--duration`; a longer one is refused with 400 (exit 2). The grant's reason is the request's own unless `--reason` replaces it. [backend]
- Approval is also refused with 400 (exit 2) for your own request and when the requester can no longer be granted access: they now see the personal data unmasked, or can no longer read the table. Approve and deny answer an unknown request id, or one on another table, with 404 (exit 3), and one already decided or expired with 409 (exit 2). [backend]
- A denial's reason is the requester's `deny_reason`, and starts their 24-hour wait before they can ask again on that table. Neither decision can be undone: revoke an approval's grant with `pii-grant revoke`. [backend]
- `approve` and `deny` take the table from `--table`/`--table-id`; `deny` requires `--reason`, and both check the reason like `request` does. Both prompt unless `--yes`; `deny` also honors protected contexts, `approve` does not, like `pii-grant create`.
- The requested duration defaults to 1h; accepted values are 1h, 8h, 24h and 7d. `--reason` must be nonblank and at most 4000 characters. Both mutations prompt unless `--yes`; withdrawal also honors protected contexts.
- Check status by repeating `get pii-access-requests` with the same table and provider. `--page` and `--limit` select a page (50 requests by default, at most 100). JSON/YAML emit a List of request records; mutations emit one request record. `-o name` prints `pii-access-request/<id>`.

## SEE ALSO

- `qctl explain pii-grant`
- `qctl explain data`
- `qctl guide inspect-data`
