package export

import (
	"fmt"

	"github.com/byte2pixel/gh-statline/internal/doctor"
	"github.com/byte2pixel/gh-statline/internal/metrics"
)

// The four views, each built once as a Doc. A column is declared here and
// nowhere else, so every format shows the same numbers in the same order.
// Renaming a Header is a display change; renaming a Key breaks a script.
// Durations export as whole seconds and say so in the key.

func num(key, header string) Column { return Column{Key: key, Header: header, Numeric: true} }

var teamColumns = []Column{
	{Key: "login", Header: "Member"},
	num("prs_opened", "PRs"),
	num("prs_merged", "Merged"),
	num("reviews_given", "Reviews"),
	num("approved", "Approved"),
	num("commented", "Commented"),
	num("changes_requested", "Changes req."),
	num("dismissed", "Dismissed"),
	num("comments_given", "Comments given"),
	num("comments_received", "Comments recv."),
	num("cycle_time_p50_seconds", "Cycle p50"),
	num("ttfr_p50_seconds", "First review p50"),
	num("size_p50", "Size p50"),
}

func teamCells(r metrics.Row) []Cell {
	return []Cell{
		Text(r.Login), Num(r.PRsOpened), Num(r.PRsMerged), Num(r.ReviewsGiven),
		Num(r.Approved), Num(r.Commented), Num(r.ChangesReq), Num(r.Dismissed),
		Num(r.CommentsGiven), Num(r.CommentsRecv),
		Dur(r.CycleTimeP50), Dur(r.TTFRP50), Size(r.SizeP50),
	}
}

// TeamDoc is the stat lines: one row per visible member.
func TeamDoc(team string, w metrics.Window, rows []metrics.Row) Doc {
	sheet := Sheet{Key: "rows", Columns: teamColumns}
	for _, r := range rows {
		sheet.Rows = append(sheet.Rows, teamCells(r))
	}
	return Doc{
		View:   "team",
		Title:  team + " — " + w.Label,
		Meta:   Meta{Team: team, Window: windowMeta(w)},
		Sheets: []Sheet{sheet},
	}
}

var personRepoColumns = []Column{
	{Key: "repo", Header: "Repo"},
	num("prs_opened", "PRs"),
	num("prs_merged", "Merged"),
	num("reviews_given", "Reviews"),
	num("comments_given", "Comments"),
}

// PersonDoc is one member's totals and per-repo breakdown. Markdown states
// the totals as a sentence, so that sheet is machine-only.
func PersonDoc(login string, w metrics.Window, row metrics.Row, repos []metrics.RepoBreakdown) Doc {
	row.Login = login
	totals := Sheet{Key: "totals", Columns: teamColumns, Rows: [][]Cell{teamCells(row)}, Scope: ScopeMachine}
	byRepo := Sheet{Key: "repos", Columns: personRepoColumns}
	for _, r := range repos {
		byRepo.Rows = append(byRepo.Rows, []Cell{
			Text(r.Repo), Num(r.PRsOpened), Num(r.PRsMerged), Num(r.ReviewsGiven), Num(r.CommentsGiven),
		})
	}
	return Doc{
		View:  "person",
		Title: login + " — " + w.Label,
		Lead: fmt.Sprintf("PRs opened %d · merged %d · reviews given %d (%d approved, %d commented, %d changes requested) · comments given %d / received %d",
			row.PRsOpened, row.PRsMerged, row.ReviewsGiven, row.Approved, row.Commented,
			row.ChangesReq, row.CommentsGiven, row.CommentsRecv),
		Meta:   Meta{Window: windowMeta(w)},
		Sheets: []Sheet{totals, byRepo},
	}
}

var trendWeekColumns = []Column{
	{Key: "week", Header: "Week"},
	num("prs_opened", "Opened"),
	num("prs_merged", "Merged"),
	num("reviews_given", "Reviews"),
	num("comments_given", "Comments"),
	num("cycle_time_p50_seconds", "Cycle p50"),
	num("ttfr_p50_seconds", "TTFR p50"),
}

// pct_change is null exactly when is_new is true: a percentage from a zero
// base means nothing. Mover.ChangeLabel words the same rule.
var moverColumns = []Column{
	{Key: "login", Header: "Member"},
	{Key: "metric", Header: "Metric"},
	num("prior", "Prior"),
	num("recent", "Recent"),
	num("pct_change", "Change %"),
	{Key: "is_new", Header: "New"},
	num("streak", "Streak"),
}

// TrendsDoc is the weekly trajectory plus the movers. No window: the series
// is always the trailing weeks, as on the trends page.
func TrendsDoc(team string, d metrics.TrendData, risers, fallers []metrics.Mover) Doc {
	weeks := Sheet{Key: "weeks", Columns: trendWeekColumns}
	for i, wk := range d.Weeks {
		weeks.Rows = append(weeks.Rows, []Cell{
			Text(wk.Format("2006-01-02")),
			Num(d.Team.Opened[i]), Num(d.Team.Merged[i]),
			Num(d.Team.Reviews[i]), Num(d.Team.Comments[i]),
			Dur(d.Team.Cycle[i]), Dur(d.Team.TTFR[i]),
		})
	}
	movers := Sheet{Key: "movers", Columns: moverColumns, Scope: ScopeMachine}
	var bullets []NoteItem
	for _, m := range append(append([]metrics.Mover{}, risers...), fallers...) {
		pct := Num(m.Pct)
		if m.IsNew {
			pct = None()
		}
		movers.Rows = append(movers.Rows, []Cell{
			Text(m.Login), Text(m.Metric.String()), Num(m.Prior), Num(m.Recent),
			pct, Bool(m.IsNew), Num(m.Streak),
		})
		bullets = append(bullets, NoteItem{Text: mdMover(m)})
	}
	doc := Doc{
		View:   "trends",
		Title:  team + fmt.Sprintf(" — Trends (last %d weeks)", len(d.Weeks)),
		Meta:   Meta{Team: team},
		Sheets: []Sheet{weeks, movers},
	}
	// A quiet week has no movers, and the heading over nothing is an empty
	// section.
	if len(bullets) > 0 {
		doc.Notes = []Note{{Title: "Movers", Items: bullets}}
	}
	return doc
}

// Display columns and the timestamps behind them sit side by side: a reader
// wants "4m ago", a script wants the instant. The failure text is
// machine-only; Markdown prints it in full below the table.
var syncColumns = []Column{
	{Key: "repo", Header: "Repo"},
	{Key: "last_synced", Header: "Last sync", Scope: ScopeMarkdown},
	{Key: "last_synced_at", Scope: ScopeMachine},
	{Key: "covers", Header: "Covers since", Scope: ScopeMarkdown},
	{Key: "backfill_until", Scope: ScopeMachine},
	{Key: "newest_pr", Header: "Newest PR", Scope: ScopeMarkdown},
	{Key: "watermark_updated", Scope: ScopeMachine},
	{Key: "status", Header: "Status"},
	{Key: "last_error", Scope: ScopeMachine},
	{Key: "hint", Scope: ScopeMachine},
}

// SyncStatusDoc is per-repo sync health, worst first.
func SyncStatusDoc(rep doctor.Report) Doc {
	sheet := Sheet{Key: "repos", Columns: syncColumns}
	for _, r := range rep.Rows {
		sheet.Rows = append(sheet.Rows, []Cell{
			Text(r.Repo),
			Text(r.LastSynced), Timestamp(r.LastSyncedAt),
			Text(r.Covers), Timestamp(r.BackfillUntil),
			Text(r.NewestPR), Timestamp(r.WatermarkUpdated),
			Text(r.Status.String()),
			textOrNone(r.Error), textOrNone(r.Hint),
		})
	}
	doc := Doc{
		View:   "sync",
		Title:  "Sync status — " + rep.Team,
		Lead:   rep.Summary(),
		Meta:   Meta{Team: rep.Team},
		Sheets: []Sheet{sheet},
	}
	// The failures go below the table: a cell would truncate the one thing
	// the view exists to show.
	if rep.Failing == 0 {
		return doc
	}
	note := Note{Title: "Failures"}
	for _, r := range rep.Rows {
		if r.Status != doctor.StatusFailing {
			continue
		}
		note.Items = append(note.Items, NoteItem{
			Text: "**" + mdCell(r.Repo) + "** — " + mdCell(r.Error),
			Sub:  mdCell(r.Hint),
		})
	}
	doc.Notes = append(doc.Notes, note)
	return doc
}

// textOrNone renders an absent string as null, not an empty string.
func textOrNone(s string) Cell {
	if s == "" {
		return None()
	}
	return Text(s)
}

func windowMeta(w metrics.Window) *WindowMeta {
	return &WindowMeta{Label: w.Label, Start: w.Start, End: w.End}
}
