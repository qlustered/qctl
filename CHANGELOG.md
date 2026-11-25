# Changelog

User-facing changes in each qctl release. A release `X.Y.Z` supports the
Qluster server minor `X.Y`.

## 2.140.0

For Qluster server 2.140.

- make release accepts an SSH-cloned mirror
- make release drafts a missing changelog section instead of refusing
- TableRuleTemplate: declarative diff/apply of a report's rules on a table
- TableRule create relies on server auto-mapping and prints resolved bindings
- Refresh Go dependencies and security fixes
- apply -f TableSeries: refuse a plan that touches a locked member before any write

## 2.136.0

First public release of qctl, the Qluster command-line interface.

- Manage tables, rules, files, cloud sources, jobs, alerts, reviews and
  deliverables with kubectl-style verbs: `get`, `describe`, `apply -f`,
  `create`, `delete`, and `-o json|yaml|name` output.
- `qctl guide`, `qctl api-resources` and `qctl explain` document
  workflows and resources from the command line.
- qctl runs the release built for your server's version, building it from
  this repository's tag on first use.
- `qctl skill install claude-code|codex` installs an operating skill for
  coding agents.
