# AGENTS.md — working on Statline as an agent

Statline (`gh statline`) is a Go TUI that aggregates GitHub pull-request
stats per team member across configured repos: a local SQLite cache is
filled by an incremental GraphQL sync, `internal/metrics` computes the
numbers, and Bubble Tea renders them. **The numbers are the product** —
correctness of metric definitions matters more than anything else here.

## Build, test, lint

```sh
go build ./...        # pure Go, no CGO, works on Windows/macOS/Linux
go test ./...         # full suite, no network, no real config touched
go test -race ./...   # CI runs this on Linux
golangci-lint run     # CI parity; errcheck included — bare `go vet` is NOT enough
gofmt -l .            # must be clean before pushing
```

- Go 1.26. CI matrix: Linux/macOS/Windows build+test, Linux `-race`, golangci-lint.
- Tests use `db.Open(":memory:")` and fake `gh.Doer` implementations. Nothing
  needs credentials or the network. If your test touches config persistence,
  set `t.Setenv("STATLINE_CONFIG", ...)` to a temp path (see
  `internal/tui/app/app_test.go: testDeps`) so you never clobber the
  developer's real `config.yml`.
- `STATLINE_CONFIG` and `STATLINE_DB` env vars redirect the config file and
  cache DB — use them for any manual experiment.

## Manual/visual verification without a terminal session

The TUI can be rendered headlessly against seeded data:

```sh
go run . seed                       # deterministic 38-member "demo" team, 120d history
STATLINE_DUMP=1 go test ./internal/tui/app -run TestDumpView -v
# STATLINE_DUMP_VIEW=charts|trends, STATLINE_DUMP_PRE/_POST send keystrokes, e.g. PRE="w,w"
```

Point `STATLINE_CONFIG`/`STATLINE_DB` at scratch paths first so seed data
stays out of real config. The README demo GIF is scripted in `vhs/demo.tape`.

## Architecture map

Data flow: `gh` (GraphQL) → `syncer` (incremental walk) → `db` (SQLite cache)
→ `metrics` (all numbers) → `tui/pages` + `export` (render).

| Package | Role | Notes |
|---|---|---|
| `internal/cmd` | Cobra commands: root TUI, `init`, `sync`, `seed` (hidden), `version` | `setup.go: bootstrap()` is the shared startup path |
| `internal/config` | YAML config: teams, members, repos, bot globs, UI prefs | File is source of truth; wizard writes it; in-app changes rewrite it (comments don't survive) |
| `internal/gh` | Auth, GraphQL client, query documents | `Doer` interface is the ONE seam to GitHub; tests fake it with JSON fixtures |
| `internal/db` | SQLite cache: schema, migrations, writes | Cache is disposable; deleting it costs a re-sync. Pool capped at 1 conn — drain/close `sql.Rows` before the next query |
| `internal/syncer` | Incremental PR walk, rate limiting, retry | TUI-agnostic; progress via typed `Event` channel |
| `internal/metrics` | **Single source of truth for every number** | SQL for counts, Go for medians; golden tests pin exact values |
| `internal/seed` | Deterministic fake data generator | `no_sync: true` teams are never fetched |
| `internal/export` | Markdown table export | Must match metric definitions exactly |
| `internal/tui/*` | Bubble Tea v2 app: pages, overlays, wizard, theme, keys | `app.go` routes; pages are mostly pure render from precomputed data |

### Key invariants (do not break silently)

- **All times are unix epoch seconds UTC** in the DB and in `metrics.Window`
  (half-open `[Start, End)`).
- **Bot exclusion is read-time policy, not sync-time**: synced data stays
  complete. Two mechanisms: `users.is_bot` (GraphQL `__typename == "Bot"`)
  filtered in SQL, and the config `exclude_bots` globs (`config.BotMatcher`)
  applied Go-side where individual logins are inspected (TTFR, comments
  received). Both must be applied when adding a metric.
- **Self-activity never counts**: reviews or comments on your own PR are
  excluded everywhere (`author_login != p.author_login`).
- **Hidden members** (`hidden: true`) are excluded from views via
  `teamMembers()` (`WHERE hidden = 0`) but their data stays in the cache.
- **Medians are lower-middle** (`median()` in metrics.go) so the result is an
  actually-observed value. `0`/`-1` are the "no data" sentinels
  (CycleTimeP50/TTFRP50 zero, SizeP50 -1).
- **Sync walks by `updatedAt` DESC** with a per-repo watermark minus 1h
  overlap; the walk stops at watermark-or-backfill-horizon. Consequence: PRs
  untouched since before the backfill horizon are invisible. `CoverageFloor`
  gates the tile deltas and trends length so we never show numbers the cache
  can't back. Preserve that honesty when adding views.
- **`SavePullRequests` is delete-and-replace** for a PR's reviews/comments —
  idempotent re-syncs, handles dismissals/deletions without diffing. Sync
  bookkeeping (`sync_state`) commits only after a clean walk.
- **Config file is source of truth for teams**; `db.MirrorTeam` re-mirrors it
  into `teams`/`team_members`/`team_repos` on every startup. Never treat the
  DB team tables as authoritative.
- **DB schema changes** go in a new `internal/db/migrations/NNNN_*.sql` file
  (applied by `PRAGMA user_version` ordering). Never edit `0001_init.sql`.

### Metric definition gotchas (read before touching metrics)

- Review-thread replies arrive as GitHub "COMMENTED" reviews; v0.1 counts
  them as reviews (documented known inflation of the commented bucket).
- `DISMISSED` review state exists in the schema and can be stored, but no
  metric branch counts it — an APPROVED-then-dismissed review currently
  disappears from review counts when GitHub rewrites the state.
- TTFR = first non-bot, non-author review on PRs *created* in the window;
  the review itself may fall outside the window.
- Cycle time is attributed to the merge week/window; size to the created
  window; TTFR to the created window. Trends (`trends.go`) use fixed 7-day
  buckets ending "now", independent of the UI window.
- `PunchCard` uses **local time**; everything else is UTC.
- Draft PRs count in every metric except `OpenAging` (which filters
  `is_draft = 0`). Closed-unmerged PRs count toward "opened" and size but
  produce no cycle sample.

## Change process

- **Any metric change or new metric needs golden tests** in
  `internal/metrics` (see `metrics_test.go: fixture()` — a hand-computed
  scenario with exact expected values, comments explaining each number).
  If a change alters a definition, say so explicitly in the PR description
  and update README "Metric definitions".
- User-visible changes get a `CHANGELOG.md` line under `## Unreleased`.
- Keep PRs focused; commits are squash-merged so PR title/description matter.
- Export (`internal/export`) and README key tables must stay in sync with
  UI changes.
- New GraphQL fields: extend the query documents in `gh/queries.go`, the
  node structs, `syncer.convertPR`, the DB schema (new migration), and the
  store — in that order — and add a fake-`Doer` test in `syncer`.

## Testing patterns to copy

- `internal/metrics/metrics_test.go` — golden-value fixture: build PRs via
  `store.SavePullRequests`, assert exact Row values.
- `internal/syncer/engine_test.go` — `pagedDoer` fake serving canned JSON
  pages keyed on query document + cursor; asserts watermark/backfill state.
- `internal/tui/app/app_test.go` — `teatest` harness with `emptyPageDoer`;
  drives the real Bubble Tea program and greps rendered output.
- `internal/tui/app/persist_test.go` / `sync_test.go` — config persistence
  and sync-event bridging.

## Known weak points (verified against the code, good first targets)

1. ~~`internal/gh` and `internal/config` have no test files~~ Fixed: see
   `gh/client_test.go` (`IsRetryable`, `Actor`) and `config/config_test.go`
   + `load_test.go` (`BotMatcher`, defaults, validation, YAML round-trip).
   Token fallback (`auth.go`) remains untested (needs subprocess seam).
2. `sync_state.last_error` is written but **never read by any UI** — a repo
   can silently fail every sync (renamed/private repo) and views just go
   stale. Partially fixed: `gh-statline sync` now **exits non-zero** when
   any repo fails (verified against the real API). The TUI still shows
   nothing; a `doctor`/`status` view of sync_state is the remaining gap.
   Renamed repos never self-heal because targets come from config.
3. `botLogins()` loads the entire `users` table into an `IN (...)` list per
   query — fine today, but it's an O(all users) pattern that will not scale
   and silently degrades if the list exceeds SQLite's parameter limit
   (modernc default is high, but the pattern is fragile).
4. Metric SQL strings are assembled by concatenation with positional `?`
   args appended in matching order (see `fillCommentsGiven`) — correctness
   depends on arg-order discipline with zero compiler help. Extreme care
   when editing; a mismatched append compiles and returns wrong numbers.
5. `Movers` percent-change clamps `prior == 0` to +100% and volume floors
   are hardcoded (`moverFloor`) — tune with care, values are load-bearing
   for the Trends UI.
6. Time-based tests use real `time.Now()` offsets; there is no clock
   injection in `metrics` (seed has `Options.Now`). Flakes near week/bucket
   boundaries are possible; prefer adding a `now` parameter over sleeping.

## Style

- Package docs explain *why* (see `metrics`, `db/open.go`); keep that bar.
- Comments document invariants and GitHub API quirks, not mechanics. Match
  the existing compact, declarative comment voice.
- Errors: wrap with context (`fmt.Errorf("fetching %s: %w", ...)`);
  errcheck exclusions are only `Rows.Close`/`DB.Close`/`Tx.Rollback`.
