package cmd

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/byte2pixel/gh-statline/internal/config"
	"github.com/byte2pixel/gh-statline/internal/db"
	"github.com/byte2pixel/gh-statline/internal/gh"
)

const day = int64(86400)

// exportConfig is testConfig with a second member, so the person view has
// someone to drill into who is not the first row.
func exportConfig() config.Config {
	cfg := testConfig()
	cfg.Teams[0].Members = []config.Member{{Login: "alice"}, {Login: "bob"}}
	return cfg
}

// seedCache writes a scenario into the cache the command will open:
//
//	alice opens one PR and merges it a day later (cycle 1d, size 120),
//	     bob approves it 12h in (TTFR 12h)
//	bob opens one PR and leaves it open (size 10, no cycle, no first
//	     review: the "no data" sentinels)
//
// Dated a few days back so any window from 7d up covers it.
func seedCache(t *testing.T) {
	t.Helper()
	env, err := bootstrap("")
	if err != nil {
		t.Fatal(err)
	}
	defer env.Close()

	now := time.Now().Unix()
	at := func(d int64) int64 { return now - d }
	i64 := func(v int64) *int64 { return &v }
	repoID := env.Targets[0].RepoID

	prs := []db.PullRequest{{
		ID: "PR1", RepoID: repoID, Number: 1, Author: "alice", Title: "one",
		State: "MERGED", CreatedAt: at(5 * day), MergedAt: i64(at(4 * day)),
		UpdatedAt: at(4 * day), Additions: 100, Deletions: 20, ChangedFiles: 3,
		Reviews: []db.Review{{
			ID: "R1", Author: "bob", State: "APPROVED",
			SubmittedAt: at(5*day) + day/2, CommentCount: 2,
		}},
	}, {
		ID: "PR2", RepoID: repoID, Number: 2, Author: "bob", Title: "two",
		State: "OPEN", CreatedAt: at(3 * day), UpdatedAt: at(3 * day),
		Additions: 5, Deletions: 5, ChangedFiles: 1,
	}}
	if err := env.Store.SavePullRequests(prs); err != nil {
		t.Fatal(err)
	}
	// A completed walk, so the coverage-gated views have a floor.
	if err := env.Store.SetSyncState(db.SyncState{
		RepoID: repoID, WatermarkUpdated: i64(now), BackfillUntil: i64(at(90 * day)),
		LastSyncedAt: i64(now),
	}); err != nil {
		t.Fatal(err)
	}
}

func setupExport(t *testing.T) {
	t.Helper()
	isolate(t)
	writeConfig(t, exportConfig())
	seedCache(t)
}

// The same numbers three ways. bob has no merged PR and no review on his
// own, so his medians are absent rather than zero.
func TestExportTeamFormats(t *testing.T) {
	t.Run("markdown is what the y key copies", func(t *testing.T) {
		setupExport(t)
		out, err := runCmd(t, "export", "--view", "team", "--window", "30d")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "## testers — Last 30 days") {
			t.Errorf("missing the heading:\n%s", out)
		}
		if !strings.Contains(out, "| alice | 1 | 1 | 0 | 0 | 0 | 0 | 0 | 0 | 2 | 24.0h | 12.0h | 120 |") {
			t.Errorf("alice's line is not what the table shows:\n%s", out)
		}
		if !strings.Contains(out, "| bob | 1 | 0 | 1 | 1 | 0 | 0 | 0 | 2 | 0 | – | – | 10 |") {
			t.Errorf("bob's line is not what the table shows:\n%s", out)
		}
	})

	t.Run("json nulls the sentinels", func(t *testing.T) {
		setupExport(t)
		out, err := runCmd(t, "export", "--view", "team", "--format", "json", "--window", "30d")
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			View string `json:"view"`
			Meta struct {
				Team   string `json:"team"`
				Window struct {
					Label string `json:"label"`
				} `json:"window"`
				GeneratedAt string `json:"generated_at"`
			} `json:"meta"`
			Rows []map[string]any `json:"rows"`
		}
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if doc.View != "team" || doc.Meta.Team != "testers" || doc.Meta.Window.Label != "Last 30 days" {
			t.Errorf("meta = %+v, want the team and window it was run for", doc.Meta)
		}
		if doc.Meta.GeneratedAt == "" {
			t.Errorf("no generated_at, so nothing dates the numbers:\n%s", out)
		}
		if len(doc.Rows) != 2 {
			t.Fatalf("rows = %d, want alice and bob:\n%s", len(doc.Rows), out)
		}
		alice, bob := doc.Rows[0], doc.Rows[1]
		if alice["login"] != "alice" || alice["cycle_time_p50_seconds"] != float64(day) {
			t.Errorf("alice = %v, want a 1d cycle in seconds", alice)
		}
		if alice["ttfr_p50_seconds"] != float64(day/2) {
			t.Errorf("alice ttfr = %v, want 12h in seconds", alice["ttfr_p50_seconds"])
		}
		if bob["cycle_time_p50_seconds"] != nil || bob["ttfr_p50_seconds"] != nil {
			t.Errorf("bob = %v, want null medians rather than zero", bob)
		}
		if bob["size_p50"] != float64(10) || bob["reviews_given"] != float64(1) {
			t.Errorf("bob = %v, want his open PR's size and the review he gave", bob)
		}
	})

	t.Run("csv headers are the machine keys", func(t *testing.T) {
		setupExport(t)
		out, err := runCmd(t, "export", "--view", "team", "--format", "csv", "--window", "30d")
		if err != nil {
			t.Fatal(err)
		}
		recs, err := csv.NewReader(strings.NewReader(out)).ReadAll()
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if len(recs) != 3 {
			t.Fatalf("records = %d, want a header and two members:\n%s", len(recs), out)
		}
		if got := strings.Join(recs[0][:3], ","); got != "login,prs_opened,prs_merged" {
			t.Errorf("header starts %q, want the machine keys", got)
		}
		if got := recs[1][10]; got != "86400" {
			t.Errorf("alice cycle p50 = %q, want seconds", got)
		}
		// An empty field, not a 0 and not the display dash.
		if got := recs[2][10]; got != "" {
			t.Errorf("bob cycle p50 = %q, want an empty field", got)
		}
	})
}

func TestExportPersonView(t *testing.T) {
	t.Run("the totals and the repo breakdown", func(t *testing.T) {
		setupExport(t)
		out, err := runCmd(t, "export", "--view", "person", "--member", "alice", "--format", "json")
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Totals []map[string]any `json:"totals"`
			Repos  []map[string]any `json:"repos"`
		}
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if len(doc.Totals) != 1 || doc.Totals[0]["login"] != "alice" {
			t.Fatalf("totals = %v, want one row for alice:\n%s", doc.Totals, out)
		}
		if len(doc.Repos) != 1 || doc.Repos[0]["repo"] != "acme/api" {
			t.Errorf("repos = %v, want the one repo she worked in", doc.Repos)
		}
	})

	t.Run("a member the view cannot show is named", func(t *testing.T) {
		setupExport(t)
		_, err := runCmd(t, "export", "--view", "person", "--member", "nobody")
		if err == nil || !strings.Contains(err.Error(), `"nobody" is not a visible member`) {
			t.Fatalf("err = %v, want the unknown member named", err)
		}
	})

	t.Run("the member flag is required", func(t *testing.T) {
		setupExport(t)
		_, err := runCmd(t, "export", "--view", "person")
		if err == nil || !strings.Contains(err.Error(), "--member") {
			t.Fatalf("err = %v, want the missing flag named", err)
		}
	})
}

// The sync view is the doctor report, so either command answers the same
// question the same way.
func TestExportSyncViewMatchesDoctorJSON(t *testing.T) {
	setupExport(t)
	viaExport, err := runCmd(t, "export", "--view", "sync", "--format", "json")
	if err != nil {
		t.Fatal(err)
	}
	viaDoctor, err := runCmd(t, "doctor", "--json")
	if err != nil {
		t.Fatal(err)
	}

	var a, b struct {
		Repos []map[string]any `json:"repos"`
	}
	if err := json.Unmarshal([]byte(viaExport), &a); err != nil {
		t.Fatalf("%v\n%s", err, viaExport)
	}
	if err := json.Unmarshal([]byte(viaDoctor), &b); err != nil {
		t.Fatalf("%v\n%s", err, viaDoctor)
	}
	if len(a.Repos) != 1 || len(b.Repos) != 1 {
		t.Fatalf("repos = %d and %d, want one each:\n%s\n%s", len(a.Repos), len(b.Repos), viaExport, viaDoctor)
	}
	r := a.Repos[0]
	if r["repo"] != "acme/api" || r["status"] != "ok" || r["last_error"] != nil {
		t.Errorf("repo = %v, want a healthy acme/api", r)
	}
	// The raw timestamp, not the "4m ago" the table shows.
	ts, ok := r["last_synced_at"].(string)
	if !ok {
		t.Fatalf("last_synced_at = %v, want an RFC 3339 string", r["last_synced_at"])
	}
	if _, err := time.Parse(time.RFC3339, ts); err != nil {
		t.Errorf("last_synced_at = %q: %v", ts, err)
	}
	for _, k := range []string{"repo", "status", "last_error", "backfill_until", "watermark_updated"} {
		if a.Repos[0][k] != b.Repos[0][k] {
			t.Errorf("%s: export says %v, doctor says %v", k, a.Repos[0][k], b.Repos[0][k])
		}
	}
}

func TestExportWindowFlags(t *testing.T) {
	t.Run("a custom range", func(t *testing.T) {
		setupExport(t)
		from := time.Now().AddDate(0, 0, -10).UTC().Format("2006-01-02")
		to := time.Now().UTC().Format("2006-01-02")
		out, err := runCmd(t, "export", "--view", "team", "--from", from, "--to", to)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, from+" → "+to) {
			t.Errorf("heading does not name the range:\n%s", out)
		}
	})

	t.Run("half a range is an error", func(t *testing.T) {
		setupExport(t)
		if _, err := runCmd(t, "export", "--from", "2026-01-01"); err == nil ||
			!strings.Contains(err.Error(), "--from and --to go together") {
			t.Fatalf("err = %v, want the pairing explained", err)
		}
	})

	t.Run("a backwards range is an error", func(t *testing.T) {
		setupExport(t)
		_, err := runCmd(t, "export", "--from", "2026-02-01", "--to", "2026-01-01")
		if err == nil || !strings.Contains(err.Error(), "before") {
			t.Fatalf("err = %v, want the order explained", err)
		}
	})

	t.Run("a window that is not days is an error", func(t *testing.T) {
		setupExport(t)
		_, err := runCmd(t, "export", "--window", "last month")
		if err == nil || !strings.Contains(err.Error(), "number of days") {
			t.Fatalf("err = %v, want the format explained", err)
		}
	})
}

func TestExportRejectsUnknownViewAndFormat(t *testing.T) {
	setupExport(t)
	_, err := runCmd(t, "export", "--format", "xml")
	if err == nil || !strings.Contains(err.Error(), "md, csv or json") {
		t.Fatalf("err = %v, want the formats listed", err)
	}
	// A fresh format too: the first error would otherwise fire again.
	_, err = runCmd(t, "export", "--view", "charts", "--format", "md")
	if err == nil || !strings.Contains(err.Error(), "team, person, trends or sync") {
		t.Fatalf("err = %v, want the views listed", err)
	}
}

func TestExportWritesAFile(t *testing.T) {
	setupExport(t)
	path := filepath.Join(t.TempDir(), "team.csv")
	out, err := runCmd(t, "export", "--view", "team", "--format", "csv", "--output", path)
	if err != nil {
		t.Fatal(err)
	}
	if out != "" {
		t.Errorf("stdout should be empty when writing a file:\n%s", out)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), "login,prs_opened") {
		t.Errorf("file does not hold the csv:\n%s", b)
	}
}

// A local-only team has no GitHub behind it, and export never asks for one.
func TestExportReadsALocalOnlyTeamWithoutAClient(t *testing.T) {
	isolate(t)
	cfg := exportConfig()
	cfg.Teams[0].NoSync = true
	writeConfig(t, cfg)
	seedCache(t)
	newClient = func() (gh.Doer, error) {
		t.Fatal("export built a GitHub client")
		return nil, nil
	}
	out, err := runCmd(t, "export", "--view", "team")
	if err != nil {
		t.Fatalf("err = %v:\n%s", err, out)
	}
	if !strings.Contains(out, "| alice |") {
		t.Errorf("no numbers for a local-only team:\n%s", out)
	}
}
