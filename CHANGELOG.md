# Changelog

## Unreleased

- `o` opens a pull request from the "Open PRs now" card in your browser:
  the oldest while the card has the focus on the charts grid, or the marked
  row once the card is expanded, where `j`/`k` walk the five stalest. The
  card was the one place in the app that named a PR and then left you to
  type its number into a browser yourself. The URL is built for the host
  `gh` is logged in to, so it works on Enterprise Server, and the launcher
  is `gh`'s own: `GH_BROWSER`, then `gh config get browser`, then
  `BROWSER`, then the OS default (#53).
- `--config` and `--db` on every command point statline at another config
  file and cache, and `--version` works on the root command. The env vars
  `STATLINE_CONFIG` and `STATLINE_DB` did the same, but only from the
  environment, which is awkward for a one-off run and easy to leave set by
  mistake; a flag outranks its env var, as in `gh`. `gh statline --version`
  prints the same line as `gh statline version`, so either spelling answers
  the first question in a bug report (#53).
- `gh statline export` prints any view (team, person, trends, sync status)
  as Markdown, CSV or JSON, to stdout or a file. Export existed in exactly
  one form before this: press `y`, get Markdown, on the clipboard, which is
  no use to a spreadsheet, a dashboard, or anything running from cron. Like
  `doctor` it reads the cache only, so it works offline and on local-only
  teams, and `--format md` is byte for byte what `y` copies. `sync --json`
  and `doctor --json` finish the headless path with one parseable object
  saying which repo failed, when it last synced cleanly, and how far back
  the cache covers, in place of `Printf` output a script would have to
  scrape. All three name their per-repo fields identically (#52).
- The three formats render from one representation instead of three
  renderers that would drift apart on the first metric change. A view
  declares its columns once, with a stable machine key beside each display
  heading, so rewording a column in the UI cannot rename it in someone's
  spreadsheet. The "no data" sentinels collapse at that boundary too: a
  median nobody has a sample for is `null` in JSON and an empty CSV field,
  never the `0` or `-1` the metrics layer uses internally, which a dashboard
  would have averaged in as a measurement (#52).
- `R` opens a repo picker that narrows every view to a subset of the
  team's repos: the team table, the charts and their tiles, the trends, the
  person drill-down, and what `y` copies. The header names the repo, or
  says `2/5 repos`, and the exported heading lists them, so a table pasted
  into a per-repo standup cannot pass for the whole team's numbers. The
  list scrolls inside the terminal and `/` narrows it by name, with `a`
  and `n` checking or clearing whatever is shown, so a team with a hundred
  repos gets down to the three that matter in a few keystrokes. The
  filter is one-shot per session, like a custom date range, and a team
  switch drops it. The metrics layer had supported this from the start,
  correctly, behind a field nothing in the UI ever set; the SQL that
  applies it appends its arguments by hand, so the remaining entry points
  got golden tests before the key went live (#51).
- The app has a sync-status view on `S`, and the status bar carries
  `⚠ N repo(s) failing` for as long as any repo is failing. The count is
  read from the cache rather than from the running session, so a repo that
  broke yesterday is a warning the moment the app opens, not something you
  learn if you happen to watch the next sync. The view lists every repo
  with its last clean sync, the date the cache covers back to, and the
  failure in full underneath the row it belongs to; `y` copies the lot as
  Markdown. Repo failures no longer take over the status bar either: one
  wrapped API error truncated into that line used to hide the freshness
  message behind it until the next sync, while saying nothing about which
  of the other repos were also stale (#50).
- `gh statline doctor` reports per-repo sync health from the cache: when
  each repo last completed a walk, how far back the cache honestly covers,
  and the error from its last failed sync. `sync_state` has recorded all of
  this since the schema was written and nothing ever read it back, so a repo
  that fails every sync — renamed, made private, deleted — showed up only as
  numbers that stopped moving, which looks exactly like a quiet week. The
  command reads the cache only, so it works offline and on local-only teams,
  and it exits non-zero when any repo is failing, which is what makes it
  useful next to `sync` in cron. A repo that no longer resolves gets told so
  in as many words: sync targets come from the config file, so that failure
  never self-heals (#50).
- A cache written by a newer version of statline is now refused on open,
  with an explanation, instead of opening and failing later. Migrations run
  when their version is above `PRAGMA user_version`, so a database from a
  newer build had nothing left to apply and opened cleanly; the mismatch
  surfaced afterwards as scattered `no such column` errors with nothing
  pointing at the cause. Reachable by rolling back an extension upgrade, or
  by syncing a cache directory between machines on different versions. The
  error names both schema versions and the cache path, and says to delete it
  or upgrade (#49).
- In-app config changes are validated before they reach disk, and the write
  is flushed before the rename. A mutation that produced an invalid config
  used to write a file the next start rejected outright, so the failure
  landed far from the change that caused it; it is reported on the spot now
  and the last good file is left untouched. The temp-file-and-rename dance
  also promised more than it delivered, since without an fsync a power loss
  between the write and the rename can publish a zero-length config (#49).
- Expanding the `?` help no longer pushes the bottom of the frame off
  screen. The layout budgeted three rows for the full help while it renders
  one row per binding in its tallest column, which is six, so the status bar
  and footer ran past the last line of the terminal. The layout measures the
  footer now, so it stays right as bindings come and go. The test for it
  found one more row: the team table sized its viewport against a header it
  did not have yet, leaving that page one row too tall from startup until
  the first resize (#54).
- Rate-limit reset times now name their zone, as in `rate limited until
  17:04 CDT`, in the TUI and in `gh-statline sync`. They are the only clock
  times statline shows in local time rather than UTC, because they answer
  "when can I retry" rather than "when did this happen", and unlabelled they
  were a coin flip. The README states the rule, including the punch card's
  deliberate local-time bucketing (#54).
- `ui.theme: light|dark` pins the palette instead of asking the terminal for
  its background color. Terminals that never answer that query, among them
  older conhost and some tmux and CI setups, kept the dark-assumed default
  on a light background. The chrome was hard to read, the light-mode chart
  ramps never engaged, and there was no way to say otherwise. A pinned theme
  also skips the query. Hand-edited only, since the app never writes the key
  and leaves a config that doesn't set it alone (#54).
- `GH_HOST` and a `gh` logged in to a GitHub Enterprise Server are no longer
  ignored. Token lookup and the API endpoint both read a hardcoded
  `github.com`, so an enterprise-only login failed with "no GitHub
  credentials found" and no hint at which host statline had searched. Both
  now resolve the host the way `gh` does, and the failure names that host
  and the env var that works there, which is `GH_ENTERPRISE_TOKEN` outside
  github.com. The queries are still only tested against github.com, so GHES
  is best effort (#54).
- An empty charts, trends, or team view on a local-only (`no_sync`) team no
  longer says "press s to sync". That key answers "sync disabled for this
  team", so the hint sent the only people who ever read it nowhere. It now
  points them at seeding or importing data, and the trends page stops
  promising the view unlocks after the first full sync (#54).
- Sync retries now follow GitHub's instructions instead of a fixed
  2s/8s/20s/45s ladder: a `Retry-After` or spent-quota reset is honoured
  (bounded to 10 minutes and 1 hour respectively) and pauses every worker,
  not just the one that hit it. A 403 that is not a rate limit (SAML
  enforcement, missing scope, IP allowlist) fails the repo at once instead
  of after 75 seconds of retries, and a transient GraphQL error
  (`INTERNAL`, a nested-field timeout) is re-asked instead of failing the
  repo for the run. `sync.concurrency` is capped at 10, and a rate-limit
  pause is reported once with its duration rather than once per worker
  (#40).
- Charts and trends now share one card-grid shell. What changes on screen:
  the fullscreen keys (`f`, `d`/`u`, `g`/`G`, `space`, `home`/`end`) live in
  the keymap and show up in `?` help; trends fullscreen cards pan with
  `h`/`l` like the charts; `h`/`l` on the charts grid stop at the row edge
  instead of wrapping onto the next row; the movers streak badge reads
  "up 4w running" everywhere, including the Markdown export; and an exact
  tie between two metrics of one member ranks opened, merged, reviews,
  comments. The dump harness takes `STATLINE_DUMP_NOW` to pin its clock
  (#47).
- A PR whose GitHub node id changed while keeping its repo and number
  (repo deleted and recreated under the same name, PR transferred, org
  migration) used to fail that repo's sync forever; the only way out was
  deleting the cache. The stale cached row is now replaced and sync
  continues (#39).
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
