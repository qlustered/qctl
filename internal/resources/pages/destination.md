---
summary: a database qctl writes cleaned data to
aliases:
kind: Destination
schema: openapi:DestinationSchemaFull
guide:
signature: fe1be4b
---

## USAGE

    qctl apply -f <file.yaml>   # kind: Destination
    qctl apply destination -f <file.yaml>
    qctl describe destination <id>
    qctl get destinations

## EXAMPLES

    # every destination in the org
    qctl get destinations

    # full configuration of one destination
    qctl describe destination 7

    # create or update a destination from a manifest
    qctl apply destination -f destination.yaml

    # the same manifest through the generic dispatcher
    qctl apply -f destination.yaml

## GOTCHAS

- `apply -f <file> -o name` prints `destination/<id>`, including an unchanged result. Several applied documents print one reference per line in file order; prose goes to stderr.

- Integer fields in a YAML manifest reject fractional numbers during parsing (exit 2): `2.7` is refused instead of becoming `2`. Whole-valued YAML numbers such as `2.0` remain accepted.
- `describe destination <id>` rejects a non-numeric ID locally as bad usage (exit 2), before any API request.
- `apply` substitutes `${VAR}` only when it is the entire value of `host`, `database_name`, `user`, or `password`; dollar signs in other values are kept literally.
- a cloud source's manifest configures where files get picked up from (a bucket/SFTP/URL, with `schedule`/`pattern` for recurring pickup) to feed ingestion; a destination's manifest configures a database connection (`host`/`port`/`user`/`password`) that table data can be written out to — opposite ends of the pipeline, never the same manifest kind.
- there is no `delete destination` — the command tree wires `apply`, `get`, and `describe` for this resource, plus `undo`/`redo destination` for its change history.

## SEE ALSO

- `qctl explain cloud-source`
- `qctl guide onboard-table`
- `qctl undo destination <id>`
