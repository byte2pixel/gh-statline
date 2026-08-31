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
	cond, condArgs := repoCond(f)
	q := `
		SELECT p.created_at, p.merged_at
		FROM pull_requests p
		JOIN team_repos tr ON tr.repo_id = p.repo_id AND tr.team_id = ?
		JOIN team_members tm ON tm.team_id = tr.team_id AND tm.login = p.author_login
		WHERE p.merged_at >= ? AND p.merged_at < ?` + cond
	args := append([]any{f.TeamID, w.Start, w.End}, condArgs...)
	rs, err := dbh.Query(q, args...)
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
	cond, condArgs := repoCond(f)
	q := `
		SELECT p.additions + p.deletions
		FROM pull_requests p
		JOIN team_repos tr ON tr.repo_id = p.repo_id AND tr.team_id = ?
		JOIN team_members tm ON tm.team_id = tr.team_id AND tm.login = p.author_login
		WHERE p.created_at >= ? AND p.created_at < ?` + cond
	args := append([]any{f.TeamID, w.Start, w.End}, condArgs...)
	rs, err := dbh.Query(q, args...)
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

// ReviewMatrix counts who reviewed whom within the window.
func ReviewMatrix(dbh *sql.DB, f Filter, w Window) (Matrix, error) {
	logins, err := teamMembers(dbh, f.TeamID)
	if err != nil {
		return Matrix{}, err
	}
	idx := make(map[string]int, len(logins))
	visible := logins[:0]
	for _, l := range logins {
		if f.Bots != nil && f.Bots.IsBot(l) {
			continue
		}
		idx[l] = len(visible)
		visible = append(visible, l)
	}
	others := len(visible) // trailing column index
	m := Matrix{Logins: visible, Counts: make([][]int, len(visible))}
	for i := range m.Counts {
		m.Counts[i] = make([]int, len(visible)+1)
	}

	cond, condArgs := repoCond(f)
	q := `
		SELECT r.author_login, p.author_login, COUNT(*)
		FROM reviews r
		JOIN pull_requests p ON p.id = r.pr_id
		JOIN team_repos tr ON tr.repo_id = p.repo_id AND tr.team_id = ?
		WHERE r.submitted_at >= ? AND r.submitted_at < ?
		  AND r.author_login != p.author_login` + cond + `
		GROUP BY r.author_login, p.author_login`
	args := append([]any{f.TeamID, w.Start, w.End}, condArgs...)
	rs, err := dbh.Query(q, args...)
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
		ri, rok := idx[reviewer]
		if !rok {
			continue // reviews *received* from outsiders aren't this chart
		}
		ai, aok := idx[author]
		if !aok {
			ai = others
			hasOthers = true
			m.Counts[ri][ai] += n
			n = m.Counts[ri][ai]
		} else {
			m.Counts[ri][ai] = n
		}
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
	cond, condArgs := repoCond(f)

	q := `
		SELECT p.created_at
		FROM pull_requests p
		JOIN team_repos tr ON tr.repo_id = p.repo_id AND tr.team_id = ?
		JOIN team_members tm ON tm.team_id = tr.team_id AND tm.login = p.author_login
		WHERE p.created_at >= ? AND p.created_at < ?` + cond
	args := append([]any{f.TeamID, w.Start, w.End}, condArgs...)
	rs, err := dbh.Query(q, args...)
	if err != nil {
		return p, err
	}
	for rs.Next() {
		var ts int64
		if err := rs.Scan(&ts); err != nil {
			rs.Close()
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
		JOIN pull_requests p ON p.id = r.pr_id
		JOIN team_repos tr ON tr.repo_id = p.repo_id AND tr.team_id = ?
		JOIN team_members tm ON tm.team_id = tr.team_id AND tm.login = r.author_login
		WHERE r.submitted_at >= ? AND r.submitted_at < ?
		  AND r.author_login != p.author_login` + cond
	args = append([]any{f.TeamID, w.Start, w.End}, condArgs...)
	rs, err = dbh.Query(q, args...)
	if err != nil {
		return p, err
	}
	for rs.Next() {
		var ts int64
		if err := rs.Scan(&ts); err != nil {
			rs.Close()
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

// OpenAging buckets open PRs by age and lists the stalest few.
func OpenAging(dbh *sql.DB, f Filter) (Aging, error) {
	a := Aging{Buckets: Dist{Labels: []string{"<1d", "<3d", "<1w", "<2w", "2w+"}, Counts: make([]int, 5)}}
	cond, condArgs := repoCond(f)
	q := `
		SELECT re.owner || '/' || re.name, p.number, p.title, p.author_login, p.created_at
		FROM pull_requests p
		JOIN repos re ON re.id = p.repo_id
		JOIN team_repos tr ON tr.repo_id = p.repo_id AND tr.team_id = ?
		JOIN team_members tm ON tm.team_id = tr.team_id AND tm.login = p.author_login
		WHERE p.state = 'OPEN' AND p.is_draft = 0` + cond + `
		ORDER BY p.created_at ASC`
	args := append([]any{f.TeamID}, condArgs...)
	rs, err := dbh.Query(q, args...)
	if err != nil {
		return a, err
	}
	defer rs.Close()
	now := time.Now().Unix()
	for rs.Next() {
		var s StalePR
		var created int64
		if err := rs.Scan(&s.Repo, &s.Number, &s.Title, &s.Author, &created); err != nil {
			return a, err
		}
		age := now - created
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
