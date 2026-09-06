package pages

// noDataHint is the line every page shows before any data lands. A
// local-only team has no sync to press s for. Its rows come from `seed`
// or from a hand-built cache, so the default hint would send its user at
// a key that answers "sync disabled for this team".
func noDataHint(noSync bool) string {
	if noSync {
		return "\n  No data yet — this team is local-only (no_sync); seed or import data."
	}
	return "\n  No data yet — press s to sync."
}

// weeklyHint is the trends variant. Weekly buckets need a coverage floor
// that only a completed sync (or `seed`) records, so a local-only team can
// hold plenty of PRs and still have no weeks to plot.
func weeklyHint(noSync bool) string {
	if noSync {
		return "\n  No weekly history — this team is local-only (no_sync); seed or import data."
	}
	return "\n  Trends unlock after the first full sync completes."
}
