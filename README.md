# qctl

qctl is the command-line interface for [Qluster](https://qluster.ai). It
manages an organization's tables, rules, files, jobs, alerts and reviews,
following kubectl's conventions: `get`, `describe`, `apply -f <manifest>`,
`-o json|yaml|name`.

> **This repository is a release mirror.** qctl is developed in a private
> repository; each release appears here as one commit with its tag. Issues are
> welcome — see [CONTRIBUTING.md](CONTRIBUTING.md). Pull requests are not
> accepted.

## Install

qctl needs Go 1.26.11 or later.

```bash
go install github.com/qlustered/qctl/cmd/qctl@latest
```

Or build from source:

```bash
git clone https://github.com/qlustered/qctl.git
cd qctl
make build      # writes ./qctl
make install    # or install it into $GOPATH/bin
```

Release tags are bare `MAJOR.MINOR.PATCH` (`2.136.0`), not Go module
versions such as `v2.136.0`. `go install …@2.136.0` installs that tag's commit
under a pseudo-version (`v0.0.0-…`), not as release `2.136.0`. You rarely need
to pin one: qctl runs the release that matches your server, as described next.

## Versions

A qctl release `X.Y.Z` supports exactly one Qluster server minor, `X.Y`; the
patch numbers are independent. Before a command that talks to a server, qctl
asks the server for its version and, when a qctl release exists for that
minor, runs the newest patch of it. The first time, it builds that release
from this repository's tag into `~/.qctl/versions/` (git, make and Go must be
on `PATH`). So whichever qctl you installed, every command runs the release
built for your server.

When no release exists for the server's minor, qctl prints a warning and runs
the qctl you invoked. `qctl cache releases gc` removes cached builds. Running a
release from a cache is supported on Linux and macOS; elsewhere qctl warns
and runs itself.

## Get started

```bash
# Point qctl at your Qluster server and log in through the browser.
qctl config set-context prod --server https://<your-qluster-server>
qctl auth login

# Find your way around.
qctl guide                       # task guides, starting with discover-capabilities
qctl api-resources               # every resource qctl manages
qctl explain <resource>          # what a resource is, with examples and gotchas
qctl get tables
```

Contexts live in `~/.qctl/config`; `--context <name>` picks one for a single
command. The login token goes to the OS keyring, or to a `0600` file under
`~/.qctl` when no keyring is available.

Every context is protected by default: a destructive command must name its
context with `--context`. `--yes` does not count.

## Coding agents

qctl ships an operating skill for coding agents:

```bash
qctl skill install claude-code
qctl skill install codex
```

The skill tells the agent how to find commands (`qctl guide`, `qctl explain`),
read only the output it needs, and handle exit codes and confirmations.
`qctl skill status <target>` reports what is installed; re-running the install
updates a copy qctl installed.

Rule files that run on Qluster are written with the Python SDK,
[qluster-sdk](https://github.com/qlustered/qluster-sdk).

## License

Apache License 2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE). The license
grants no rights to the Qluster name or marks.
