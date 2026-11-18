---
summary: a user's grant and effective level on one table
aliases: [table-members]
kind:
schema: openapi:DataSetMemberSchema
guide: authenticate
signature: ad301d9
---

## USAGE

    qctl get table-members [table]
    qctl table-member leave [table]
    qctl table-member remove <user-id>
    qctl table-member set-permission <user-id> --permission <permission>

## EXAMPLES

    # list grants in email order, with role, granted and effective levels
    qctl get table-members orders

    # machine-readable member records, including full user UUIDs
    qctl get table-members --table-id 12 -o json

    # grant new access; an existing grant keeps its level
    qctl share table orders --to alice@example.com --permission editor --yes

    # change an existing grant using a UUID from the member listing
    qctl table-member set-permission 11111111-2222-3333-4444-555555555555 --table orders --permission editor --yes

    # remove another member's grant
    qctl table-member remove 11111111-2222-3333-4444-555555555555 --table orders --yes

    # remove your own grant
    qctl table-member leave orders --yes

## GOTCHAS

- The listing shows grants issued through the current organization to its active members other than external users, ordered by email. ops/admin can list without a grant; other callers need table access. [backend]
- `permission_level` is the grant; `effective_permission_level` also accounts for the organization's role. ops/admin have effective owner access even without a grant. For other roles, effective access is the lower of the grant and the role's ceiling; granting owner does not add missing role permissions. [backend]
- Changing a level or removing another user's grant requires effective owner access or ops/admin. Any member may remove their own grant. Removing or lowering the last owner grant returns 409 (exit 2) unless the caller is ops/admin. [backend]
- External users access tables through assigned cloud sources and cannot receive or change table grants (400, exit 2). A leftover grant can still be removed by UUID even though it is absent from the member listing. [backend]
- `leave` reads the caller's identity from the current organization's `auth me` endpoint. It removes only that account's grant; ops/admin retain access through their role.
- Each mutation prompts unless `--yes` and honors protected contexts, so `--yes` does not replace `--context`. Removing or lowering your own grant can require another owner to restore it.
- `get table-members -o json|yaml` emits a List containing the API member records. `set-permission` emits the updated member; `remove` and `leave` emit `{result: removed, dataset_id, user_id}`. Prompts stay on stderr. There is no name output: a grant requires both table and user IDs.
- `--permission` accepts exactly `owner`, `editor`, and `read_only`; invalid or empty values exit 2 before a request. `share table` defaults to `read_only`, while `set-permission` requires an explicit value. Existing grants are never changed by repeating `share table`.

## SEE ALSO

- `qctl explain table`
- `qctl guide authenticate`
