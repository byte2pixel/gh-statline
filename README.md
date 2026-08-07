# Statline

> GitHub team statistics in your terminal.

Statline (`gh statline`) is a terminal dashboard for the numbers behind your
team's pull-request workflow — who's opening, merging, reviewing, approving,
and commenting, across the repos your team actually works in.

**Status: pre-release, under active development.**

## Features (v0.1 roadmap)

- **Team leaderboard** — sortable table: PRs opened/merged, reviews given
  (approved / commented / changes requested), cycle time, PR size, comments
  given vs received, per member.
- **Charts** — PR throughput over time, review load, sparkline trends.
- **Person drill-down** — per-repo breakdown and trends for one teammate.
- **Time windows** — 7/14/30/90-day presets plus custom date ranges.
- **Teams your way** — import a GitHub org team, then add/remove members and
  repos freely in local config. Multiple team profiles, switchable in-app.
- **Local cache** — incremental sync into SQLite; instant startup, works
  offline, no re-fetching what you already have.
- **Markdown export** — copy any view as a Markdown table for standups/1:1s.
- Keyboard-first (vim keys + arrows) with mouse support for the basics.

## Install

Once released:

```sh
gh extension install byte2pixel/gh-statline
gh statline
```

Standalone binaries will also be attached to each GitHub release.

## Auth

Statline reuses your GitHub CLI credentials (`gh auth login`), falling back to
`GITHUB_TOKEN`. It needs `repo` and `read:org` scopes.

## Development

```sh
go build ./...
go test ./...
go run .
```

Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea),
[Lip Gloss](https://github.com/charmbracelet/lipgloss),
[Bubbles](https://github.com/charmbracelet/bubbles),
[ntcharts](https://github.com/NimbleMarkets/ntcharts),
[BubbleZone](https://github.com/lrstanley/bubblezone), and
[Harmonica](https://github.com/charmbracelet/harmonica).

## License

MIT
