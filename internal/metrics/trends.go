package metrics

import (
	"database/sql"
	"fmt"
	"sort"
	"time"
)

// TrendWeeks is how many trailing 7-day buckets the Trends view shows.
const TrendWeeks = 12

const weekSecs = 7 * 86400

// TrendData is everything the Trends view renders: fixed weekly buckets
// ending now, independent of the UI time window.
type TrendData struct {
	Weeks   []time.Time // start of each 7-day bucket, oldest first
	Team    TeamTrend
	Members []MemberTrend // visible members, count metrics only
}

// TeamTrend holds one value per week for each headline metric. Slices are
// the same length as TrendData.Weeks.
type TeamTrend struct {
	Opened, Merged, Reviews, Comments []int
	Cycle, TTFR                       []time.Duration // weekly p50; 0 = no samples
}

// MemberTrend holds one member's weekly counts. Medians stay team-level:
// per-member weekly medians are too noisy at a few PRs per week.
type MemberTrend struct {
	Login                             string
	Opened, Merged, Reviews, Comments []int
}

// TrendSeries computes up to weeks trailing 7-day buckets ending now. The
// series is truncated to whole weeks the cache provably covers (per
// CoverageFloor); zero weeks until the first full sync completes, so short
// history never renders as misleading zeros. Definitions match TeamStats:
// hidden members and bots are excluded, self-reviews and self-comments
// don't count, cycle is attributed to the merge week and TTFR to the PR's
// created week.
func TrendSeries(dbh *sql.DB, f Filter, weeks int, now time.Time) (TrendData, error) {
	end := now.Unix() + 1
	n := weeks
	floor, ok := CoverageFloor(dbh, f.TeamID)
	if !ok {
		n = 0
	} else if covered := int((end - floor) / weekSecs); covered < n {
		n = covered
	}
	if n <= 0 {
		return TrendData{}, nil
	}
	start := end - int64(n)*weekSecs
	weekIdx := func(ts int64) int {
		if ts < start {
			return -1
		}
		return int((ts - start) / weekSecs)
	}

	members, err := visibleMembers(dbh, f)
	if err != nil {
		return TrendData{}, err
	}
	byLogin := make(map[string]*MemberTrend, len(members))
	order := make([]string, 0, len(members))
	for _, m := range members {
		byLogin[m] = &MemberTrend{
			Login:    m,
			Opened:   make([]int, n),
			Merged:   make([]int, n),
			Reviews:  make([]int, n),
			Comments: make([]int, n),
		}
		order = append(order, m)
	}

	d := TrendData{
		Weeks: make([]time.Time, n),
		Team: TeamTrend{
			Opened: make([]int, n), Merged: make([]int, n),
			Reviews: make([]int, n), Comments: make([]int, n),
			Cycle: make([]time.Duration, n), TTFR: make([]time.Duration, n),
		},
	}
	for i := range d.Weeks {
		d.Weeks[i] = time.Unix(start+int64(i)*weekSecs, 0).UTC()
	}

	span := Window{Start: start, End: end}

	// PRs: opened per created week, merged per merge week, team cycle
	// samples per merge week.
	q := `
		SELECT p.author_login, p.created_at, p.merged_at
		FROM pull_requests p` + teamRepos() + `
		WHERE (p.created_at >= :start OR p.merged_at >= :start)` +
		repoScope() + visibleMember("p.author_login")
	rs, err := dbh.Query(q, namedArgs(f, span)...)
	if err != nil {
		return TrendData{}, err
	}
	defer rs.Close()
	cycles := make([][]int64, n)
	for rs.Next() {
		var login string
		var created int64
		var merged sql.NullInt64
		if err := rs.Scan(&login, &created, &merged); err != nil {
			return TrendData{}, err
		}
		m, member := byLogin[login]
		if !member {
			continue
		}
		if i := weekIdx(created); i >= 0 && i < n && created < end {
			m.Opened[i]++
		}
		if merged.Valid {
			if i := weekIdx(merged.Int64); i >= 0 && i < n && merged.Int64 < end {
				m.Merged[i]++
				cycles[i] = append(cycles[i], merged.Int64-created)
			}
		}
	}
	rs.Close()
	if err := rs.Err(); err != nil {
		return TrendData{}, err
	}

	// Reviews given and review-thread comments, self-reviews excluded.
	q = `
		SELECT r.author_login, r.submitted_at, r.comment_count
		FROM reviews r
		JOIN pull_requests p ON p.id = r.pr_id` + teamRepos() + `
		WHERE r.submitted_at >= :start AND r.submitted_at < :end
		  AND r.author_login != p.author_login` +
		repoScope() + visibleMember("r.author_login")
	rs, err = dbh.Query(q, namedArgs(f, span)...)
	if err != nil {
		return TrendData{}, err
	}
	defer rs.Close()
	for rs.Next() {
		var login string
		var submitted int64
		var comments int
		if err := rs.Scan(&login, &submitted, &comments); err != nil {
			return TrendData{}, err
		}
		if m, member := byLogin[login]; member {
			if i := weekIdx(submitted); i >= 0 && i < n {
				m.Reviews[i]++
				m.Comments[i] += comments
			}
		}
	}
	rs.Close()
	if err := rs.Err(); err != nil {
		return TrendData{}, err
	}

	// Conversation-tab comments on others' PRs.
	q = `
		SELECT ic.author_login, ic.created_at
		FROM issue_comments ic
		JOIN pull_requests p ON p.id = ic.pr_id` + teamRepos() + `
		WHERE ic.created_at >= :start AND ic.created_at < :end
		  AND ic.author_login != p.author_login` +
		repoScope() + visibleMember("ic.author_login")
	rs, err = dbh.Query(q, namedArgs(f, span)...)
	if err != nil {
		return TrendData{}, err
	}
	defer rs.Close()
	for rs.Next() {
		var login string
		var created int64
		if err := rs.Scan(&login, &created); err != nil {
			return TrendData{}, err
		}
		if m, member := byLogin[login]; member {
			if i := weekIdx(created); i >= 0 && i < n {
				m.Comments[i]++
			}
		}
	}
	rs.Close()
	if err := rs.Err(); err != nil {
		return TrendData{}, err
	}

	// TTFR samples bucketed by the PR's created week.
	samples, err := firstReviews(dbh, f, span)
	if err != nil {
		return TrendData{}, err
	}
	ttfrs := make([][]int64, n)
	for _, s := range samples {
		if i := weekIdx(s.Created); i >= 0 && i < n {
			ttfrs[i] = append(ttfrs[i], s.Secs)
		}
	}

	for i := 0; i < n; i++ {
		if v, ok := median(cycles[i]); ok {
			d.Team.Cycle[i] = time.Duration(v) * time.Second
		}
		if v, ok := median(ttfrs[i]); ok {
			d.Team.TTFR[i] = time.Duration(v) * time.Second
		}
	}
	for _, login := range order {
		m := byLogin[login]
		d.Members = append(d.Members, *m)
		for i := 0; i < n; i++ {
			d.Team.Opened[i] += m.Opened[i]
			d.Team.Merged[i] += m.Merged[i]
			d.Team.Reviews[i] += m.Reviews[i]
			d.Team.Comments[i] += m.Comments[i]
		}
	}
	return d, nil
}

// Metric identifies one of the four weekly count series a trend carries.
// The movers ranking, the trends cards, and the exports all look a series
// up by it, never by its label, so renaming a label cannot strand a lookup.
type Metric int

const (
	MetricOpened Metric = iota
	MetricMerged
	MetricReviews
	MetricComments
)

// CountMetrics lists every Metric in display order.
var CountMetrics = []Metric{MetricOpened, MetricMerged, MetricReviews, MetricComments}

// String is the label the movers print for the metric.
func (m Metric) String() string {
	switch m {
	case MetricOpened:
		return "PRs opened"
	case MetricMerged:
		return "PRs merged"
	case MetricReviews:
		return "reviews"
	case MetricComments:
		return "comments"
	}
	return fmt.Sprintf("metric(%d)", int(m))
}

// Of returns the member's weekly series for the metric.
func (m Metric) Of(t MemberTrend) []int {
	switch m {
	case MetricOpened:
		return t.Opened
	case MetricMerged:
		return t.Merged
	case MetricReviews:
		return t.Reviews
	case MetricComments:
		return t.Comments
	}
	return nil
}

// OfTeam returns the team-wide weekly series for the metric.
func (m Metric) OfTeam(t TeamTrend) []int {
	switch m {
	case MetricOpened:
		return t.Opened
	case MetricMerged:
		return t.Merged
	case MetricReviews:
		return t.Reviews
	case MetricComments:
		return t.Comments
	}
	return nil
}

// Mover is one member/metric pair whose recent volume shifted meaningfully.
type Mover struct {
	Login  string
	Metric Metric
	Recent int  // sum of the newest h weeks (h = min(4, weeks/2))
	Prior  int  // sum of the h weeks before that
	Pct    int  // signed percent change (PctChange); 0 and meaningless when IsNew
	IsNew  bool // Prior == 0: activity from a zero base has no percentage
	Streak int  // signed run of consecutive same-sign WoW deltas ending now
}

// Rising reports the mover's direction. IsNew movers carry no percentage,
// so the sign of Pct can't be consulted — new activity is always a rise.
func (m Mover) Rising() bool { return m.IsNew || m.Pct > 0 }

// ChangeLabel is the single rendering of a mover's change: "new" for a zero
// base, otherwise a signed whole percent. The movers card, its fullscreen
// export, and the Markdown export all call it, so the three surfaces cannot
// drift apart.
func (m Mover) ChangeLabel() string {
	if m.IsNew {
		return "new"
	}
	return fmt.Sprintf("%+d%%", m.Pct)
}

// badgeStreakMin is how many consecutive same-direction weeks earn a streak
// badge next to a mover.
const badgeStreakMin = 3

// BadgeStreak returns the signed streak once it is long enough to badge,
// else 0. Renderers word the badge themselves but never pick the threshold.
func (m Mover) BadgeStreak() int {
	if m.Streak >= badgeStreakMin || m.Streak <= -badgeStreakMin {
		return m.Streak
	}
	return 0
}

// Arrow is the direction glyph every mover line leads with.
func (m Mover) Arrow() string {
	if m.Rising() {
		return "▲"
	}
	return "▼"
}

// StreakLabel words the streak badge ("up 3w running"), or "" below the
// badge threshold. The movers card, its export table, and the Markdown
// export all print it verbatim, so the wording cannot drift.
func (m Mover) StreakLabel() string {
	switch s := m.BadgeStreak(); {
	case s > 0:
		return fmt.Sprintf("up %dw running", s)
	case s < 0:
		return fmt.Sprintf("down %dw running", -s)
	}
	return ""
}

// moverFloor is the minimum volume (on the larger of the two halves) a
// member/metric pair needs before a percent change is worth reporting —
// roughly one event per week — so 1→3 PRs never shows up as +200%.
var moverFloor = [...]int{
	MetricOpened:   4,
	MetricMerged:   4,
	MetricReviews:  6,
	MetricComments: 8,
}

// Movers ranks members whose recent weeks shifted most vs the weeks before:
// percent change gated by a per-metric volume floor, |Pct| descending with
// absolute delta then login as tie-breaks, at most one metric per member
// per direction, and at most limit each way (limit <= 0 means everyone who
// qualifies). Needs at least 4 weeks of history. A zero prior is flagged
// IsNew instead of pretending to be a percentage: those risers outrank every
// finite percent change (a zero base is an unbounded relative move) and rank
// by recent volume among themselves, so a 0→20 ramp can no longer fall below
// a 6→18 tripling or off the card entirely.
func Movers(members []MemberTrend, limit int) (risers, fallers []Mover) {
	if len(members) == 0 {
		return nil, nil
	}
	weeks := len(members[0].Opened)
	if weeks < 4 {
		return nil, nil
	}
	h := weeks / 2
	if h > 4 {
		h = 4
	}

	var cands []Mover
	for _, m := range members {
		for _, mt := range CountMetrics {
			vals := mt.Of(m)
			var recent, prior int
			for i := weeks - h; i < weeks; i++ {
				recent += vals[i]
			}
			for i := weeks - 2*h; i < weeks-h; i++ {
				prior += vals[i]
			}
			larger := recent
			if prior > larger {
				larger = prior
			}
			if larger < moverFloor[mt] {
				continue
			}
			c := Mover{
				Login: m.Login, Metric: mt,
				Recent: recent, Prior: prior,
				Streak: streak(vals),
			}
			if prior == 0 {
				c.IsNew = true // recent >= floor here, so this is real volume
			} else {
				c.Pct = PctChange(prior, recent)
				if c.Pct == 0 {
					continue
				}
			}
			cands = append(cands, c)
		}
	}

	sort.Slice(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		if a.IsNew != b.IsNew {
			return a.IsNew
		}
		if abs(a.Pct) != abs(b.Pct) {
			return abs(a.Pct) > abs(b.Pct)
		}
		if abs(a.Recent-a.Prior) != abs(b.Recent-b.Prior) {
			return abs(a.Recent-a.Prior) > abs(b.Recent-b.Prior)
		}
		if a.Login != b.Login {
			return a.Login < b.Login
		}
		return a.Metric < b.Metric // declaration order, not label order
	})

	pick := func(up bool) []Mover {
		var out []Mover
		seen := map[string]bool{}
		for _, c := range cands {
			if c.Rising() != up || seen[c.Login] {
				continue
			}
			seen[c.Login] = true
			out = append(out, c)
			if limit > 0 && len(out) == limit {
				break
			}
		}
		return out
	}
	return pick(true), pick(false)
}

// streak counts consecutive week-over-week deltas of the same sign ending
// at the latest week, signed by direction; a flat week breaks the run.
func streak(vals []int) int {
	n := len(vals)
	if n < 2 {
		return 0
	}
	last := vals[n-1] - vals[n-2]
	if last == 0 {
		return 0
	}
	sign := 1
	if last < 0 {
		sign = -1
	}
	run := 0
	for i := n - 1; i > 0; i-- {
		d := vals[i] - vals[i-1]
		if d == 0 || (d > 0) != (sign > 0) {
			break
		}
		run++
	}
	return sign * run
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
