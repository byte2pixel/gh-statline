package gh

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Response decoding was covered only indirectly, through the fake Doer in
// the syncer. These drive each query function against canned payloads, so a
// change to a node struct or a response shape fails in the package that
// owns it.

type doerCall struct {
	query string
	vars  map[string]interface{}
}

// recordingDoer answers with canned JSON and keeps every call, so a test can
// assert on the variables a query function sent as well as what it decoded.
type recordingDoer struct {
	reply func(vars map[string]interface{}) (string, error)
	// replyQuery is consulted first when set, for lookups that issue more
	// than one query document and must answer each differently.
	replyQuery func(query string, vars map[string]interface{}) (string, error)
	calls      []doerCall
}

func (d *recordingDoer) DoWithContext(_ context.Context, query string, vars map[string]interface{}, resp interface{}) error {
	d.calls = append(d.calls, doerCall{query: query, vars: vars})
	var payload string
	var err error
	if d.replyQuery != nil {
		payload, err = d.replyQuery(query, vars)
	} else {
		payload, err = d.reply(vars)
	}
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(payload), resp)
}

func canned(payload string) *recordingDoer {
	return &recordingDoer{reply: func(map[string]interface{}) (string, error) { return payload, nil }}
}

func fixed(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

func TestViewer(t *testing.T) {
	d := canned(`{"viewer": {"login": "alice", "organizations": {"nodes": [
		{"login": "acme"}, {"login": "globex"}]}}}`)

	login, orgs, err := Viewer(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	if login != "alice" {
		t.Errorf("login = %q, want alice", login)
	}
	if len(orgs) != 2 || orgs[0] != "acme" || orgs[1] != "globex" {
		t.Errorf("orgs = %q, want [acme globex]", orgs)
	}

	// An account in no organizations is a normal answer, not an error.
	_, orgs, err = Viewer(context.Background(),
		canned(`{"viewer": {"login": "solo", "organizations": {"nodes": []}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(orgs) != 0 {
		t.Errorf("orgs = %q, want none", orgs)
	}
}

func TestOrgTeams(t *testing.T) {
	d := canned(`{"organization": {"teams": {"totalCount": 2, "nodes": [
		{"slug": "platform-eng", "name": "Platform Engineering"},
		{"slug": "mobile", "name": "Mobile"}]}}}`)

	list, err := OrgTeams(context.Background(), d, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if list.Total != 2 || list.Missing() != 0 {
		t.Errorf("total = %d, missing = %d; want 2 and 0", list.Total, list.Missing())
	}
	teams := list.Teams
	want := []TeamInfo{
		{Slug: "platform-eng", Name: "Platform Engineering"},
		{Slug: "mobile", Name: "Mobile"},
	}
	if len(teams) != len(want) {
		t.Fatalf("got %d teams, want %d", len(teams), len(want))
	}
	for i := range teams {
		if teams[i] != want[i] {
			t.Errorf("team %d = %+v, want %+v", i, teams[i], want[i])
		}
	}
	if got := d.calls[0].vars["org"]; got != "acme" {
		t.Errorf("org variable = %v, want acme", got)
	}
}

// The wizard seeds a config from this, so the owner has to come from each
// repository rather than from the org being queried, and an archived repo
// must stay flagged rather than be silently dropped.
func TestTeamDetails(t *testing.T) {
	d := canned(`{"organization": {"team": {
		"members": {"totalCount": 2, "nodes": [{"login": "alice"}, {"login": "bob"}]},
		"repositories": {"totalCount": 2, "nodes": [
			{"name": "api", "isArchived": false, "owner": {"login": "acme"}},
			{"name": "legacy", "isArchived": true, "owner": {"login": "acme-labs"}}]}}}}`)

	imp, err := TeamDetails(context.Background(), d, "acme", "platform-eng")
	if err != nil {
		t.Fatal(err)
	}
	if imp.MembersTotal != 2 || imp.ReposTotal != 2 || imp.MissingMembers() != 0 || imp.MissingRepos() != 0 {
		t.Errorf("totals = (%d, %d), missing = (%d, %d); want (2, 2) and (0, 0)",
			imp.MembersTotal, imp.ReposTotal, imp.MissingMembers(), imp.MissingRepos())
	}
	members, repos := imp.Members, imp.Repos
	if len(members) != 2 || members[0] != "alice" || members[1] != "bob" {
		t.Errorf("members = %q, want [alice bob]", members)
	}
	want := []TeamRepo{
		{Owner: "acme", Name: "api"},
		{Owner: "acme-labs", Name: "legacy", Archived: true},
	}
	if len(repos) != len(want) {
		t.Fatalf("got %d repos, want %d", len(repos), len(want))
	}
	for i := range repos {
		if repos[i] != want[i] {
			t.Errorf("repo %d = %+v, want %+v", i, repos[i], want[i])
		}
	}
	for i, c := range d.calls {
		if c.vars["org"] != "acme" || c.vars["slug"] != "platform-eng" {
			t.Errorf("call %d sent org/slug = %v/%v, want acme/platform-eng", i, c.vars["org"], c.vars["slug"])
		}
	}
}

// The PR page is the widest decode in the package: nullable timestamps, a
// deleted author, a bot author, an unsubmitted review, and the nested
// pageInfo that tells the syncer a PR overflowed the first 50 reviews.
func TestFetchPRPageDecodesEveryField(t *testing.T) {
	d := canned(`{
	  "rateLimit": {"cost": 3, "remaining": 4997, "resetAt": "2026-06-01T13:00:00Z"},
	  "repository": {"pullRequests": {
	    "totalCount": 42,
	    "pageInfo": {"hasNextPage": true, "endCursor": "Y3Vyc29y"},
	    "nodes": [
	      {
	        "id": "PR_1", "number": 7, "title": "add a thing", "state": "MERGED", "isDraft": false,
	        "author": {"login": "alice", "__typename": "User"},
	        "createdAt": "2026-06-01T09:00:00Z", "updatedAt": "2026-06-01T12:00:00Z",
	        "mergedAt": "2026-06-01T11:00:00Z", "closedAt": "2026-06-01T11:00:00Z",
	        "additions": 120, "deletions": 30, "changedFiles": 4,
	        "reviews": {"totalCount": 51, "pageInfo": {"hasNextPage": true, "endCursor": "rc1"},
	          "nodes": [
	            {"id": "RV_1", "author": {"login": "dependabot", "__typename": "Bot"},
	             "state": "COMMENTED", "submittedAt": "2026-06-01T10:00:00Z",
	             "comments": {"totalCount": 2}},
	            {"id": "RV_2", "author": null, "state": "PENDING", "submittedAt": null,
	             "comments": {"totalCount": 0}}
	          ]},
	        "comments": {"totalCount": 1, "pageInfo": {"hasNextPage": false, "endCursor": ""},
	          "nodes": [{"id": "IC_1", "author": {"login": "bob", "__typename": "User"},
	                     "createdAt": "2026-06-01T10:30:00Z"}]}
	      },
	      {
	        "id": "PR_2", "number": 8, "title": "still open", "state": "OPEN", "isDraft": true,
	        "author": null,
	        "createdAt": "2026-06-01T08:00:00Z", "updatedAt": "2026-06-01T08:30:00Z",
	        "mergedAt": null, "closedAt": null,
	        "additions": 1, "deletions": 0, "changedFiles": 1,
	        "reviews": {"totalCount": 0, "pageInfo": {"hasNextPage": false, "endCursor": ""}, "nodes": []},
	        "comments": {"totalCount": 0, "pageInfo": {"hasNextPage": false, "endCursor": ""}, "nodes": []}
	      }
	    ]}}}`)

	page, err := FetchPRPage(context.Background(), d, "acme", "api", "", 50)
	if err != nil {
		t.Fatal(err)
	}

	if page.TotalCount != 42 || !page.HasNextPage || page.EndCursor != "Y3Vyc29y" {
		t.Errorf("page envelope = (%d, %v, %q), want (42, true, Y3Vyc29y)",
			page.TotalCount, page.HasNextPage, page.EndCursor)
	}
	if page.RateLimit.Cost != 3 || page.RateLimit.Remaining != 4997 ||
		!page.RateLimit.ResetAt.Equal(fixed(t, "2026-06-01T13:00:00Z")) {
		t.Errorf("rateLimit = %+v", page.RateLimit)
	}
	if len(page.Nodes) != 2 {
		t.Fatalf("got %d nodes, want 2", len(page.Nodes))
	}

	pr := page.Nodes[0]
	if pr.ID != "PR_1" || pr.Number != 7 || pr.Title != "add a thing" ||
		pr.State != "MERGED" || pr.IsDraft {
		t.Errorf("PR_1 identity = %+v", pr)
	}
	if pr.Additions != 120 || pr.Deletions != 30 || pr.ChangedFiles != 4 {
		t.Errorf("PR_1 diff stats = (%d, %d, %d), want (120, 30, 4)",
			pr.Additions, pr.Deletions, pr.ChangedFiles)
	}
	if pr.MergedAt == nil || !pr.MergedAt.Equal(fixed(t, "2026-06-01T11:00:00Z")) {
		t.Errorf("mergedAt = %v, want the merge time", pr.MergedAt)
	}
	if pr.Author.SafeLogin() != "alice" || pr.Author.IsBot() {
		t.Errorf("PR_1 author = %+v", pr.Author)
	}
	// A PR whose reviews overflowed the nested first:50 has to say so, or the
	// syncer never issues the follow-up query.
	if !pr.Reviews.PageInfo.HasNextPage || pr.Reviews.PageInfo.EndCursor != "rc1" ||
		pr.Reviews.TotalCount != 51 {
		t.Errorf("reviews pageInfo = %+v, totalCount %d", pr.Reviews.PageInfo, pr.Reviews.TotalCount)
	}
	if len(pr.Reviews.Nodes) != 2 {
		t.Fatalf("got %d reviews, want 2", len(pr.Reviews.Nodes))
	}
	if rv := pr.Reviews.Nodes[0]; !rv.Author.IsBot() || rv.Comments.TotalCount != 2 {
		t.Errorf("bot review decoded as %+v", rv)
	}
	// A PENDING review carries no submittedAt. The nil has to survive so
	// callers can skip it, rather than reading a zero time as 1970.
	if rv := pr.Reviews.Nodes[1]; rv.SubmittedAt != nil ||
		rv.Author.SafeLogin() != "ghost" || rv.Author.IsBot() {
		t.Errorf("pending review decoded as %+v (submittedAt %v)", rv, rv.SubmittedAt)
	}
	if len(pr.Comments.Nodes) != 1 || pr.Comments.Nodes[0].ID != "IC_1" ||
		!pr.Comments.Nodes[0].CreatedAt.Equal(fixed(t, "2026-06-01T10:30:00Z")) {
		t.Errorf("comments = %+v", pr.Comments.Nodes)
	}

	open := page.Nodes[1]
	if !open.IsDraft || open.MergedAt != nil || open.ClosedAt != nil {
		t.Errorf("open draft decoded as %+v", open)
	}
	if open.Author.SafeLogin() != "ghost" {
		t.Errorf("deleted author = %q, want ghost", open.Author.SafeLogin())
	}
}

// The cursor variable is omitted on the first page and sent on later ones.
// Passing an explicit null would be a different query to the API.
func TestFetchPRPageSendsCursorOnlyWhenSet(t *testing.T) {
	empty := `{"rateLimit": {}, "repository": {"pullRequests": {
		"totalCount": 0, "pageInfo": {"hasNextPage": false, "endCursor": ""}, "nodes": []}}}`

	d := canned(empty)
	if _, err := FetchPRPage(context.Background(), d, "acme", "api", "", 50); err != nil {
		t.Fatal(err)
	}
	vars := d.calls[0].vars
	if _, ok := vars["cursor"]; ok {
		t.Errorf("first page sent a cursor: %v", vars["cursor"])
	}
	if vars["owner"] != "acme" || vars["name"] != "api" || vars["pageSize"] != 50 {
		t.Errorf("vars = %v", vars)
	}

	d = canned(empty)
	if _, err := FetchPRPage(context.Background(), d, "acme", "api", "c1", 50); err != nil {
		t.Fatal(err)
	}
	if got := d.calls[0].vars["cursor"]; got != "c1" {
		t.Errorf("cursor = %v, want c1", got)
	}
}

// The probe is the cheap head-of-list snapshot a multi-page walk verifies
// itself against. A repo with no PRs is a legitimate answer, not an error.
func TestFetchPRProbe(t *testing.T) {
	d := canned(`{"rateLimit": {"cost": 1, "remaining": 4999, "resetAt": "2026-06-01T13:00:00Z"},
		"repository": {"pullRequests": {"totalCount": 9, "nodes": [
			{"id": "PR_top", "updatedAt": "2026-06-01T12:00:00Z"}]}}}`)

	probe, err := FetchPRProbe(context.Background(), d, "acme", "api")
	if err != nil {
		t.Fatal(err)
	}
	if probe.TotalCount != 9 || probe.FirstID != "PR_top" ||
		!probe.FirstUpdated.Equal(fixed(t, "2026-06-01T12:00:00Z")) {
		t.Errorf("probe = %+v", probe)
	}
	if probe.RateLimit.Remaining != 4999 {
		t.Errorf("rateLimit = %+v", probe.RateLimit)
	}

	empty, err := FetchPRProbe(context.Background(), canned(
		`{"rateLimit": {}, "repository": {"pullRequests": {"totalCount": 0, "nodes": []}}}`),
		"acme", "new")
	if err != nil {
		t.Fatal(err)
	}
	if empty.FirstID != "" || !empty.FirstUpdated.IsZero() || empty.TotalCount != 0 {
		t.Errorf("empty repo probe = %+v, want zero values", empty)
	}
}

// A failed query returns the error and nothing else. Half-decoded results
// would be indistinguishable from a genuinely empty org, team, or repo.
func TestQueryErrorsPropagate(t *testing.T) {
	boom := errors.New("403 Forbidden")
	failing := func() *recordingDoer {
		return &recordingDoer{reply: func(map[string]interface{}) (string, error) { return "", boom }}
	}

	if _, orgs, err := Viewer(context.Background(), failing()); !errors.Is(err, boom) || orgs != nil {
		t.Errorf("Viewer = (%v, %v), want the error and no orgs", orgs, err)
	}
	if list, err := OrgTeams(context.Background(), failing(), "acme"); !errors.Is(err, boom) || list != nil {
		t.Errorf("OrgTeams = (%v, %v), want the error and a nil list", list, err)
	}
	if imp, err := TeamDetails(context.Background(), failing(), "acme", "platform-eng"); !errors.Is(err, boom) || imp != nil {
		t.Errorf("TeamDetails = (%v, %v), want the error and a nil import", imp, err)
	}
	if page, err := FetchPRPage(context.Background(), failing(), "acme", "api", "", 50); !errors.Is(err, boom) || page != nil {
		t.Errorf("FetchPRPage = (%v, %v), want the error and a nil page", page, err)
	}
	if probe, err := FetchPRProbe(context.Background(), failing(), "acme", "api"); !errors.Is(err, boom) || probe != nil {
		t.Errorf("FetchPRProbe = (%v, %v), want the error and a nil probe", probe, err)
	}
}

// reviewPage builds one page of the PRReviews response.
func reviewPage(id, nextCursor string, remaining int) string {
	next := "false"
	if nextCursor != "" {
		next = "true"
	}
	return fmt.Sprintf(`{
		"rateLimit": {"cost": 1, "remaining": %d, "resetAt": "2026-06-01T13:00:00Z"},
		"node": {"reviews": {
			"pageInfo": {"hasNextPage": %s, "endCursor": %q},
			"nodes": [{"id": %q, "author": {"login": "bob", "__typename": "User"},
			           "state": "APPROVED", "submittedAt": "2026-06-01T10:00:00Z",
			           "comments": {"totalCount": 1}}]}}}`,
		remaining, next, nextCursor, id)
}

// The overflow fetchers loop until the API stops offering a next page,
// threading the cursor through and keeping the newest rate-limit reading —
// the one the caller uses to decide whether to pause.
func TestFetchAllReviewsPagesToTheEnd(t *testing.T) {
	d := &recordingDoer{reply: func(vars map[string]interface{}) (string, error) {
		if vars["cursor"] == nil {
			return reviewPage("RV_1", "p2", 100), nil
		}
		return reviewPage("RV_2", "", 98), nil
	}}

	reviews, rl, err := FetchAllReviews(context.Background(), d, "PR_1", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(reviews) != 2 || reviews[0].ID != "RV_1" || reviews[1].ID != "RV_2" {
		t.Fatalf("reviews = %+v, want RV_1 then RV_2", reviews)
	}
	if len(d.calls) != 2 {
		t.Fatalf("made %d calls, want 2", len(d.calls))
	}
	if got := d.calls[1].vars["cursor"]; got != "p2" {
		t.Errorf("second page cursor = %v, want p2", got)
	}
	for i, c := range d.calls {
		if c.vars["id"] != "PR_1" {
			t.Errorf("call %d sent id %v, want PR_1", i, c.vars["id"])
		}
	}
	if rl.Remaining != 98 {
		t.Errorf("rateLimit.Remaining = %d, want 98 from the last page", rl.Remaining)
	}
}

// Resuming from the walk leaves off at a cursor the main query handed back,
// so it must be sent on the very first request.
func TestFetchAllReviewsResumesFromACursor(t *testing.T) {
	d := &recordingDoer{reply: func(map[string]interface{}) (string, error) {
		return reviewPage("RV_9", "", 50), nil
	}}
	if _, _, err := FetchAllReviews(context.Background(), d, "PR_1", "rc1"); err != nil {
		t.Fatal(err)
	}
	if got := d.calls[0].vars["cursor"]; got != "rc1" {
		t.Errorf("first call cursor = %v, want rc1", got)
	}
}

// A failure partway through returns what was already collected alongside the
// error, so a caller that retries knows the walk was incomplete rather than
// empty.
func TestFetchAllReviewsReturnsPartialResultsOnError(t *testing.T) {
	boom := errors.New("connection reset")
	d := &recordingDoer{reply: func(vars map[string]interface{}) (string, error) {
		if vars["cursor"] == nil {
			return reviewPage("RV_1", "p2", 100), nil
		}
		return "", boom
	}}

	reviews, _, err := FetchAllReviews(context.Background(), d, "PR_1", "")
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the transport failure", err)
	}
	if len(reviews) != 1 || reviews[0].ID != "RV_1" {
		t.Errorf("reviews = %+v, want the page that did arrive", reviews)
	}
}

func TestFetchAllCommentsPagesToTheEnd(t *testing.T) {
	page := func(id, nextCursor string) string {
		next := "false"
		if nextCursor != "" {
			next = "true"
		}
		return fmt.Sprintf(`{
			"rateLimit": {"cost": 1, "remaining": 77, "resetAt": "2026-06-01T13:00:00Z"},
			"node": {"comments": {
				"pageInfo": {"hasNextPage": %s, "endCursor": %q},
				"nodes": [{"id": %q, "author": {"login": "carol", "__typename": "User"},
				           "createdAt": "2026-06-01T10:30:00Z"}]}}}`,
			next, nextCursor, id)
	}
	d := &recordingDoer{reply: func(vars map[string]interface{}) (string, error) {
		if vars["cursor"] == nil {
			return page("IC_1", "p2"), nil
		}
		return page("IC_2", ""), nil
	}}

	comments, rl, err := FetchAllComments(context.Background(), d, "PR_1", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 2 || comments[0].ID != "IC_1" || comments[1].ID != "IC_2" {
		t.Fatalf("comments = %+v, want IC_1 then IC_2", comments)
	}
	if !comments[0].CreatedAt.Equal(fixed(t, "2026-06-01T10:30:00Z")) {
		t.Errorf("createdAt = %v", comments[0].CreatedAt)
	}
	if got := d.calls[1].vars["cursor"]; got != "p2" {
		t.Errorf("second page cursor = %v, want p2", got)
	}
	if rl.Remaining != 77 {
		t.Errorf("rateLimit.Remaining = %d, want 77", rl.Remaining)
	}
}

// The wizard's three lookups used to ask for GitHub's first page of 100 and
// stop, so a large org's team picker and a big team's member and repo lists
// were silently cut. Each now walks to the end and carries the connection's
// totalCount, which is what lets the wizard say when a list was cut (#103).

func loginNodes(logins ...string) string {
	parts := make([]string, len(logins))
	for i, l := range logins {
		parts[i] = fmt.Sprintf(`{"login": %q}`, l)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func pageInfo(nextCursor string) string {
	return fmt.Sprintf(`{"hasNextPage": %v, "endCursor": %q}`, nextCursor != "", nextCursor)
}

func TestViewerPagesOrganizations(t *testing.T) {
	page := func(nextCursor string, logins ...string) string {
		return fmt.Sprintf(`{"viewer": {"login": "alice", "organizations": {"pageInfo": %s, "nodes": %s}}}`,
			pageInfo(nextCursor), loginNodes(logins...))
	}
	d := &recordingDoer{reply: func(vars map[string]interface{}) (string, error) {
		if vars["cursor"] == nil {
			return page("p2", "acme", "globex"), nil
		}
		return page("", "initech"), nil
	}}

	login, orgs, err := Viewer(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	if login != "alice" {
		t.Errorf("login = %q, want alice", login)
	}
	if got := strings.Join(orgs, " "); got != "acme globex initech" {
		t.Errorf("orgs = %q, want them in page order", got)
	}
	if len(d.calls) != 2 {
		t.Fatalf("made %d calls, want 2", len(d.calls))
	}
	// The cursor variable is omitted on the first page and sent on later
	// ones; an explicit null would be a different query to the API.
	if _, ok := d.calls[0].vars["cursor"]; ok {
		t.Errorf("first page sent a cursor: %v", d.calls[0].vars["cursor"])
	}
	if got := d.calls[1].vars["cursor"]; got != "p2" {
		t.Errorf("second page cursor = %v, want p2", got)
	}
}

func teamsPage(nextCursor string, total int, slugs ...string) string {
	nodes := make([]string, len(slugs))
	for i, s := range slugs {
		nodes[i] = fmt.Sprintf(`{"slug": %q, "name": %q}`, s, strings.ToUpper(s))
	}
	return fmt.Sprintf(`{"organization": {"teams": {"totalCount": %d, "pageInfo": %s, "nodes": [%s]}}}`,
		total, pageInfo(nextCursor), strings.Join(nodes, ", "))
}

func TestOrgTeamsPagesToTheEnd(t *testing.T) {
	d := &recordingDoer{reply: func(vars map[string]interface{}) (string, error) {
		switch vars["cursor"] {
		case nil:
			return teamsPage("p2", 5, "a", "b"), nil
		case "p2":
			return teamsPage("p3", 5, "c", "d"), nil
		case "p3":
			return teamsPage("", 5, "e"), nil
		}
		return "", fmt.Errorf("unexpected cursor %v", vars["cursor"])
	}}

	list, err := OrgTeams(context.Background(), d, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if list.Total != 5 || list.Missing() != 0 {
		t.Errorf("total = %d, missing = %d; want 5 and 0", list.Total, list.Missing())
	}
	slugs := make([]string, len(list.Teams))
	for i, tm := range list.Teams {
		slugs[i] = tm.Slug
	}
	if got := strings.Join(slugs, ""); got != "abcde" {
		t.Errorf("teams = %q, want abcde in page order", got)
	}
	if len(d.calls) != 3 {
		t.Fatalf("made %d calls, want 3", len(d.calls))
	}
	if _, ok := d.calls[0].vars["cursor"]; ok {
		t.Errorf("first page sent a cursor: %v", d.calls[0].vars["cursor"])
	}
	if d.calls[1].vars["cursor"] != "p2" || d.calls[2].vars["cursor"] != "p3" {
		t.Errorf("cursors sent = %v, %v; want p2 then p3", d.calls[1].vars["cursor"], d.calls[2].vars["cursor"])
	}
	for i, c := range d.calls {
		if c.vars["org"] != "acme" {
			t.Errorf("call %d sent org %v, want acme", i, c.vars["org"])
		}
	}
}

// The page ceiling is a loop guard against an API that never stops
// offering a next page. Hitting it is not an error: the wizard reports the
// shortfall from totalCount, whatever caused it.
func TestOrgTeamsStopsAtThePageCap(t *testing.T) {
	d := canned(teamsPage("again", 6000, "t"))

	list, err := OrgTeams(context.Background(), d, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.calls) != maxListPages {
		t.Errorf("made %d calls, want exactly maxListPages (%d)", len(d.calls), maxListPages)
	}
	if len(list.Teams) != maxListPages {
		t.Errorf("kept %d teams, want one per page", len(list.Teams))
	}
	if want := 6000 - maxListPages; list.Missing() != want {
		t.Errorf("Missing() = %d, want %d", list.Missing(), want)
	}
}

func membersPage(nextCursor string, total int, logins ...string) string {
	return fmt.Sprintf(`{"organization": {"team": {"members": {"totalCount": %d, "pageInfo": %s, "nodes": %s}}}}`,
		total, pageInfo(nextCursor), loginNodes(logins...))
}

func reposPage(nextCursor string, total int, names ...string) string {
	nodes := make([]string, len(names))
	for i, n := range names {
		nodes[i] = fmt.Sprintf(`{"name": %q, "isArchived": false, "owner": {"login": "acme"}}`, n)
	}
	return fmt.Sprintf(`{"organization": {"team": {"repositories": {"totalCount": %d, "pageInfo": %s, "nodes": [%s]}}}}`,
		total, pageInfo(nextCursor), strings.Join(nodes, ", "))
}

// Members and repositories are separate connections on the team node, each
// with its own cursor, so they are walked as two documents: one finishing
// early must not be re-fetched on every page of the other.
func TestTeamDetailsPagesMembersAndReposIndependently(t *testing.T) {
	d := &recordingDoer{replyQuery: func(query string, vars map[string]interface{}) (string, error) {
		switch {
		case strings.Contains(query, "members("):
			if vars["cursor"] == nil {
				return membersPage("m2", 3, "alice", "bob"), nil
			}
			return membersPage("", 3, "carol"), nil
		case strings.Contains(query, "repositories("):
			switch vars["cursor"] {
			case nil:
				return reposPage("r2", 4, "api", "web"), nil
			case "r2":
				return reposPage("r3", 4, "cli"), nil
			}
			return reposPage("", 4, "docs"), nil
		}
		return "", fmt.Errorf("unexpected query %q", query)
	}}

	imp, err := TeamDetails(context.Background(), d, "acme", "platform-eng")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(imp.Members, " "); got != "alice bob carol" || imp.MembersTotal != 3 {
		t.Errorf("members = %q (total %d), want alice bob carol (3)", got, imp.MembersTotal)
	}
	names := make([]string, len(imp.Repos))
	for i, r := range imp.Repos {
		names[i] = r.Owner + "/" + r.Name
	}
	if got := strings.Join(names, " "); got != "acme/api acme/web acme/cli acme/docs" || imp.ReposTotal != 4 {
		t.Errorf("repos = %q (total %d), want four in page order (4)", got, imp.ReposTotal)
	}
	if imp.MissingMembers() != 0 || imp.MissingRepos() != 0 {
		t.Errorf("missing = (%d, %d), want nothing missing", imp.MissingMembers(), imp.MissingRepos())
	}
	if len(d.calls) != 5 {
		t.Fatalf("made %d calls, want 2 for members + 3 for repos", len(d.calls))
	}
	for i, c := range d.calls {
		if strings.Contains(c.query, "members(") && strings.Contains(c.query, "repositories(") {
			t.Errorf("call %d asked for both connections in one document", i)
		}
		if c.vars["org"] != "acme" || c.vars["slug"] != "platform-eng" {
			t.Errorf("call %d sent org/slug = %v/%v", i, c.vars["org"], c.vars["slug"])
		}
	}
	// Members are walked first; each walk starts without a cursor.
	if got := d.calls[1].vars["cursor"]; got != "m2" {
		t.Errorf("second members page cursor = %v, want m2", got)
	}
	if _, ok := d.calls[2].vars["cursor"]; ok {
		t.Errorf("first repos page sent a cursor: %v", d.calls[2].vars["cursor"])
	}
	if got := d.calls[4].vars["cursor"]; got != "r3" {
		t.Errorf("third repos page cursor = %v, want r3", got)
	}
}

// The wizard has no resume path, so a failure on any page drops the whole
// import: a team with its members but none of its repos would look like a
// complete one.
func TestTeamDetailsDropsEverythingOnALateError(t *testing.T) {
	boom := errors.New("connection reset")
	d := &recordingDoer{replyQuery: func(query string, _ map[string]interface{}) (string, error) {
		if strings.Contains(query, "members(") {
			return membersPage("", 1, "alice"), nil
		}
		return "", boom
	}}

	imp, err := TeamDetails(context.Background(), d, "acme", "platform-eng")
	if !errors.Is(err, boom) || imp != nil {
		t.Errorf("TeamDetails = (%v, %v), want the error and a nil import", imp, err)
	}
}
