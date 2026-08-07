// Package metrics is the single source of truth for Statline's metric
// definitions. The leaderboard, charts, person view, and export all consume
// the rows computed here, so the numbers always agree.
//
// Counts are computed in SQL (GROUP BY member with window/repo/bot filters);
// medians are computed in Go because SQLite has no median() and the per-window
// row counts are small. Bot exclusion happens at read time only — synced data
// stays complete, policy stays changeable. SQL-side bot filtering uses the
// users.is_bot flag (from GraphQL __typename); the config glob list
// additionally filters reviewer logins Go-side where reviews are inspected
// individually (time-to-first-review).
package metrics

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/byte2pixel/gh-statline/internal/config"
)

// Window is a half-open time range [Start, End) in unix epoch seconds UTC.
type Window struct {
	Start int64
	End   int64
	Label string
}

// LastDays returns a preset window ending now.
func LastDays(n int) Window {
	now := time.Now().UTC()
	return Window{
		Start: now.AddDate(0, 0, -n).Unix(),
		End:   now.Unix() + 1,
		Label: fmt.Sprintf("Last %d days", n),
	}
}

// Range returns a custom window covering from..to inclusive of the whole
// "to" day.
func Range(from, to time.Time) Window {
	return Window{
		Start: from.UTC().Truncate(24 * time.Hour).Unix(),
		End:   to.UTC().Truncate(24*time.Hour).Unix() + 86400,
		Label: from.Format("2006-01-02") + " → " + to.Format("2006-01-02"),
	}
}

// Filter selects whose activity to compute over which repos.
type Filter struct {
	TeamID  int64
	RepoIDs []int64 // nil/empty = all of the team's repos
	Bots    *config.BotMatcher
}

// Row is one member's stat line for a window.
type Row struct {
	Login         string
	PRsOpened     int
	PRsMerged     int
	Approved      int
	Commented     int
	ChangesReq    int
	ReviewsGiven  int // sum of the three states above
	CommentsGiven int // review-thread + conversation comments on others' PRs
	CommentsRecv  int // others' comments on this member's PRs
	CycleTimeP50  time.Duration // open → merge, PRs merged in window; 0 = no data
	TTFRP50       time.Duration // open → first non-bot, non-author review
	SizeP50       int           // additions+deletions, PRs opened in window; -1 = no data
}

// Leaderboard computes one Row per visible team member.
func Leaderboard(dbh *sql.DB, f Filter, w Window) ([]Row, error) {
	members, err := teamMembers(dbh, f.TeamID)
	if err != nil {
		return nil, err
	}
	rows := make(map[string]*Row, len(members))
	order := make([]string, 0, len(members))
	for _, m := range members {
		if f.Bots != nil && f.Bots.IsBot(m) {
			continue
		}
		rows[m] = &Row{Login: m, SizeP50: -1}
		order = append(order, m)
	}

	steps := []func(*sql.DB, Filter, Window, map[string]*Row) error{
		fillPRCounts,
		fillReviewCounts,
		fillCommentsGiven,
		fillCommentsReceived,
		fillMedians,
	}
	for _, step := range steps {
		if err := step(dbh, f, w, rows); err != nil {
			return nil, err
		}
	}

	out := make([]Row, 0, len(order))
	for _, login := range order {
		out = append(out, *rows[login])
	}
	return out, nil
}

func teamMembers(dbh *sql.DB, teamID int64) ([]string, error) {
	rs, err := dbh.Query(`SELECT login FROM team_members WHERE team_id = ? AND hidden = 0 ORDER BY login`, teamID)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var members []string
	for rs.Next() {
		var l string
		if err := rs.Scan(&l); err != nil {
			return nil, err
		}
		members = append(members, l)
	}
	return members, rs.Err()
}

// botLogins returns every known login that should be excluded as a bot:
// flagged is_bot by GraphQL typename, or matching the config glob list.
func botLogins(dbh *sql.DB, f Filter) ([]string, error) {
	rs, err := dbh.Query(`SELECT login, is_bot FROM users`)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []string
	for rs.Next() {
		var login string
		var isBot int
		if err := rs.Scan(&login, &isBot); err != nil {
			return nil, err
		}
		if isBot == 1 || (f.Bots != nil && f.Bots.IsBot(login)) {
			out = append(out, login)
		}
	}
	return out, rs.Err()
}

// notBotCond returns "AND <col> NOT IN (...)" plus args, or "" when there
// are no known bots.
func notBotCond(col string, bots []string) (string, []any) {
	if len(bots) == 0 {
		return "", nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(bots)), ",")
	args := make([]any, len(bots))
	for i, b := range bots {
		args[i] = b
	}
	return " AND " + col + " NOT IN (" + ph + ")", args
}

// repoCond returns "AND p.repo_id IN (...)" limited to the filter's repos,
// plus its args. The team_repos join already restricts to team repos when no
// explicit filter is set.
func repoCond(f Filter) (string, []any) {
	if len(f.RepoIDs) == 0 {
		return "", nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(f.RepoIDs)), ",")
	args := make([]any, len(f.RepoIDs))
	for i, id := range f.RepoIDs {
		args[i] = id
	}
	return " AND p.repo_id IN (" + ph + ")", args
}

func fillPRCounts(dbh *sql.DB, f Filter, w Window, rows map[string]*Row) error {
	cond, condArgs := repoCond(f)
	q := `
		SELECT p.author_login,
		       COUNT(CASE WHEN p.created_at >= ? AND p.created_at < ? THEN 1 END),
		       COUNT(CASE WHEN p.merged_at  >= ? AND p.merged_at  < ? THEN 1 END)
		FROM pull_requests p
		JOIN team_repos tr ON tr.repo_id = p.repo_id AND tr.team_id = ?
		JOIN team_members tm ON tm.team_id = tr.team_id AND tm.login = p.author_login
		WHERE (p.created_at >= ? OR p.merged_at >= ?)` + cond + `
		GROUP BY p.author_login`
	args := append([]any{w.Start, w.End, w.Start, w.End, f.TeamID, w.Start, w.Start}, condArgs...)
	rs, err := dbh.Query(q, args...)
	if err != nil {
		return err
	}
	defer rs.Close()
	for rs.Next() {
		var login string
		var opened, merged int
		if err := rs.Scan(&login, &opened, &merged); err != nil {
			return err
		}
		if r, ok := rows[login]; ok {
			r.PRsOpened, r.PRsMerged = opened, merged
		}
	}
	return rs.Err()
}

func fillReviewCounts(dbh *sql.DB, f Filter, w Window, rows map[string]*Row) error {
	cond, condArgs := repoCond(f)
	// Self-"reviews" are excluded: replying to a thread on your own PR makes
	// GitHub create an implicit COMMENTED review by you on your own PR.
	q := `
		SELECT r.author_login, r.state, COUNT(*)
		FROM reviews r
		JOIN pull_requests p ON p.id = r.pr_id
		JOIN team_repos tr ON tr.repo_id = p.repo_id AND tr.team_id = ?
		JOIN team_members tm ON tm.team_id = tr.team_id AND tm.login = r.author_login
		WHERE r.submitted_at >= ? AND r.submitted_at < ?
		  AND r.author_login != p.author_login` + cond + `
		GROUP BY r.author_login, r.state`
	args := append([]any{f.TeamID, w.Start, w.End}, condArgs...)
	rs, err := dbh.Query(q, args...)
	if err != nil {
		return err
	}
	defer rs.Close()
	for rs.Next() {
		var login, state string
		var n int
		if err := rs.Scan(&login, &state, &n); err != nil {
			return err
		}
		r, ok := rows[login]
		if !ok {
			continue
		}
		switch state {
		case "APPROVED":
			r.Approved += n
		case "CHANGES_REQUESTED":
			r.ChangesReq += n
		case "COMMENTED":
			r.Commented += n
		}
	}
	if err := rs.Err(); err != nil {
		return err
	}
	for _, r := range rows {
		r.ReviewsGiven = r.Approved + r.Commented + r.ChangesReq
	}
	return nil
}

func fillCommentsGiven(dbh *sql.DB, f Filter, w Window, rows map[string]*Row) error {
	cond, condArgs := repoCond(f)
	// Review-thread comments carry the author of their parent review; a
	// conversation-tab comment is its own row. Own-PR comments don't count.
	q := `
		SELECT login, SUM(n) FROM (
			SELECT r.author_login AS login, COALESCE(SUM(r.comment_count), 0) AS n
			FROM reviews r
			JOIN pull_requests p ON p.id = r.pr_id
			JOIN team_repos tr ON tr.repo_id = p.repo_id AND tr.team_id = ?
			WHERE r.submitted_at >= ? AND r.submitted_at < ?
			  AND r.author_login != p.author_login` + cond + `
			GROUP BY r.author_login
			UNION ALL
			SELECT ic.author_login AS login, COUNT(*) AS n
			FROM issue_comments ic
			JOIN pull_requests p ON p.id = ic.pr_id
			JOIN team_repos tr ON tr.repo_id = p.repo_id AND tr.team_id = ?
			WHERE ic.created_at >= ? AND ic.created_at < ?
			  AND ic.author_login != p.author_login` + cond + `
			GROUP BY ic.author_login
		) GROUP BY login`
	args := append([]any{f.TeamID, w.Start, w.End}, condArgs...)
	args = append(args, f.TeamID, w.Start, w.End)
	args = append(args, condArgs...)
	rs, err := dbh.Query(q, args...)
	if err != nil {
		return err
	}
	defer rs.Close()
	for rs.Next() {
		var login string
		var n int
		if err := rs.Scan(&login, &n); err != nil {
			return err
		}
		if r, ok := rows[login]; ok {
			r.CommentsGiven = n
		}
	}
	return rs.Err()
}

func fillCommentsReceived(dbh *sql.DB, f Filter, w Window, rows map[string]*Row) error {
	cond, condArgs := repoCond(f)
	bots, err := botLogins(dbh, f)
	if err != nil {
		return err
	}
	revBot, revBotArgs := notBotCond("r.author_login", bots)
	icBot, icBotArgs := notBotCond("ic.author_login", bots)
	// Grouped by the PR author; bot commenters (typename or glob) excluded.
	q := `
		SELECT login, SUM(n) FROM (
			SELECT p.author_login AS login, COALESCE(SUM(r.comment_count), 0) AS n
			FROM reviews r
			JOIN pull_requests p ON p.id = r.pr_id
			JOIN team_repos tr ON tr.repo_id = p.repo_id AND tr.team_id = ?
			WHERE r.submitted_at >= ? AND r.submitted_at < ?
			  AND r.author_login != p.author_login` + cond + revBot + `
			GROUP BY p.author_login
			UNION ALL
			SELECT p.author_login AS login, COUNT(*) AS n
			FROM issue_comments ic
			JOIN pull_requests p ON p.id = ic.pr_id
			JOIN team_repos tr ON tr.repo_id = p.repo_id AND tr.team_id = ?
			WHERE ic.created_at >= ? AND ic.created_at < ?
			  AND ic.author_login != p.author_login` + cond + icBot + `
			GROUP BY p.author_login
		) GROUP BY login`
	args := append([]any{f.TeamID, w.Start, w.End}, condArgs...)
	args = append(args, revBotArgs...)
	args = append(args, f.TeamID, w.Start, w.End)
	args = append(args, condArgs...)
	args = append(args, icBotArgs...)
	rs, err := dbh.Query(q, args...)
	if err != nil {
		return err
	}
	defer rs.Close()
	for rs.Next() {
		var login string
		var n int
		if err := rs.Scan(&login, &n); err != nil {
			return err
		}
		if r, ok := rows[login]; ok {
			r.CommentsRecv = n
		}
	}
	return rs.Err()
}

// fillMedians computes cycle time (PRs merged in window), size (PRs opened
// in window), and time-to-first-review (first non-author, non-bot review on
// PRs opened in window).
func fillMedians(dbh *sql.DB, f Filter, w Window, rows map[string]*Row) error {
	cond, condArgs := repoCond(f)

	q := `
		SELECT p.author_login, p.created_at, p.merged_at, p.additions + p.deletions
		FROM pull_requests p
		JOIN team_repos tr ON tr.repo_id = p.repo_id AND tr.team_id = ?
		JOIN team_members tm ON tm.team_id = tr.team_id AND tm.login = p.author_login
		WHERE (p.created_at >= ? OR p.merged_at >= ?)` + cond
	args := append([]any{f.TeamID, w.Start, w.Start}, condArgs...)
	rs, err := dbh.Query(q, args...)
	if err != nil {
		return err
	}
	cycles := map[string][]int64{}
	sizes := map[string][]int64{}
	for rs.Next() {
		var login string
		var created int64
		var merged sql.NullInt64
		var size int64
		if err := rs.Scan(&login, &created, &merged, &size); err != nil {
			rs.Close()
			return err
		}
		if created >= w.Start && created < w.End {
			sizes[login] = append(sizes[login], size)
		}
		if merged.Valid && merged.Int64 >= w.Start && merged.Int64 < w.End {
			cycles[login] = append(cycles[login], merged.Int64-created)
		}
	}
	rs.Close()
	if err := rs.Err(); err != nil {
		return err
	}

	// Time to first review: pull every candidate review for window-opened
	// PRs, pick the first acceptable reviewer per PR in Go so the config
	// bot-glob list can veto reviewers the is_bot flag missed.
	q = `
		SELECT p.id, p.author_login, p.created_at, r.author_login, r.submitted_at, u.is_bot
		FROM pull_requests p
		JOIN team_repos tr ON tr.repo_id = p.repo_id AND tr.team_id = ?
		JOIN team_members tm ON tm.team_id = tr.team_id AND tm.login = p.author_login
		JOIN reviews r ON r.pr_id = p.id AND r.author_login != p.author_login
		JOIN users u ON u.login = r.author_login
		WHERE p.created_at >= ? AND p.created_at < ?` + cond + `
		ORDER BY p.id, r.submitted_at`
	args = append([]any{f.TeamID, w.Start, w.End}, condArgs...)
	rs, err = dbh.Query(q, args...)
	if err != nil {
		return err
	}
	ttfrs := map[string][]int64{}
	seenPR := map[string]bool{}
	for rs.Next() {
		var prID, prAuthor, reviewer string
		var created, submitted int64
		var isBot int
		if err := rs.Scan(&prID, &prAuthor, &created, &reviewer, &submitted, &isBot); err != nil {
			rs.Close()
			return err
		}
		if seenPR[prID] {
			continue
		}
		if isBot == 1 || (f.Bots != nil && f.Bots.IsBot(reviewer)) {
			continue
		}
		seenPR[prID] = true
		ttfrs[prAuthor] = append(ttfrs[prAuthor], submitted-created)
	}
	rs.Close()
	if err := rs.Err(); err != nil {
		return err
	}

	for login, r := range rows {
		if v, ok := median(cycles[login]); ok {
			r.CycleTimeP50 = time.Duration(v) * time.Second
		}
		if v, ok := median(ttfrs[login]); ok {
			r.TTFRP50 = time.Duration(v) * time.Second
		}
		if v, ok := median(sizes[login]); ok {
			r.SizeP50 = int(v)
		}
	}
	return nil
}

// median returns the middle value (lower-middle for even counts, keeping the
// result an actually-observed value) and whether data existed.
func median(vals []int64) (int64, bool) {
	if len(vals) == 0 {
		return 0, false
	}
	sort.Slice(vals, func(i, j int) bool { return vals[i] < vals[j] })
	return vals[(len(vals)-1)/2], true
}
