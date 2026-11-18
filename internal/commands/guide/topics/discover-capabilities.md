# Find out what qctl can do, from a cold start

Run the request first; look things up only when a command fails or you do not
know its syntax. Then climb one rung at a time, and stop as soon as you have
the answer.

| Rung | Command | Answers |
|------|---------|---------|
| 1 | `qctl <command> --help` | one command's flags and examples |
| 2 | `qctl guide` | which task guide fits (2 KB index) |
| 3 | `qctl guide --search "<words>"`, `qctl guide <topic> --section <heading>` | one section of a guide |
| 3 | `qctl explain <resource>.<field>` | one field: type, enum values |
| 3 | `qctl explain <resource> --grep <word>` | only the gotchas that mention the word |
| 4 | `qctl explain <resource> --gotchas`, `qctl guide <topic>` | a whole page, only if a gap remains |

When you do not know which resource a task is about:

    qctl api-resources

One row per resource: aliases, every verb that acts on it, its `apply -f`
kind, and its guide. It reads only the binary, so it needs no server or login.
The table view is about 6 KB; `-o json` (about 15 KB) adds each row's schema
source and summary — reach for it only to parse.

## Learn a noun

    qctl explain table
    qctl explain table.migration_policy

`explain <resource>` prints USAGE and EXAMPLES, counts the GOTCHAS, and always
exits 0 for a known resource, server or not. `--gotchas` prints them all;
`--grep <word>` prints only the ones that mention the word. The dotted form, `-o json` and `-o yaml`
fetch the live schema and need a reachable server, except for a resource
whose schema ships in the binary (`manifest:` in `api-resources -o json`).

## Learn a task

    qctl guide --search "alert resolve"
    qctl guide triage-alerts --section "need input"

A guide walks a task across several commands. An unknown topic name exits 2
and suggests the closest ones.

## Check whether anything is waiting for you

    qctl get notifications --count

`needs_you` counts what is asking you for something: a mention, a review
request or assignment, a file ready to download. Several of these have no
other read path, so check it once per session. The whole task:
`qctl guide check-inbox`.

## Avoid

- `qctl docs` dumps every command and flag as one Markdown document of
  several hundred KB. Grep it if you must; never read it whole.
- Reading qctl or atlas source: the binary's help, `explain` and `guide` are
  the contract.

## Shell completion

    qctl completion install --yes
    qctl completion zsh

## See also

- `qctl explain cli`
- `qctl guide output-and-scripting`
- `qctl guide authenticate`
- `qctl guide switch-context`
- `qctl guide check-inbox`
