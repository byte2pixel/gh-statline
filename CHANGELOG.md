# Changelog

## v0.1.0 (unreleased)

Initial release.

- Team leaderboard: PRs opened/merged, reviews by state (approved /
  commented / changes requested), comments given vs received, median cycle
  time, time-to-first-review, and PR size per member; sortable, responsive
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
