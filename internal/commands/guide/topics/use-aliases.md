# Short names for long UUIDs

    qctl get data orders --include-dataset-rule-ids     # mints r-1, r-2, ... for the rules it names
    qctl describe table-rule r-1                        # an alias works wherever the UUID would
    qctl cache aliases list
    qctl cache aliases resolve r-1
    qctl cache aliases clear

A table rule (the backend's "dataset rule") is keyed by UUID. Two commands
give each table rule they print a short local alias, `r-1`, `r-2`, ...: `get
data --include-dataset-rule-ids`, in its Triggered Rules column, and
`describe row`, in its verdict table. Neither mints one under `-o json|yaml`,
and nothing else does: `apply` and `get table-rules` print full UUIDs. Only
table rules get aliases. Every rule of the store: `qctl explain alias`.

## Use one

    qctl describe table-rule r-1

A command that takes a table rule identifier accepts the alias in place of a
name or UUID, and an `r-*` alias needs no `--table`/`--table-id`, which a raw
name or UUID still does (`qctl guide triage-alerts`).

## See and expand them

    qctl cache aliases list
    qctl cache aliases resolve r-1

`list` prints ALIAS/RESOURCE/UUID for the current server. `resolve` prints
only the full UUID, so it is safe to embed in a script. Aliases live in
`~/.qctl/cache/aliases.db`, per server: the same alias on two servers can name
two unrelated rules, and nothing is synced or shared.

## Start clean

    qctl cache aliases clear

Clears the current server's aliases (`--all-servers` clears every server).
Nothing on the backend knows about them, so no table rule is affected, but the
numbering restarts at `r-1` for whichever rule is minted next: never assume an
alias means what it did before a clear. The same store caches rule-revision
SHORT-IDs, which the clear drops too; the next `qctl get rule <short-id>`
re-learns them from a catalog listing.

## See also

- `qctl explain alias`
- `qctl guide triage-alerts`
