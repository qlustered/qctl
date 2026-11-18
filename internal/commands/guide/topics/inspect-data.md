# Look at a table's rows

`qctl get data` fetches one page of a table's rows and renders it as a compact,
self-describing Markdown payload built for agents: a summary line, an aligned
result table, and optional provenance. Use it to answer "what does this data
actually look like" without opening the warehouse.

## One page at a time

    qctl get data orders

One backend page is fetched per invocation — the default is page 1, 20 rows.
There is no auto-pagination: ask for the next page explicitly.

    qctl get data orders --page 2 --limit 100

## Every row: export, don't page

To scan, count or hand over a whole table, export it instead of paging:

    qctl download data orders --output ./orders.csv.zip
    qctl download data orders --is-bad-data --alert-item-id 8817 --output ./held.csv.zip

The export is the WHOLE matching set (up to ten million rows) as a ZIP holding
one CSV — unzip it before reading. It takes the same row filters as `get
data`, plus `--fields` to pick columns. qctl waits up to `--wait-timeout`
seconds (900 by default) and exits 124 if the file is not ready; re-running
requests the export again. Money and percent cells are written as their
display digits. Limits and formatting: `qctl explain data --grep export`.

## Look at the quarantined rows

Rows that failed a rule are held separately. `--is-bad-data` returns those
instead of the clean ones, and `--include-dataset-rule-ids` says which rule
each row tripped.

    qctl get data orders --is-bad-data --include-dataset-rule-ids

Start from a rule family with `--rule <slug>`:

    qctl get table-rules orders
    qctl get table-rules orders --state disabled
    qctl get rules --columns slug,name
    qctl get data orders --is-bad-data --rule email-check --include-dataset-rule-ids
    qctl download data orders --is-bad-data --rule email-check --output ./email-check.csv.zip

The RULE column names the family; `get rules` supplies its slug. `--rule`
includes every instance of that family on the table, across releases and
lifecycle states. It requires `--is-bad-data` and cannot be combined with
`--dataset-rule-ids`, the lower-level filter for explicit table-rule UUIDs.
An unknown slug or a family with no instances on the table exits 3 before
reading rows or queuing an export. A matched family can have no flagged rows.
`--state disabled` lists paused rules; it filters lifecycle state rather than
the validation evidence shown by `describe table-rule`.

Every flagged cell carries an `[A<id>]` or `[W<id>]` marker. The Issues
column lists every token on the row with its backend field name, including
whole-row issues that have no cell, and `--include-issues` adds one ISSUES line
per distinct alert or warning id. See `qctl guide triage-alerts` for the longer
investigation workflow, and `qctl guide edit-data` to fix the values themselves.

A cell pinned by a steward's overwrite carries `[S]`, on the clean page as well
as the quarantined one, and a STEWARD OVERRIDES section under the table lists
each pin as `<row id> <field>: <value it displaced> → <value now>`. The
displaced value is what the steward fixes still in effect replaced, as the
backend logged it. `qctl guide edit-data` says what puts a pin there and what
takes one away.

The Triggered Rules column uses `r-*` aliases; resolve one to its UUID with:

    qctl cache aliases resolve r-1

## Rows waiting for someone's approval

Not every quarantined row failed a rule. A row nobody has decided can be kept
until someone approves it -- because it may be a duplicate, because the
table's data review policy requires review, or because the table quarantines
rows until approved -- and Row State names the reason as
`held-for-approval=<reason>`. An undecided row in a submitted data review
that is not closed reads `waiting-for-approval` instead:

    qctl get data orders --is-bad-data --held-for-approval

    qctl get data orders --is-bad-data --held-for-approval --approval-hold-reason possible_duplicate

    qctl get data orders --is-bad-data --waiting-for-approval

`qctl describe table orders` counts the waiting rows, and the held ones by
reason. Approve them with
`qctl rows release`; under a data review policy that requires review, or on a
table that quarantines rows until approved, that records your approval, skips the rows you edited or submitted, and a row leaves only once enough
people who neither edited nor submitted it have approved. The result names
the rows released and the ones still short of approvals (`1 of 2 approvals`)
(`qctl guide review-data`):

    qctl rows release orders --status held_for_approval --yes

Such a row can still have issues: releasing it lets it leave quarantine once
it passes, it does not make it pass.

## Why is THIS row quarantined

The Triggered Rules column says which rules touched a row. For the verdict each
one reached, when, and which revision produced it, describe the row:

    qctl describe row 1042 --table orders

One block per rule run, newest first, each naming the manifest it executed and
listing the rules that did something to the row, with the `[A<id>]`/`[W<id>]`
tokens they raised. `-v` lists every rule in the manifest, including the ones
that passed.

    qctl describe row 1042 --table orders -v

The rule aliases are the same `r-*` handles the data page uses, so a rule reads
the same in both. Follow one to the rule itself, or a token to its message:

    qctl describe table-rule r-1 --table orders

    qctl describe alert 8817

## Narrow it down

    qctl get data orders --fields id,amount --limit 5

    qctl get data orders --filter-by-field-name status --filter-by-field-value open

    qctl get data orders --search-query "acme"

qctl checks only a few flag values itself, such as a non-positive `--limit` or a
`--fields` that names no column, and leaves the rest to the backend: it does not
check `--max-cell-chars` against the server's accepted range, for instance.

## Feed it to something else

`-o table` (and the unset default) render the Markdown payload and are not
column-selectable; `-o name` is rejected with exit 2 — this command has no
name view. For machine consumption use JSON or YAML, which return the backend
response untouched:

    qctl get data orders --limit 5 -o json

## Read and answer a comment on a row

People leave discussions anchored to a row, or to named cells of it. They are
not in the default payload — ask for them:

    qctl get data orders --include-comment-threads

The extra Comment Threads column names every UNRESOLVED thread on each row as
`<thread-id> [<comments>] <targets>`. That id is the handle for everything
else; there is no way to list a table's threads without it.

    qctl get comment-threads --table orders <thread-id>     # the summary row

    qctl describe comment-thread <thread-id> --table orders # the transcript

Answer one, then close it out:

    qctl comment-thread reply <thread-id> --table orders --body "fixed at the source"

    qctl comment-thread resolve <thread-id> --table orders

Going the other way, a thread id narrows the data to the rows it is about:

    qctl get data orders --comment-thread-id <thread-id>

Rule-review discussions are not these: they keep `qctl review comment`.

## Where the columns came from

    qctl get table-schema orders     # the columns and their types

    qctl describe table orders       # the columns plus the rules bound to them

## Request unmasked personal data

If you need temporary unmasked access, ask the table's admins with a reason,
then repeat the listing to check their decision:

    qctl pii-access-request request --table orders --duration 1h --reason "reconcile refunds" --yes
    qctl get pii-access-requests orders -o json

Read `state`, `deny_reason` and `grant_expires_at`. To withdraw an open request,
use its id from the listing:

    qctl pii-access-request withdraw pii-access-request/7 --table orders --yes

For a table shared from another organization, add `--shared` to each command.
See `qctl explain pii-access-request` for request limits and status details.

## See also

- `qctl explain data`
- `qctl explain comment-thread`
- `qctl guide edit-data`
- `qctl guide inspect-schema`
- `qctl guide triage-alerts`
