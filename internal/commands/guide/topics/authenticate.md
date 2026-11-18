# Log in, check who you are, switch orgs

    qctl auth me                    # who you are, your role, every active organization
    qctl get tables --org acme      # one command in another organization; writes nothing
    qctl auth switch-org acme       # change the stored default organization
    qctl auth logout                # revoke this CLI session, delete the credential

If you are an agent, do not run `qctl auth login`: it needs a browser and can
change the stored default organization. Missing or expired credentials exit 4
and name the server and organization; ask the user to run `qctl auth login` in
their own terminal, then re-run the failed command. Act in the organization you
were given: a failure is never a reason to try another one (below). Credentials,
the keyring and every logout outcome: `qctl explain session --gotchas`; choosing an
organization: `qctl explain org`.

## Confirm

    qctl auth me

Prints what the current token resolves to: context, server, user ID, email,
role, the configured (default) organization, every active organization, and
whether the account is active. `qctl info` is a shortcut for `qctl config
current-context`: it reads only the saved context, with no network call. For
your organizations read Active Organizations here: `qctl get orgs` calls an
operations-only endpoint that authenticates browser cookies, so even an ops CLI
token can get a 401 there.

## Log in

    qctl auth login

Opens a browser for a Kinde device-code OAuth flow and stores the resulting
token in your OS keyring, falling back to a `0600` `~/.qctl/credentials.json`
when the keyring is unavailable. Login also caches your organization list, which `auth
switch-org` resolves against, and **overwrites the stored default
organization** with whichever one the token exchange resolved to, so logging in
after an `auth switch-org` can silently put you back on a different
organization.

## Choose the organization

Every command that needs an organization takes the `--org` (`-O`) flag if set,
otherwise `QCTL_ORG`, otherwise the organization stored in the current context.
`--org` and `QCTL_ORG` write nothing to disk. Only these change what is stored:

- `qctl auth switch-org <name-or-id>` writes the current context's
  organization, and so does `qctl config set-context --current --org <name-or-id>`;
- `qctl auth login` writes it as a side effect;
- `qctl config use-context <name>` moves to another context, and so to the
  organization that context stores.

    qctl auth switch-org acme

Resolves `acme` against the cached list: the exact name, a full or prefix UUID,
or a fuzzy partial name that asks for confirmation on stderr. Without a
terminal a fuzzy match exits 2 unless you pass `--yes`; confirm the
organization with the user instead of reaching for it. An organization created
since your last login does not resolve until you log in again.

## The scope is fixed, and a failure is not a reason to try another org

Whichever of the three supplied the organization, it is fixed for the whole
invocation. qctl has **no cross-organization fallback**: nothing re-resolves the
organization, retries in another one, or re-authenticates because a request
failed. A `403` or `404` means the resource is unavailable *in that
organization*, and the error says so:

    $ qctl get table 8 -o yaml
    failed to get table: request failed: User is not authorized to access table 8

    Scoped to organization Qluster-dev (from the current context).
    This account has no access to table 8. No other organization will help.
    Run `qctl auth me` to see your role and email. Ask the table's owner or an
    org admin to grant this account access:
      qctl share table 8 --to <email from qctl auth me> --yes
    The grant level and your role both control allowed actions. Editing needs an editor or owner grant.
    Then re-run this command.

The parenthetical names the setting that supplied the scope:
`from the --org flag`, `from QCTL_ORG`, or `from the current context`. On the table detail
endpoint this denial means the table **is** in this organization and your account has no assignment to it;
the same advice names every denied id when the backend reports `User is not
authorized to access tables [8, 12]`. `qctl share table` stores a `read_only`
grant, which allows reads only: a command that changes the table is then
refused with `You have view-only access to table 8. Ask a table owner for
edit access.` until an owner raises the grant to `editor` or `owner`. A grant
adds no permission a role lacks.

Other 403s, including `Table 8 is not associated with this organization`
(which reads the same whether the table lives elsewhere or does not exist),
and lookup 404s carry the organization note:

    Scoped to organization Qluster-dev (from the current context). qctl does
    not search other organizations; do not switch organizations or retry with
    `--org`. Confirm the intended organization with the user.

A `qctl get data` 404 whose code reports the state of a table it did find
(such as `TABLE_SETUP_IN_PROGRESS`, `EMPTY_TABLE_MESSAGE` or
`COMPOSITE_BUILD_BLOCKED`) carries no note: nothing was missed. Either way,
trying each organization in turn is a scope expansion nobody asked for. Stop,
report the blocker, and ask for access or for the right organization.

## Inspect CLI sessions

    qctl auth sessions
    qctl auth sessions --never-used
    qctl auth sessions --columns name,status,first_used_at

`--never-used` shows sessions whose tokens all expired without a recorded first
use. Fresh logins are excluded; token expiry supplies the age cutoff. The optional
`first_used_at` column reports first use, not recent activity. Token lookup failures
make the filtered command fail; missing token rows are skipped with a warning.

## Log out

    qctl auth logout

Revokes this CLI session on the server, then deletes the locally stored
credential. The context, its default organization and the cached organization
list stay, so the next command that needs a credential asks for `auth login`
again. `--local` skips the server call and warns that the token stays valid
until it expires. If the server already rejects the token, the credential is
deleted anyway; any other server or transport failure keeps it so a retry can
reach it. Two stored copies, a keyring that refuses, and `--all`: `qctl
explain session --grep logout`.

## See also

- `qctl explain session`
- `qctl explain org`
- `qctl guide switch-context`

## Manage table access

Use `qctl get table-members orders` to see each user's UUID, role, granted
level and effective level. `qctl describe table orders` also shows grant
levels and your own effective `My access` level.

Share at the level the person needs:

    qctl share table orders --to alice@example.com --permission editor --yes

An existing grant keeps its level even if you repeat the share with another
`--permission`. Change it explicitly, using the UUID from the member list:

    qctl table-member set-permission 11111111-2222-3333-4444-555555555555 --table orders --permission editor --yes

Remove that grant, or remove your own:

    qctl table-member remove 11111111-2222-3333-4444-555555555555 --table orders --yes
    qctl table-member leave orders --yes

A table owner or ops/admin can manage other members; a member can leave.
Only ops/admin can lower or remove the last owner grant. Leaving as ops/admin
removes your grant but you retain access through your role. Other callers
can need another owner to share the table again. These mutations honor
protected contexts: `--yes` skips their own prompt, and a protected context
still needs `--context`.

An external user's access comes from assigned cloud sources; table grants
cannot be added or changed for them. Existing external grants can be removed.
For internal callers other than ops/admin, a grant of `read_only` refuses
table writes even when the organization role permits the action; ask a table
owner to change the grant to `editor` or `owner`. Your role's permissions
still apply at either level.
