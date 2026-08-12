# Changelog

## Unreleased

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
