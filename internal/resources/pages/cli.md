---
summary: "qctl itself: version, docs, completion, the agent skill installer, and the discovery commands"
aliases:
kind:
schema:
guide:
signature: 4d8ddfb
---

## USAGE

    qctl api-resources
    qctl completion bash
    qctl completion fish
    qctl completion install
    qctl completion powershell
    qctl completion zsh
    qctl docs
    qctl explain RESOURCE[.FIELD]
    qctl guide [topic]
    qctl help [command]
    qctl skill install TARGET
    qctl skill status TARGET
    qctl skill uninstall TARGET
    qctl version

## EXAMPLES

    # every resource and every verb, machine-readable — for when the resource is unknown; it is large, so filter it
    qctl api-resources -o json

    # what a resource is, how to drive it, and its gotchas
    qctl explain table

    # list an object's child keys and a command to explain one
    qctl explain deliverable.spec.sheets

    # include every nested sheet, column binding and row-order key
    qctl explain deliverable --recursive

    # list every task-oriented walkthrough qctl ships
    qctl guide

    # which guides, and which of their sections, mention a word
    qctl guide --search quarantine

    # print one section of a guide instead of the whole topic
    qctl guide instantiate-rule --section "Attaching several rules"

    # the complete command reference as one page of several hundred KB — grep it rather than read it
    qctl docs

    # detect the shell and wire up completion
    qctl completion install

    # install the agent operating skill into Codex's skill directory
    qctl skill install codex

    # what is installed there, and whether this qctl carries something newer
    qctl skill status codex

    # inspect a Codex copy installed at the old location
    qctl skill status codex --legacy

    # install the same operating skill for local Claude Code sessions
    qctl skill install claude-code

    # --help for a command you don't already have the name of
    qctl help get

    # client build/version, plus the current context if one is configured
    qctl version

    # the same as {version, commit, current_context}; current_context is null when none is set
    qctl version -o json

## GOTCHAS

- `completion install --shell` accepts `bash`, `zsh`, `fish` and `powershell`; an unsupported shell exits 2 before installation begins.
- `completion install` requires confirmation before writing the completion script or shell configuration. Without an interactive stdin, pass `--yes`/`-y` or it exits 2; declining at a terminal exits 1. The confirmation question is written to stderr.
- Printed follow-up recipes for commands that require confirmation include `--yes` when stdin cannot answer a prompt. Redirects from Python apply to rule submission and from an ambiguous deleted table name to recovery by ID also retain an explicit `--yes`. Skill installation, update, replacement and legacy removal recipes follow the same terminal rule.

- `api-resources`, `guide`, `docs`, `version`, `completion` (including `install`), `skill`, and `help` never touch a server or a credential — they don't even try, and neither does `explain` for a resource whose `schema` in `qctl api-resources -o json` is empty or begins `manifest:`. An empty `schema` leaves only the page, and a `manifest:` one is served from the binary, so neither needs a bootstrap or the network in any output mode.
- a resource whose `schema` begins `openapi:` is the exception: `explain` bootstraps and fetches its live schema over the network in **every** output mode, table included — a plain `qctl explain table` makes the same HTTP round-trip `-o json` does and is just as slow against an unreachable server; it still never needs a credential.
- `explain <res>` in table output (the default) never fails for a resource that exists in the catalog: with no server, or an unreachable one, an `openapi:` resource still prints its page and exits 0 with a stderr warning, and every other resource prints with no warning at all — in table mode only an unresolvable resource *name* errors.
- `-o json`/`-o yaml` and any dotted `RESOURCE.FIELD` skip the page entirely and return the schema alone, so they fail where table mode would still print a page. A resource whose `schema` is empty exits 2 (`qctl explain session -o json` is an error, not a page), as do an unknown name and a field path the schema does not have; an `openapi:` resource also exits 1 without a reachable server, while the `manifest:` ones work offline.
- A dotted `explain RESOURCE.FIELD` in text output lists the field's immediate child keys under `FIELDS`, including object-array item keys. `--recursive` lists all nested keys, from either a resource or a dotted field path; recursive OpenAPI references stop where the schema names the repeated component. JSON/YAML always carry the complete extracted schema, regardless of `--recursive`.
- `explain <res>` counts the page's GOTCHAS instead of printing them: `--gotchas` prints them all, and `--grep <term>` prints only the ones that contain the term, ignoring case, with no other section and no schema fetch — so `--grep` never touches a server, even for an `openapi:` resource. Neither flag combines with `-o json`, `-o yaml`, a dotted `RESOURCE.FIELD` or a command name; each of those exits 2.
- `explain -o json` and `-o yaml` print the same document under OpenAPI's key names (`type`, `format`, `description`, `readOnly`, `writeOnly`, `default`, `enum`, `nullable`, `items`, `properties`, `anyOf`, `oneOf`) and leave out an empty key, so an absent `enum` means none and an absent `readOnly` or `writeOnly` means false. The shape is not OpenAPI's, though: `properties` is a list of objects that each carry their own `name`, a field's `required` is a boolean where the schema's is OpenAPI's list of names, `ref` is the bare component name rather than a `$ref` pointer, and a `{type: null}` alternative is never listed — a field that is one schema or null is that schema with `nullable: true`, and `anyOf`/`oneOf` appear only for a field with two or more non-null alternatives, which carry no `name`.
- `explain` of a resource whose writes take a different schema from its read (`cloud-source` is one) lists the fields of both: a top-level field only writes take is marked `<write-only>` (`WRITEONLY: true` for a dotted path, `writeOnly: true` in `-o json|yaml`), and one only the read carries is marked `<read-only>`. It still lists the wire schema, not the manifest, so a `<read-only>` field can be a manifest key that `apply` ignores. Against a server whose spec lacks that write schema, `explain` lists the read alone.
- `explain` marks a field that `apply` accepts only on creation `<create-only>`, with its accepted values when restricted: cloud-source `state` shows `<create-only: draft>`. A dotted path prints `CREATEONLY: true` and `CREATEONLY VALUES: draft`; JSON/YAML carry `createOnly: true` and `createOnlyValues: [draft]`. The read's allowed values remain under `Allowed:`, `ALLOWED VALUES` or `enum`.
- `qctl skill install TARGET` writes one personal-scope directory for the requested host: `codex` uses `~/.agents/skills/qctl` regardless of `CODEX_HOME`, while `claude-code` uses `$CLAUDE_CONFIG_DIR/skills/qctl`, falling back to `~/.claude/skills/qctl` when that variable is unset or empty. Install and uninstall write or remove only the selected `qctl` directory, leaving sibling skills and project-scope installs alone.
- `--legacy` on any `skill` verb selects the deprecated Codex location: `$CODEX_HOME/skills/qctl`, or `~/.codex/skills/qctl` when the variable is unset or empty; using it with `claude-code` exits 2. To migrate, run `qctl skill install codex`, then `qctl skill uninstall codex --legacy`. Installing in the new location preserves the old copy, including local edits. `qctl skill status codex` reports an existing old copy in a separate `legacyInstall` block under JSON/YAML and a separate section under table output; `qctl skill status codex --legacy` reports only the old location.
- Normal invocations report an installed managed skill that differs from this binary's bundled content, local edits to a managed copy, or a legacy Codex copy on stderr. Unchanged notices are suppressed for 24 hours per host using `~/.qctl/skill-notice-<target>.json`; changed notices can appear sooner. Checks and cache writes are best effort: a read failure does not fail the command, and a failed cache write can make the notice repeat. Absent or current skills alone create no cache. Skill management, shell completion, help and version commands skip this check; notices never update or remove a skill.
- both targets are offline like the rest of this page: no server, credential or organization is used, and a wrong target or argument count exits 2 before the destination is resolved. Nothing installs either target automatically — not the package, first run, `auth login` or an upgrade. A Claude Code install is a personal skill on this machine: it is available to local sessions but does not follow the user into Cowork or Claude Code cloud sessions.
- an install for either target REFUSES rather than overwrites: qctl owns the destination only while every file still matches the SHA-256 recorded in the `.qctl-skill.json` it wrote, and an added file counts as a change too. Edited, or installed by something other than qctl, and `install`/`uninstall` exit 1 with nothing touched until you pass `--force`; `--yes` answers the prompt and does not imply `--force`. Reinstalling what is already current and uninstalling what is absent are no-ops at exit 0. `qctl skill status TARGET` also exits 0 for all five states, including `modified` and `unrecognized`: read `state` (`-o json`/`-o yaml`), because only a read failure is non-zero. Its installed and bundled versions are content digests of that target's skill files, not qctl's version, so a qctl release that changes nothing under `internal/agentskill/assets/<target>/qctl` leaves that target's install `current`.
- `--request-timeout` bounds ONE HTTP attempt and nothing above it: a `--watch`'s own `--timeout` bounds the whole watch and is a separate budget, and the pause `qctl` takes between throttled dry-run attempts is outside both. `0` means the 30-second default, not "no deadline". It overrides a package's own deliberate figure in BOTH directions, so `--request-timeout 10s` shortens a file transfer that would otherwise get 300 seconds, and a negative value is exit 2.
- `--retries N` repeats only the SAFE methods — `GET`, `HEAD`, `OPTIONS` — and only on a transport failure, a `429`, or a `502`/`503`/`504`; a `POST`, `PATCH`, `PUT` or `DELETE` is never repeated for you, and neither is a plain `500`, which is the application answering rather than a hiccup. It defaults to `0`, so nothing is retried unless you ask. Each attempt gets the full `--request-timeout`, waits double each time from 1s to a 30s cap, and honours an in-range `Retry-After`; every retry prints one line to stderr, so a `-o json` stdout stays parseable.
- `-v` is a count flag, not a boolean: `-vvv` is three, not one. 7 turns on structured HTTP logging, 8 adds curl commands with redacted tokens, and 9 adds curl commands with the **full, unredacted** bearer token — don't paste `-v=9` output anywhere shared.
- human output renders C0/C1 terminal controls found in server text as visible `\xNN` escapes before it adds any styling, so the server cannot recolor the terminal, move the cursor, or rewrite earlier output; JSON/YAML preserve the original string value and let their encoders quote it safely.
- unless you pass `--max-column-width`, it follows stdout: 80 columns when stdout is a terminal, no limit when it is a pipe or a file — so a table cell it cuts on screen prints whole in a script. A width you pass applies either way, and `0` is no limit.
- an `--*-id` flag is never a wildcard: `--table-id 0` (or any zero or negative value passed to an `--*-id` flag) fails with `--table-id must be a positive integer, got 0` and makes **no** request — it does not fall back to an unfiltered listing. For the standard table selector, an explicitly empty `--table` is invalid and `--table` together with `--table-id` is a conflict even when the name is empty. These command-line errors are all exit 2 and are checked before config, organization, or credentials are loaded, so they do not turn into “not logged in” on a new machine. Pass no selector at all only where the command documents an organization-wide form.

## SEE ALSO

- `qctl guide discover-capabilities`
- `qctl guide output-and-scripting`
