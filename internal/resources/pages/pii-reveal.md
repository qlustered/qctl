---
summary: one masked personal-data value shown unmasked, and recorded
aliases:
kind:
schema: openapi:PiiRevealSchema
guide:
signature: e56a55f
---

## USAGE

    qctl reveal alert <alert-id>
    qctl reveal cell [table] --field <field> --row-id <row-id>
    qctl reveal warning <warning-id>

## EXAMPLES

    # one cell of a personal-data column, value alone on stdout
    qctl reveal cell orders --row-id 1042 --field email

    # a cell of a quarantined row, as the server sent it
    qctl reveal cell --table-id 42 --row-id 7 --field phone --is-bad-data -o json

    # an alert's message with its personal data unmasked; the table is read from the alert
    qctl reveal alert 123

    # a warning's message, naming its table
    qctl reveal warning warning/88 --table orders

## GOTCHAS

- Revealing needs the pii:reveal permission, which the admin, ops and read_only roles hold. The member, editor and external roles do not hold it because they already see personal data unmasked, and the agent role cannot reveal at all; each gets a 403 (exit 4), as does a user without access to the table. [backend]
- A user who reads the table through a share from another organization cannot reveal: the route answers 403 (exit 4) under their organization. [backend]
- The server allows 20 reveals per user per table in a rolling hour, counted across qctl and the web app together; past that it answers 429 (exit 1) with a Retry-After, which qctl turns into "Try again in N minutes" and the `qctl pii-grant create` an admin can run instead. A reveal that fails (400, 404) is not counted. [backend]
- An active `pii-grant` does not lift the reveal limit; it unmasks the table's ordinary reads, so with one read the values through `qctl get data` instead. [backend]
- Every successful reveal is recorded with who asked, the table, the target and the time; a refused reveal records nothing. [backend]
- `reveal cell` refuses a column that is not one of the table's personal-data columns, a reserved column such as id included, with a 400 (exit 2), and a row that does not exist or was deleted with a 404 (exit 3). [backend]
- `reveal cell` reads the table's clean rows, or its quarantined rows under `--is-bad-data`; an id the chosen side does not hold is a 404 (exit 3), so a quarantined row needs the flag. [backend]
- A cell's value comes back as text whatever the column's type, and null as null: the default view prints nothing on stdout for null and says so on stderr, and `-o json|yaml` keep the distinction from an empty string. [backend]
- `reveal alert` and `reveal warning` without `--table`/`--table-id` read the issue first, the request `qctl describe alert` or `qctl describe warning` makes, to learn its table. An issue on another table than the one named is a 404 (exit 3). [backend]
- The default view prints only the value on stdout, so `$(qctl reveal cell ...)` captures it; the "recorded as event N" line goes to stderr. qctl never stores a revealed value, but `-v 7` logs the response body, value included, on stderr.
- There is no batch form: each command reveals exactly one target, and each successful one counts toward the limit.

## SEE ALSO

- `qctl explain pii-grant`
- `qctl explain data`
- `qctl explain alert`
- `qctl explain warning`
