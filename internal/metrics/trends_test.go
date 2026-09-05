package metrics

import (
	"testing"
	"time"

	"github.com/byte2pixel/gh-statline/internal/config"
	"github.com/byte2pixel/gh-statline/internal/db"
)

// wk maps a whole-days-ago fixture offset to its week index in a full
// 12-week series. Buckets end at now+1, so an event k days ago sits
// strictly inside bucket 11 - k/7 (never on a boundary).
func wk(daysAgo int64) int {
	return TrendWeeks - 1 - int(daysAgo/7)
}

func setFloor(t *testing.T, store *db.Store, repoID, floor int64) {
	t.Helper()
	if err := store.SetSyncState(db.SyncState{RepoID: repoID, BackfillUntil: &floor}); err != nil {
		t.Fatal(err)
	}
}

func TestTrendSeriesGoldenValues(t *testing.T) {
	store, teamID, repoID := fixture(t)
	setFloor(t, store, repoID, fixedNow.AddDate(0, 0, -120).Unix())
	f := Filter{TeamID: teamID, Bots: config.NewBotMatcher(config.Default().ExcludeBots)}

	d, err := TrendSeries(store.DB, f, TrendWeeks, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Weeks) != TrendWeeks {
		t.Fatalf("weeks = %d, want %d", len(d.Weeks), TrendWeeks)
	}
	for i := 1; i < len(d.Weeks); i++ {
		if d.Weeks[i].Sub(d.Weeks[i-1]) != 7*24*time.Hour {
			t.Fatalf("week %d not 7d after week %d", i, i-1)
		}
	}
	if last := d.Weeks[len(d.Weeks)-1]; last.AddDate(0, 0, 7).Unix() != fixedNow.Unix()+1 {
		t.Errorf("newest bucket should end at now+1, ends %v", last.AddDate(0, 0, 7))
	}

	if len(d.Members) != 2 || d.Members[0].Login != "alice" || d.Members[1].Login != "bob" {
		t.Fatalf("members = %+v, want alice, bob (carol hidden)", d.Members)
	}
	alice, bob := d.Members[0], d.Members[1]

	checkWeeks := func(name string, got []int, want map[int]int) {
		t.Helper()
		for i, v := range got {
			if v != want[i] {
				t.Errorf("%s[%d] = %d, want %d", name, i, v, want[i])
			}
		}
	}

	// PR1 opened -10d merged -9d, PR2 opened -5d, PR4 opened -40d merged -35d.
	checkWeeks("alice.Opened", alice.Opened, map[int]int{wk(40): 1, wk(10): 1, wk(5): 1})
	checkWeeks("alice.Merged", alice.Merged, map[int]int{wk(35): 1, wk(9): 1})
	// R3a on bob's PR3 at -2d.
	checkWeeks("alice.Reviews", alice.Reviews, map[int]int{wk(2): 1})
	// C3a at -2d; R3a has 0 thread comments; self-comment C1b excluded.
	checkWeeks("alice.Comments", alice.Comments, map[int]int{wk(2): 1})

	// PR3 opened -3d merged -1d.
	checkWeeks("bob.Opened", bob.Opened, map[int]int{wk(3): 1})
	checkWeeks("bob.Merged", bob.Merged, map[int]int{wk(1): 1})
	// R1b at -9.5d and R1c (DISMISSED) at -9d land in the same week; R2b at
	// -4d. Bot reviews aren't bob's.
	checkWeeks("bob.Reviews", bob.Reviews, map[int]int{wk(9): 2, wk(4): 1})
	// R1b 2 thread comments + C1a at -9d; R2b 1 comment at -4d.
	checkWeeks("bob.Comments", bob.Comments, map[int]int{wk(9): 3, wk(4): 1})

	// Team counts are the member sums.
	for i := range d.Weeks {
		if d.Team.Opened[i] != alice.Opened[i]+bob.Opened[i] {
			t.Errorf("team.Opened[%d] = %d, want member sum", i, d.Team.Opened[i])
		}
		if d.Team.Comments[i] != alice.Comments[i]+bob.Comments[i] {
			t.Errorf("team.Comments[%d] = %d, want member sum", i, d.Team.Comments[i])
		}
	}

	// Cycle p50 per merge week: PR4 5d, PR1 1d, PR3 2d.
	wantCycle := map[int]time.Duration{wk(35): 120 * time.Hour, wk(9): 24 * time.Hour, wk(1): 48 * time.Hour}
	for i, c := range d.Team.Cycle {
		if c != wantCycle[i] {
			t.Errorf("team.Cycle[%d] = %v, want %v", i, c, wantCycle[i])
		}
	}
	// TTFR p50 per created week: PR1 12h (bot review skipped) at wk(10);
	// PR2 24h (glob bot skipped) and PR3 24h both created in wk(5)==wk(3).
	wantTTFR := map[int]time.Duration{wk(10): 12 * time.Hour, wk(5): 24 * time.Hour}
	for i, v := range d.Team.TTFR {
		if v != wantTTFR[i] {
			t.Errorf("team.TTFR[%d] = %v, want %v", i, v, wantTTFR[i])
		}
	}
}

// The weekly TTFR query reads is_bot through an outer join for the same
// reason ttfrSamples does: a reviewer with no users row must not erase the
// week's first-review latency.
func TestTrendTTFRSurvivesMissingUserRow(t *testing.T) {
	store, teamID, repoID := fixture(t)
	setFloor(t, store, repoID, fixedNow.AddDate(0, 0, -120).Unix())
	if _, err := store.DB.Exec(`DELETE FROM users WHERE login = 'bob'`); err != nil {
		t.Fatal(err)
	}

	d, err := TrendSeries(store.DB, Filter{
		TeamID: teamID,
		Bots:   config.NewBotMatcher(config.Default().ExcludeBots),
	}, TrendWeeks, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	// PR1 opened 10 days ago; its only non-bot review is bob's, 12h later.
	if got := d.Team.TTFR[wk(10)]; got != 12*time.Hour {
		t.Errorf("team.TTFR[wk(10)] = %v, want 12h (bob's review was dropped)", got)
	}
}

func TestTrendSeriesCoverageTruncation(t *testing.T) {
	store, teamID, repoID := fixture(t)
	f := Filter{TeamID: teamID, Bots: config.NewBotMatcher(nil)}

	// No sync yet: no honest history, no error.
	d, err := TrendSeries(store.DB, f, TrendWeeks, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Weeks) != 0 {
		t.Errorf("weeks before first sync = %d, want 0", len(d.Weeks))
	}

	// 30 days of coverage: 4 whole weeks, the partial 5th dropped.
	setFloor(t, store, repoID, fixedNow.AddDate(0, 0, -30).Unix())
	d, err = TrendSeries(store.DB, f, TrendWeeks, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Weeks) != 4 {
		t.Fatalf("weeks at 30d coverage = %d, want 4", len(d.Weeks))
	}
	// PR4 (-40d/-35d) is behind the floor and must not leak into week 0.
	if d.Team.Opened[0] != 0 || d.Team.Merged[0] != 0 {
		t.Errorf("week 0 opened/merged = %d/%d, want 0/0 (PR4 behind floor)",
			d.Team.Opened[0], d.Team.Merged[0])
	}
	var opened int
	for _, v := range d.Team.Opened {
		opened += v
	}
	if opened != 3 {
		t.Errorf("total opened in 4 weeks = %d, want 3", opened)
	}

	// Less than one whole week of coverage: nothing to show.
	setFloor(t, store, repoID, fixedNow.AddDate(0, 0, -2).Unix())
	d, err = TrendSeries(store.DB, f, TrendWeeks, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Weeks) != 0 {
		t.Errorf("weeks at 2d coverage = %d, want 0", len(d.Weeks))
	}
}

func member(login string, opened, merged, reviews, comments []int) MemberTrend {
	return MemberTrend{Login: login, Opened: opened, Merged: merged, Reviews: reviews, Comments: comments}
}

func flat(n, v int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func TestMovers(t *testing.T) {
	z := flat(8, 0)

	t.Run("requires four weeks", func(t *testing.T) {
		r, f := Movers([]MemberTrend{member("a", flat(3, 9), flat(3, 9), flat(3, 9), flat(3, 9))}, 3)
		if r != nil || f != nil {
			t.Errorf("3-week movers = %v / %v, want none", r, f)
		}
	})

	t.Run("volume floor suppresses tiny denominators", func(t *testing.T) {
		// 1 PR prior half, 3 recent: +200% but max(1,3) < floor 4.
		r, _ := Movers([]MemberTrend{member("a",
			[]int{0, 0, 0, 1, 0, 1, 1, 1}, z, z, z)}, 3)
		if len(r) != 0 {
			t.Errorf("below-floor riser reported: %+v", r)
		}
	})

	t.Run("riser with empty prior is flagged new, not a fake percent", func(t *testing.T) {
		r, f := Movers([]MemberTrend{member("a",
			[]int{0, 0, 0, 0, 1, 1, 1, 2}, z, z, z)}, 3)
		if len(r) != 1 || !r[0].IsNew || r[0].Metric != MetricOpened || r[0].Pct != 0 || r[0].Prior != 0 || r[0].Recent != 5 {
			t.Fatalf("new-activity riser = %+v", r)
		}
		if len(f) != 0 {
			t.Errorf("new activity landed in fallers: %+v", f)
		}
	})

	t.Run("new activity outranks every finite percent, by volume among itself", func(t *testing.T) {
		gain := func(prior, recent int) []int {
			return []int{prior, prior, prior, prior, recent, recent, recent, recent}
		}
		// The issue #41 inversion: 0→20 reviews used to clamp to +100% and
		// rank below a 6→18 tripling, pushing the biggest genuine riser off
		// the card.
		r, _ := Movers([]MemberTrend{
			member("ramp", z, z, gain(0, 5), z), // 0→20: new
			member("trip", z, z, gain(2, 6), z), // 8→24: +200%
			member("bump", z, z, gain(0, 2), z), // 0→8: new, smaller volume
		}, 3)
		if len(r) != 3 {
			t.Fatalf("risers = %+v, want 3", r)
		}
		if r[0].Login != "ramp" || r[1].Login != "bump" || r[2].Login != "trip" {
			t.Errorf("riser order = %s, %s, %s; want ramp, bump, trip",
				r[0].Login, r[1].Login, r[2].Login)
		}
	})

	t.Run("collapse to zero stays a truthful -100 faller", func(t *testing.T) {
		_, f := Movers([]MemberTrend{member("a", z, z,
			[]int{5, 5, 5, 5, 0, 0, 0, 0}, z)}, 3)
		if len(f) != 1 || f[0].Pct != -100 || f[0].IsNew {
			t.Fatalf("collapse faller = %+v, want Pct -100 and not IsNew", f)
		}
	})

	t.Run("faller percent and streak", func(t *testing.T) {
		_, f := Movers([]MemberTrend{member("a", z, []int{3, 3, 3, 3, 4, 3, 2, 1}, z, z)}, 3)
		if len(f) != 1 {
			t.Fatalf("fallers = %+v", f)
		}
		// prior 3+3+3+3=12, recent 4+3+2+1=10 → -17%; down 3 weeks running.
		if f[0].Pct != -17 || f[0].Streak != -3 {
			t.Errorf("faller = %+v, want Pct -17 Streak -3", f[0])
		}
	})

	t.Run("one metric per member per direction", func(t *testing.T) {
		up := []int{0, 0, 0, 0, 2, 2, 3, 3}
		r, _ := Movers([]MemberTrend{member("a", up, up, up, up)}, 3)
		if len(r) != 1 {
			t.Fatalf("risers = %+v, want a single entry for member a", r)
		}
	})

	t.Run("top three, ranked by percent then delta then login", func(t *testing.T) {
		gain := func(prior, recent int) []int {
			return []int{prior, prior, prior, prior, recent, recent, recent, recent}
		}
		r, _ := Movers([]MemberTrend{
			member("carl", gain(1, 2), z, z, z), // +100%, delta 4
			member("beth", gain(1, 3), z, z, z), // +200%
			member("adam", gain(2, 3), z, z, z), // +50%
			member("dana", gain(2, 4), z, z, z), // +100%, delta 8
			member("evan", gain(4, 5), z, z, z), // +25%
		}, 3)
		if len(r) != 3 {
			t.Fatalf("risers = %+v, want 3", r)
		}
		if r[0].Login != "beth" || r[1].Login != "dana" || r[2].Login != "carl" {
			t.Errorf("riser order = %s, %s, %s; want beth, dana, carl",
				r[0].Login, r[1].Login, r[2].Login)
		}
	})

	t.Run("six weeks uses three-week halves", func(t *testing.T) {
		r, _ := Movers([]MemberTrend{member("a",
			[]int{1, 1, 1, 2, 2, 2}, z[:6], z[:6], z[:6])}, 3)
		if len(r) != 1 || r[0].Prior != 3 || r[0].Recent != 6 || r[0].Pct != 100 {
			t.Fatalf("6-week riser = %+v, want prior 3 recent 6", r)
		}
	})
}

// PctChange is the single percent-change definition every surface renders;
// 7→10 pins rounding (truncation said 42), 8→24 pins the plain case, and
// 40→39 pins half-away-from-zero on the negative side.
func TestPctChange(t *testing.T) {
	cases := []struct {
		prior, recent, want int
	}{
		{7, 10, 43},
		{8, 24, 200},
		{40, 39, -3}, // -2.5 rounds away from zero
		{20, 0, -100},
		{10, 10, 0},
		{0, 20, 0}, // zero base: no percentage; callers label it "new"
	}
	for _, c := range cases {
		if got := PctChange(c.prior, c.recent); got != c.want {
			t.Errorf("PctChange(%d, %d) = %d, want %d", c.prior, c.recent, got, c.want)
		}
	}
}

// ChangeLabel and BadgeStreak are the shared mover rendering rules; the TUI
// card, its fullscreen export, and the Markdown export must all go through
// them rather than re-deriving "new" and the streak threshold.
func TestMoverRenderRules(t *testing.T) {
	if got := (Mover{IsNew: true, Recent: 5}).ChangeLabel(); got != "new" {
		t.Errorf(`IsNew ChangeLabel = %q, want "new"`, got)
	}
	if got := (Mover{Pct: 43}).ChangeLabel(); got != "+43%" {
		t.Errorf(`riser ChangeLabel = %q, want "+43%%"`, got)
	}
	if got := (Mover{Pct: -17}).ChangeLabel(); got != "-17%" {
		t.Errorf(`faller ChangeLabel = %q, want "-17%%"`, got)
	}
	for streak, want := range map[int]int{2: 0, 3: 3, -2: 0, -3: -3, 5: 5} {
		if got := (Mover{Streak: streak}).BadgeStreak(); got != want {
			t.Errorf("BadgeStreak(streak %d) = %d, want %d", streak, got, want)
		}
	}
	if !(Mover{IsNew: true}).Rising() || !(Mover{Pct: 1}).Rising() || (Mover{Pct: -1}).Rising() {
		t.Error("Rising: want true for IsNew and positive Pct, false for negative")
	}
}

func TestStreak(t *testing.T) {
	cases := []struct {
		vals []int
		want int
	}{
		{[]int{1, 2, 3, 4}, 3},
		{[]int{4, 3, 2, 1}, -3},
		{[]int{1, 2, 2, 3}, 1}, // flat week resets the run
		{[]int{1, 2, 3, 3}, 0}, // flat latest week: no streak
		{[]int{3, 1, 2, 3}, 2}, // direction change bounds the run
		{[]int{5}, 0},
		{nil, 0},
	}
	for _, c := range cases {
		if got := streak(c.vals); got != c.want {
			t.Errorf("streak(%v) = %d, want %d", c.vals, got, c.want)
		}
	}
}

// Arrow and StreakLabel are the one wording of a mover's direction and
// streak; the card, its export table, and the Markdown export print them
// verbatim.
func TestMoverLabels(t *testing.T) {
	cases := []struct {
		m           Mover
		arrow, want string
	}{
		{Mover{IsNew: true, Streak: 2}, "▲", ""},
		{Mover{Pct: 40, Streak: 3}, "▲", "up 3w running"},
		{Mover{Pct: -40, Streak: -4}, "▼", "down 4w running"},
		{Mover{Pct: -40, Streak: -2}, "▼", ""},
	}
	for _, c := range cases {
		if got := c.m.Arrow(); got != c.arrow {
			t.Errorf("%+v: Arrow() = %q, want %q", c.m, got, c.arrow)
		}
		if got := c.m.StreakLabel(); got != c.want {
			t.Errorf("%+v: StreakLabel() = %q, want %q", c.m, got, c.want)
		}
	}
}

// Every Metric resolves to its own series on both trend shapes and prints
// the label the movers use, so a lookup can no longer depend on a label
// staying spelled one way.
func TestMetricSeries(t *testing.T) {
	mt := member("a", []int{1}, []int{2}, []int{3}, []int{4})
	tt := TeamTrend{Opened: []int{10}, Merged: []int{20}, Reviews: []int{30}, Comments: []int{40}}
	want := map[Metric]struct {
		label        string
		member, team int
	}{
		MetricOpened:   {"PRs opened", 1, 10},
		MetricMerged:   {"PRs merged", 2, 20},
		MetricReviews:  {"reviews", 3, 30},
		MetricComments: {"comments", 4, 40},
	}
	if len(CountMetrics) != len(want) {
		t.Fatalf("CountMetrics has %d entries, want %d", len(CountMetrics), len(want))
	}
	for _, m := range CountMetrics {
		w := want[m]
		if got := m.String(); got != w.label {
			t.Errorf("%d.String() = %q, want %q", int(m), got, w.label)
		}
		if got := m.Of(mt); len(got) != 1 || got[0] != w.member {
			t.Errorf("%s.Of(member) = %v, want [%d]", m, got, w.member)
		}
		if got := m.OfTeam(tt); len(got) != 1 || got[0] != w.team {
			t.Errorf("%s.OfTeam(team) = %v, want [%d]", m, got, w.team)
		}
	}
}
