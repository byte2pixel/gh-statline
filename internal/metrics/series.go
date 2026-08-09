package metrics

import (
	"database/sql"
	"time"
)

// Bucket is one throughput time slice; Day is the bucket's start instant.
type Bucket struct {
	Day    time.Time
	Opened int
	Merged int
}

// BucketSize picks the throughput bucket duration for a window so every
// preset lands near ~30 buckets — enough columns to fill the chart width at
// 7d or 90d just like the daily 30d view does.
func BucketSize(w Window) time.Duration {
	span := time.Duration(w.End-w.Start) * time.Second
	steps := []time.Duration{
		6 * time.Hour, 12 * time.Hour, 24 * time.Hour,
		48 * time.Hour, 72 * time.Hour, 7 * 24 * time.Hour,
	}
	for _, d := range steps {
		if span <= 36*d {
			return d
		}
	}
	return steps[len(steps)-1]
}

// Throughput returns opened/merged counts for team members' PRs across the
// window in BucketSize-sized slices, zero-filled so charts show gaps.
func Throughput(dbh *sql.DB, f Filter, w Window) ([]Bucket, error) {
	cond, condArgs := repoCond(f)
	q := `
		SELECT p.created_at, p.merged_at
		FROM pull_requests p
		JOIN team_repos tr ON tr.repo_id = p.repo_id AND tr.team_id = ?
		JOIN team_members tm ON tm.team_id = tr.team_id AND tm.login = p.author_login
		WHERE (p.created_at >= ? OR p.merged_at >= ?)` + cond
	args := append([]any{f.TeamID, w.Start, w.Start}, condArgs...)
	rs, err := dbh.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rs.Close()

	durSecs := int64(BucketSize(w) / time.Second)
	// Day-or-larger buckets align to UTC midnight so labels are calendar
	// dates; sub-daily buckets align to the window start.
	start := w.Start
	if durSecs >= 86400 {
		start = time.Unix(w.Start, 0).UTC().Truncate(24 * time.Hour).Unix()
	}
	idx := func(ts int64) int {
		return int((ts - start) / durSecs)
	}
	n := int((w.End - start + durSecs - 1) / durSecs)
	if n < 1 {
		n = 1
	}
	buckets := make([]Bucket, n)
	for i := range buckets {
		buckets[i].Day = time.Unix(start+int64(i)*durSecs, 0).UTC()
	}

	for rs.Next() {
		var created int64
		var merged sql.NullInt64
		if err := rs.Scan(&created, &merged); err != nil {
			return nil, err
		}
		if created >= w.Start && created < w.End {
			if i := idx(created); i >= 0 && i < n {
				buckets[i].Opened++
			}
		}
		if merged.Valid && merged.Int64 >= w.Start && merged.Int64 < w.End {
			if i := idx(merged.Int64); i >= 0 && i < n {
				buckets[i].Merged++
			}
		}
	}
	return buckets, rs.Err()
}

// RepoBreakdown is one person's activity within a single repo.
type RepoBreakdown struct {
	Repo          string
	PRsOpened     int
	PRsMerged     int
	ReviewsGiven  int
	CommentsGiven int
}

// PersonRepos breaks one member's window activity down per repo, ordered by
// PRs opened descending; repos with zero activity are omitted.
func PersonRepos(dbh *sql.DB, f Filter, w Window, login string) ([]RepoBreakdown, error) {
	cond, condArgs := repoCond(f)
	byRepo := map[string]*RepoBreakdown{}
	get := func(repo string) *RepoBreakdown {
		if b, ok := byRepo[repo]; ok {
			return b
		}
		b := &RepoBreakdown{Repo: repo}
		byRepo[repo] = b
		return b
	}

	q := `
		SELECT re.owner || '/' || re.name,
		       COUNT(CASE WHEN p.created_at >= ? AND p.created_at < ? THEN 1 END),
		       COUNT(CASE WHEN p.merged_at  >= ? AND p.merged_at  < ? THEN 1 END)
		FROM pull_requests p
		JOIN repos re ON re.id = p.repo_id
		JOIN team_repos tr ON tr.repo_id = p.repo_id AND tr.team_id = ?
		WHERE p.author_login = ? AND (p.created_at >= ? OR p.merged_at >= ?)` + cond + `
		GROUP BY re.id`
	args := append([]any{w.Start, w.End, w.Start, w.End, f.TeamID, login, w.Start, w.Start}, condArgs...)
	rs, err := dbh.Query(q, args...)
	if err != nil {
		return nil, err
	}
	for rs.Next() {
		var repo string
		var opened, merged int
		if err := rs.Scan(&repo, &opened, &merged); err != nil {
			rs.Close()
			return nil, err
		}
		b := get(repo)
		b.PRsOpened, b.PRsMerged = opened, merged
	}
	rs.Close()
	if err := rs.Err(); err != nil {
		return nil, err
	}

	q = `
		SELECT re.owner || '/' || re.name, COUNT(*), COALESCE(SUM(r.comment_count), 0)
		FROM reviews r
		JOIN pull_requests p ON p.id = r.pr_id
		JOIN repos re ON re.id = p.repo_id
		JOIN team_repos tr ON tr.repo_id = p.repo_id AND tr.team_id = ?
		WHERE r.author_login = ? AND r.author_login != p.author_login
		  AND r.submitted_at >= ? AND r.submitted_at < ?` + cond + `
		GROUP BY re.id`
	args = append([]any{f.TeamID, login, w.Start, w.End}, condArgs...)
	rs, err = dbh.Query(q, args...)
	if err != nil {
		return nil, err
	}
	for rs.Next() {
		var repo string
		var reviews, comments int
		if err := rs.Scan(&repo, &reviews, &comments); err != nil {
			rs.Close()
			return nil, err
		}
		b := get(repo)
		b.ReviewsGiven, b.CommentsGiven = reviews, comments
	}
	rs.Close()
	if err := rs.Err(); err != nil {
		return nil, err
	}

	// Conversation-tab comments on others' PRs count as comments given,
	// matching the team stats definition.
	q = `
		SELECT re.owner || '/' || re.name, COUNT(*)
		FROM issue_comments ic
		JOIN pull_requests p ON p.id = ic.pr_id
		JOIN repos re ON re.id = p.repo_id
		JOIN team_repos tr ON tr.repo_id = p.repo_id AND tr.team_id = ?
		WHERE ic.author_login = ? AND ic.author_login != p.author_login
		  AND ic.created_at >= ? AND ic.created_at < ?` + cond + `
		GROUP BY re.id`
	args = append([]any{f.TeamID, login, w.Start, w.End}, condArgs...)
	rs, err = dbh.Query(q, args...)
	if err != nil {
		return nil, err
	}
	for rs.Next() {
		var repo string
		var n int
		if err := rs.Scan(&repo, &n); err != nil {
			rs.Close()
			return nil, err
		}
		get(repo).CommentsGiven += n
	}
	rs.Close()
	if err := rs.Err(); err != nil {
		return nil, err
	}

	out := make([]RepoBreakdown, 0, len(byRepo))
	for _, b := range byRepo {
		out = append(out, *b)
	}
	sortBreakdowns(out)
	return out, nil
}

func sortBreakdowns(bs []RepoBreakdown) {
	for i := 1; i < len(bs); i++ { // insertion sort: lists are tiny
		for j := i; j > 0 && (bs[j].PRsOpened > bs[j-1].PRsOpened ||
			(bs[j].PRsOpened == bs[j-1].PRsOpened && bs[j].Repo < bs[j-1].Repo)); j-- {
			bs[j], bs[j-1] = bs[j-1], bs[j]
		}
	}
}

// PersonActivity returns one value per day: PRs opened plus reviews given,
// a simple pulse line for sparklines.
func PersonActivity(dbh *sql.DB, f Filter, w Window, login string) ([]float64, error) {
	cond, condArgs := repoCond(f)
	start := time.Unix(w.Start, 0).UTC().Truncate(24 * time.Hour)
	end := time.Unix(w.End-1, 0).UTC().Truncate(24 * time.Hour)
	n := int(end.Sub(start).Hours()/24) + 1
	if n < 1 {
		n = 1
	}
	days := make([]float64, n)
	add := func(ts int64) {
		i := int(time.Unix(ts, 0).UTC().Truncate(24*time.Hour).Sub(start).Hours() / 24)
		if i >= 0 && i < n {
			days[i]++
		}
	}

	q := `
		SELECT p.created_at
		FROM pull_requests p
		JOIN team_repos tr ON tr.repo_id = p.repo_id AND tr.team_id = ?
		WHERE p.author_login = ? AND p.created_at >= ? AND p.created_at < ?` + cond
	args := append([]any{f.TeamID, login, w.Start, w.End}, condArgs...)
	rs, err := dbh.Query(q, args...)
	if err != nil {
		return nil, err
	}
	for rs.Next() {
		var ts int64
		if err := rs.Scan(&ts); err != nil {
			rs.Close()
			return nil, err
		}
		add(ts)
	}
	rs.Close()
	if err := rs.Err(); err != nil {
		return nil, err
	}

	q = `
		SELECT r.submitted_at
		FROM reviews r
		JOIN pull_requests p ON p.id = r.pr_id
		JOIN team_repos tr ON tr.repo_id = p.repo_id AND tr.team_id = ?
		WHERE r.author_login = ? AND r.author_login != p.author_login
		  AND r.submitted_at >= ? AND r.submitted_at < ?` + cond
	args = append([]any{f.TeamID, login, w.Start, w.End}, condArgs...)
	rs, err = dbh.Query(q, args...)
	if err != nil {
		return nil, err
	}
	for rs.Next() {
		var ts int64
		if err := rs.Scan(&ts); err != nil {
			rs.Close()
			return nil, err
		}
		add(ts)
	}
	rs.Close()
	return days, rs.Err()
}
