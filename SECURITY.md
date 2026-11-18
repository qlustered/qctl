# Security policy

## Reporting a vulnerability

Please report security vulnerabilities privately, not in a public issue.

Use GitHub's private vulnerability reporting: open the
[Security tab](https://github.com/qlustered/qctl/security) of this repository
and choose **Report a vulnerability**. Only the maintainers can see the
report.

Include the qctl version (`qctl version`), your operating system, the steps
to reproduce the problem, and its impact as you understand it. We will
acknowledge the report, keep you informed while we investigate, and credit
you in the release notes when the fix ships, unless you prefer otherwise.

## Supported versions

Each qctl release supports one Qluster server minor. Fixes ship as a new
patch release of each affected minor; qctl picks up the newest patch of your
server's minor automatically.
