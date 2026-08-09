# Contributing to Statline

Thanks for your interest! Issues and pull requests are welcome.

## Before you start

- For bugs, open an issue with the steps to reproduce (the bug report
  template asks for the essentials).
- For features, please open an issue first so we can agree on the shape
  before you invest time in code.

## Development setup

Statline is pure Go — no C toolchain, no external services:

```sh
go build ./...
go test ./...
go run .
```

Useful dev tools:

- `go run . seed` fills a deterministic local-only "demo" team (38 members,
  120 days of history) so every view renders at scale without touching real
  data. Point `STATLINE_CONFIG` / `STATLINE_DB` at scratch paths to keep it
  out of your real config.
- The dump harness renders any view headlessly:
  `STATLINE_DUMP=1 go test ./internal/tui/app -run TestDumpView -v` — see
  `internal/tui/app/dump_test.go` for `STATLINE_DUMP_VIEW`/`_PRE`/`_POST`.
- CI parity is `golangci-lint run` (errcheck included), not bare `go vet`.

## Pull requests

- Keep PRs focused; unrelated refactors make review slower.
- CI must pass: build + tests on Linux/macOS/Windows, `-race`, and
  golangci-lint. Run `gofmt` before pushing.
- New metrics or metric changes need golden tests in
  `internal/metrics` — the numbers are the product, so they're pinned
  exactly.
- User-visible changes get a line in `CHANGELOG.md` under the unreleased
  version.
- Commits are squash-merged, so a clean PR title and description matter
  more than individual commit messages.

## Metric definitions

`internal/metrics` is the single source of truth for what every number
means (see the package doc and README "Metric definitions"). If a change
alters a definition, call it out explicitly in the PR — agreement on
definitions matters more than the code.
