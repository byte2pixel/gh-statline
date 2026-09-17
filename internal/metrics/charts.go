package metrics

import (
	"database/sql"
	"fmt"
	"time"
)

// TrendPoint is one week of cycle-time trend.
type TrendPoint struct {
	WeekStart time.Time
	Median    time.Duration
	Merged    int
}

// CycleTrend returns the weekly median open→merge duration for PRs merged in
// the window. Weeks with no merges are present with Merged == 0.
func CycleTrend(dbh *sql.DB, f Filter, w Window) ([]TrendPoint, error) {
	q := `
		SELECT p.created_at, p.merged_at
		FROM pull_requests p` + teamRepos() + `
		WHERE p.merged_at >= :start AND p.merged_at < :end` +
		repoScope() + visibleMember("p.author_login")
	rs, err := dbh.Query(q, namedArgs(f, w)...)
	if err != nil {
		return nil, err
	}
	defer rs.Close()

	start := time.Unix(w.Start, 0).UTC().Truncate(24 * time.Hour)
	weekIdx := func(ts int64) int {
		return bucketIdx(dayStart(ts), start.Unix(), 7*86400)
	}
	weeks := weekIdx(w.End-1) + 1
	if weeks < 1 {
		weeks = 1
	}
	cycles := make([][]int64, weeks)
	for rs.Next() {
		var created, merged int64
		if err := rs.Scan(&created, &merged); err != nil {
			return nil, err
		}
		if i := weekIdx(merged); i >= 0 && i < weeks {
			cycles[i] = append(cycles[i], merged-created)
		}
	}
	if err := rs.Err(); err != nil {
		return nil, err
	}

	out := make([]TrendPoint, weeks)
	for i := range out {
		out[i].WeekStart = start.AddDate(0, 0, i*7)
		out[i].Merged = len(cycles[i])
		if v, ok := median(cycles[i]); ok {
			out[i].Median = time.Duration(v) * time.Second
		}
	}
	return out, nil
}

// Dist is a labeled histogram.
type Dist struct {
	Labels []string
	Counts []int
}

func (d Dist) Total() int {
	t := 0
	for _, c := range d.Counts {
		t += c
	}
	return t
}

// TTFRDistribution buckets how long window-opened PRs waited for their first
// human review.
func TTFRDistribution(dbh *sql.DB, f Filter, w Window) (Dist, error) {
	d := Dist{Labels: []string{"<1h", "<4h", "<1d", "<3d", "3d+"}, Counts: make([]int, 5)}
	samples, err := ttfrSamples(dbh, f, w)
	if err != nil {
		return d, err
	}
	for _, list := range samples {
		for _, secs := range list {
			switch {
			case secs < 3600:
				d.Counts[0]++
			case secs < 4*3600:
				d.Counts[1]++
			case secs < 86400:
				d.Counts[2]++
			case secs < 3*86400:
				d.Counts[3]++
			default:
				d.Counts[4]++
			}
		}
	}
	return d, nil
}

// SizeDistribution buckets window-opened PRs by lines changed.
func SizeDistribution(dbh *sql.DB, f Filter, w Window) (Dist, error) {
	d := Dist{Labels: []string{"XS", "S", "M", "L", "XL"}, Counts: make([]int, 5)}
	q := `
		SELECT p.additions + p.deletions
		FROM pull_requests p` + teamRepos() + `
		WHERE p.created_at >= :start AND p.created_at < :end` +
		repoScope() + visibleMember("p.author_login")
	rs, err := dbh.Query(q, namedArgs(f, w)...)
	if err != nil {
		return d, err
	}
	defer rs.Close()
	for rs.Next() {
		var size int
		if err := rs.Scan(&size); err != nil {
			return d, err
		}
		switch {
		case size < 10:
			d.Counts[0]++
		case size < 50:
			d.Counts[1]++
		case size < 250:
			d.Counts[2]++
		case size < 1000:
			d.Counts[3]++
		default:
			d.Counts[4]++
		}
	}
	return d, rs.Err()
}

// Matrix is the reviewer × author review-count grid. Reviewer rows are the
// team's visible members; author columns are the members plus one trailing
// "(others)" column for reviews the team gave on non-member PRs.
type Matrix struct {
	Logins  []string // member axis order (rows; also the first author columns)
	Authors []string // Logins + "(others)" when any such reviews exist
	Counts  [][]int  // Counts[reviewer][author]
	Max     int
}

// ReviewMatrix counts who reviewed whom within the window. Reviews on
// bot-authored PRs (users.is_bot or the config globs) are excluded — the
// chart is about team collaboration, not dependabot triage. Max covers only
// the member↔member cells: "(others)" aggregates every non-member author
// into one column, and letting that aggregate set the heat scale flattened
// the ramp across the real cells.
func ReviewMatrix(dbh *sql.DB, f Filter, w Window) (Matrix, error) {
	visible, err := visibleMembers(dbh, f)
	if err != nil {
		return Matrix{}, err
	}
	idx := make(map[string]int, len(visible))
	for i, l := range visible {
		idx[l] = i
	}
	others := len(visible) // trailing column index
	m := Matrix{Logins: visible, Counts: make([][]int, len(visible))}
	for i := range m.Counts {
		m.Counts[i] = make([]int, len(visible)+1)
	}

	// Reviewers are the visible members; authors are anyone human, so a
	// visible author lands in a cell and any other in "(others)". Reviews
	// *received* from outsiders are not this chart.
	q := `
		SELECT r.author_login, p.author_login, COUNT(*)
		FROM reviews r
		JOIN pull_requests p ON p.id = r.pr_id` + teamRepos() + `
		WHERE r.submitted_at >= :start AND r.submitted_at < :end
		  AND r.author_login != p.author_login` +
		repoScope() + visibleMember("r.author_login") + notBot("p.author_login") + `
		GROUP BY r.author_login, p.author_login`
	rs, err := dbh.Query(q, namedArgs(f, w)...)
	if err != nil {
		return m, err
	}
	defer rs.Close()
	hasOthers := false
	for rs.Next() {
		var reviewer, author string
		var n int
		if err := rs.Scan(&reviewer, &author, &n); err != nil {
			return m, err
		}
		ri := idx[reviewer]
		ai, aok := idx[author]
		if !aok {
			hasOthers = true
			m.Counts[ri][others] += n
			continue // the aggregate column never sets Max
		}
		m.Counts[ri][ai] = n
		if n > m.Max {
			m.Max = n
		}
	}
	m.Authors = visible
	if hasOthers {
		m.Authors = append(append([]string{}, visible...), "(others)")
	} else {
		for i := range m.Counts {
			m.Counts[i] = m.Counts[i][:len(visible)]
		}
	}
	return m, rs.Err()
}

// Punch is a weekday × hour activity grid (local time); row 0 is Monday.
type Punch struct {
	Counts [7][24]int
	Max    int
	Total  int
}

// PunchCard counts member PR-opens and reviews by local weekday and hour.
func PunchCard(dbh *sql.DB, f Filter, w Window) (Punch, error) {
	var p Punch
	add := func(ts int64) {
		t := time.Unix(ts, 0).Local()
		day := (int(t.Weekday()) + 6) % 7 // Monday first
		p.Counts[day][t.Hour()]++
		p.Total++
		if p.Counts[day][t.Hour()] > p.Max {
			p.Max = p.Counts[day][t.Hour()]
		}
	}
	q := `
		SELECT p.created_at
		FROM pull_requests p` + teamRepos() + `
		WHERE p.created_at >= :start AND p.created_at < :end` +
		repoScope() + visibleMember("p.author_login")
	rs, err := dbh.Query(q, namedArgs(f, w)...)
	if err != nil {
		return p, err
	}
	defer rs.Close()
	for rs.Next() {
		var ts int64
		if err := rs.Scan(&ts); err != nil {
			return p, err
		}
		add(ts)
	}
	rs.Close()
	if err := rs.Err(); err != nil {
		return p, err
	}

	q = `
		SELECT r.submitted_at
		FROM reviews r
		JOIN pull_requests p ON p.id = r.pr_id` + teamRepos() + `
		WHERE r.submitted_at >= :start AND r.submitted_at < :end
		  AND r.author_login != p.author_login` +
		repoScope() + visibleMember("r.author_login")
	rs, err = dbh.Query(q, namedArgs(f, w)...)
	if err != nil {
		return p, err
	}
	defer rs.Close()
	for rs.Next() {
		var ts int64
		if err := rs.Scan(&ts); err != nil {
			return p, err
		}
		add(ts)
	}
	rs.Close()
	return p, rs.Err()
}

// StalePR is one open PR in the aging list.
type StalePR struct {
	Repo    string
	Number  int
	Title   string
	Author  string
	AgeDays int
}

// Aging describes currently-open non-draft PRs. It is a "right now" view —
// no window — and covers only PRs present in the cache (the incremental walk
// cannot see PRs untouched since before the backfill horizon).
type Aging struct {
	Buckets Dist
	Total   int
	Stalest []StalePR
}

// OpenAging buckets open PRs by age as of now and lists the stalest few.
func OpenAging(dbh *sql.DB, f Filter, now time.Time) (Aging, error) {
	a := Aging{Buckets: Dist{Labels: []string{"<1d", "<3d", "<1w", "<2w", "2w+"}, Counts: make([]int, 5)}}
	// No window: the aging is "right now", so :start and :end go unused.
	q := `
		SELECT re.owner || '/' || re.name, p.number, p.title, p.author_login, p.created_at
		FROM pull_requests p
		JOIN repos re ON re.id = p.repo_id` + teamRepos() + `
		WHERE p.state = 'OPEN' AND p.is_draft = 0` +
		repoScope() + visibleMember("p.author_login") + `
		ORDER BY p.created_at ASC`
	rs, err := dbh.Query(q, namedArgs(f, Window{})...)
	if err != nil {
		return a, err
	}
	defer rs.Close()
	at := now.Unix()
	for rs.Next() {
		var s StalePR
		var created int64
		if err := rs.Scan(&s.Repo, &s.Number, &s.Title, &s.Author, &created); err != nil {
			return a, err
		}
		age := at - created
		s.AgeDays = int(age / 86400)
		a.Total++
		switch {
		case age < 86400:
			a.Buckets.Counts[0]++
		case age < 3*86400:
			a.Buckets.Counts[1]++
		case age < 7*86400:
			a.Buckets.Counts[2]++
		case age < 14*86400:
			a.Buckets.Counts[3]++
		default:
			a.Buckets.Counts[4]++
		}
		if len(a.Stalest) < 5 {
			a.Stalest = append(a.Stalest, s)
		}
	}
	return a, rs.Err()
}

// FmtDur renders a duration compactly for chart labels: 45s, 12m, 3.5h, 2.1d.
func FmtDur(d time.Duration) string {
	switch {
	case d == 0:
		return "–"
	case d < 90*time.Second:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < 90*time.Minute:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 36*time.Hour:
		return fmt.Sprintf("%.1fh", d.Hours())
	default:
		return fmt.Sprintf("%.1fd", d.Hours()/24)
	}
}
