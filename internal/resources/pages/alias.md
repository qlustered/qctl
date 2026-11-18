---
summary: short local names qctl remembers for UUIDs
aliases:
kind:
schema:
guide: use-aliases
signature: c960b5d
---

## USAGE

    qctl cache aliases clear
    qctl cache aliases list
    qctl cache aliases resolve <alias>

## EXAMPLES

    # every alias qctl has cached for this server
    qctl cache aliases list

    # the full UUID behind a persisted alias
    qctl cache aliases resolve r-1

    # forget the cached aliases for this server
    qctl cache aliases clear

    # forget them for every server
    qctl cache aliases clear --all-servers

## GOTCHAS

- `cache aliases resolve` exits 3 for a well-formed alias absent from the current server's cache, including when no cache file exists yet. A malformed alias exits 2.
- only two commands mint aliases, for each table rule their default output names: `get data --include-dataset-rule-ids` (the Triggered Rules column) and `describe row`. Neither mints under `-o json|yaml`, and no other command mints one — `apply` and `get table-rules` print full UUIDs. Today only table rules (prefix `r`) get an alias; integer-ID resources are never aliased.
- the same store also caches rule-revision SHORT-IDs — the 8 hex digits `get rules` prints — but those are not aliases: nothing mints an `r-N` for them, `cache aliases list` and `cache aliases resolve` never show them, and they are scoped per org as well as per server. They are what lets `qctl get rule 660e8400` expand a SHORT-ID locally, which matters because the backend cannot search by id.
- `cache aliases list -o json|yaml` prints one row per alias — `alias`, `resource`, `uuid` — and `[]` when the server has none, where the table says `No aliases for the current server.`. `resource` is the catalog name (`table-rule`) while the table's RESOURCE column keeps the store's own `dataset_rule`. There is no `-o name` view: an alias is not a `kind/<id>`.
- they're scoped per server (keyed by the same normalized endpoint used for credentials) — the same alias number on two different servers can point at two unrelated table rules.
- clearing is safe — nothing on the backend knows about aliases, so deleting the cache can't affect a table rule — but the clear wipes the counter along with the entries: the next allocation starts back at `r-1` for whichever rule gets minted first, not necessarily the rule that used to be `r-1`. Don't assume an alias means the same thing after a clear.
- `cache aliases clear` drops the server's whole bucket, so it discards the cached SHORT-IDs too: nothing breaks, but the next `qctl get rule <short-id>` pays for a catalog listing to re-learn it, and in an org with more rule revisions than that listing's 1000-row cap the SHORT-ID stops resolving until something reseeds it (e.g. `qctl get rules`).

## SEE ALSO

- `qctl guide use-aliases`
- `qctl explain table-rule`
