package metrics

import (
	"database/sql"
	"time"
)

// This file is the one-shot loaders behind the TUI's data messages: each
// gathers everything one view needs in a single call, so the composition
// (and the coverage-honesty gating) is testable against an in-memory DB
// without driving the Bubble Tea program.

// TileTrend compares the current window's headline stats to the previous
// equal-length window for the stat tiles. HasPrev is false when the cache
// doesn't reach back far enough for an honest comparison.
type TileTrend struct {
	HasPrev                   bool
	PrevOpened, PrevMerged    int
	PrevReviews, PrevComments int
	Cycle, PrevCycle          time.Duration // team p50 open→merge
	TTFR, PrevTTFR            time.Duration // team p50 time to first review
}

// Dashboard is every dataset behind the team and charts views — the stat
// lines plus all chart series — loaded in one shot.
type Dashboard struct {
	Rows      []Row
	Buckets   []Bucket
	BucketDur time.Duration // width of one throughput bucket
	Tiles     TileTrend
	Trend     []TrendPoint
	TTFR      Dist
	Sizes     Dist
	Matrix    Matrix
	Punch     Punch
	Aging     Aging
}

// LoadDashboard runs every query behind the dashboard for one window; now
// dates the open-PR aging, which has no window of its own.
func LoadDashboard(dbh *sql.DB, f Filter, w Window, now time.Time) (Dashboard, error) {
	d := Dashboard{BucketDur: BucketSize(w)}
	var err error
	if d.Rows, err = TeamStats(dbh, f, w); err != nil {
		return Dashboard{}, err
	}
	if d.Buckets, err = Throughput(dbh, f, w); err != nil {
		return Dashboard{}, err
	}
	if d.Trend, err = CycleTrend(dbh, f, w); err != nil {
		return Dashboard{}, err
	}
	if d.TTFR, err = TTFRDistribution(dbh, f, w); err != nil {
		return Dashboard{}, err
	}
	if d.Sizes, err = SizeDistribution(dbh, f, w); err != nil {
		return Dashboard{}, err
	}
	if d.Matrix, err = ReviewMatrix(dbh, f, w); err != nil {
		return Dashboard{}, err
	}
	if d.Punch, err = PunchCard(dbh, f, w); err != nil {
		return Dashboard{}, err
	}
	if d.Aging, err = OpenAging(dbh, f, now); err != nil {
		return Dashboard{}, err
	}
	if d.Tiles.Cycle, d.Tiles.TTFR, err = TeamMedians(dbh, f, w); err != nil {
		return Dashboard{}, err
	}
	// Compare against the previous window only when the cache provably
	// reaches back that far; the coverage deepens the longer statline
	// is used, so this flips on by itself.
	prevW := PrevWindow(w)
	if floor, ok := CoverageFloor(dbh, f.TeamID); ok && prevW.Start >= floor {
		d.Tiles.HasPrev = true
		prevRows, err := TeamStats(dbh, f, prevW)
		if err != nil {
			return Dashboard{}, err
		}
		for _, r := range prevRows {
			d.Tiles.PrevOpened += r.PRsOpened
			d.Tiles.PrevMerged += r.PRsMerged
			d.Tiles.PrevReviews += r.ReviewsGiven
			d.Tiles.PrevComments += r.CommentsGiven
		}
		if d.Tiles.PrevCycle, d.Tiles.PrevTTFR, err = TeamMedians(dbh, f, prevW); err != nil {
			return Dashboard{}, err
		}
	}
	return d, nil
}

// PersonData is one member's drill-down dataset: the per-repo breakdown
// plus the daily activity series.
type PersonData struct {
	Repos    []RepoBreakdown
	Activity []float64
}

// LoadPerson runs the queries behind the person drill-down in one shot.
func LoadPerson(dbh *sql.DB, f Filter, w Window, login string) (PersonData, error) {
	var d PersonData
	var err error
	if d.Repos, err = PersonRepos(dbh, f, w, login); err != nil {
		return PersonData{}, err
	}
	if d.Activity, err = PersonActivity(dbh, f, w, login); err != nil {
		return PersonData{}, err
	}
	return d, nil
}
