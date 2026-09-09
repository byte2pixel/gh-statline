// Package doctor turns the cache's per-repo sync bookkeeping into a
// report about how much of what statline shows can still be trusted.
//
// sync_state records the truth — when a repo last completed a walk, how
// deep the walk reached, and why the last one failed — but a failing repo
// is invisible in every other view: its numbers simply stop moving, which
// looks exactly like a quiet week. This package is the one place that
// classifies and phrases that state, so the TUI page, the Markdown export
// and `gh statline doctor` can never disagree about whether a repo is
// healthy or how stale it is.
//
// Everything in a Report is render-safe: last_error is written by GitHub
// and repo names come from a config file that may have skipped validation,
// so both are sanitized on the way in rather than at each of the three
// call sites.
package doctor

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/byte2pixel/gh-statline/internal/config"
	"github.com/byte2pixel/gh-statline/internal/db"
	"github.com/byte2pixel/gh-statline/internal/metrics"
	"github.com/byte2pixel/gh-statline/internal/text"
)

// Status is a repo's sync health, ordered by how much attention it wants:
// the zero value is the healthy one, and rows sort on it descending.
type Status int

const (
	StatusOK Status = iota
	StatusNeverSynced
	StatusFailing
)

func (s Status) String() string {
	switch s {
	case StatusFailing:
		return "failing"
	case StatusNeverSynced:
		return "never synced"
	default:
		return "ok"
	}
}

// Row is one repo's health. The display strings are derived once against
// the report's clock so nothing downstream re-derives them and drifts. The
// timestamps behind them ride along for the JSON and CSV exports, which
// want the instant rather than "4m ago".
type Row struct {
	Repo       string // owner/name
	Status     Status
	LastSynced string // "4m ago" | "never"
	Covers     string // "2026-01-12" | "—"
	NewestPR   string // "1.1h ago" | "—"
	Error      string // sanitized last_error; empty when clean
	Hint       string // what to do about it; empty when there's nothing to say

	// The sync_state values behind LastSynced, Covers and NewestPR, in
	// unix seconds UTC. nil means the event never happened.
	LastSyncedAt     *int64
	BackfillUntil    *int64
	WatermarkUpdated *int64
}

// Report is the whole team's sync health.
type Report struct {
	Team   string
	NoSync bool // local-only team: nothing here was ever going to sync
	Rows   []Row
	// Covers is the date the cache is trustworthy back to for the whole
	// team (metrics.CoverageFloor), empty until every repo has completed a
	// walk. It is the same floor that gates the trends view, so a blank
	// Trends page is explained here.
	Covers      string
	Failing     int
	NeverSynced int
}

// Healthy reports whether every repo that has ever synced completed its
// last walk. It drives the TUI's warning badge and the exit status of
// `gh statline doctor`, so a repo that has never synced deliberately does
// not count against it: a fresh install has nothing wrong with it yet, and
// failing there would teach a cron job to ignore the command. That state
// is still reported, as NeverSynced and in Summary.
func (r Report) Healthy() bool { return r.Failing == 0 }

// notFoundMsg is what GitHub's GraphQL API says about a repo that no
// longer resolves — renamed, made private, or deleted, which it
// deliberately does not distinguish. Sync targets come from the config
// file, so this class of failure never self-heals: every future sync
// asks for the same dead name. Matching on GitHub's prose is fragile, but
// a miss costs the hint and leaves the error itself intact.
const notFoundMsg = "Could not resolve to a Repository"

// Build derives the report. floor/hasFloor come from
// metrics.CoverageFloor, which owns that definition; passing it in keeps
// the rule in one place instead of recomputing it from the same rows.
func Build(team config.Team, states []db.RepoSyncState, floor int64, hasFloor bool, now time.Time) Report {
	rep := Report{Team: text.Sanitize(team.Name), NoSync: team.NoSync}
	if hasFloor {
		rep.Covers = date(&floor)
	}
	for _, st := range states {
		row := Row{
			Repo:             text.Sanitize(st.String()),
			LastSynced:       age(st.LastSyncedAt, now),
			Covers:           date(st.BackfillUntil),
			NewestPR:         age(st.WatermarkUpdated, now),
			LastSyncedAt:     st.LastSyncedAt,
			BackfillUntil:    st.BackfillUntil,
			WatermarkUpdated: st.WatermarkUpdated,
		}
		// A never-synced repo reads "never" rather than "—": the column is
		// about an event that hasn't happened, not a value we don't have.
		if st.WatermarkUpdated == nil {
			row.NewestPR = "—"
		}
		switch {
		case st.LastError != nil && *st.LastError != "":
			row.Status = StatusFailing
			row.Error = text.Sanitize(*st.LastError)
			if strings.Contains(row.Error, notFoundMsg) {
				row.Hint = "renamed, private, or deleted — fix the repo entry in config.yml"
			}
			rep.Failing++
		case st.LastSyncedAt == nil:
			row.Status = StatusNeverSynced
			rep.NeverSynced++
		}
		rep.Rows = append(rep.Rows, row)
	}
	// Worst first. The store already ordered by name and the sort is
	// stable, so names stay alphabetical inside each group.
	sort.SliceStable(rep.Rows, func(i, j int) bool {
		return rep.Rows[i].Status > rep.Rows[j].Status
	})
	return rep
}

// Load reads the bookkeeping for a team and builds its report.
func Load(store *db.Store, team config.Team, teamID int64, now time.Time) (Report, error) {
	states, err := store.ListSyncStates(teamID)
	if err != nil {
		return Report{}, fmt.Errorf("reading sync state: %w", err)
	}
	// CoverageFloor reports ok=false for its own query errors too, which
	// costs the coverage line and nothing else.
	floor, hasFloor := metrics.CoverageFloor(store.DB, teamID)
	return Build(team, states, floor, hasFloor, now), nil
}

// Summary is the headline line above the table, shared by the page and the
// CLI so the two can't describe the same cache differently.
func (r Report) Summary() string {
	if len(r.Rows) == 0 {
		return "no repos configured for this team"
	}
	parts := []string{plural(len(r.Rows), "repo")}
	if r.Failing > 0 {
		parts = append(parts, fmt.Sprintf("%d failing", r.Failing))
	}
	if r.NeverSynced > 0 {
		parts = append(parts, fmt.Sprintf("%d never synced", r.NeverSynced))
	}
	if r.Failing == 0 && r.NeverSynced == 0 {
		parts = append(parts, "all synced cleanly")
	}
	if r.Covers != "" {
		parts = append(parts, "cache covers since "+r.Covers)
	} else {
		parts = append(parts, "coverage pending a full sync")
	}
	if r.NoSync {
		parts = append(parts, "local-only (no_sync)")
	}
	return strings.Join(parts, " · ")
}

// age renders how long ago ts was, in the same duration vocabulary as the
// rest of the app. Under a minute is "just now" because metrics.FmtDur
// renders a zero duration as the no-data dash, and a clock skewed into the
// future would otherwise count backwards.
func age(ts *int64, now time.Time) string {
	if ts == nil {
		return "never"
	}
	if d := now.Sub(time.Unix(*ts, 0)); d >= time.Minute {
		return metrics.FmtDur(d) + " ago"
	}
	return "just now"
}

// date renders a stored timestamp as its UTC calendar day, matching the
// invariant that every time in the cache is UTC epoch seconds.
func date(ts *int64) string {
	if ts == nil {
		return "—"
	}
	return time.Unix(*ts, 0).UTC().Format("2006-01-02")
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
