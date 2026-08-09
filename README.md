# Statline

> GitHub team statistics in your terminal.

Statline (`gh statline`) is a terminal dashboard for the numbers behind your
team's pull-request workflow — who's opening, merging, reviewing, approving,
and commenting, across the repos your team actually works in.

## Features

- **Team leaderboard** — one sortable stat line per member: PRs opened and
  merged, reviews given (approved / commented / changes requested), comments
  given vs received, median cycle time (open → merge), median time to first
  review, and median PR size.
- **Charts** — a 3×3 dashboard that always fits one screen: PR throughput,
  review outcomes, cycle-time trend, who-reviews-whom matrix, first-review
  latency, PR sizes, open-PR aging, and an activity punch card, topped by
  stat tiles comparing the window to the previous one (▲/▼ deltas appear
  once the cache covers both). Any card expands to a scrollable fullscreen
  view; the review matrix pans with its name labels pinned.
- **Trends** — where the numbers are heading: a card per headline metric
  showing its 12-week trajectory with week-over-week deltas, plus a movers
  card for the members whose recent volume rose or fell the most
  ("reviews 2 → 8, up 4 weeks running"). Cards expand to fullscreen —
  count metrics break down into per-member weekly sparklines, movers drop
  the top-3 cap. Weekly buckets are fixed regardless of the time window,
  and the series only reaches as far back as the cache honestly covers.
- **Person drill-down** — headline stats, daily activity sparkline, and a
  per-repo breakdown for any teammate.
- **Time windows** — cycle 7/14/30/90-day presets with `w`, or pick a custom
  date range with `r`.
- **Teams your way** — a setup wizard imports a GitHub org team (members +
  assigned repos) into a local config you can edit freely: add contractors,
  hide alumni, track repos the team isn't formally assigned. Multiple team
  profiles, switchable in-app with `t`.
- **Local cache** — incremental sync into SQLite (pure Go, no CGO): instant
  startup, offline browsing, no re-fetching what you already have. A
  headless `gh statline sync` keeps the cache warm from cron.
- **Markdown export** — `y` copies the current view as a Markdown table for
  standups, retros, and 1:1 notes.
- Keyboard-first (vim keys + arrows) with clickable tabs and rows, wheel
  scrolling, and an adaptive light/dark Charm-style theme.

## Install

As a GitHub CLI extension (recommended):

```sh
gh extension install byte2pixel/gh-statline
gh statline
```

Standalone binaries are attached to each [release](https://github.com/byte2pixel/gh-statline/releases)
(download, rename to `gh-statline`, put it on your PATH), or build from
source with `go install github.com/byte2pixel/gh-statline@latest`.

## Auth & scopes

Statline reuses your GitHub CLI credentials (`gh auth login`), falling back
to `GITHUB_TOKEN`. It needs the `repo` and `read:org` scopes.

## Usage

```sh
gh statline              # open the TUI (first run launches the setup wizard)
gh statline init         # add another team profile
gh statline sync         # refresh the cache without the TUI (cron-friendly)
gh statline sync --team platform --backfill 180
```

### Keys

| Key | Action |
|---|---|
| `1` / `2` / `3` / `tab` | Leaderboard / charts / trends |
| `enter` / `esc` | Drill into member / back |
| `j`/`k`, arrows | Move selection |
| `h`/`l`, `←`/`→` | Change sort column |
| `-` | Flip sort direction |
| `f` / `enter` | Expand the focused card (charts, trends) |
| `j`/`k` · `h`/`l` | Scroll / pan a fullscreen card |
| `pgup`/`pgdn`, `d`/`u`, `g`/`G` | Page / half-page / jump in a fullscreen card |
| `w` | Cycle time window |
| `r` | Custom date range |
| `t` | Switch team |
| `s` | Sync now |
| `y` | Copy view as Markdown |
| `?` | Full help |
| `q` | Quit |

## Configuration

Lives at `~/.config/gh-statline/config.yml` (Windows:
`%AppData%\gh-statline\config.yml`) — wizard-written, human-editable:

```yaml
default_team: platform
exclude_bots: ["*[bot]", "dependabot*", "renovate*"]
teams:
  - name: platform
    org: acme
    gh_team_slug: platform-eng   # provenance of the import; optional
    # no_sync: true              # local-only profile; sync never touches GitHub
    members:
      - {login: alice}
      - {login: bob, hidden: true}   # kept in cache, hidden from views
    repos:
      - {owner: acme, name: api}
      - {owner: acme, name: web}
sync: {backfill_days: 120, page_size: 25, concurrency: 3}
```

The SQLite cache lives in the user cache dir and is safe to delete — it
just re-syncs.

## Metric definitions

- Counts are attributed to the person acting: reviews to the reviewer,
  comments to their author. Commenting on your own PR never counts.
- Review-thread replies arrive as GitHub "commented" reviews; v0.1 counts
  them as such (a known inflation of the commented bucket).
- Time to first review ignores bots and the PR author.
- Medians use the lower-middle value; a `–` means no data in the window.
- The `updatedAt`-ordered incremental walk cannot see PRs untouched since
  before the backfill horizon (default 120 days) — deepen with
  `sync --backfill N`. Fetched data is never deleted, so local coverage
  grows the longer you use statline; the tile deltas turn on per window
  once the cache provably covers the previous period.

## Development

```sh
go build ./...   # pure Go, no C toolchain needed
go test ./...
go run .
go run . seed    # dev helper: seeds a deterministic local-only "demo" team
                 # (38 members, 120d of history) to inspect charts at scale
```

To render views headlessly (sizes, scroll states, seeded data), use the
dump harness: `STATLINE_DUMP=1 go test ./internal/tui/app -run TestDumpView -v`
— see `dump_test.go` for the `STATLINE_DUMP_VIEW`/`_PRE`/`_POST` options.

Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea) v2,
[Lip Gloss](https://github.com/charmbracelet/lipgloss),
[Bubbles](https://github.com/charmbracelet/bubbles),
[ntcharts](https://github.com/NimbleMarkets/ntcharts),
[BubbleZone](https://github.com/lrstanley/bubblezone),
[Harmonica](https://github.com/charmbracelet/harmonica),
[go-gh](https://github.com/cli/go-gh), and
[modernc.org/sqlite](https://gitlab.com/cznic/sqlite).

## License

MIT
