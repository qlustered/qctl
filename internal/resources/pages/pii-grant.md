---
summary: one user's temporary unmasked access to a table's personal data
aliases:
kind:
schema: openapi:PiiGrantSchema
guide:
signature: 703c343
---

## USAGE

    qctl get pii-grants [table]
    qctl pii-grant create --to <email> --reason <reason>
    qctl pii-grant revoke <grant-id>

## EXAMPLES

    # every grant on the table, active, expired and revoked, newest first
    qctl get pii-grants orders

    # who revoked what, and why
    qctl get pii-grants --table-id 42 --columns id,state,grantee,revoke_reason,revoke_note

    # the grants as the server sent them
    qctl get pii-grants orders -o json

    # let a table member see the table's personal data unmasked for an hour (the default)
    qctl pii-grant create --table orders --to alice@example.com --reason "INC-4411: reconcile refunds"

    # name the user by UUID, for longer, without a prompt
    qctl pii-grant create --table-id 42 --to-id 11111111-2222-3333-4444-555555555555 --duration 8h --reason "audit" --yes

    # end a grant early; the reason defaults to no_longer_needed
    qctl pii-grant revoke 7 --table orders

    # record why it ended, with a note (required when the reason is other)
    qctl pii-grant revoke pii-grant/7 --table-id 42 --reason compromised --note "laptop lost" --yes

## GOTCHAS

- Every route needs the pii:grant permission, which only the admin and ops roles hold; anyone else gets a 403 (exit 4) carrying the server's message. [backend]
- Only a user who does not already see personal data unmasked can be granted access. In the table's own organization the member, editor and external roles already do, as do admin and ops, so a grant there goes to a read_only or agent member; a grant to anyone already unmasked is refused with a 400 (exit 2). [backend]
- `pii-grant create` is also refused with a 400 (exit 2) for yourself, and for a user who cannot read the table: neither a member of this organization holding a grant on the table through it, nor a non-external member of another organization the table is shared through. [backend]
- `--to` looks the email up in the table's member listing (`qctl get table-members`), which shows only this organization's members. A user who reads the table through a share from another organization is not there, so name them with `--to-id <uuid>`.
- A grant cannot be extended or edited. Granting the same user again adds a second, independent grant; the server does not refuse it, and each one must be revoked on its own. [backend]
- The duration runs from the moment the server records the grant: exactly 1h, 8h, 24h or 7d, nothing longer. [backend]
- STATE is computed when the server answers, not stored: revoked once revoked, otherwise expired once its expiry has passed, otherwise active. A revoked grant stays revoked after its expiry passes. [backend]
- `pii-grant revoke` answers an unknown grant id, or one on another table, with a 404 (exit 3), and a grant that has already expired or been revoked with a 409 (exit 2). [backend]
- `--reason other` needs a `--note`; qctl refuses it without one (exit 2) before any request. A blank note is sent as no note.
- Both mutations prompt unless `--yes`, and need `--yes` when stdin is not a terminal. `pii-grant revoke` also honors protected contexts, so `--yes` does not replace `--context`; `pii-grant create` does not, like `share table`.
- `get pii-grants` lists up to 100 grants a page (50 by default). `-o json|yaml` emit a List of the grant records; `create` and `revoke` emit the grant itself, and `-o name` prints `pii-grant/<id>`, which `pii-grant revoke` takes back.

## SEE ALSO

- `qctl explain table-member`
- `qctl explain table`
- `qctl explain pii-access-request`
