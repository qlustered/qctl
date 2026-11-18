---
summary: the saved server and org qctl talks to
aliases:
kind:
schema:
guide: switch-context
signature: 55028e0
---

## USAGE

    qctl config current-context
    qctl config delete-context <name>
    qctl config list-contexts
    qctl config set-context [name]
    qctl config use-context <name>
    qctl info

## EXAMPLES

    # drop a context you no longer point at (asks first)
    qctl config delete-context my-context

    # same, without the confirmation prompt
    qctl config delete-context my-context --yes

    # point qctl at a new server -- this also caches its schema and switches to it
    qctl config set-context production --server https://api.qluster.ai

    # the same, for a server running on your own machine
    qctl config set-context local --server http://localhost:8000

    # change one field of an existing context, leaving the rest alone
    qctl config set-context production --output json

    # stop passing --org on every command: persist a default into the current context
    qctl config set-context --current --org "Acme Corporation"

    # a UUID prefix resolves too, as long as it is unambiguous
    qctl config set-context --current --org 550e8400

    # show the context qctl is using right now
    qctl info

    # the server and org the next command will use, and where each came from
    qctl info -o json

    # every context as a line the context commands take back
    qctl config list-contexts -o name

    # keep contexts in a file of your own, and bootstrap it in one go
    qctl config set-context scratch --config /tmp/scratch-config --server http://localhost:8000

    # every context is protected; allowlist a development one so its destructive commands need no --context
    qctl config set-context local --protected=false

    # show another saved context without switching to it
    qctl config current-context --context staging

    # run the invoked qctl against a local atlas, not the release matching its version
    qctl config set-context local --skip-release-check

    # check the release again for that context
    qctl config set-context local --skip-release-check=false

## GOTCHAS

- `config use-context <name>` and `config delete-context <name>` exit 3 when the named context is missing. Deleting the current context exits 2, including with `--yes`; switch to another context first.
- Updating an existing context with `config set-context <name>` leaves the current context unchanged; creating a new context still activates it. Use `config use-context <name>` to activate an existing context.
- `config set-context --org <partial-name>` requires confirmation on stderr for a fuzzy name; use an exact name, unambiguous UUID prefix, full UUID, or `--yes` in a script.
- a context stores a server (and its default org); credentials are stored separately, keyed by server **and** org — switching to a context with a server/org pair you haven't logged into yet means `qctl auth login` again, even if you're already logged in elsewhere.
- `qctl config use-context <name>` isn't purely local: it re-fetches and caches the target server's OpenAPI spec before switching, so an unreachable target server fails the switch outright rather than just warning.
- `qctl info` is not just similarly named — it's the exact same command as `qctl config current-context` under a shorter alias (same code, different `Use`/`Short` text), so anything true of one is true of the other.
- the text view of `qctl info` shows the STORED context only, so `QCTL_ORG="Beta Org" qctl info` still names the context's own org. `-o json|yaml` adds a `scope` block — `server`, `server_source`, `organization_id`, `organization_name`, `organization_source` — holding what the next command would use after `--server`, `--org`, `QCTL_SERVER` and `QCTL_ORG`, each source `flag`, `env` or `context`. Every key is always present and null when nothing selects it; an org that is selected but matches nothing cached exits 2, as the next API command would. `-o name` exits 2, as does any `-o` but `table` on `use-context` and `delete-context`.
- `qctl config list-contexts -o json|yaml` prints one row per context, sorted by name: `current`, `name`, `server`, `organization_id`, `organization_name`, `output` and `protected`, with null for an unset value, and `[]` when no context is configured. `-o name` prints `context/<name>`, keyed by name because contexts are, and `set-context`, `use-context` and `delete-context` take that line back: everything after the first `/` is the name, so `context/team/stage` names `team/stage`, and a context literally named `context/<x>` is reached as `<x>`. Any other kind's reference (`table/42`) exits 2 there. A context carrying `output: json`, `yaml` or `name` applies to a plain `list-contexts`.
- contexts live in `~/.qctl/config`, and `--config <path>` reads a different file instead. A `--config` path that doesn't exist is an error for every command **except** `config set-context`, which creates it — so set-context is how you bootstrap a config anywhere but the default location. A missing *default* config is never an error; it's just a first run.
- a context's `server` is the origin only (`https://api.qluster.ai`), never with `/api` appended — the API paths already start with `/api`, so a trailing `/api` in the context produces requests to `/api/api/...`.
- a context fixes the organization for every command that reads it (`--org` wins, then `QCTL_ORG`, then the context's default), and qctl never falls back to another one: a `403` or `404` means the resource is unavailable *in that organization*, and the error names the organization it was scoped to and where that came from. The exception is a `get data` 404 whose code reports the state of a table it DID find (such as `TABLE_SETUP_IN_PROGRESS`, `EMPTY_TABLE_MESSAGE` or `COMPOSITE_BUILD_BLOCKED`): nothing missed, so it names no organization. Retrying in a different organization is a deliberate change you make yourself — with `--org`, `QCTL_ORG`, `qctl auth switch-org`, `qctl config set-context --current --org <name-or-id>`, or `qctl config use-context <name>` — never something qctl does for you.

- `--context <name>` is a global flag: it runs one command against that saved context — server, organization and credentials — without switching, and nothing the command saves makes it current. A name with no saved context exits 2.
- every context is protected unless allowlisted, including one saved by an older qctl with no `protected` key: destructive commands — the `delete` and `kill` groups, `rows delete-quarantined`, both `delete-comment` commands, `queue-message retire`, `file replace-older`, and `alert resolve` with a `delete_*`, `discard_*`, `trash_file`, `trash_alert_associated_files`, `reject_held_file` or `reject_held_email_file` action — are refused unless they carry `--context <name>`. A terminal is asked y/N instead; a script or agent gets exit 2. `--yes` never answers it, and every other command is unaffected.
- `config set-context <name> --protected=false` allowlists a context (stored as `protected: false`) and asks for confirmation at an interactive terminal: `--yes` does not answer it, so a script or agent gets exit 2 and allowlisting stays a person's decision. `--protected` protects it again. `config delete-context` needs no terminal: a context created again under the same name starts out protected.
- the protection follows the server a destructive command will reach: when `--server` or `QCTL_SERVER` points anywhere but the current context's own server, it is asked or refused even on an allowlisted context, and `--context` satisfies it only by naming a context whose server that is.
- every command that talks to the server first asks it for its atlas version and runs the qctl release tagged with the same MAJOR.MINOR (the newest patch) in place of the invoked qctl, keeping its arguments, streams, signals and exit status. A release that is not installed is built from source into `~/.qctl/versions/<tag>/` on first use, which needs git, make and Go; a failed build exits 1 and never falls back to another qctl. No release for the server's minor, or a version that is not MAJOR.MINOR.PATCH, prints one `qctl: warning:` line on stderr and runs the invoked qctl; when the version check goes unanswered, the release the last answered check selected runs if it is installed, and otherwise the invoked qctl runs with no warning. `config`, `info`, `version`, `help` and `--help`, `completion`, `cache`, `guide`, `docs`, `api-resources` and `skill` never check.
- `config use-context <name>` runs the same check before it switches and prints `Atlas version`, `Running qctl`, `Selected qctl` (`running`, `installed` or `built`) and `Compatibility`, building the release first when needed; a failed build exits 1 and leaves the current context unchanged.
- `config set-context <name> --skip-release-check` (stored as `skipReleaseCheck: true`) turns the check off for that context, for a local atlas; `--skip-release-check=false` turns it back on. It applies only when the context supplies the server: with `--server` or `QCTL_SERVER` pointing elsewhere the check runs against that server. `QCTL_SKIP_RELEASE_CHECK=1` turns the check off everywhere.

## SEE ALSO

- `qctl explain session`
- `qctl guide switch-context`
