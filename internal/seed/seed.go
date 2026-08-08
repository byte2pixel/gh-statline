// Package seed generates deterministic demo data — a fake team with months
// of PR/review/comment history — so the TUI can be inspected at realistic
// scale without touching GitHub. The demo team is marked no_sync so the app
// never tries to fetch its repos.
package seed

import (
	"fmt"
	"math"
	"math/rand"
	"time"

	"github.com/byte2pixel/gh-statline/internal/config"
	"github.com/byte2pixel/gh-statline/internal/db"
)

// Options control the generator. The zero value gets sensible defaults.
type Options struct {
	Members int       // fake teammates (default 38, max 45)
	Days    int       // history depth in days (default 120)
	Seed    int64     // RNG seed (default 1)
	Now     time.Time // "current" instant; injected so tests are reproducible
}

func (o Options) withDefaults() Options {
	if o.Members <= 0 {
		o.Members = 38
	}
	if o.Members > len(namePool) {
		o.Members = len(namePool)
	}
	if o.Days <= 0 {
		o.Days = 120
	}
	if o.Seed == 0 {
		o.Seed = 1
	}
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	return o
}

const (
	Org        = "demo-org"
	botLogin   = "ci-robot[bot]"
	otherLogin = "sam-contractor" // non-member human: fills the matrix "(others)" column
)

var repoNames = []string{"api", "web", "infra"}

var namePool = []string{
	"aiko", "brennan", "carmen", "dmitri", "elena", "farid", "gwen", "hiro", "imani", "jonas",
	"kavya", "liam", "mireille", "nadia", "oscar", "priya", "quentin", "rosa", "sanjay", "tamsin",
	"umar", "valentina", "wes", "xiomara", "yusuf", "zara", "alberto", "bianca", "cedric", "delphine",
	"emeka", "freya", "gustav", "hannah", "ilya", "jade", "kofi", "lucia", "matteo", "nora",
	"otis", "paloma", "ravi", "sofia", "theo",
}

// Team builds the local-only demo team profile for the config file.
func Team(name string, o Options) config.Team {
	o = o.withDefaults()
	t := config.Team{Name: name, Org: Org, NoSync: true}
	for i := 0; i < o.Members; i++ {
		t.Members = append(t.Members, config.Member{Login: namePool[i]})
	}
	for _, r := range repoNames {
		t.Repos = append(t.Repos, config.Repo{Owner: Org, Name: r})
	}
	return t
}

// persona shapes one member's habits so the charts show texture instead of
// uniform noise.
type persona struct {
	login     string
	pod       int // review clique; most reviews stay in-pod
	workStart int // local hour the day usually begins
	speed     int // TTFR bucket bias 0 (fast) … 4 (slow)
	weekend   bool
	prCount   int
}

// Generate produces the full PR history for the demo team. Identical
// (team, Options) inputs — including Now — yield identical output, and the
// deterministic IDs make re-seeding an idempotent upsert.
func Generate(t config.Team, repoIDs map[string]int64, o Options) []db.PullRequest {
	o = o.withDefaults()
	rng := rand.New(rand.NewSource(o.Seed))
	g := &gen{rng: rng, now: o.Now, days: o.Days, repoIDs: repoIDs, seq: map[string]int{}}

	for i, m := range t.Members {
		g.personas = append(g.personas, persona{
			login:     m.Login,
			pod:       i % 4,
			workStart: 8 + rng.Intn(4),
			speed:     rng.Intn(5),
			weekend:   i == 5 || i == 19,
			prCount:   4 + rng.Intn(9),
		})
	}

	// Forced open PRs pin every aging bucket, including genuinely stale ones
	// for the "stalest" list. Everything else is organic.
	staleAges := []float64{95, 70, 50, 35, 22, 15, 8, 3}
	recentAges := []float64{0.3, 0.6, 1.5, 2.2}
	for i, age := range staleAges {
		g.memberPR(i%len(g.personas), age, forceOpen)
	}
	for i, age := range recentAges {
		g.memberPR((len(staleAges)+i)%len(g.personas), age, forceOpen)
	}

	for i, p := range g.personas {
		for n := 0; n < p.prCount; n++ {
			g.memberPR(i, rng.Float64()*float64(o.Days), organic)
		}
	}

	// Bot PRs and reviews verify the exclusion paths visibly: the bot must
	// appear in no member-keyed chart.
	for n := 0; n < 12; n++ {
		g.outsiderPR(botLogin, true, rng.Float64()*float64(o.Days))
	}
	// A non-member human's PRs reviewed by members feed the matrix "(others)"
	// column; created on Saturdays to guarantee weekend punch-card cells.
	// Evenly spaced so several always land inside the 90d window.
	for n := 0; n < 6; n++ {
		g.outsiderPR(otherLogin, false, (float64(n)+0.5)*float64(o.Days)/6)
	}

	return g.prs
}

type prKind int

const (
	organic prKind = iota
	forceOpen
)

type gen struct {
	rng      *rand.Rand
	now      time.Time
	days     int
	repoIDs  map[string]int64
	seq      map[string]int
	personas []persona
	prs      []db.PullRequest
	reviewed int // global count, drives the TTFR round-robin
}

func (g *gen) pickRepo() string {
	r := g.rng.Float64()
	switch {
	case r < 0.5:
		return repoNames[0]
	case r < 0.8:
		return repoNames[1]
	default:
		return repoNames[2]
	}
}

// pickReviewer prefers the author's pod so the matrix shows clusters.
func (g *gen) pickReviewer(author int) int {
	for tries := 0; tries < 24; tries++ {
		j := g.rng.Intn(len(g.personas))
		if j == author {
			continue
		}
		if g.rng.Float64() < 0.75 && g.personas[j].pod != g.personas[author].pod {
			continue
		}
		return j
	}
	return (author + 1) % len(g.personas)
}

// stamp picks an instant daysAgo back, aligned to the persona's work rhythm
// (weekday-biased, business hours) so the punch card shows structure.
func (g *gen) stamp(p persona, daysAgo float64) time.Time {
	day := g.now.Add(-time.Duration(daysAgo * 24 * float64(time.Hour)))
	if wd := day.Weekday(); wd == time.Saturday || wd == time.Sunday {
		keep := 0.12
		if p.weekend {
			keep = 0.55
		}
		if g.rng.Float64() >= keep {
			day = day.AddDate(0, 0, -2) // slide onto Thu/Fri
		}
	}
	hour := p.workStart + int(math.Abs(g.rng.NormFloat64())*3)
	if hour > 19 {
		hour = 19 - g.rng.Intn(3)
	}
	return time.Date(day.Year(), day.Month(), day.Day(), hour, g.rng.Intn(60), g.rng.Intn(60), 0, day.Location())
}

// ttfr samples a first-review delay inside the given distribution bucket.
func (g *gen) ttfr(bucket int) time.Duration {
	switch bucket {
	case 0:
		return time.Duration(5+g.rng.Intn(50)) * time.Minute
	case 1:
		return time.Duration(65+g.rng.Intn(170)) * time.Minute
	case 2:
		return time.Duration(5+g.rng.Intn(18)) * time.Hour
	case 3:
		return time.Duration(25+g.rng.Intn(46)) * time.Hour
	default:
		return time.Duration(73+g.rng.Intn(200)) * time.Hour
	}
}

// size samples lines-changed across every distribution bucket.
func (g *gen) size() int {
	r := g.rng.Float64()
	switch {
	case r < 0.18:
		return 1 + g.rng.Intn(9)
	case r < 0.50:
		return 10 + g.rng.Intn(40)
	case r < 0.80:
		return 50 + g.rng.Intn(200)
	case r < 0.95:
		return 250 + g.rng.Intn(750)
	default:
		return 1000 + g.rng.Intn(4000)
	}
}

func (g *gen) reviewState() (string, int) {
	r := g.rng.Float64()
	switch {
	case r < 0.60:
		return "APPROVED", g.rng.Intn(4)
	case r < 0.85:
		return "COMMENTED", 1 + g.rng.Intn(5)
	default:
		return "CHANGES_REQUESTED", 1 + g.rng.Intn(6)
	}
}

func (g *gen) newPR(repo, author string, isBot bool, created time.Time) *db.PullRequest {
	g.seq[repo]++
	n := g.seq[repo]
	size := g.size()
	adds := size * (55 + g.rng.Intn(35)) / 100
	return &db.PullRequest{
		ID:           fmt.Sprintf("SEEDPR_%s_%05d", repo, n),
		RepoID:       g.repoIDs[Org+"/"+repo],
		Number:       n,
		Author:       author,
		AuthorIsBot:  isBot,
		Title:        titles[(n+len(author))%len(titles)] + fmt.Sprintf(" (%s#%d)", repo, n),
		State:        "OPEN",
		CreatedAt:    created.Unix(),
		UpdatedAt:    created.Unix(),
		Additions:    adds,
		Deletions:    size - adds,
		ChangedFiles: 1 + size/80,
	}
}

// addReviews attaches 1–3 reviews starting at created+TTFR; the first 15
// reviewed PRs round-robin the TTFR buckets so every bucket is populated.
func (g *gen) addReviews(pr *db.PullRequest, author int, includeBot bool) {
	bucket := g.personas[author].speed
	if jitter := g.rng.Float64(); jitter < 0.3 {
		bucket = g.rng.Intn(5)
	}
	if g.reviewed < 15 {
		bucket = g.reviewed % 5
	}
	g.reviewed++

	created := time.Unix(pr.CreatedAt, 0)
	// An occasional bot review lands before any human one; TTFR must skip it.
	if includeBot && g.rng.Float64() < 0.10 {
		at := created.Add(2 * time.Minute)
		pr.Reviews = append(pr.Reviews, db.Review{
			ID:          fmt.Sprintf("SEEDRV_%s_bot", pr.ID),
			Author:      botLogin,
			AuthorIsBot: true,
			State:       "COMMENTED",
			SubmittedAt: at.Unix(),
		})
	}
	at := created.Add(g.ttfr(bucket))
	n := 1 + g.rng.Intn(3)
	for i := 0; i < n; i++ {
		state, comments := g.reviewState()
		reviewer := g.pickReviewer(author)
		pr.Reviews = append(pr.Reviews, db.Review{
			ID:           fmt.Sprintf("SEEDRV_%s_%d", pr.ID, i),
			Author:       g.personas[reviewer].login,
			State:        state,
			SubmittedAt:  at.Unix(),
			CommentCount: comments,
		})
		at = at.Add(time.Duration(2+g.rng.Intn(20)) * time.Hour)
	}
}

func (g *gen) addComments(pr *db.PullRequest, author int, until time.Time) {
	created := time.Unix(pr.CreatedAt, 0)
	span := until.Sub(created)
	if span <= 0 {
		return
	}
	n := g.rng.Intn(4)
	for i := 0; i < n; i++ {
		who := g.personas[g.pickReviewer(author)].login
		isBot := false
		if g.rng.Float64() < 0.08 {
			who, isBot = botLogin, true // must be excluded from comments-received
		}
		pr.Comments = append(pr.Comments, db.IssueComment{
			ID:          fmt.Sprintf("SEEDIC_%s_%d", pr.ID, i),
			Author:      who,
			AuthorIsBot: isBot,
			CreatedAt:   created.Add(time.Duration(g.rng.Float64() * float64(span))).Unix(),
		})
	}
}

// finalize settles state (merge/close/open), drafts, and updated_at.
func (g *gen) finalize(pr *db.PullRequest, kind prKind, created time.Time) {
	if kind == forceOpen {
		pr.State = "OPEN"
	} else {
		// Lognormal cycle time, median ≈ 26h, clamped to [1h, 14d].
		cycle := time.Duration(math.Exp(g.rng.NormFloat64()*1.1+math.Log(26)) * float64(time.Hour))
		cycle = min(max(cycle, time.Hour), 14*24*time.Hour)
		done := created.Add(cycle)
		switch {
		case done.After(g.now):
			pr.State = "OPEN"
			pr.IsDraft = g.rng.Float64() < 0.15
		case g.rng.Float64() < 0.08:
			pr.State = "CLOSED"
			at := done.Unix()
			pr.ClosedAt = &at
		default:
			pr.State = "MERGED"
			at := done.Unix()
			pr.MergedAt = &at
			pr.ClosedAt = &at
		}
	}
	last := pr.CreatedAt
	for _, r := range pr.Reviews {
		last = max(last, r.SubmittedAt)
	}
	for _, c := range pr.Comments {
		last = max(last, c.CreatedAt)
	}
	if pr.MergedAt != nil {
		last = max(last, *pr.MergedAt)
	}
	if pr.ClosedAt != nil {
		last = max(last, *pr.ClosedAt)
	}
	pr.UpdatedAt = min(last, g.now.Unix())
}

func (g *gen) memberPR(author int, daysAgo float64, kind prKind) {
	p := g.personas[author]
	var created time.Time
	if kind == forceOpen {
		// Exact age — the aging buckets and stalest list depend on it, so
		// skip the work-rhythm stamp's weekday adjustment.
		created = g.now.Add(-time.Duration(daysAgo * 24 * float64(time.Hour)))
	} else {
		created = g.stamp(p, daysAgo)
	}
	pr := g.newPR(g.pickRepo(), p.login, false, created)
	if kind == forceOpen || g.rng.Float64() > 0.12 { // a few PRs go unreviewed
		g.addReviews(pr, author, true)
	}
	end := g.now
	g.addComments(pr, author, end)
	g.finalize(pr, kind, created)
	g.prs = append(g.prs, *pr)
}

// outsiderPR is authored by a non-member (bot or contractor) and reviewed by
// members, exercising bot exclusion and the matrix "(others)" column.
func (g *gen) outsiderPR(login string, isBot bool, daysAgo float64) {
	created := g.now.Add(-time.Duration(daysAgo * 24 * float64(time.Hour)))
	if !isBot {
		// Contractor works Saturdays: guarantees weekend punch-card cells
		// via member reviews submitted the same day.
		shift := (int(created.Weekday()) + 1) % 7
		created = created.AddDate(0, 0, -shift)
		created = time.Date(created.Year(), created.Month(), created.Day(), 14, g.rng.Intn(60), 0, 0, created.Location())
	}
	pr := g.newPR(g.pickRepo(), login, isBot, created)
	reviewer := g.rng.Intn(len(g.personas))
	pr.Reviews = append(pr.Reviews, db.Review{
		ID:           fmt.Sprintf("SEEDRV_%s_0", pr.ID),
		Author:       g.personas[reviewer].login,
		State:        "APPROVED",
		SubmittedAt:  created.Add(time.Duration(1+g.rng.Intn(6)) * time.Hour).Unix(),
		CommentCount: g.rng.Intn(3),
	})
	g.finalize(pr, organic, created)
	g.prs = append(g.prs, *pr)
}

var titles = []string{
	"Fix flaky retry logic in worker pool",
	"Add pagination to the audit log endpoint",
	"Refactor session cache invalidation",
	"Bump linter and fix new warnings",
	"Handle nil deref in webhook parser",
	"Migrate settings page to new form kit",
	"Add rate-limit headers to public API",
	"Speed up cold-start by lazy-loading locales",
	"Fix off-by-one in usage report ranges",
	"Instrument queue depth metrics",
	"Support wildcard routes in the proxy",
	"Clean up feature flags from Q1 launch",
	"Harden CSV import against bad encodings",
	"Add dark-mode tokens to design system",
	"Retry failed uploads with backoff",
	"Extract shared validation middleware",
	"Fix timezone drift in scheduled digests",
	"Cache org membership lookups",
	"Add e2e coverage for checkout flow",
	"Reduce bundle size via route splitting",
}
