package wizard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/byte2pixel/gh-statline/internal/config"
	"github.com/byte2pixel/gh-statline/internal/gh"
	"github.com/byte2pixel/gh-statline/internal/tui/theme"
)

// scriptedDoer answers the wizard's three queries with canned payloads.
type scriptedDoer struct{}

func (scriptedDoer) DoWithContext(_ context.Context, query string, _ map[string]interface{}, resp interface{}) error {
	var payload string
	switch {
	case strings.Contains(query, "viewer"):
		payload = `{"viewer": {"login": "mel", "organizations": {"nodes": [{"login": "acme"}, {"login": "other-org"}]}}}`
	case strings.Contains(query, "teams(first"):
		payload = `{"organization": {"teams": {"nodes": [
			{"slug": "platform", "name": "Platform Eng"},
			{"slug": "design", "name": "Design"}
		]}}}`
	case strings.Contains(query, "team(slug"):
		payload = `{"organization": {"team": {
			"members": {"nodes": [{"login": "alice"}, {"login": "bob"}]},
			"repositories": {"nodes": [
				{"name": "api", "isArchived": false, "owner": {"login": "acme"}},
				{"name": "legacy", "isArchived": true, "owner": {"login": "acme"}}
			]}}}}`
	default:
		payload = `{}`
	}
	return json.Unmarshal([]byte(payload), resp)
}

// orglessDoer simulates a personal account with no org memberships.
type orglessDoer struct{}

func (orglessDoer) DoWithContext(_ context.Context, query string, _ map[string]interface{}, resp interface{}) error {
	if strings.Contains(query, "viewer") {
		return json.Unmarshal([]byte(`{"viewer": {"login": "solo", "organizations": {"nodes": []}}}`), resp)
	}
	return json.Unmarshal([]byte(`{}`), resp)
}

func TestWizardManualFlow(t *testing.T) {
	tm := teatest.NewTestModel(t, New(orglessDoer{}, nil), teatest.WithInitialTermSize(100, 30))

	enter := tea.KeyPressMsg{Code: tea.KeyEnter}
	wait := func(s string) {
		t.Helper()
		teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
			return bytes.Contains(b, []byte(s))
		}, teatest.WithDuration(5*time.Second))
	}

	wait("Describe the team") // no orgs → straight to the manual form
	tm.Type("myspace")
	tm.Send(enter) // → members field
	tm.Type("alice, bob")
	tm.Send(enter) // → repos field
	tm.Type("acme/api charm/tea")
	tm.Send(enter) // submit → review
	wait("alice")
	tm.Send(enter) // accept all → name step
	wait("profile")
	tm.Send(enter) // default name = org ("myspace")

	result, err := Outcome(tm.FinalModel(t, teatest.WithFinalTimeout(5*time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("wizard returned no team")
	}
	team := *result
	if team.Name != "myspace" || team.Org != "myspace" || team.GHTeamSlug != "" {
		t.Errorf("name/org/slug = %q/%q/%q", team.Name, team.Org, team.GHTeamSlug)
	}
	if len(team.Members) != 2 || team.Members[0].Login != "alice" || team.Members[1].Login != "bob" {
		t.Errorf("members = %+v", team.Members)
	}
	if len(team.Repos) != 2 || team.Repos[0].String() != "acme/api" || team.Repos[1].String() != "charm/tea" {
		t.Errorf("repos = %+v", team.Repos)
	}
}

func TestWizardFullFlow(t *testing.T) {
	tm := teatest.NewTestModel(t, New(scriptedDoer{}, []string{"platform"}),
		teatest.WithInitialTermSize(100, 30))

	enter := tea.KeyPressMsg{Code: tea.KeyEnter}
	wait := func(s string) {
		t.Helper()
		teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
			return bytes.Contains(b, []byte(s))
		}, teatest.WithDuration(5*time.Second))
	}

	// Wait on strings unique to each step: the diff renderer may split
	// static prefixes across frames, so shared words never re-appear whole.
	wait("acme")
	tm.Send(enter) // acme
	wait("Platform Eng")
	tm.Send(enter) // platform
	wait("alice")
	// Exclude bob: move down once, toggle.
	tm.Send(tea.KeyPressMsg{Code: 'j', Text: "j"})
	tm.Send(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	tm.Send(enter)
	wait("profile")
	tm.Send(enter) // accept suggested unique name

	result, err := Outcome(tm.FinalModel(t, teatest.WithFinalTimeout(5*time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("wizard returned no team")
	}
	team := *result
	if team.Name != "platform-2" { // "platform" already existed
		t.Errorf("name = %q, want platform-2", team.Name)
	}
	if team.Org != "acme" || team.GHTeamSlug != "platform" {
		t.Errorf("org/slug = %q/%q", team.Org, team.GHTeamSlug)
	}
	if len(team.Members) != 1 || team.Members[0].Login != "alice" {
		t.Errorf("members = %+v, want just alice (bob toggled off)", team.Members)
	}
	if len(team.Repos) != 1 || team.Repos[0].Name != "api" {
		t.Errorf("repos = %+v, want just acme/api (legacy is archived)", team.Repos)
	}
}

// runFilterCmds executes cmd, feeding any list.FilterMatchesMsg back into
// the model the way the runtime would. Every other message (blinks, ticks)
// is dropped to keep the test finite and deterministic.
func runFilterCmds(m tea.Model, cmd tea.Cmd) tea.Model {
	if cmd == nil {
		return m
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			m = runFilterCmds(m, c)
		}
	case list.FilterMatchesMsg:
		var next tea.Cmd
		m, next = m.Update(msg)
		return runFilterCmds(m, next)
	}
	return m
}

// TestWizardTeamFilter reproduces issue #15: typing into the `/` filter
// must narrow the team list, which only happens if the wizard routes the
// list's async FilterMatchesMsg back into it.
func TestWizardTeamFilter(t *testing.T) {
	var m tea.Model = New(scriptedDoer{}, nil)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m, _ = m.Update(teamsMsg{teams: []gh.TeamInfo{
		{Slug: "platform", Name: "Platform Eng"},
		{Slug: "design", Name: "Design"},
	}, total: 2})

	press := func(code rune, text string) {
		var cmd tea.Cmd
		m, cmd = m.Update(tea.KeyPressMsg{Code: code, Text: text})
		m = runFilterCmds(m, cmd)
	}
	press('/', "/")
	press('d', "d")
	press('e', "e")
	press('s', "s")

	if got := m.(Model).teams.VisibleItems(); len(got) != 1 {
		t.Fatalf("visible teams after filtering on \"des\" = %d, want 1", len(got))
	}

	enter := tea.KeyPressMsg{Code: tea.KeyEnter}
	m, _ = m.Update(enter) // apply the filter
	m, _ = m.Update(enter) // select the surviving team
	if got := m.(Model).slug; got != "design" {
		t.Fatalf("selected slug = %q, want %q", got, "design")
	}
}

// Embedded in the app, the wizard reports its ending as a message instead
// of quitting the program: the team on success, nothing when the user
// backs out, the error when GitHub refused it.
func TestEmbeddedWizardReportsInsteadOfQuitting(t *testing.T) {
	th := theme.New(true)
	wiz := New(orglessDoer{}, nil).Embedded(th)
	done := func(cmd tea.Cmd) DoneMsg {
		t.Helper()
		if cmd == nil {
			t.Fatal("the ending produced no command")
		}
		msg, ok := cmd().(DoneMsg)
		if !ok {
			t.Fatalf("emitted %#v, want DoneMsg", cmd())
		}
		return msg
	}

	// Backing out of the manual form, the first step for an org-less account.
	m, _ := wiz.Update(viewerMsg{login: "solo"})
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if msg := done(cmd); msg.Team != nil || msg.Err != nil {
		t.Errorf("esc emitted %+v, want an empty DoneMsg", msg)
	}

	// Naming the profile.
	w := m.(Model)
	w.review = newReviewList(&w.theme, []string{"alice"}, nil)
	w.step = stepName
	w.nameIn.SetValue("fresh")
	_, cmd = w.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if msg := done(cmd); msg.Team == nil || msg.Team.Name != "fresh" || msg.Err != nil {
		t.Errorf("enter on the name emitted %+v, want the fresh team", msg)
	}

	// A failed query.
	_, cmd = wiz.Update(failMsg{errors.New("boom")})
	if msg := done(cmd); msg.Err == nil || msg.Team != nil {
		t.Errorf("a failure emitted %+v, want the error", msg)
	}

	// esc at the org list, the first step for everyone else. The list must
	// not be filtering, or esc belongs to it.
	m2, _ := New(scriptedDoer{}, nil).Embedded(th).Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m2, _ = m2.Update(viewerMsg{login: "mel", orgs: []string{"acme"}})
	_, cmd = m2.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if msg := done(cmd); msg.Team != nil || msg.Err != nil {
		t.Errorf("esc at the org list emitted %+v, want an empty DoneMsg", msg)
	}
}

// Standalone, the same endings still quit the program, so init and the
// first run read them through Outcome as before.
func TestStandaloneWizardStillQuits(t *testing.T) {
	m, _ := New(orglessDoer{}, nil).Update(viewerMsg{login: "solo"})
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd == nil {
		t.Fatal("esc on the first step produced no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("standalone esc emitted %#v, want tea.QuitMsg", cmd())
	}
}

// Outcome is the only reader of the wizard's private error, so its three
// endings are pinned here: aborted, failed, and configured.
func TestOutcome(t *testing.T) {
	if _, err := Outcome(nil); err == nil {
		t.Error("a foreign final model was accepted")
	}
	if team, err := Outcome(Model{}); err != nil || team != nil {
		t.Errorf("aborted wizard = %v, %v; want nil, nil", team, err)
	}
	boom := errors.New("auth failed")
	if _, err := Outcome(Model{err: boom}); !errors.Is(err, boom) {
		t.Errorf("failed wizard err = %v, want %v", err, boom)
	}
	want := &config.Team{Name: "t"}
	if got, err := Outcome(Model{Result: want}); err != nil || got != want {
		t.Errorf("configured wizard = %v, %v; want the result", got, err)
	}
}

// pagedTeamDoer is a large org as the API answers it: one viewer org, 150
// teams over two pages, and a team whose 250 members span three pages and
// whose 150 repos span two. Every page carries totalCount and pageInfo;
// the cursor is the next page number. It answers any team slug with the
// same team.
type pagedTeamDoer struct{}

func (pagedTeamDoer) DoWithContext(_ context.Context, query string, vars map[string]interface{}, resp interface{}) error {
	cursor, _ := vars["cursor"].(string)
	var payload string
	switch {
	case strings.Contains(query, "viewer"):
		payload = `{"viewer": {"login": "mel", "organizations": {"nodes": [{"login": "acme"}]}}}`
	case strings.Contains(query, "teams("):
		payload = pagedConnection([]string{"organization", "teams"}, cursor, 150, func(i int) string {
			if i == 1 {
				return `{"slug": "platform", "name": "Platform Eng"}`
			}
			return fmt.Sprintf(`{"slug": "t%03d", "name": "Team %03d"}`, i, i)
		})
	case strings.Contains(query, "members("):
		payload = pagedConnection([]string{"organization", "team", "members"}, cursor, 250, func(i int) string {
			return fmt.Sprintf(`{"login": "m%03d"}`, i)
		})
	case strings.Contains(query, "repositories("):
		payload = pagedConnection([]string{"organization", "team", "repositories"}, cursor, 150, func(i int) string {
			return fmt.Sprintf(`{"name": "r%03d", "isArchived": false, "owner": {"login": "acme"}}`, i)
		})
	default:
		payload = `{}`
	}
	return json.Unmarshal([]byte(payload), resp)
}

// pagedConnection renders one page of 100 out of total nodes, numbered
// from 1, nested under path.
func pagedConnection(path []string, cursor string, total int, node func(i int) string) string {
	page, _ := strconv.Atoi(cursor)
	start, end := page*100, min(page*100+100, total)
	nodes := make([]string, 0, end-start)
	for i := start + 1; i <= end; i++ {
		nodes = append(nodes, node(i))
	}
	next := ""
	if end < total {
		next = strconv.Itoa(page + 1)
	}
	conn := fmt.Sprintf(`{"totalCount": %d, "pageInfo": {"hasNextPage": %v, "endCursor": %q}, "nodes": [%s]}`,
		total, next != "", next, strings.Join(nodes, ", "))
	for i := len(path) - 1; i >= 0; i-- {
		conn = fmt.Sprintf(`{%q: %s}`, path[i], conn)
	}
	return conn
}

// runCmd executes cmd, flattening batches, and returns the first message
// of type T it produced. Other messages (spinner ticks) are dropped.
func runCmd[T tea.Msg](t *testing.T, cmd tea.Cmd) T {
	t.Helper()
	var found *T
	var walk func(tea.Cmd)
	walk = func(cmd tea.Cmd) {
		if cmd == nil || found != nil {
			return
		}
		switch msg := cmd().(type) {
		case tea.BatchMsg:
			for _, c := range msg {
				walk(c)
			}
		case T:
			found = &msg
		}
	}
	walk(cmd)
	if found == nil {
		var zero T
		t.Fatalf("the command produced no %T", zero)
	}
	return *found
}

func plainView(m tea.Model) string { return ansi.Strip(m.View().Content) }

// TestWizardImportsWholeTeam is the PRD success metric "whole-team import":
// a team bigger than one API page arrives complete, in API order, and the
// review step says how many of each it holds (#103).
func TestWizardImportsWholeTeam(t *testing.T) {
	var m tea.Model = New(pagedTeamDoer{}, nil)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 500}) // tall enough to show both review headings
	m, _ = m.Update(viewerMsg{login: "mel", orgs: []string{"acme"}})

	enter := tea.KeyPressMsg{Code: tea.KeyEnter}
	var cmd tea.Cmd
	m, cmd = m.Update(enter) // acme
	teams := runCmd[teamsMsg](t, cmd)
	if len(teams.teams) != 150 || teams.total != 150 {
		t.Fatalf("teams = %d of %d, want 150 of 150", len(teams.teams), teams.total)
	}
	m, _ = m.Update(teams)
	if v := plainView(m); !strings.Contains(v, "Pick a team in acme (150)") {
		t.Errorf("team list title lacks the count:\n%s", v)
	}

	m, cmd = m.Update(enter) // platform, the first team
	details := runCmd[detailsMsg](t, cmd)
	m, _ = m.Update(details)

	w := m.(Model)
	if w.step != stepReview || len(w.review.items) != 400 {
		t.Fatalf("step = %v with %d review items, want the review of 400", w.step, len(w.review.items))
	}
	v := plainView(m)
	for _, want := range []string{"Members (250)", "Repos (150)"} {
		if !strings.Contains(v, want) {
			t.Errorf("review view lacks %q", want)
		}
	}
	if strings.Contains(v, "Showing") {
		t.Errorf("a complete import reported a cut:\n%s", v)
	}
	team := w.review.toTeam("platform", w.org, w.slug)
	if team.GHTeamSlug != "platform" || team.Org != "acme" {
		t.Errorf("org/slug = %q/%q", team.Org, team.GHTeamSlug)
	}
	if n := len(team.Members); n != 250 || team.Members[0].Login != "m001" || team.Members[n-1].Login != "m250" {
		t.Errorf("members = %d, first %q, last %q; want 250 from m001 to m250", n, team.Members[0].Login, team.Members[n-1].Login)
	}
	if n := len(team.Repos); n != 150 || team.Repos[0].String() != "acme/r001" || team.Repos[n-1].String() != "acme/r150" {
		t.Errorf("repos = %d, first %q, last %q; want 150 from acme/r001 to acme/r150", n, team.Repos[0], team.Repos[n-1])
	}
}

// TestWizardSaysWhenAListWasCut is the PRD success metric "truncation
// honesty": whenever fewer rows arrived than the API counted, a note under
// the step title says so, and the headings count what is actually there
// (#103).
func TestWizardSaysWhenAListWasCut(t *testing.T) {
	var m tea.Model = New(scriptedDoer{}, nil)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	w := m.(Model)
	w.login, w.org = "mel", "acme"
	m = w

	teams := make([]gh.TeamInfo, 5000)
	for i := range teams {
		teams[i] = gh.TeamInfo{Slug: fmt.Sprintf("t%04d", i), Name: fmt.Sprintf("Team %04d", i)}
	}
	m, _ = m.Update(teamsMsg{teams: teams, total: 5210})
	v := plainView(m)
	for _, want := range []string{
		"Pick a team in acme (5210)",
		"Showing 5000 of 5210 teams: the list was cut.",
		"Enter manually",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("cut team list lacks %q:\n%s", want, v)
		}
	}

	members := make([]string, 1000)
	for i := range members {
		members[i] = fmt.Sprintf("m%04d", i)
	}
	repos := make([]gh.TeamRepo, 150)
	for i := range repos {
		repos[i] = gh.TeamRepo{Owner: "acme", Name: fmt.Sprintf("r%03d", i)}
	}
	w = m.(Model)
	w.slug = "platform"
	m = w
	cases := []struct {
		membersTotal, reposTotal int
		note                     string
	}{
		{1437, 150, "Showing 1000 of 1437 members and 150 of 150 repos: the member list was cut."},
		{1000, 151, "Showing 1000 of 1000 members and 150 of 151 repos: the repo list was cut."},
		{1437, 151, "Showing 1000 of 1437 members and 150 of 151 repos: the member and repo lists were cut."},
	}
	for _, tc := range cases {
		m, _ = m.Update(detailsMsg{Members: members, MembersTotal: tc.membersTotal, Repos: repos, ReposTotal: tc.reposTotal})
		v := plainView(m)
		note, heading := strings.Index(v, tc.note), strings.Index(v, "Members (1000)")
		if note < 0 || heading < 0 || note > heading {
			t.Errorf("totals %d/%d: want %q above the heading Members (1000):\n%s", tc.membersTotal, tc.reposTotal, tc.note, v)
		}
		if !strings.Contains(v, "config.yml") {
			t.Errorf("totals %d/%d: the note does not say where the rest go", tc.membersTotal, tc.reposTotal)
		}
	}

	// A complete import says nothing.
	m, _ = m.Update(detailsMsg{Members: members, MembersTotal: 1000, Repos: repos, ReposTotal: 150})
	if v := plainView(m); strings.Contains(v, "Showing") {
		t.Errorf("a complete import reported a cut:\n%s", v)
	}

	// The manual path never imported anything, so it never reports a cut,
	// even after a cut import was abandoned for it.
	m, _ = m.Update(detailsMsg{Members: members, MembersTotal: 1437, Repos: repos, ReposTotal: 150})
	w = m.(Model).toManual("acme", "Describe the team yourself.")
	w.review = newReviewList(&w.theme, []string{"alice"}, nil)
	w.step = stepReview
	if v := plainView(w); strings.Contains(v, "Showing") {
		t.Errorf("the manual review reported a cut:\n%s", v)
	}
}
