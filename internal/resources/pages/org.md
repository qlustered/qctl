---
summary: the organization whose resources qctl acts on
aliases:
kind:
schema:
guide:
signature: f8f7104
---

## USAGE

    qctl auth switch-org <org_id_or_name>
    qctl get orgs

## EXAMPLES

    # switch the active org by name (quote names containing spaces)
    qctl auth switch-org "Acme Corporation"

    # a UUID prefix resolves too, as long as it is unambiguous
    qctl auth switch-org 550e8400

    # or the full UUID -- all three forms are matched against the locally cached org list
    qctl auth switch-org 550e8400-e29b-41d4-a716-446655440000

    # ops-staff only: every organization on this server
    qctl get orgs

    # only the active ones
    qctl get orgs --active-only

    # search by name (at least three characters)
    qctl get orgs --search Acme

## GOTCHAS

- `get orgs` calls an operations-only endpoint that currently authenticates browser cookies rather than CLI bearer tokens; even an ops CLI token can receive 401. Use `auth me` for your Active Organizations. [backend]
- `get orgs` prints a nonempty server `msg` as plain text on stderr in every output format; the returned rows and pagination are preserved. A nonempty `--search` shorter than three characters exits 2 before any request.
- A fuzzy partial name in `auth switch-org` requires confirmation on stderr; pass the exact name, an unambiguous UUID prefix, or `--yes` for non-interactive use. Declining leaves the configuration unchanged; a non-interactive fuzzy match without `--yes` exits 2.
- `auth switch-org` resolves its argument against the organization list cached locally at your last login (`ctx.Organizations`), not a live query — an org created since then won't resolve by name until you log in again.
- For an ops user, `auth switch-org --allow-plaintext-token-store` writes the new token to the 0600 credentials file. Without `QCTL_ALLOW_PLAINTEXT_TOKENS=1`, it warns if an older keyring entry for the target organization is readable; later commands use whichever credential has the later `created_at`. Non-ops switching stores no new token, so this flag has no effect there.
- `--org`/`QCTL_ORG` override the active org for one invocation only (flag > env > context, resolved fresh every command); to persist a new default into the current context use `auth switch-org`, or `qctl config set-context --current --org <name-or-id>`, which resolves the same way and writes the same field (`auth login` writes it too) — see `qctl explain context`.

## SEE ALSO

- `qctl explain session`
- `qctl explain privilege-request`
- `qctl guide authenticate`
