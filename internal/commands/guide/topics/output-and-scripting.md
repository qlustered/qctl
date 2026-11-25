# Drive qctl from a script

Branch on the exit code, parse `-o json` or `-o name`, pass `--yes` to every
write, preview with `diff -f`, and give every wait a `--timeout`.

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | Success. Also: `diff -f` found no differences, `rows check-unique` found the columns unique, a list watch reached its `--timeout`. |
| 1 | The server answered 429 or 5xx, the network failed, an `-f` file could not be read, or you declined a prompt at a terminal. Also the verdict of `diff -f` and `rows check-unique` — see below. |
| 2 | Bad usage: this command line will not succeed as written. Includes a server 400, 409 or 422 refusal, and a prompt with no terminal and no `--yes`. |
| 3 | Not found (404). |
| 4 | Unauthorized (401, 403), or not logged in, or the login expired. |
| 5 | The server's API is incompatible with this qctl. |
| 124 | A wait for one thing to end gave up: `--wait-timeout`, a `download` waiting for its file, `get dry-run-job --watch` or `inspect dry-run-job --follow` at `--timeout`, `get latest-alert --watch --exit-on-change` with no change. Nothing it waited on is cancelled. |
| 130, 143 | Interrupted by `SIGINT` or `SIGTERM`. |

Exit 2 covers all input qctl rejects as malformed: an unknown `--columns` or
`--order-by` name, an unknown `explain` resource or `guide` topic, two
conflicting filter flags, a non-positive `--table-id`, an `-o` value the
command does not support, an empty positional argument, a wrong number of
positional arguments, an unknown flag, and an unknown command or subcommand.
Every rejection `apply -f` makes on its own exits 2 and sends nothing: YAML
syntax, `apiVersion`, an unknown `kind` or field, a value that fails validation,
an immutable field changed.
In a multi-document file the exit code is the **first** failure's.

Two read-only commands spend exit 1 on a valid verdict: `qctl diff -f` means
"differences found" and `qctl rows check-unique` means "not unique". For both,
**exit 1 with stderr empty** is the verdict, and **exit 1 with stderr output**
is a runtime failure (an unreadable file, a 5xx, a 504 from check-unique's
30-second statement timeout). Capture stderr before you branch:

    qctl diff -f table-rule.yaml > diff.txt 2> diff.err
    # 0                  nothing to apply
    # 1, diff.err empty  differences: safe to run apply -f
    # anything else      stop

A declined document in `apply -f`, or in a multi-document
`alert resolve -f` or `warning resolve -f`, is skipped, not failed.

## Waits

    qctl get ingestion-jobs --table orders --watch --timeout 10m

`--timeout` takes a duration (`90s`, `10m`); `0` or unset means no limit
(10 minutes for `inspect dry-run-job --follow`). With no one to press Ctrl-C,
always pass it, or set `QCTL_WATCH_TIMEOUT`. A list watch (`get
ingestion-jobs`, `get profiling-jobs`, `get dry-run-jobs`, `get
queue-messages`, `get table --job-activity`, a plain `get latest-alert`) has
no end to reach, so its `--timeout` is how it finishes: exit 0 after
`timed out after <duration>; resume with: <command>` on stderr. A wait for one
thing to end exits 124 instead (table above). Each wait prints one stderr line
before it starts, naming its limit.

## Pick a format

    qctl get tables -o json | jq -r '.items[].id'
    qctl get tables -o name | xargs -n1 qctl describe table

`json` and `yaml` carry every field. A paged `get` list wraps one page in
`{kind: List, items, page, limit, total, next_page}`: iterate `.items[]`, and
pass `next_page` back as `--page` until it is `null`. Under `table` and `name`
a stderr line says "More available" instead. `get history`, `get
notifications`, `get rule-suggestions`, `get rule-reviews`, `get
data-review-groups`, `get change-proposals` and the privilege lists print their
own paged object.

`name` prints one `kind/<id>` per line (`table/42`). Every argument or id flag
of that kind takes it back (`--table-id table/42`); a wrong prefix exits 2.
`get table-schema` prints column names, `get value-recommendations` values,
and `get standard-field-suggestions` `<column>/<standard field slug>` instead.

Each command takes its own `-o` set; an unknown value exits 2 and lists it.
A write whose result you act on prints a result document under `-o json` and
moves its prose to stderr — `qctl upload file data.csv --table orders --yes -o
json | jq .ingestion_job_id` names the job to follow. A verb that only reports
what it changed renders `table` alone.

`qctl info -o json` says which server and organization the next command will
use, and where each came from (`flag`, `env`, `context`).

`qctl config set-context --current --output json` makes `json` the default
for every command that supports it, exactly as if you had typed `-o json`; an
explicit `-o` wins. Some `describe` commands print a YAML manifest only when
no `-o` was given, so a stored `output:` changes them too.

## Trim the table

    qctl get tables --columns id,name --no-headers

Both change `-o table` only. An unknown column exits 2 with a `valid:` list
(on `get`, before any request and under every `-o`). Table cells are padded
with spaces, not tabs: do not split them with `cut -f` or `awk -F'\t'` — use
`-o name` or `-o json`. `--max-column-width`: `qctl explain cli --grep max-column-width`.

## Non-interactive writes

    qctl apply -f table.yaml --yes

Every command that confirms before it acts — `delete`, `undelete`, `upload`,
`submit`, `run`, `kill`, `rows`, `alert resolve`, `alert submit`, `apply` and
the rest — exits 2 when `--yes` is absent and stdin is not a terminal. The
preview is printed and nothing is written. `apply -f` prompts for `Table` and
`CloudSource` updates that change something, `TableSchema` changes, and
`TableRule` instantiates and patches; pass `--yes` for every kind anyway.
Under `-o json|yaml`, `Table` and `CloudSource` exit 2 without printing, so
stdout stays parseable.

Write only what the user asked for: preview first (`--dry-run`, `--check`,
`diff -f`), check the count it reports, then repeat with `--yes`.

## Preview before writing

    qctl diff -f table-rule.yaml

It currently supports exactly these manifest kinds from
`diff.diffKinds`: `Deliverable`, `LinkedTable`, `StandardField`, `Table`, `TableKind`, `TableRule`, `TableRuleTemplate`, `TableSchema`, `TableSeries`.
`TableRule` only in patch mode (`metadata.id` set). It prints the changed
fields and the exact request body `apply -f` would send or, for a kind apply
writes in several requests, each request in order, and writes nothing.
`TableRuleTemplate` is the exception: it takes `--table` or `--table-id`, the
server stores each preview as a rule merge, and it exits 2 while an item is a
conflict; `qctl diff --help` has the rest.
A file of several `---` documents exits 2: split it and diff each one. Branch
on its exit code as shown under Exit codes.

## Deadlines, retries and debugging

    qctl get tables --request-timeout 2m --retries 3
    qctl get tables -vvvvvvv

`--request-timeout` bounds one HTTP attempt; `--retries` repeats only `GET`,
`HEAD` and `OPTIONS` on a transport failure, 429, 502, 503 or 504. `-v` is a
count: 7 logs HTTP, 8 adds a curl with the token redacted, 9 prints the token
in full. Details: `qctl explain cli --grep curl`.

## See also

- `qctl explain cli`
- `qctl guide discover-capabilities`
