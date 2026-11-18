# Point qctl at a different server

    qctl info                                   # the current context
    qctl config list-contexts                   # every saved context
    qctl get tables --context staging           # one command against another context; writes nothing
    qctl config use-context staging             # switch
    qctl config set-context staging --server https://staging.example.com

A **context** is the saved server, default organization and output format used
when nothing overrides them, stored in `~/.qctl/config` with one marked
current. Work in the context you were given: switch, or name another with
`--context`, only when the user asked for it. Every rule, including
`--config` and the config file's shape: `qctl explain context`.

## What context you are in

    qctl info
    qctl config list-contexts

`info` is a shortcut for `config current-context`: the context name, its
server, its output format if set, its default organization, and `Protected:
yes` (with the command that allowlists it) or `Protected: no (allowlisted)`.
`list-contexts` prints a NAME/SERVER/ORGANIZATION/PROTECTED table with `*`
before the current one; `acme (+2 more)` means more organizations are cached
from a login, with `acme` the default.

## Add or update one

    qctl config set-context staging --server https://staging.example.com
    qctl config set-context production --output json

Creating a context needs `--server`. qctl checks the server answers, caches
its OpenAPI spec, fetches its Kinde auth config, and makes the new context
current. A failed Kinde fetch is only a `Warning: could not fetch auth config`
on stderr, and a later `auth login` there fails with `Auth configuration not
available`; re-run the same `set-context` once the server answers. Updating an
existing context leaves the current one unchanged, and only a `--server`
change re-validates. A fuzzy `--org` name asks for confirmation on stderr;
pass the exact name or UUID.

## Switch, or run one command elsewhere

    qctl config use-context staging
    qctl get tables --context staging
    qctl get tables --server https://staging.example.com

`use-context` makes the context current and re-fetches the server's OpenAPI
spec, so an unreachable server fails the switch ("failed to cache API
schema"). It never refreshes the Kinde auth config; `set-context <name>
--server <url>` does. Credentials are per server and organization, so a
context you never logged into needs `qctl auth login` (`qctl guide
authenticate`).

`use-context` also checks which qctl release the server needs and prints
`Atlas version`, `Running qctl`, `Selected qctl` and `Compatibility`, building
the selected release from source first when it is not installed (progress on
stderr). A failed build exits 1 and leaves the current context as it was.
Every command that talks to the server makes the same check and runs the
matching release in place of the invoked qctl; `qctl explain context` has the
rules. `qctl config set-context local --skip-release-check` or
`QCTL_SKIP_RELEASE_CHECK=1` turns the check off for a local atlas.

`--context` runs one command against a saved context's server, organization
and credentials, writes nothing, and exits 2 for an unknown name. `--server`
overrides only the server, the way `--org` overrides only the organization,
with no validation or spec caching first.

## Protected contexts

Every context is protected unless allowlisted. On a protected context a
destructive command (the `delete` and `kill` groups, `rows
delete-quarantined`, the two `delete-comment` commands, `queue-message
retire`, `file replace-older`, and `alert resolve` with a `delete_*`,
`discard_*`, `trash_file`, `trash_alert_associated_files`, `reject_held_file`
or `reject_held_email_file` action) must name the context:

    qctl delete rule old-rule --context production --yes

Without `--context`, a terminal asks `Run ... against it? [y/N]` and a script
or agent gets exit 2. `--yes` answers the command's own prompt, never this
one. Confirm with the user that the change is meant for that context before
you name it. Reads and ordinary writes are unaffected.

    qctl config set-context local --protected=false

Allowlists a development context. It asks at an interactive terminal and
`--yes` does not answer it, so allowlisting is always a person's decision;
`--protected` protects it again. The protection follows the server a command
reaches: a `--server` or `QCTL_SERVER` other than the current context's own is
asked or refused even on an allowlisted context.

## Remove one

    qctl config delete-context staging --yes

Refuses to delete the current context; `use-context` to another first. A
context created again under a deleted one's name starts out protected.

## See also

- `qctl explain context`
- `qctl guide authenticate`
