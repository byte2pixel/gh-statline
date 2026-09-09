// Package export renders Statline views as Markdown for standups, retros
// and 1:1 notes, CSV for spreadsheets, and JSON for scripts. All three
// render from the same Doc, built per view in views.go.
package export

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/byte2pixel/gh-statline/internal/doctor"
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

func mdCell(s string) string { return strings.TrimSpace(text.Sanitize(cellReplacer.Replace(s))) }

// heading keeps a title on one line. Pipes are harmless outside a table.
func heading(s string) string { return strings.TrimSpace(text.Sanitize(lineReplacer.Replace(s))) }

// Markdown renders a Doc: heading, lead paragraph, a table per sheet, then
// the notes. Machine-scoped sheets and columns are left out.
func Markdown(d Doc) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s\n\n", heading(d.Title))
	if d.Lead != "" {
		fmt.Fprintf(&b, "%s\n\n", heading(d.Lead))
	}
	for i, s := range d.sheets(false) {
		if i > 0 {
			b.WriteString("\n")
		}
		cols := s.columns(false)
		b.WriteString("|")
		for _, c := range cols {
			b.WriteString(" " + mdCell(c.Header) + " |")
		}
		b.WriteString("\n|")
		for _, c := range cols {
			if c.Numeric {
				b.WriteString("---:|")
			} else {
				b.WriteString("---|")
			}
		}
		b.WriteString("\n")
		for _, r := range s.Rows {
			b.WriteString("|")
			for _, v := range s.cells(r, false) {
				b.WriteString(" " + mdValue(v) + " |")
			}
			b.WriteString("\n")
		}
	}
	for _, n := range d.Notes {
		fmt.Fprintf(&b, "\n### %s\n\n", heading(n.Title))
		for _, it := range n.Items {
			b.WriteString("- " + it.Text + "\n")
			if it.Sub != "" {
				b.WriteString("  - " + it.Sub + "\n")
			}
		}
	}
	return b.String()
}

// mdValue is a cell for a reader: durations in the app's vocabulary, the
// no-data dash for an absent value.
func mdValue(c Cell) string {
	switch c.kind {
	case kindText:
		return mdCell(c.text)
	case kindDur:
		return metrics.FmtDur(c.dur)
	case kindNone:
		return "–"
	default:
		return c.plain()
	}
}

// TeamStats renders the stat lines as a GitHub-flavored Markdown table.
func TeamStats(team string, w metrics.Window, rows []metrics.Row) string {
	return Markdown(TeamDoc(team, w, rows))
}

// Person renders one member's per-repo breakdown.
func Person(login string, w metrics.Window, row metrics.Row, repos []metrics.RepoBreakdown) string {
	return Markdown(PersonDoc(login, w, row, repos))
}

// SyncStatus renders per-repo sync health: the table, then the full error
// for anything failing.
func SyncStatus(rep doctor.Report) string {
	return Markdown(SyncStatusDoc(rep))
}

// Trends renders the weekly series and the movers. Bullets here, matching
// the trends card; the machine formats get the same movers as rows.
func Trends(team string, d metrics.TrendData, risers, fallers []metrics.Mover) string {
	return Markdown(TrendsDoc(team, d, risers, fallers))
}

// mdMover is one movers bullet. Arrow, change, and streak wording come from
// Mover itself, so this line and the trends card cannot drift apart.
func mdMover(m metrics.Mover) string {
	s := fmt.Sprintf("%s %s — %s %d → %d (%s)", m.Arrow(), heading(m.Login), heading(m.Metric.String()), m.Prior, m.Recent, m.ChangeLabel())
	if st := m.StreakLabel(); st != "" {
		s += ", " + st
	}
	return s
}

// Table renders any headers+rows as a titled GitHub-flavored Markdown table.
// Columns holding nothing but numbers are right-aligned so card exports read
// like the team stats table rather than drifting from it.
//
// The one path with no typed columns: card exports format their own rows,
// so alignment is sniffed and a short row stays short.
func Table(title string, headers []string, rows [][]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s\n\n", heading(title))

	b.WriteString("|")
	for _, h := range headers {
		b.WriteString(" " + mdCell(h) + " |")
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
			b.WriteString(" " + mdCell(v) + " |")
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
