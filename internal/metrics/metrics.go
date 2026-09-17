// Package metrics is the single source of truth for Statline's metric
// definitions. The team stats, charts, person view, and export all consume
// the rows computed here, so the numbers always agree.
//
// Counts are computed in SQL (GROUP BY member with window/repo/bot filters);
// medians are computed in Go because SQLite has no median() and the per-window
// row counts are small. Bot and hidden-member exclusion happens at read time
// only — synced data stays complete, policy stays changeable — and it is
// defined once, in the database: the visible_members and bot_actors views
// (migration 0002, fed by the config mirror). Every query scopes its actor
// columns through the fragments in query.go and binds the shared named
// parameters; no query spells out a bot or hidden predicate of its own.
package metrics

import (
	"database/sql"
	"fmt"
	"math"
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

// LastDays returns a preset window of n days ending at now.
func LastDays(n int, now time.Time) Window {
	now = now.UTC()
	return Window{
		Start: now.AddDate(0, 0, -n).Unix(),
		End:   now.Unix() + 1,
		Label: fmt.Sprintf("Last %d days", n),
	}
}

// Range returns a custom window covering from..to inclusive of the whole
// "to" day. Both bounds are UTC days, and the label is derived from them
// rather than from the caller's location, so it can never name a different
// day than the window covers.
func Range(from, to time.Time) Window {
	start := from.UTC().Truncate(24 * time.Hour)
	end := to.UTC().Truncate(24 * time.Hour)
	return Window{
		Start: start.Unix(),
		End:   end.Unix() + 86400,
		Label: start.Format("2006-01-02") + " → " + end.Format("2006-01-02"),
	}
}

// PrevWindow returns the equal-length window immediately before w, for
// window-over-window comparisons.
func PrevWindow(w Window) Window {
	span := w.End - w.Start
	return Window{Start: w.Start - span, End: w.Start, Label: "previous " + w.Label}
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
	Dismissed     int           // review whose state GitHub rewrote to DISMISSED
	ReviewsGiven  int           // sum of the four states above
	CommentsGiven int           // review-thread + conversation comments on others' PRs
	CommentsRecv  int           // others' comments on this member's PRs
	CycleTimeP50  time.Duration // open → merge, PRs merged in window; 0 = no data
	TTFRP50       time.Duration // open → first non-bot, non-author review
	SizeP50       int           // additions+deletions, PRs opened in window; -1 = no data
}

// TeamStats computes one Row per visible team member.
func TeamStats(dbh *sql.DB, f Filter, w Window) ([]Row, error) {
	members, err := visibleMembers(dbh, f)
	if err != nil {
		return nil, err
	}
	rows := make(map[string]*Row, len(members))
	order := make([]string, 0, len(members))
	for _, m := range members {
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

// visibleMembers returns the team members that views actually show, in
// login order: the visible_members view, which is the roster minus hidden
// members and bot_actors (users.is_bot or a config glob).
func visibleMembers(dbh *sql.DB, f Filter) ([]string, error) {
	rs, err := dbh.Query(`SELECT login FROM visible_members WHERE team_id = :team ORDER BY login`,
		sql.Named("team", f.TeamID))
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	out := []string{}
	for rs.Next() {
		var l string
		if err := rs.Scan(&l); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rs.Err()
}

// visibleCond restricts an actor column to the members views show, for the
// team-level aggregates that have no per-member result map to filter against
// afterwards. It assumes the query joins team_members as tm on that same
// column. Append its args immediately after the string is concatenated: the
// placeholders are positional.
func visibleCond(dbh *sql.DB, f Filter, col string) (string, []any, error) {
	bots, err := botLogins(dbh, f)
	if err != nil {
		return "", nil, err
	}
	cond, args := notBotCond(col, bots)
	return " AND tm.hidden = 0" + cond, args, nil
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
	q := `
		SELECT p.author_login,
		       COUNT(CASE WHEN p.created_at >= :start AND p.created_at < :end THEN 1 END),
		       COUNT(CASE WHEN p.merged_at  >= :start AND p.merged_at  < :end THEN 1 END)
		FROM pull_requests p` + teamRepos() + `
		WHERE (p.created_at >= :start OR p.merged_at >= :start)` +
		repoScope() + visibleMember("p.author_login") + `
		GROUP BY p.author_login`
	rs, err := dbh.Query(q, namedArgs(f, w)...)
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
	// Self-"reviews" are excluded: replying to a thread on your own PR makes
	// GitHub create an implicit COMMENTED review by you on your own PR.
	q := `
		SELECT r.author_login, r.state, COUNT(*)
		FROM reviews r
		JOIN pull_requests p ON p.id = r.pr_id` + teamRepos() + `
		WHERE r.submitted_at >= :start AND r.submitted_at < :end
		  AND r.author_login != p.author_login` +
		repoScope() + visibleMember("r.author_login") + `
		GROUP BY r.author_login, r.state`
	rs, err := dbh.Query(q, namedArgs(f, w)...)
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
		case "DISMISSED":
			// The review happened; GitHub just rewrote its state, usually
			// because a push invalidated an approval. Every other view
			// counts it, so dropping it here made one document disagree
			// with itself.
			r.Dismissed += n
		}
	}
	if err := rs.Err(); err != nil {
		return err
	}
	for _, r := range rows {
		r.ReviewsGiven = r.Approved + r.Commented + r.ChangesReq + r.Dismissed
	}
	return nil
}

func fillCommentsGiven(dbh *sql.DB, f Filter, w Window, rows map[string]*Row) error {
	// Review-thread comments carry the author of their parent review; a
	// conversation-tab comment is its own row. Own-PR comments don't count.
	q := `
		SELECT login, SUM(n) FROM (
			SELECT r.author_login AS login, COALESCE(SUM(r.comment_count), 0) AS n
			FROM reviews r
			JOIN pull_requests p ON p.id = r.pr_id` + teamRepos() + `
			WHERE r.submitted_at >= :start AND r.submitted_at < :end
			  AND r.author_login != p.author_login` +
		repoScope() + visibleMember("r.author_login") + `
			GROUP BY r.author_login
			UNION ALL
			SELECT ic.author_login AS login, COUNT(*) AS n
			FROM issue_comments ic
			JOIN pull_requests p ON p.id = ic.pr_id` + teamRepos() + `
			WHERE ic.created_at >= :start AND ic.created_at < :end
			  AND ic.author_login != p.author_login` +
		repoScope() + visibleMember("ic.author_login") + `
			GROUP BY ic.author_login
		) GROUP BY login`
	rs, err := dbh.Query(q, namedArgs(f, w)...)
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
	// Grouped by the PR author. The commenter need not be a member (an
	// outside reviewer's comment is still one you received), only human.
	q := `
		SELECT login, SUM(n) FROM (
			SELECT p.author_login AS login, COALESCE(SUM(r.comment_count), 0) AS n
			FROM reviews r
			JOIN pull_requests p ON p.id = r.pr_id` + teamRepos() + `
			WHERE r.submitted_at >= :start AND r.submitted_at < :end
			  AND r.author_login != p.author_login` +
		repoScope() + visibleMember("p.author_login") + notBot("r.author_login") + `
			GROUP BY p.author_login
			UNION ALL
			SELECT p.author_login AS login, COUNT(*) AS n
			FROM issue_comments ic
			JOIN pull_requests p ON p.id = ic.pr_id` + teamRepos() + `
			WHERE ic.created_at >= :start AND ic.created_at < :end
			  AND ic.author_login != p.author_login` +
		repoScope() + visibleMember("p.author_login") + notBot("ic.author_login") + `
			GROUP BY p.author_login
		) GROUP BY login`
	rs, err := dbh.Query(q, namedArgs(f, w)...)
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
	q := `
		SELECT p.author_login, p.created_at, p.merged_at, p.additions + p.deletions
		FROM pull_requests p` + teamRepos() + `
		WHERE (p.created_at >= :start OR p.merged_at >= :start)` +
		repoScope() + visibleMember("p.author_login")
	rs, err := dbh.Query(q, namedArgs(f, w)...)
	if err != nil {
		return err
	}
	defer rs.Close()
	cycles := map[string][]int64{}
	sizes := map[string][]int64{}
	for rs.Next() {
		var login string
		var created int64
		var merged sql.NullInt64
		var size int64
		if err := rs.Scan(&login, &created, &merged, &size); err != nil {
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

	ttfrs, err := ttfrSamples(dbh, f, w)
	if err != nil {
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

// firstReview is one PR's time to first review: the PR's author and
// creation instant, and the latency to its earliest human, non-author
// review.
type firstReview struct {
	Author  string
	Created int64
	Secs    int64
}

// firstReviews returns the first-review latency of every PR a visible
// member created in the window and that has at least one review by
// somebody else who is not a bot. The reviewer need not be on the roster
// (an outside review is still a first review) and needs no users row: the
// bot_actors view lists bots, so an unknown reviewer is simply human.
func firstReviews(dbh *sql.DB, f Filter, w Window) ([]firstReview, error) {
	q := `
		SELECT p.author_login, p.created_at, MIN(r.submitted_at) - p.created_at
		FROM pull_requests p` + teamRepos() + `
		JOIN reviews r ON r.pr_id = p.id AND r.author_login != p.author_login` + notBot("r.author_login") + `
		WHERE p.created_at >= :start AND p.created_at < :end` +
		repoScope() + visibleMember("p.author_login") + `
		GROUP BY p.id`
	rs, err := dbh.Query(q, namedArgs(f, w)...)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []firstReview
	for rs.Next() {
		var s firstReview
		if err := rs.Scan(&s.Author, &s.Created, &s.Secs); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rs.Err()
}

// ttfrSamples groups firstReviews by author, in seconds.
func ttfrSamples(dbh *sql.DB, f Filter, w Window) (map[string][]int64, error) {
	samples, err := firstReviews(dbh, f, w)
	if err != nil {
		return nil, err
	}
	ttfrs := map[string][]int64{}
	for _, s := range samples {
		ttfrs[s.Author] = append(ttfrs[s.Author], s.Secs)
	}
	return ttfrs, nil
}

// TeamMedians returns the team-level p50 cycle time (open→merge for PRs
// merged in the window) and p50 time-to-first-review (PRs opened in the
// window). Zero means no data.
func TeamMedians(dbh *sql.DB, f Filter, w Window) (cycle, ttfr time.Duration, err error) {
	q := `
		SELECT p.merged_at - p.created_at
		FROM pull_requests p` + teamRepos() + `
		WHERE p.merged_at >= :start AND p.merged_at < :end` +
		repoScope() + visibleMember("p.author_login")
	rs, err := dbh.Query(q, namedArgs(f, w)...)
	if err != nil {
		return 0, 0, err
	}
	defer rs.Close()
	var cycles []int64
	for rs.Next() {
		var c int64
		if err := rs.Scan(&c); err != nil {
			return 0, 0, err
		}
		cycles = append(cycles, c)
	}
	rs.Close()
	if err := rs.Err(); err != nil {
		return 0, 0, err
	}
	if v, ok := median(cycles); ok {
		cycle = time.Duration(v) * time.Second
	}

	samples, err := firstReviews(dbh, f, w)
	if err != nil {
		return 0, 0, err
	}
	all := make([]int64, 0, len(samples))
	for _, s := range samples {
		all = append(all, s.Secs)
	}
	if v, ok := median(all); ok {
		ttfr = time.Duration(v) * time.Second
	}
	return cycle, ttfr, nil
}

// CoverageFloor reports how far back the cache is trustworthy for a team:
// the oldest backfill horizon ever synced across its repos (sync_state
// accretes — data fetched once is never deleted, so coverage deepens the
// longer statline is used). ok is false until every repo has completed a
// sync.
func CoverageFloor(dbh *sql.DB, teamID int64) (floor int64, ok bool) {
	var total, covered int
	var minFloor sql.NullInt64
	err := dbh.QueryRow(`
		SELECT COUNT(*), COUNT(ss.backfill_until), MIN(ss.backfill_until)
		FROM team_repos tr
		LEFT JOIN sync_state ss ON ss.repo_id = tr.repo_id
		WHERE tr.team_id = ?`, teamID).Scan(&total, &covered, &minFloor)
	if err != nil || total == 0 || covered < total || !minFloor.Valid {
		return 0, false
	}
	return minFloor.Int64, true
}

// PctChange is the one percent-change definition every view renders: the
// signed change from prior to recent, rounded to the nearest whole percent
// (half away from zero). The stat tiles used to truncate while the movers
// rounded, so the same 7→10 week read ▲42% on one and +43% on the other.
// A zero prior has no percentage — callers label that case "new" themselves
// and get 0 here, never a clamped stand-in.
func PctChange(prior, recent int) int {
	if prior == 0 {
		return 0
	}
	return int(math.Round(100 * float64(recent-prior) / float64(prior)))
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
