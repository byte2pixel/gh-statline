# Changelog

## v0.1.0 (unreleased)

Initial release.

- Team leaderboard: PRs opened/merged, reviews by state (approved /
  commented / changes requested), comments given vs received, median cycle
  time, time-to-first-review, and PR size per member; sortable, responsive
  columns.
- Charts dashboard: stat tiles, PR throughput over time, review-load bars
  with spring animation.
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
