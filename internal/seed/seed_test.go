package seed_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/byte2pixel/gh-statline/internal/config"
	"github.com/byte2pixel/gh-statline/internal/db"
	"github.com/byte2pixel/gh-statline/internal/metrics"
	"github.com/byte2pixel/gh-statline/internal/seed"
)

func TestTeamShape(t *testing.T) {
	team := seed.Team("demo", seed.Options{Members: 38})
	if len(team.Members) != 38 {
		t.Errorf("members = %d, want 38", len(team.Members))
	}
	if !team.NoSync {
		t.Error("demo team must be no_sync")
	}
	if len(team.Repos) != 3 {
		t.Errorf("repos = %d, want 3", len(team.Repos))
	}
}

func TestGenerateDeterministic(t *testing.T) {
	o := seed.Options{Members: 38, Days: 120, Seed: 7, Now: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)}
	team := seed.Team("demo", o)
	ids := map[string]int64{"demo-org/api": 1, "demo-org/web": 2, "demo-org/infra": 3}
	a := seed.Generate(team, ids, o)
	b := seed.Generate(team, ids, o)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("Generate is not deterministic for identical options")
	}
	if len(a) == 0 {
		t.Fatal("Generate produced no PRs")
	}
}

// seedStore seeds a fresh in-memory DB and returns everything needed to run
// the real metrics queries against it.
func seedStore(t *testing.T, o seed.Options) (*sql.DB, metrics.Filter) {
	t.Helper()
	sqldb, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqldb.Close() })

	store := db.NewStore(sqldb)
	team := seed.Team("demo", o)
	teamID, repoIDs, err := store.MirrorTeam(team)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SavePullRequests(seed.Generate(team, repoIDs, o)); err != nil {
		t.Fatal(err)
	}
	// Mark the fake repos as synced like the seed command does; CoverageFloor
	// (and so TrendSeries) reports no history at all without it.
	now := o.Now.Unix()
	horizon := o.Now.AddDate(0, 0, -o.Days).Unix()
	for _, id := range repoIDs {
		st := db.SyncState{RepoID: id, WatermarkUpdated: &now, BackfillUntil: &horizon, LastSyncedAt: &now}
		if err := store.SetSyncState(st); err != nil {
			t.Fatal(err)
		}
	}
	return sqldb, metrics.Filter{TeamID: teamID, Bots: config.NewBotMatcher(config.Default().ExcludeBots)}
}

// TestEveryChartLightsUp is the load-bearing check: seeded data must populate
// every chart the app renders, at every window preset, with the bot excluded
// everywhere.
func TestEveryChartLightsUp(t *testing.T) {
	// Real "now": the metrics windows and OpenAging both use time.Now().
	sqldb, f := seedStore(t, seed.Options{Members: 38, Days: 120, Seed: 1, Now: time.Now()})

	for _, days := range []int{7, 30, 90} {
		w := metrics.LastDays(days)

		rows, err := metrics.TeamStats(sqldb, f, w)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 38 {
			t.Fatalf("%dd: team stats rows = %d, want 38", days, len(rows))
		}
		for _, r := range rows {
			if strings.Contains(r.Login, "[bot]") || r.Login == "sam-contractor" {
				t.Errorf("%dd: non-member %q appears in team stats", days, r.Login)
			}
		}

		ttfr, err := metrics.TTFRDistribution(sqldb, f, w)
		if err != nil {
			t.Fatal(err)
		}
		for i, c := range ttfr.Counts {
			if c == 0 {
				t.Errorf("%dd: TTFR bucket %q is empty", days, ttfr.Labels[i])
			}
		}

		buckets, err := metrics.Throughput(sqldb, f, w)
		if err != nil {
			t.Fatal(err)
		}
		opened := 0
		for _, b := range buckets {
			opened += b.Opened
		}
		if opened == 0 {
			t.Errorf("%dd: throughput shows no opened PRs", days)
		}
	}

	// Deeper assertions at the widest preset, where coverage is deterministic.
	w := metrics.LastDays(90)

	sizes, err := metrics.SizeDistribution(sqldb, f, w)
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range sizes.Counts {
		if c == 0 {
			t.Errorf("size bucket %q is empty", sizes.Labels[i])
		}
	}

	trend, err := metrics.CycleTrend(sqldb, f, w)
	if err != nil {
		t.Fatal(err)
	}
	weeksWithMerges := 0
	for _, p := range trend {
		if p.Merged > 0 {
			weeksWithMerges++
		}
	}
	if weeksWithMerges < 8 {
		t.Errorf("cycle trend: only %d weeks with merges, want >= 8", weeksWithMerges)
	}

	matrix, err := metrics.ReviewMatrix(sqldb, f, w)
	if err != nil {
		t.Fatal(err)
	}
	if len(matrix.Logins) != 38 {
		t.Errorf("matrix rows = %d, want 38", len(matrix.Logins))
	}
	if len(matrix.Authors) == 0 || matrix.Authors[len(matrix.Authors)-1] != "(others)" {
		t.Error("matrix is missing the (others) author column")
	}
	if matrix.Max == 0 {
		t.Error("matrix has no reviews")
	}

	punch, err := metrics.PunchCard(sqldb, f, w)
	if err != nil {
		t.Fatal(err)
	}
	weekday, weekend := 0, 0
	for day := range punch.Counts {
		for _, c := range punch.Counts[day] {
			if day >= 5 { // Monday-first: 5 = Saturday, 6 = Sunday
				weekend += c
			} else {
				weekday += c
			}
		}
	}
	if weekday == 0 || weekend == 0 {
		t.Errorf("punch card: weekday=%d weekend=%d, want both > 0", weekday, weekend)
	}

	aging, err := metrics.OpenAging(sqldb, f)
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range aging.Buckets.Counts {
		if c == 0 {
			t.Errorf("aging bucket %q is empty", aging.Buckets.Labels[i])
		}
	}
	if len(aging.Stalest) == 0 || aging.Stalest[0].AgeDays < 90 {
		t.Errorf("stalest open PR should be >= 90 days old, got %+v", aging.Stalest)
	}

	trends, err := metrics.TrendSeries(sqldb, f, metrics.TrendWeeks)
	if err != nil {
		t.Fatal(err)
	}
	if len(trends.Weeks) != metrics.TrendWeeks {
		t.Fatalf("trend weeks = %d, want %d (120d coverage)", len(trends.Weeks), metrics.TrendWeeks)
	}
	for i := range trends.Weeks {
		if trends.Team.Opened[i] == 0 || trends.Team.Merged[i] == 0 ||
			trends.Team.Reviews[i] == 0 || trends.Team.Comments[i] == 0 {
			t.Errorf("trend week %d has an empty count series: %+v", i, trends.Team)
		}
	}
	nonzero := func(ds []time.Duration) int {
		n := 0
		for _, d := range ds {
			if d > 0 {
				n++
			}
		}
		return n
	}
	if nonzero(trends.Team.Cycle) < 8 || nonzero(trends.Team.TTFR) < 8 {
		t.Errorf("cycle/TTFR nonzero weeks = %d/%d, want >= 8 each",
			nonzero(trends.Team.Cycle), nonzero(trends.Team.TTFR))
	}
	risers, fallers := metrics.Movers(trends.Members, 3)
	for _, m := range append(append([]metrics.Mover{}, risers...), fallers...) {
		if m.Pct == 0 {
			t.Errorf("mover with zero percent change: %+v", m)
		}
		if m.Recent < 4 && m.Prior < 4 {
			t.Errorf("mover below every volume floor: %+v", m)
		}
	}

	given, recvd := 0, 0
	rows, err := metrics.TeamStats(sqldb, f, w)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		given += r.CommentsGiven
		recvd += r.CommentsRecv
	}
	if given == 0 || recvd == 0 {
		t.Errorf("comments given=%d received=%d, want both > 0", given, recvd)
	}
}

// TestReseedIsIdempotent re-saves a second generation (different wall-clock
// Now, same seed): the deterministic IDs must upsert, not duplicate.
func TestReseedIsIdempotent(t *testing.T) {
	sqldb, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer sqldb.Close()

	store := db.NewStore(sqldb)
	team := seed.Team("demo", seed.Options{})
	_, repoIDs, err := store.MirrorTeam(team)
	if err != nil {
		t.Fatal(err)
	}

	count := func() int {
		var n int
		if err := sqldb.QueryRow(`SELECT COUNT(*) FROM pull_requests`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	if err := store.SavePullRequests(seed.Generate(team, repoIDs, seed.Options{Now: time.Now()})); err != nil {
		t.Fatal(err)
	}
	first := count()
	if err := store.SavePullRequests(seed.Generate(team, repoIDs, seed.Options{Now: time.Now().Add(time.Minute)})); err != nil {
		t.Fatal(err)
	}
	if second := count(); second != first {
		t.Errorf("re-seed changed PR count: %d -> %d", first, second)
	}
}
