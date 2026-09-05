// Package export renders Statline views as Markdown for pasting into
// standups, retros, and 1:1 notes.
package export

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/byte2pixel/gh-statline/internal/metrics"
	"github.com/byte2pixel/gh-statline/internal/text"
)

var (
	// A literal pipe ends the cell early and a newline ends the row, so both
	// have to go. PR titles arrive from the GitHub API and routinely contain
	// pipes; one of them used to break the whole table. Escape sequences in a
	// title would ride the clipboard onto whatever terminal pastes it, so
	// text.Sanitize runs on every cell too.
	cellReplacer = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "|", `\|`)
	lineReplacer = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ")
)

func cell(s string) string { return strings.TrimSpace(text.Sanitize(cellReplacer.Replace(s))) }

// heading keeps a title on one line. Pipes are harmless outside a table.
func heading(s string) string { return strings.TrimSpace(text.Sanitize(lineReplacer.Replace(s))) }

// TeamStats renders the stat lines as a GitHub-flavored Markdown table.
func TeamStats(team string, w metrics.Window, rows []metrics.Row) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s — %s\n\n", heading(team), heading(w.Label))
	b.WriteString("| Member | PRs | Merged | Reviews | Approved | Commented | Changes req. | Dismissed | Comments given | Comments recv. | Cycle p50 | First review p50 | Size p50 |\n")
	b.WriteString("|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %d | %d | %d | %d | %d | %d | %s | %s | %s |\n",
			cell(r.Login), r.PRsOpened, r.PRsMerged, r.ReviewsGiven, r.Approved, r.Commented,
			r.ChangesReq, r.Dismissed, r.CommentsGiven, r.CommentsRecv,
			metrics.FmtDur(r.CycleTimeP50), metrics.FmtDur(r.TTFRP50), mdSize(r.SizeP50))
	}
	return b.String()
}

// Person renders one member's per-repo breakdown.
func Person(login string, w metrics.Window, row metrics.Row, repos []metrics.RepoBreakdown) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s — %s\n\n", heading(login), heading(w.Label))
	fmt.Fprintf(&b, "PRs opened %d · merged %d · reviews given %d (%d approved, %d commented, %d changes requested) · comments given %d / received %d\n\n",
		row.PRsOpened, row.PRsMerged, row.ReviewsGiven, row.Approved, row.Commented,
		row.ChangesReq, row.CommentsGiven, row.CommentsRecv)
	b.WriteString("| Repo | PRs | Merged | Reviews | Comments |\n|---|---:|---:|---:|---:|\n")
	for _, r := range repos {
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %d |\n",
			cell(r.Repo), r.PRsOpened, r.PRsMerged, r.ReviewsGiven, r.CommentsGiven)
	}
	return b.String()
}

// Trends renders the weekly trajectory series and the movers list.
func Trends(team string, d metrics.TrendData, risers, fallers []metrics.Mover) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s — Trends (last %d weeks)\n\n", heading(team), len(d.Weeks))
	b.WriteString("| Week | Opened | Merged | Reviews | Comments | Cycle p50 | TTFR p50 |\n")
	b.WriteString("|---|---:|---:|---:|---:|---:|---:|\n")
	for i, wk := range d.Weeks {
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %d | %s | %s |\n",
			wk.Format("2006-01-02"), d.Team.Opened[i], d.Team.Merged[i],
			d.Team.Reviews[i], d.Team.Comments[i],
			metrics.FmtDur(d.Team.Cycle[i]), metrics.FmtDur(d.Team.TTFR[i]))
	}
	if len(risers)+len(fallers) > 0 {
		b.WriteString("\n### Movers\n\n")
		for _, m := range risers {
			b.WriteString("- " + mdMover(m) + "\n")
		}
		for _, m := range fallers {
			b.WriteString("- " + mdMover(m) + "\n")
		}
	}
	return b.String()
}

// mdMover is one movers bullet. Arrow, change, and streak wording come from
// Mover itself, so this line and the trends card cannot drift apart.
func mdMover(m metrics.Mover) string {
	s := fmt.Sprintf("%s %s — %s %d → %d (%s)", m.Arrow(), heading(m.Login), heading(m.Metric), m.Prior, m.Recent, m.ChangeLabel())
	if st := m.StreakLabel(); st != "" {
		s += ", " + st
	}
	return s
}

// Table renders any headers+rows as a titled GitHub-flavored Markdown table.
// Columns holding nothing but numbers are right-aligned so card exports read
// like the team stats table rather than drifting from it.
func Table(title string, headers []string, rows [][]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s\n\n", heading(title))

	b.WriteString("|")
	for _, h := range headers {
		b.WriteString(" " + cell(h) + " |")
	}
	b.WriteString("\n|")
	for i := range headers {
		if numericColumn(rows, i) {
			b.WriteString("---:|")
		} else {
			b.WriteString("---|")
		}
	}
	b.WriteString("\n")

	for _, r := range rows {
		b.WriteString("|")
		for _, v := range r {
			b.WriteString(" " + cell(v) + " |")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// numericColumn reports whether column i holds at least one number and
// nothing but numbers, ignoring blanks and the "no data" dash.
func numericColumn(rows [][]string, i int) bool {
	found := false
	for _, r := range rows {
		if i >= len(r) {
			continue
		}
		switch v := strings.TrimSpace(r[i]); v {
		case "", "–":
		default:
			if _, err := strconv.ParseFloat(v, 64); err != nil {
				return false
			}
			found = true
		}
	}
	return found
}

func mdSize(s int) string {
	if s < 0 {
		return "–"
	}
	return fmt.Sprintf("%d", s)
}
