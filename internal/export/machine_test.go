package export

import (
	"encoding/csv"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/byte2pixel/gh-statline/internal/metrics"
)

func teamDoc(t *testing.T) Doc {
	t.Helper()
	d := TeamDoc("platform", metrics.Window{
		Start: time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC).Unix(),
		Label: "Last 30 days",
	}, goldenRows())
	d.Meta.GeneratedAt = time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	return d
}

// A median nobody has a sample for is absent, not zero. A dashboard that
// averaged the sentinel would be wrong and look right.
func TestJSONEmitsNullForTheNoDataSentinels(t *testing.T) {
	out, err := JSON(teamDoc(t))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Rows []map[string]any `json:"rows"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(doc.Rows) != 2 {
		t.Fatalf("rows = %d, want 2:\n%s", len(doc.Rows), out)
	}

	alice, bob := doc.Rows[0], doc.Rows[1]
	if got := alice["cycle_time_p50_seconds"]; got != float64(26*3600) {
		t.Errorf("alice cycle p50 = %v, want %d seconds", got, 26*3600)
	}
	if got := alice["ttfr_p50_seconds"]; got != float64(45*60) {
		t.Errorf("alice ttfr p50 = %v, want %d seconds", got, 45*60)
	}
	if got := alice["size_p50"]; got != float64(120) {
		t.Errorf("alice size p50 = %v, want 120", got)
	}
	for _, key := range []string{"cycle_time_p50_seconds", "ttfr_p50_seconds", "size_p50"} {
		v, ok := bob[key]
		if !ok {
			t.Errorf("bob is missing %s entirely", key)
		}
		if v != nil {
			t.Errorf("bob %s = %v, want null", key, v)
		}
	}
	// A count of zero is a measurement, not a sentinel.
	if got := bob["prs_opened"]; got != float64(0) {
		t.Errorf("bob prs_opened = %v, want 0", got)
	}
}

// Column order survives into the JSON; a Go map would have sorted the keys.
func TestJSONKeepsColumnOrderAndMeta(t *testing.T) {
	out, err := JSON(teamDoc(t))
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, want := range []string{
		`"view": "team"`,
		`"title": "platform — Last 30 days"`,
		`"team": "platform"`,
		`"label": "Last 30 days"`,
		`"start": "2026-05-16T00:00:00Z"`,
		`"end": "2026-06-15T00:00:00Z"`,
		`"generated_at": "2026-06-15T12:00:00Z"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in:\n%s", want, got)
		}
	}
	if i, j := strings.Index(got, `"login"`), strings.Index(got, `"prs_opened"`); i < 0 || j < i {
		t.Errorf("login should come before prs_opened, as it does in the table:\n%s", got)
	}
}

// The keys are the contract. A heading can be reworded; a key rename breaks
// a script, so it has to be deliberate.
func TestTeamColumnKeys(t *testing.T) {
	want := []string{
		"login", "prs_opened", "prs_merged", "reviews_given", "approved", "commented",
		"changes_requested", "dismissed", "comments_given", "comments_received",
		"cycle_time_p50_seconds", "ttfr_p50_seconds", "size_p50",
	}
	got := make([]string, len(teamColumns))
	for i, c := range teamColumns {
		got[i] = c.Key
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("team column keys =\n%v\nwant\n%v", got, want)
	}
}

func TestCSVHeadersAreKeysAndSentinelsAreEmpty(t *testing.T) {
	out, err := CSV(teamDoc(t))
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
	if recs[0][0] != "login" || recs[0][10] != "cycle_time_p50_seconds" {
		t.Errorf("header = %v, want the machine keys", recs[0])
	}
	if got := recs[1][10]; got != "93600" {
		t.Errorf("alice cycle p50 = %q, want 93600 seconds", got)
	}
	for _, i := range []int{10, 11, 12} {
		if got := recs[2][i]; got != "" {
			t.Errorf("bob column %s = %q, want an empty field", recs[0][i], got)
		}
	}
}

// A person and a trends export are two tables each. Writing only the first
// would drop the movers.
func TestCSVWritesEverySheet(t *testing.T) {
	d := PersonDoc("alice", metrics.Window{Label: "Last 30 days"}, goldenRows()[0],
		[]metrics.RepoBreakdown{{Repo: "acme/api", PRsOpened: 6, PRsMerged: 5}})
	out, err := CSV(d)
	if err != nil {
		t.Fatal(err)
	}
	blocks := strings.Split(strings.TrimSuffix(out, "\n"), "\n\n")
	if len(blocks) != 2 {
		t.Fatalf("blocks = %d, want the totals and the repo breakdown:\n%s", len(blocks), out)
	}
	if !strings.HasPrefix(blocks[0], "login,prs_opened") {
		t.Errorf("first block is not the totals:\n%s", blocks[0])
	}
	if !strings.HasPrefix(blocks[1], "repo,prs_opened") {
		t.Errorf("second block is not the repo breakdown:\n%s", blocks[1])
	}
}

// The totals are a sentence in Markdown and a row in the machine formats;
// the movers are bullets there and rows here. Neither may cross.
func TestSheetScopes(t *testing.T) {
	md := Markdown(PersonDoc("alice", metrics.Window{Label: "Last 30 days"}, goldenRows()[0], nil))
	if strings.Contains(md, "login") {
		t.Errorf("the machine-only totals sheet reached the Markdown:\n%s", md)
	}

	trends := TrendsDoc("acme", metrics.TrendData{}, []metrics.Mover{{
		Login: "alice", Metric: metrics.MetricReviews, Recent: 8, IsNew: true, Streak: 4,
	}}, nil)
	out, err := JSON(trends)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Movers []map[string]any `json:"movers"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Movers) != 1 {
		t.Fatalf("movers = %d, want 1:\n%s", len(doc.Movers), out)
	}
	m := doc.Movers[0]
	if m["is_new"] != true || m["prior"] != float64(0) || m["recent"] != float64(8) {
		t.Errorf("mover = %v, want a new riser from 0 to 8", m)
	}
	// A percentage from a zero base means nothing, as the "(new)" bullet
	// says.
	if m["pct_change"] != nil {
		t.Errorf("pct_change = %v, want null for a mover with no prior", m["pct_change"])
	}
}

// GitHub errors and hand-edited repo names reach the machine formats too. A
// control character there corrupts the CSV field or the terminal that cats
// it.
func TestMachineFormatsSanitizeUntrustedText(t *testing.T) {
	d := TeamDoc("evil \x1b]0;pwned\a team", metrics.Window{Label: "Last 7 days"},
		[]metrics.Row{{Login: "a\x1b[31mlice", SizeP50: -1}})

	js, err := JSON(d)
	if err != nil {
		t.Fatal(err)
	}
	out, err := CSV(d)
	if err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string]string{"json": string(js), "csv": out} {
		for _, bad := range []string{"\x1b", "\a", "pwned"} {
			if strings.Contains(got, bad) {
				t.Errorf("%s carries %q:\n%s", name, bad, got)
			}
		}
		if !strings.Contains(got, "alice") {
			t.Errorf("%s mangled the printable text:\n%s", name, got)
		}
	}
}
