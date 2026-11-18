# Fix values in quarantine

Find the rows, preview the change, write it with `--yes`, then read the rows
back. Every write here lands on QUARANTINED rows only; `qctl rows
send-to-quarantine` brings a clean row into reach. Edit only what the user
asked you to change. Every flag and edge case: `qctl explain data`.

## Find the rows

    qctl get data orders --is-bad-data --include-dataset-rule-ids
    qctl get data orders --is-bad-data --search-query "N/A"
    qctl get data orders --is-bad-data --filter-by-field-name state --filter-by-field-value Calif.
    qctl get data orders --is-bad-data --alert-item-id 8817

Each flagged cell carries an `[A<id>]` or `[W<id>]` marker. The `id` column is
what `set-cell` takes. Reading the listing: `qctl guide inspect-data`; reading
the alert: `qctl guide triage-alerts`.

## Preview, then replace

    qctl rows search-replace orders --search "N/A" --replace "" --dry-run
    qctl rows search-replace orders --search "N/A" --replace "" --field notes --dry-run
    qctl rows search-replace orders --search "N/A" --replace "" --field notes --yes

The preview is the route's own dry run: it prints the match count and writes
nothing. Without `--field` the count is per field, so a row matching in two
columns counts twice. With `--yes` every matching cell is written in one
uncapped transaction, so the preview count is the size of the write.

Matching is a case-insensitive substring with Excel wildcards (`*`, `?`, `~`
to escape). `--case-sensitive` makes it exact-case; `--regex` makes `--search`
a Python `re` pattern and `--replace` an `re.sub` template:

    qctl rows search-replace orders --search '^\s+' --replace "" --regex --field city --dry-run

A pattern the server cannot compile is a 422 on the preview, exit 2.

## Fix one cell

    qctl rows set-cell orders --row-id 1042 --row-id 1043 --field state --value CA --yes
    qctl get data orders --is-bad-data --filter-by-field-name id --filter-by-field-value 1042

The route reports no count of any kind: exit 0 means "accepted", never
"changed" — an id from the clean listing matches nothing and still answers
ok. Read each row back. Writing a flagged cell clears its marker; an alert
resolves once no quarantined row carries it. `--value ""` writes an empty
string, not NULL.

A `set-cell` value is also a pin, marked `[S]` in `get data`. Undo works one
eligible change batch of your own at a time in reverse order within its scope:
your later eligible changes must be undone before the cell edit can be undone.

## Pick a value

    qctl get value-recommendations orders --field state --value Calif. --alert-item-id 8817
    qctl get value-recommendations orders --field state --value "" -o name

Best first; STRONG marks values the server believes valid. An empty `--value`
lists what the field normally holds. It is a read: nothing is written until
you `set-cell` the value or resolve the alert with it. Read the whole list —
the ranking is brute force.

## Check a key

    qctl rows check-unique orders --column order_id
    qctl rows check-unique orders --column region --column order_id

Repeated `--column`s form one key. Exit 0 is unique, 1 (stderr empty) is not
unique, with up to five duplicate groups printed. It reads CLEAN rows only:
fix and re-validate first, then ask.

## What happens next

Rows a write touched are queued for re-validation after it commits. A row
that passes returns to the clean table unless the table holds it; one that
still fails keeps its other markers. Until the queue drains the row reads as
awaiting processing, and a second write matches nothing:

    qctl get data orders --is-bad-data --awaiting-processing=false --filter-by-field-name id --filter-by-field-value 1042

A write takes back a release. A passing row can still be held for approval
(a possible duplicate, a review policy, or `quarantine_rows_until_approved`);
it then shows `held-for-approval=<reason>` until `qctl rows release` or an
approved data review lets it go. Under a review policy or
`quarantine_rows_until_approved` your own release skips a row you edited, so someone else has to approve it:

    qctl get data orders --is-bad-data --held-for-approval
    qctl describe table orders

While a submitted data review of the row is not closed, Row State reads
`waiting-for-approval` instead, and `--waiting-for-approval` lists those rows.
`describe table` counts the waiting rows, and splits the held ones by reason.
A row someone sent to quarantine by hand keeps its manual hold until it is
released, whatever you fix (`qctl guide triage-alerts`). On a linked (composite) table `set-cell` is
a cell override plus a recompute instead; see `qctl rows set-cell --help`.

## Read the record

    qctl get history --table orders --model TableValueOverwrite --field state
    qctl get data orders --is-bad-data --show-row-logs --filter-by-field-name id --filter-by-field-value 1042

The history item names the field, old and new value, author and time, but not
the rows; the row's own `row_logs_` does. Once a row passes, read its log from
the clean page, without `--is-bad-data`.

## See also

- `qctl explain data`
- `qctl explain history`
- `qctl guide inspect-data`
- `qctl guide triage-alerts`
- `qctl guide data-review-policy`
