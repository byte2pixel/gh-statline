# Changelog

## Unreleased

- PR titles (and every other externally sourced string) are now sanitized
  before they reach the terminal or the Markdown export: escape sequences
  (CSI/OSC and friends) are stripped whole, so a hostile title in a watched
  repo can no longer retitle your terminal window or inject control
  sequences into a paste. Truncation and column padding now count display
  cells instead of bytes/runes, fixing mojibake on narrow windows and
  misaligned charts for CJK/emoji content. Config validation additionally
  rejects team names, orgs, logins, and repos containing control
  characters (#43).
- A failed sync can no longer damage a repo's incremental bookkeeping.
  The failure path used to write the whole `sync_state` row back and, if
  the preceding read failed, that write NULLed the watermark and backfill
  floor, forcing a full re-backfill and blanking trends until it
  finished. Failures now record only `last_error`, `last_synced_at` means
  "last successful walk" instead of being bumped on failures, and
  quitting mid-sync no longer leaves `last_error = "context canceled"`
  behind (#38).
- Incremental sync can no longer silently lose PRs to a mid-walk reorder:
  after any walk that spanned multiple pages, the engine re-probes the top
  of the PR list and re-walks (up to 3 attempts) if anything changed
  underneath it. A PR whose `updatedAt` is missing or zero now fails that
  repo's sync instead of masquerading as the stop condition and truncating
  the walk. `backfill_until` records the depth a walk actually reached
  rather than the configured horizon, so coverage-gated views (tile
  deltas, trend length) can briefly shorten for a repo that had
  over-claimed — it deepens again automatically on the next sync (#37).
- Quitting during a sync now cancels it and exits once the last write has
  landed, instead of leaving the sync writing to a database the app had
  already closed. Press quit a second time to leave without waiting.
  Switching teams cancels the old team's sync and starts one for the new
  team; before, the old sync kept running and the new team did not sync
  until you pressed `s`. Also closed: a short startup window where `s`
  could launch a second concurrent sync over the same cache (#36).
- Percent changes are now computed one way everywhere: rounded to the
  nearest whole percent (`metrics.PctChange`). The stat tiles used to
  truncate while the movers card rounded, so the same 7→10 week could read
  ▲42% on a tile and +43% in the movers list (#41).
- Movers: a member whose prior weeks were empty is now flagged as `new`
  instead of being clamped to +100%. New activity ranks ahead of every
  percentage riser (ordered by volume among itself), so a 0→20 ramp can no
  longer rank below a 6→18 tripling or drop off the card entirely. The
  "new" label and streak-badge rules are now defined once in `metrics` and
  shared by the trends card, its fullscreen view, and the Markdown
  export (#41).
- Fix: the review matrix counted reviews on bot-authored PRs (dependabot,
  renovate, and friends) in its "(others)" column, and folded that
  column's running total into the heat-map maximum, washing out the color
  ramp across the real member↔member cells. Bot-authored PRs are excluded,
  and "(others)" no longer sets the scale (#41).

- The default `exclude_bots` globs now include `copilot*`, so Copilot code
  review (login `copilot-pull-request-reviewer`, no `[bot]` suffix) is
  excluded by the glob backstop and not only by the synced bot flag.
  Configs written by older versions keep their saved list; add the glob by
  hand to get the same cover (#62).
- `gh statline sync` now exits non-zero when any repo fails to sync, so
  cron jobs and scripts notice stale data instead of silently trusting old
  numbers.
- Reviews GitHub has marked dismissed now count as reviews given and get
  their own `Dism` column. The team table was the only view that dropped
  them, so a person's review total could be lower there than in the trends,
  the review matrix, or their own per-repo breakdown (#35).
- Fix: hidden members and bots were excluded from the team table but still
  counted in every chart, so the throughput card could disagree with the
  "PRs opened" tile for the same window, and hiding a teammate did not
  remove them from the charts. A team member flagged as a bot by GitHub also
  kept its own stat line unless a glob happened to match its login (#34).
- Fix: copying the open-PR aging card as Markdown produced a broken table
  whenever a pull-request title contained a `|` or a line break. Cell
  contents are now escaped, and numeric columns line up to the right like
  the team stats table (#45).
- Fix: sorting by Cycle, TTFR, or Size put the members with no data at the
  top instead of the bottom, so the default descending sort could open on a
  screen of dashes. They now sort last whichever way the column is sorted (#42).
- Fix: a custom date range labelled itself with your local calendar day
  while covering UTC days, so the header could name a day the numbers did
  not cover (#42).
- Fix: switching teams while a sync was running rewrote the repo list that
  sync was still walking, so pull requests could be cached against another
  team's repository (#32).
- Fix: the SQLite cache and its write-ahead log were created world-readable,
  exposing pull-request titles from private repositories to other users on
  the machine. They are now owner-only, and existing caches are tightened
  the next time statline opens them (#33).

## v0.3.0 (2026-08-12)

- Teams can now be deleted from inside the app: press `d` in the team
  switcher (`t`), confirm with `y`. Deleting the active team switches to
  the first remaining team in the config; the last team can't be
  deleted (#21).
- Fix: the who-reviews-whom matrix now fits 3-digit review counts instead
  of truncating them; cells are right-aligned like the punch card (#23).

## v0.2.0 (2026-08-11)

- Statline now remembers how you leave it: switching teams updates
  `default_team`, and time-window or sort-column changes persist under
  `ui:`, so the next launch reopens the same view. Custom date ranges
  and `--team` remain one-shot. These in-app saves rewrite `config.yml`,
  so hand-written YAML comments there don't survive a session.

## v0.1.1 (2026-08-10)

- Fix: the `/` filter in the setup wizard's org and team pickers now
  actually narrows the list (#15).
- Fix: chart cards on the Charts tab respond to mouse clicks — first
  click focuses, second click expands (#16).

## v0.1.0 (2026-08-09)

Initial release.

- Team stats: PRs opened/merged, reviews by state (approved / commented /
  changes requested), comments given vs received, median cycle time,
  time-to-first-review, and PR size per member; sortable, responsive
  columns.
- Charts dashboard: a 3×3 card grid that always fits one screen — PR
  throughput, review outcomes, cycle-time trend, who-reviews-whom matrix,
  first-review latency, PR sizes, open-PR aging, and an activity punch
  card, with spring-animated bars. Cards expand to scrollable fullscreen
  views; the review matrix pans with pinned name labels. Throughput
  buckets adapt to the window (6-hour at 7d up to 3-day at 90d).
- Stat tiles compare the current window to the previous one (▲/▼ deltas,
  plus team cycle/TTFR p50 tiles on wide terminals), shown only once the
  cache provably covers the earlier period.
- Trends tab: every headline metric as a card with its 12-week trajectory
  and week-over-week delta, plus a movers card ranking the members whose
  recent 4 weeks shifted most vs the prior 4 (percent change gated by
  per-metric volume floors, streak badges). Cards expand to fullscreen —
  count metrics break down into per-member weekly sparklines, movers drop
  the top-3 cap. Weekly buckets are fixed regardless of the time window
  and truncate at the cache's proven coverage instead of showing zeros.
- Hidden `seed` command generating a deterministic local-only demo team
  (38 members, 120 days) for development and screenshots; `no_sync: true`
  marks a team profile as never fetched from GitHub.
- Person drill-down: headline stats, daily activity sparkline, per-repo
  breakdown.
- Time windows: 7/14/30/90-day presets plus custom date ranges.
- First-run setup wizard importing a GitHub org team; `init` adds more
  team profiles; in-app team switcher.
- Local SQLite cache with watermark-based incremental sync, concurrent
  repo walks, shared rate limiting, and retry with backoff.
- Headless `sync` command for cron use.
- Markdown export of any view to the clipboard.
- Keyboard-first controls with clickable tabs/rows and wheel scrolling.
- Light/dark adaptive Charm-style theme; colorblind-safe chart palette.
