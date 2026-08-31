# Security Policy

## Supported versions

Only the latest release is supported with security fixes.

## Reporting a vulnerability

Please **do not** open a public issue for security problems. Instead, use
GitHub's private vulnerability reporting: go to the repository's
**Security** tab → **Report a vulnerability**. You'll get a response within
a week.

## Scope notes

Statline is a local tool: it authenticates with your existing GitHub CLI
credentials (or `GITHUB_TOKEN`), reads pull-request metadata for repos you
already have access to, and stores it in a local SQLite cache. It never
transmits data anywhere except to the GitHub API. Reports about token
handling, credential leakage into the cache/config/exports, or anything
that widens that footprint are especially welcome.

No credentials are written to disk. The cache does hold pull-request titles
from private repositories, so it and the config file are created readable
only by their owner (`0600`).
