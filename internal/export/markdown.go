// Package export renders Statline views as Markdown for pasting into
// standups, retros, and 1:1 notes.
package export

import (
	"fmt"
	"strings"
	"time"

	"github.com/byte2pixel/gh-statline/internal/metrics"
)

// Leaderboard renders the stat lines as a GitHub-flavored Markdown table.
func Leaderboard(team string, w metrics.Window, rows []metrics.Row) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s — %s\n\n", team, w.Label)
	b.WriteString("| Member | PRs | Merged | Reviews | Approved | Commented | Changes req. | Comments given | Comments recv. | Cycle p50 | First review p50 | Size p50 |\n")
	b.WriteString("|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %d | %d | %d | %d | %d | %s | %s | %s |\n",
			r.Login, r.PRsOpened, r.PRsMerged, r.ReviewsGiven, r.Approved, r.Commented,
			r.ChangesReq, r.CommentsGiven, r.CommentsRecv,
			mdDur(r.CycleTimeP50), mdDur(r.TTFRP50), mdSize(r.SizeP50))
	}
	return b.String()
}

// Person renders one member's per-repo breakdown.
func Person(login string, w metrics.Window, row metrics.Row, repos []metrics.RepoBreakdown) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s — %s\n\n", login, w.Label)
	fmt.Fprintf(&b, "PRs opened %d · merged %d · reviews given %d (%d approved, %d commented, %d changes requested) · comments given %d / received %d\n\n",
		row.PRsOpened, row.PRsMerged, row.ReviewsGiven, row.Approved, row.Commented,
		row.ChangesReq, row.CommentsGiven, row.CommentsRecv)
	b.WriteString("| Repo | PRs | Merged | Reviews | Comments |\n|---|---:|---:|---:|---:|\n")
	for _, r := range repos {
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %d |\n",
			r.Repo, r.PRsOpened, r.PRsMerged, r.ReviewsGiven, r.CommentsGiven)
	}
	return b.String()
}

func mdDur(d time.Duration) string {
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

func mdSize(s int) string {
	if s < 0 {
		return "–"
	}
	return fmt.Sprintf("%d", s)
}
