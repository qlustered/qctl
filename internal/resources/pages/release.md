---
summary: the qctl builds qctl keeps for each server's atlas version
aliases:
kind:
schema:
guide:
signature: 6fa96d5
---

## USAGE

    qctl cache releases gc
    qctl cache releases list
    qctl cache releases pin <version>
    qctl cache releases unpin <version>

## EXAMPLES

    # every installed or pinned release, and the saved contexts whose server needs it
    qctl cache releases list

    # the same rows, for a script
    qctl cache releases list -o json

    # what gc would keep and remove, and why; removes nothing
    qctl cache releases gc --dry-run

    # remove the releases nothing needs any more
    qctl cache releases gc

    # keep a release however long it goes unused -- it need not be installed yet
    qctl cache releases pin 2.135.1

    # let gc remove it again once nothing else keeps it
    qctl cache releases unpin 2.135.1

## GOTCHAS

- a release is the qctl tagged MAJOR.MINOR.PATCH that a command runs in place of the invoked qctl when the server's atlas has that MAJOR.MINOR (`qctl explain context` has the rules). It lives at `~/.qctl/versions/<version>/qctl` and is recorded in `~/.qctl/versions/index.json`; `--config` moves neither.
- `gc` keeps a release while a saved context's server last required it, while it is pinned, while a qctl runs or builds it, and for 30 days after it was last used (or installed, if it never was); it removes every other directory under `~/.qctl/versions` named MAJOR.MINOR.PATCH, including one the index does not record, which it ages by its modification time.
- "required by a context" is the release the server's last answered version check selected. `list` and `gc` contact no server, so a server that is unreachable now keeps what it last required, and a server never checked keeps nothing. The contexts are the ones in the config file qctl reads (`--config` included); a server reached only through `--server` or `QCTL_SERVER` keeps nothing.
- a removed release is built again from source (git, make and Go) by the next command against a server that needs it, so the first command after a `gc` can take minutes. Pin a release you need without network access to the source repository.
- `gc` also removes the build directories under `~/.qctl/versions/.staging` that are more than an hour old and whose build is not running; a younger one is kept even when its build was killed.
- `gc` keeps whatever it cannot prove idle: where the platform has no file locks, every release stays, with the reason `cannot prove it is idle`.
- `gc --dry-run` decides exactly as `gc` does, holding the same locks only while it looks, so a release it reports `in use` may be removable a moment later. The real run re-reads the index under its lock before it removes anything, so a release used since the preview is kept.
- `gc` removes nothing but `~/.qctl/versions/<version>/` directories, their index entries and `~/.qctl/versions/.staging/<version>-*/` directories: not the qctl you installed, not Go's caches, not the lock files or per-server version records beside them. It leaves alone any entry whose name is not MAJOR.MINOR.PATCH (`v2.135.1`, `2.135`, `2.135.1-rc1`), and prints every decision before it exits 1 if any removal failed.
- `pin` takes any MAJOR.MINOR.PATCH, installed or not, and builds nothing; a leading `v`, a missing patch, a suffix or a leading zero exits 2, as does the same `<version>` on `unpin`. A pin only keeps a release through `gc`: the server's version, never a pin, chooses the release a command runs.
- `list` prints what the index records: a version directory it has no entry for is not listed, but `gc` reports it.
- `list -o json|yaml` prints one row per release: `version`, `installed`, `os`, `arch`, `commit`, `installed_at`, `last_used`, `pinned`, `contexts`. `gc -o json|yaml` prints `dry_run` and one entry per decision: `kind` (`release` or `staging`), `name`, `path`, `action` (`keep` or `remove`), `removed`, `reasons`, `reason`, `contexts`, `error`. There is no `-o name`: a release is not a `kind/<id>`.

## SEE ALSO

- `qctl explain context`
- `qctl guide switch-context`
